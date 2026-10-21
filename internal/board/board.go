package board

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/pavp/tkt/internal/config"
	"github.com/pavp/tkt/internal/jira"
	"github.com/pavp/tkt/internal/ticket"
)

// Snapshot is everything the board shows, as fetched at one moment.
type Snapshot struct {
	FetchedAt time.Time            `json:"fetched_at"`
	Tickets   []ticket.Ticket      `json:"tickets"`
	Reviews   []ticket.PullRequest `json:"reviews"`
	Sprints   []jira.Sprint        `json:"sprints"`
	// Agents holds the Herdr agent status of each ticket's workspace, when the board runs inside Herdr.
	Agents map[string]string `json:"agents,omitempty"`
	// Repos lists each space's repositories, configured or inferred from Jira-linked pull requests.
	Repos    map[string][]string `json:"repos,omitempty"`
	Warnings []string            `json:"warnings,omitempty"`
	// Panels keeps each ticket's development panel, so the next sync asks Jira only for changed tickets.
	Panels map[string]DevPanel `json:"panels,omitempty"`
}

// DevPanel is a ticket's development panel as last asked: the issue's updated time it belongs to and
// when it was asked.
type DevPanel struct {
	Updated time.Time             `json:"updated"`
	Asked   time.Time             `json:"asked"`
	PRs     []jira.DevPullRequest `json:"prs,omitempty"`
}

// panelTTL bounds how long an unchanged ticket's panel is trusted, since a new pull request may not
// change the issue's updated time.
const panelTTL = 10 * time.Minute

type JiraSource interface {
	Search(ctx context.Context, jql string, fields []string) ([]jira.Issue, error)
	FieldID(ctx context.Context, name string) (string, error)
	BrowseURL(key string) string
	DevPullRequests(ctx context.Context, issueID string) ([]jira.DevPullRequest, error)
}

type GitHubSource interface {
	PullRequests(ctx context.Context, repos []string) ([]ticket.PullRequest, error)
	PullRequestsForKeys(ctx context.Context, repos, keys []string) ([]ticket.PullRequest, error)
	ReviewRequests(ctx context.Context, repos []string, direct bool) ([]ticket.PullRequest, error)
}

// Fetch builds a snapshot. Jira failures are fatal because there is no board without tickets; GitHub
// failures become warnings so the tickets still show.
func Fetch(ctx context.Context, c *config.Config, js JiraSource, gh GitHubSource, now time.Time) (Snapshot, error) {
	return FetchSince(ctx, c, js, gh, now, Snapshot{})
}

// FetchSince builds a snapshot like Fetch, reusing prev's development panels for tickets that did not
// change.
func FetchSince(ctx context.Context, c *config.Config, js JiraSource, gh GitHubSource, now time.Time, prev Snapshot) (Snapshot, error) {
	return FetchWith(ctx, c, js, gh, now, prev, Options{})
}

// Options choose what a fetch includes beyond the user's own tickets.
type Options struct {
	// Team adds the open sprints' tickets assigned to others or to nobody, marked Team. They cost one more
	// Jira search: no development panel, no search for older pull requests, and their pull requests come
	// from the repositories' listing the user's tickets already need.
	Team bool
}

// FetchWith builds a snapshot like FetchSince with opts.
func FetchWith(ctx context.Context, c *config.Config, js JiraSource, gh GitHubSource, now time.Time, prev Snapshot, opts Options) (Snapshot, error) {
	snap := Snapshot{FetchedAt: now, Panels: map[string]DevPanel{}}
	if len(c.Spaces) == 0 {
		return snap, errors.New("no Jira spaces configured: add [[spaces]] to the config or run `tkt init`")
	}
	fields, labelID := jira.ListFields, ""
	if name := c.Jira.LabelField; name != "" {
		id, err := js.FieldID(ctx, name)
		switch {
		case err == nil:
			labelID, fields = id, append(slices.Clone(fields), id)
		case errors.Is(err, jira.ErrFieldNotFound):
			snap.Warnings = append(snap.Warnings, fmt.Sprintf("Jira has no field %q: check label_field in the config", name))
		default:
			snap.Warnings = append(snap.Warnings, "ticket labels unavailable: "+err.Error())
		}
	}
	type teamResult struct {
		issues []jira.Issue
		err    error
	}
	var teamDone chan teamResult
	if opts.Team {
		teamDone = make(chan teamResult, 1)
		go func() {
			is, err := js.Search(ctx, jira.TeamSprintJQL(c.SpaceKeys()), fields)
			teamDone <- teamResult{is, err}
		}()
	}
	issues, err := js.Search(ctx, jira.MySprintJQL(c.SpaceKeys()), fields)
	if err != nil {
		return snap, err
	}
	tickets := make([]ticket.Ticket, 0, len(issues))
	ids := make(map[ticket.Key]string, len(issues))
	updated := make(map[ticket.Key]time.Time, len(issues))
	seenSprint := map[string]bool{}
	toTicket := func(is jira.Issue) ticket.Ticket {
		t := ToTicket(c, is, js.BrowseURL(is.Key))
		if labelID != "" {
			t.Label = jira.FieldText(is.Fields[labelID])
		}
		for _, sp := range is.Sprints {
			if sp.State == "active" && !seenSprint[sp.Name] {
				seenSprint[sp.Name] = true
				snap.Sprints = append(snap.Sprints, sp)
			}
		}
		return t
	}
	for _, is := range issues {
		tickets = append(tickets, toTicket(is))
		ids[ticket.Key(is.Key)] = is.ID
		updated[ticket.Key(is.Key)] = is.Updated
	}
	var team []ticket.Ticket
	if teamDone != nil {
		if r := <-teamDone; r.err != nil {
			snap.Warnings = append(snap.Warnings, "team tickets unavailable: "+r.err.Error())
		} else {
			for _, is := range r.issues {
				t := toTicket(is)
				t.Team = true
				team = append(team, t)
			}
		}
	}

	linked := map[ticket.Key][]jira.DevPullRequest{}
	devFailed := map[ticket.Key]bool{}
	ignoredAny := map[string]bool{}
	panels := devPanels(ctx, js, tickets, ids, func(t ticket.Ticket) (DevPanel, bool) {
		p, ok := prev.Panels[string(t.Key)]
		return p, ok && p.Updated.Equal(updated[t.Key]) && now.Sub(p.Asked) < panelTTL
	})
	for i, t := range tickets {
		l, err := panels[i].prs, panels[i].err
		if err == nil {
			asked := now
			if panels[i].reused {
				asked = prev.Panels[string(t.Key)].Asked
			}
			snap.Panels[string(t.Key)] = DevPanel{Updated: updated[t.Key], Asked: asked, PRs: slices.Clone(l)}
		}
		if err != nil {
			devFailed[t.Key] = true
			continue
		}
		linked[t.Key] = slices.DeleteFunc(slices.Clone(l), func(p jira.DevPullRequest) bool { return ignored(c, t.Space(), p.Repo) })
		if len(l) > len(linked[t.Key]) {
			ignoredAny[t.Space()] = true
		}
	}
	snap.Repos = spaceRepos(c, linked)
	names := allRepos(snap.Repos)
	// Review requests need only the repositories, so they load while the pull requests do.
	type reviewResult struct {
		prs []ticket.PullRequest
		err error
	}
	var reviewsDone chan reviewResult
	if mode := c.ReviewMode(); mode != config.ReviewsOff && len(names) > 0 {
		reviewsDone = make(chan reviewResult, 1)
		go func() {
			r, err := gh.ReviewRequests(ctx, names, mode == config.ReviewsDirect)
			reviewsDone <- reviewResult{r, err}
		}()
	}
	var prs []ticket.PullRequest
	var prErr error
	if len(names) > 0 {
		prs, prErr = gh.PullRequests(ctx, names)
		if prErr != nil {
			snap.Warnings = append(snap.Warnings, "pull requests unavailable: "+prErr.Error())
		}
	}
	attached := ticket.AttachPRs(tickets, prs)
	var missing []string
	for _, t := range attached {
		if devFailed[t.Key] && len(t.PRs) == 0 {
			missing = append(missing, string(t.Key))
		}
	}
	if len(missing) > 0 && len(names) > 0 && prErr == nil {
		older, err := gh.PullRequestsForKeys(ctx, names, missing)
		if err != nil {
			snap.Warnings = append(snap.Warnings, "older pull requests unavailable: "+err.Error())
		}
		attached = ticket.AttachPRs(tickets, append(prs, older...))
	}
	for i := range attached {
		attached[i].PRs = mergeLinked(attached[i].PRs, linked[attached[i].Key], prs)
	}
	tickets = attached
	for _, t := range tickets {
		for _, p := range t.PRs {
			addInferred(c, snap.Repos, t.Space(), strings.ToLower(p.Repo))
		}
	}
	for _, s := range c.Spaces {
		if len(snap.Repos[s.Key]) > 0 {
			continue
		}
		why := "none of your tickets has a pull request"
		switch {
		case ignoredAny[s.Key]:
			why = "you ignore every repository of its pull requests"
		case len(tickets) > 0 && len(devFailed) == len(tickets):
			why = "Jira does not show your tickets' pull requests"
		}
		snap.Warnings = append(snap.Warnings, s.Key+" has no repositories yet: "+why+", so CI and reviews are off; add one in the config")
	}
	snap.Tickets = ticket.GroupSubtasks(append(tickets, ticket.AttachPRs(team, prs)...))

	if reviewsDone != nil {
		r := <-reviewsDone
		reviews, err := r.prs, r.err
		if err != nil {
			snap.Warnings = append(snap.Warnings, "review requests unavailable: "+err.Error())
		}
		seen := map[string]bool{}
		for _, pr := range reviews {
			id := fmt.Sprintf("%s#%d", pr.Repo, pr.Number)
			if !seen[id] {
				seen[id] = true
				snap.Reviews = append(snap.Reviews, pr)
			}
		}
		slices.SortStableFunc(snap.Reviews, byRepoNumber)
	}
	return snap, nil
}

// spaceRepos lists each space's repositories: the configured ones plus those holding the pull requests
// Jira links to the user's tickets, so a space works without any configured repository.
func spaceRepos(c *config.Config, linked map[ticket.Key][]jira.DevPullRequest) map[string][]string {
	out := map[string][]string{}
	for _, s := range c.Spaces {
		for _, r := range s.Repos {
			addRepo(out, s.Key, r.Name)
		}
	}
	for key, prs := range linked {
		for _, p := range prs {
			addInferred(c, out, key.Space(), strings.ToLower(p.Repo))
		}
	}
	return out
}

// devPanelCalls bounds the development-panel requests sent to Jira at once.
const devPanelCalls = 8

type devPanel struct {
	prs    []jira.DevPullRequest
	err    error
	reused bool
}

// devPanels asks Jira for each ticket's linked pull requests, several at a time, since one request per
// ticket in turn made loading slow; a ticket cached reports holds is not asked. Results keep the tickets' order.
func devPanels(ctx context.Context, js JiraSource, tickets []ticket.Ticket, ids map[ticket.Key]string, cached func(ticket.Ticket) (DevPanel, bool)) []devPanel {
	out := make([]devPanel, len(tickets))
	sem := make(chan struct{}, devPanelCalls)
	var wg sync.WaitGroup
	for i, t := range tickets {
		if p, ok := cached(t); ok {
			out[i] = devPanel{prs: p.PRs, reused: true}
			continue
		}
		wg.Add(1)
		go func(i int, id string) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			prs, err := js.DevPullRequests(ctx, id)
			out[i] = devPanel{prs: prs, err: err}
		}(i, ids[t.Key])
	}
	wg.Wait()
	return out
}

// addInferred adds a repository found through pull requests unless the space ignores it.
func addInferred(c *config.Config, bySpace map[string][]string, space, repo string) {
	if !ignored(c, space, repo) {
		addRepo(bySpace, space, repo)
	}
}

// ignored reports whether space ignores repo; a repository the space configures is never ignored.
func ignored(c *config.Config, space, repo string) bool {
	s, ok := c.Space(space)
	return ok && s.Ignores(repo) && !slices.ContainsFunc(s.Repos, func(r config.Repo) bool { return strings.EqualFold(r.Name, repo) })
}

// DropIgnored removes what a snapshot holds from repositories the config now ignores, so a board read
// from the cache honors an ignore made after it was fetched.
func DropIgnored(c *config.Config, snap Snapshot) Snapshot {
	drop := func(space string, prs []ticket.PullRequest) []ticket.PullRequest {
		return slices.DeleteFunc(slices.Clone(prs), func(p ticket.PullRequest) bool { return ignored(c, space, p.Repo) })
	}
	repos := map[string][]string{}
	for space, rs := range snap.Repos {
		for _, r := range rs {
			if !ignored(c, space, r) {
				repos[space] = append(repos[space], r)
			}
		}
	}
	snap.Repos = repos
	tickets := slices.Clone(snap.Tickets)
	for i := range tickets {
		tickets[i].PRs = drop(tickets[i].Space(), tickets[i].PRs)
		subs := slices.Clone(tickets[i].Subtasks)
		for j := range subs {
			subs[j].PRs = drop(subs[j].Space(), subs[j].PRs)
		}
		tickets[i].Subtasks = subs
	}
	snap.Tickets = tickets
	snap.Reviews = slices.DeleteFunc(slices.Clone(snap.Reviews), func(p ticket.PullRequest) bool {
		for _, s := range c.Spaces {
			if ignored(c, s.Key, p.Repo) {
				return true
			}
		}
		return false
	})
	return snap
}

// addRepo adds repo to space once, ignoring case, keeping the list sorted.
func addRepo(bySpace map[string][]string, space, repo string) {
	if repo == "" || slices.ContainsFunc(bySpace[space], func(r string) bool { return strings.EqualFold(r, repo) }) {
		return
	}
	bySpace[space] = append(bySpace[space], repo)
	slices.Sort(bySpace[space])
}

func allRepos(bySpace map[string][]string) []string {
	var out []string
	for _, rs := range bySpace {
		for _, r := range rs {
			if !slices.ContainsFunc(out, func(o string) bool { return strings.EqualFold(o, r) }) {
				out = append(out, r)
			}
		}
	}
	slices.Sort(out)
	return out
}

// mergeLinked adds the pull requests Jira links to a ticket, preferring the GitHub copy of each one
// because only GitHub knows its checks and review state.
func mergeLinked(attached []ticket.PullRequest, linked []jira.DevPullRequest, fromGitHub []ticket.PullRequest) []ticket.PullRequest {
	out := attached
	has := func(repo string, n int) bool {
		return slices.ContainsFunc(out, func(p ticket.PullRequest) bool { return strings.EqualFold(p.Repo, repo) && p.Number == n })
	}
	for _, l := range linked {
		if l.Number == 0 || has(l.Repo, l.Number) {
			continue
		}
		i := slices.IndexFunc(fromGitHub, func(p ticket.PullRequest) bool { return strings.EqualFold(p.Repo, l.Repo) && p.Number == l.Number })
		if i >= 0 {
			out = append(out, fromGitHub[i])
			continue
		}
		state := ticket.PROpen
		switch l.Status {
		case "MERGED":
			state = ticket.PRMerged
		case "DECLINED":
			state = ticket.PRClosed
		}
		out = append(out, ticket.PullRequest{Repo: l.Repo, Number: l.Number, Title: l.Title, URL: l.URL, Branch: l.Branch, State: state})
	}
	slices.SortStableFunc(out, byRepoNumber)
	return out
}

func byRepoNumber(a, b ticket.PullRequest) int {
	if c := strings.Compare(a.Repo, b.Repo); c != 0 {
		return c
	}
	return a.Number - b.Number
}

// ToTicket converts a Jira issue into a board ticket using its space's status overrides; a blocked ticket
// waits in To Do unless its status is overridden.
func ToTicket(c *config.Config, is jira.Issue, url string) ticket.Ticket {
	t := ticket.Ticket{
		Assignee: is.Assignee,
		Key:      ticket.Key(is.Key),
		Type:     is.Type,
		Summary:  is.Summary,
		Status:   is.Status,
		Parent:   ticket.Key(is.Parent),
		URL:      url,
	}
	space, _ := c.Space(t.Space())
	overrides := space.Categories()
	t.Category = ticket.CategoryFor(is.Status, is.StatusCategory, overrides)
	if _, ok := overrides[is.Status]; !ok && t.Blocked() {
		t.Category = ticket.ToDo
	}
	return t
}

// Attention is one of the user's tickets blocked on them: failing CI or requested changes.
type Attention struct {
	Ref    string         `json:"ref"`
	Title  string         `json:"title"`
	Reason ticket.Reason  `json:"reason"`
	Ticket *ticket.Ticket `json:"ticket,omitempty"`
}

// NeedsYou lists the user's tickets that are blocked on them: failing CI or requested changes, in board
// order. Review requests are listed apart, in Reviews.
func (s Snapshot) NeedsYou() []Attention {
	var out []Attention
	for i := range s.Tickets {
		t := &s.Tickets[i]
		for _, r := range t.Attention() {
			out = append(out, Attention{Ref: string(t.Key), Title: t.Summary, Reason: r, Ticket: t})
		}
	}
	return out
}

var columnOrder = map[ticket.Category]int{ticket.InProgress: 0, ticket.InReview: 1, ticket.ToDo: 2, ticket.Done: 3}

// Sorted orders tickets for lists: needing attention first, then In Progress, In Review, To Do, Done.
func Sorted(tickets []ticket.Ticket) []ticket.Ticket {
	out := append([]ticket.Ticket(nil), tickets...)
	sort.SliceStable(out, func(i, j int) bool {
		ai, aj := len(out[i].Attention()) > 0, len(out[j].Attention()) > 0
		if ai != aj {
			return ai
		}
		if ci, cj := columnOrder[out[i].Category], columnOrder[out[j].Category]; ci != cj {
			return ci < cj
		}
		return out[i].Key < out[j].Key
	})
	return out
}

// Column returns the tickets in one board column.
func (s Snapshot) Column(c ticket.Category) []ticket.Ticket {
	var out []ticket.Ticket
	for _, t := range s.Tickets {
		if t.Category == c {
			out = append(out, t)
		}
	}
	return out
}

// WithoutBots drops review requests on pull requests opened by bots, such as dependabot.
func (s Snapshot) WithoutBots() Snapshot {
	s.Reviews = slices.DeleteFunc(slices.Clone(s.Reviews), func(p ticket.PullRequest) bool { return p.AuthorBot })
	return s
}

// Filter keeps the tickets and reviews of one space; an empty space keeps everything.
func (s Snapshot) Filter(space string) Snapshot {
	if space == "" {
		return s
	}
	out := s
	out.Tickets = nil
	for _, t := range s.Tickets {
		if t.Space() == space {
			out.Tickets = append(out.Tickets, t)
		}
	}
	out.Reviews = nil
	for _, pr := range s.Reviews {
		for _, k := range ticket.BranchKeys(pr.Branch) {
			if k.Space() == space {
				out.Reviews = append(out.Reviews, pr)
				break
			}
		}
	}
	out.Sprints = nil
	for _, sp := range s.Sprints {
		if sp.Space == space {
			out.Sprints = append(out.Sprints, sp)
		}
	}
	return out
}

// SprintLabel names a sprint with its time left: "S42 · 4d left", "S42 · overdue", or just the name.
func SprintLabel(sp jira.Sprint, now time.Time) string {
	switch {
	case sp.End.IsZero():
		return sp.Name
	case !sp.End.After(now):
		return sp.Name + " · overdue"
	}
	return fmt.Sprintf("%s · %dd left", sp.Name, DaysLeft(sp.End, now))
}

// DaysLeft returns whole days until end, rounding up; a past end gives 0.
func DaysLeft(end, now time.Time) int {
	if end.IsZero() || !end.After(now) {
		return 0
	}
	return int((end.Sub(now) + 24*time.Hour - time.Nanosecond) / (24 * time.Hour))
}

// TicketLine is the ticket half of a workspace's sidebar entry, at most 80 characters (Herdr's token
// limit), such as "✕ CI · #812"; alert is true when the ticket needs the user. It leaves out the key,
// which the workspace label and the branch above it already show.
func TicketLine(t ticket.Ticket) (line string, alert bool) {
	var parts []string
	reasons := t.Attention()
	switch {
	case len(reasons) > 0 && reasons[0] == ticket.ReasonCIFailing:
		parts = append(parts, "✕ CI")
	case len(reasons) > 0:
		parts = append(parts, "changes requested")
	case t.Checks() == ticket.ChecksPass:
		parts = append(parts, "✓ CI")
	default:
		parts = append(parts, t.Status)
	}
	var prs []string
	for _, pr := range t.PRs {
		if pr.State == ticket.PROpen {
			prs = append(prs, fmt.Sprintf("#%d", pr.Number))
		}
	}
	if len(prs) > 0 {
		parts = append(parts, strings.Join(prs, " "))
	}
	line = strings.Join(parts, " · ")
	if r := []rune(line); len(r) > 80 {
		line = string(r[:79]) + "…"
	}
	return line, len(reasons) > 0
}

// AgentKind groups Herdr's agent statuses into what the user acts on.
type AgentKind int

const (
	AgentNone AgentKind = iota
	AgentIdle
	AgentWorking
	AgentNeedsYou
)

// AgentLine words a Herdr agent status as the prototype does. Herdr reports "blocked" while an agent waits
// for the user's confirmation, and "unknown" for panes it cannot read, which tells the user nothing.
func AgentLine(status string) (string, AgentKind) {
	switch status {
	case "working":
		return "● claude working", AgentWorking
	case "blocked":
		return "! claude needs you", AgentNeedsYou
	case "idle", "done":
		return "○ claude idle", AgentIdle
	}
	return "no agent", AgentNone
}

// MostUrgentAgent picks the status to show for a workspace running several agents.
func MostUrgentAgent(statuses []string) string {
	best, bestKind := "", AgentNone
	for _, s := range statuses {
		if _, k := AgentLine(s); k > bestKind {
			best, bestKind = s, k
		}
	}
	return best
}

// Find returns the ticket with key, searching subtasks too.
func (s Snapshot) Find(key ticket.Key) (ticket.Ticket, bool) {
	for _, t := range s.Tickets {
		if t.Key == key {
			return t, true
		}
		for _, st := range t.Subtasks {
			if st.Key == key {
				return st, true
			}
		}
	}
	return ticket.Ticket{}, false
}
