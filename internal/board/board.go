package board

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"
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
	// Repos lists each space's repositories, configured or inferred from Jira-linked pull requests.
	Repos    map[string][]string `json:"repos,omitempty"`
	Warnings []string            `json:"warnings,omitempty"`
}

type JiraSource interface {
	Search(ctx context.Context, jql string, fields []string) ([]jira.Issue, error)
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
	snap := Snapshot{FetchedAt: now}
	if len(c.Spaces) == 0 {
		return snap, errors.New("no Jira spaces configured: add [[spaces]] to the config or run `tkt init`")
	}
	issues, err := js.Search(ctx, jira.MySprintJQL(c.SpaceKeys()), jira.ListFields)
	if err != nil {
		return snap, err
	}
	tickets := make([]ticket.Ticket, 0, len(issues))
	ids := make(map[ticket.Key]string, len(issues))
	seenSprint := map[string]bool{}
	for _, is := range issues {
		tickets = append(tickets, ToTicket(c, is, js.BrowseURL(is.Key)))
		ids[ticket.Key(is.Key)] = is.ID
		for _, sp := range is.Sprints {
			if sp.State == "active" && !seenSprint[sp.Name] {
				seenSprint[sp.Name] = true
				snap.Sprints = append(snap.Sprints, sp)
			}
		}
	}

	linked := map[ticket.Key][]jira.DevPullRequest{}
	devFailed := map[ticket.Key]bool{}
	for _, t := range tickets {
		l, err := js.DevPullRequests(ctx, ids[t.Key])
		if err != nil {
			devFailed[t.Key] = true
			continue
		}
		linked[t.Key] = l
	}
	snap.Repos = spaceRepos(c, linked)
	names := allRepos(snap.Repos)
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
	snap.Tickets = ticket.GroupSubtasks(tickets)

	if mode := c.ReviewMode(); mode != config.ReviewsOff && len(names) > 0 {
		reviews, err := gh.ReviewRequests(ctx, names, mode == config.ReviewsDirect)
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
	}
	return snap, nil
}

// spaceRepos lists each space's repositories: the configured ones plus those holding the pull requests
// Jira links to the user's tickets, so a space works without any configured repository.
func spaceRepos(c *config.Config, linked map[ticket.Key][]jira.DevPullRequest) map[string][]string {
	out := map[string][]string{}
	add := func(space, repo string) {
		if repo == "" || slices.ContainsFunc(out[space], func(r string) bool { return strings.EqualFold(r, repo) }) {
			return
		}
		out[space] = append(out[space], repo)
	}
	for _, s := range c.Spaces {
		for _, r := range s.Repos {
			add(s.Key, r.Name)
		}
	}
	for key, prs := range linked {
		for _, p := range prs {
			add(key.Space(), strings.ToLower(p.Repo))
		}
	}
	for k := range out {
		slices.Sort(out[k])
	}
	return out
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
	slices.SortStableFunc(out, func(a, b ticket.PullRequest) int {
		if c := strings.Compare(a.Repo, b.Repo); c != 0 {
			return c
		}
		return a.Number - b.Number
	})
	return out
}

// ToTicket converts a Jira issue into a board ticket using its space's status overrides.
func ToTicket(c *config.Config, is jira.Issue, url string) ticket.Ticket {
	t := ticket.Ticket{
		Key:     ticket.Key(is.Key),
		Type:    is.Type,
		Summary: is.Summary,
		Status:  is.Status,
		Parent:  ticket.Key(is.Parent),
		URL:     url,
	}
	space, _ := c.Space(t.Space())
	t.Category = ticket.CategoryFor(is.Status, is.StatusCategory, space.Categories())
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

// SidebarLabel is the one-line status Herdr shows next to a ticket's workspace, at most 80 characters
// (Herdr's token limit), such as "DEMO-1 · ✕ CI · #812 · claude working".
func SidebarLabel(t ticket.Ticket, agentStatus string) string {
	parts := []string{string(t.Key)}
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
	if agentStatus != "" {
		parts = append(parts, "claude "+agentStatus)
	}
	label := strings.Join(parts, " · ")
	if r := []rune(label); len(r) > 80 {
		label = string(r[:79]) + "…"
	}
	return label
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
