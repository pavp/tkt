package ticket

import (
	"fmt"
	"regexp"
	"slices"
	"sort"
	"strings"
)

// Key is a Jira issue key such as DEMO-123.
type Key string

var keyPattern = regexp.MustCompile(`[A-Z][A-Z0-9]+-[0-9]+`)

// KeysIn returns the uppercase issue keys found in s, in order, without duplicates.
func KeysIn(s string) []Key {
	var out []Key
	seen := map[Key]bool{}
	for _, loc := range keyPattern.FindAllStringIndex(s, -1) {
		if loc[0] > 0 && isKeyChar(s[loc[0]-1]) {
			continue
		}
		if loc[1] < len(s) && isDigit(s[loc[1]]) {
			continue
		}
		k := Key(s[loc[0]:loc[1]])
		if !seen[k] {
			seen[k] = true
			out = append(out, k)
		}
	}
	return out
}

// BranchKeys returns the keys in a branch name in any case, so "test/acto-170449-coverage" yields
// DEMO-170449. Ordinary words such as "fix-123" also match; callers keep only keys of known spaces or
// tickets, which makes that harmless.
func BranchKeys(branch string) []Key {
	return KeysIn(strings.ToUpper(branch))
}

// KeyInBranch returns the first key in branch, in any case, whose space is one of spaces.
func KeyInBranch(branch string, spaces []string) (Key, bool) {
	allowed := map[string]bool{}
	for _, s := range spaces {
		allowed[s] = true
	}
	for _, k := range BranchKeys(branch) {
		if allowed[k.Space()] {
			return k, true
		}
	}
	return "", false
}

func (k Key) Space() string {
	space, _, _ := strings.Cut(string(k), "-")
	return space
}

func isKeyChar(b byte) bool { return (b >= 'A' && b <= 'Z') || isDigit(b) }
func isDigit(b byte) bool   { return b >= '0' && b <= '9' }

// Category is a board column, independent of each space's status names.
type Category string

const (
	ToDo       Category = "todo"
	InProgress Category = "in_progress"
	InReview   Category = "review"
	Done       Category = "done"
)

var Columns = []Category{ToDo, InProgress, InReview, Done}

func ParseCategory(s string) (Category, bool) {
	for _, c := range Columns {
		if string(c) == s {
			return c, true
		}
	}
	return "", false
}

func (c Category) Title() string {
	switch c {
	case ToDo:
		return "To Do"
	case InProgress:
		return "In Progress"
	case InReview:
		return "In Review"
	case Done:
		return "Done"
	}
	return string(c)
}

// CategoryFor maps a Jira status to a column: an explicit override wins, otherwise Jira's status category
// ("new", "indeterminate", "done") decides.
func CategoryFor(status, statusCategory string, overrides map[string]Category) Category {
	if c, ok := overrides[status]; ok {
		return c
	}
	switch {
	case statusCategory == "done":
		return Done
	case statusCategory == "indeterminate" && Reviewing(status):
		return InReview
	case statusCategory == "indeterminate":
		return InProgress
	default:
		return ToDo
	}
}

// Reviewing reports whether a status name says the work is in review, in English or Spanish ("Code Review",
// "En revisión"); Jira files such statuses as in progress.
func Reviewing(status string) bool {
	s := strings.ToLower(status)
	return strings.Contains(s, "review") || strings.Contains(s, "revisi")
}

// Column places a status on the board as ToTicket does: an override wins, a blocked status waits in
// To Do, otherwise Jira's category decides.
func Column(status, statusCategory string, overrides map[string]Category) Category {
	if c, ok := overrides[status]; ok {
		return c
	}
	if BlockedName(status) {
		return ToDo
	}
	return CategoryFor(status, statusCategory, nil)
}

// BlockedName reports whether a status name says the work is blocked, as "Blocked" or "Bloqueado" do.
func BlockedName(status string) bool {
	s := strings.ToLower(status)
	return strings.Contains(s, "block") || strings.Contains(s, "bloque")
}

// Mark is the icon a card shows with the ticket's status name, beside its key.
type Mark string

const (
	MarkNone     Mark = ""
	MarkDiamond  Mark = "diamond"
	MarkBlocked  Mark = "blocked"
	MarkClock    Mark = "clock"
	MarkFlag     Mark = "flag"
	MarkStar     Mark = "star"
	MarkTriangle Mark = "triangle"
)

// Marks lists every mark, none first, in the order a user cycles through them.
var Marks = []Mark{MarkNone, MarkDiamond, MarkBlocked, MarkClock, MarkFlag, MarkStar, MarkTriangle}

// ParseMark reads a mark as the configuration writes it, "none" standing for no mark.
func ParseMark(s string) (Mark, bool) {
	if s == "none" {
		return MarkNone, true
	}
	if m := Mark(s); m != MarkNone && slices.Contains(Marks, m) {
		return m, true
	}
	return "", false
}

// MarkFor picks a status's mark: the configured one, otherwise blocked for a blocked name and a diamond
// when overrides moved it out of its automatic column.
func MarkFor(status, statusCategory string, overrides map[string]Category, marks map[string]Mark) Mark {
	if m, ok := marks[status]; ok {
		return m
	}
	switch {
	case BlockedName(status):
		return MarkBlocked
	case Column(status, statusCategory, overrides) != Column(status, statusCategory, nil):
		return MarkDiamond
	}
	return MarkNone
}

type CheckState string

const (
	ChecksNone    CheckState = ""
	ChecksPass    CheckState = "pass"
	ChecksPending CheckState = "pending"
	ChecksFail    CheckState = "fail"
)

func (c CheckState) rank() int {
	switch c {
	case ChecksFail:
		return 3
	case ChecksPending:
		return 2
	case ChecksPass:
		return 1
	}
	return 0
}

// WorstChecks returns the most urgent state among states.
func WorstChecks(states ...CheckState) CheckState {
	worst := ChecksNone
	for _, s := range states {
		if s.rank() > worst.rank() {
			worst = s
		}
	}
	return worst
}

type PRState string

const (
	PROpen   PRState = "open"
	PRMerged PRState = "merged"
	PRClosed PRState = "closed"
)

type ReviewDecision string

const (
	ReviewNone             ReviewDecision = ""
	ReviewApproved         ReviewDecision = "approved"
	ReviewChangesRequested ReviewDecision = "changes_requested"
	ReviewRequired         ReviewDecision = "review_required"
)

// MergeState says whether GitHub would merge a pull request now; empty when it would not for another
// reason, or has not worked it out yet.
type MergeState string

const (
	MergeReady     MergeState = "ready"
	MergeConflicts MergeState = "conflicts"
)

type PullRequest struct {
	Repo   string         `json:"repo"`
	Number int            `json:"number"`
	Title  string         `json:"title"`
	URL    string         `json:"url"`
	Branch string         `json:"branch"`
	State  PRState        `json:"state"`
	Checks CheckState     `json:"checks"`
	Review ReviewDecision `json:"review"`
	Author string         `json:"author,omitempty"`
	// AuthorBot marks pull requests opened by a bot account, such as dependabot.
	AuthorBot bool   `json:"author_bot,omitempty"`
	HeadSHA   string `json:"head_sha,omitempty"`
	// Approvals counts the reviewers whose latest review approves the pull request.
	Approvals int        `json:"approvals,omitempty"`
	Draft     bool       `json:"draft,omitempty"`
	Merge     MergeState `json:"merge,omitempty"`
	// Unresolved counts the review threads nobody has resolved yet.
	Unresolved int `json:"unresolved,omitempty"`
}

// ApprovalsText words the approvals, such as "2 approvals"; it is empty without any.
func (pr PullRequest) ApprovalsText() string {
	switch pr.Approvals {
	case 0:
		return ""
	case 1:
		return "1 approval"
	}
	return fmt.Sprintf("%d approvals", pr.Approvals)
}

type Ticket struct {
	Key      Key      `json:"key"`
	Type     string   `json:"type"`
	Summary  string   `json:"summary"`
	Status   string   `json:"status"`
	Category Category `json:"category"`
	Mark     Mark     `json:"mark,omitempty"`
	// Label is the value of the Jira field the configuration names, such as the ticket's product.
	Label string `json:"label,omitempty"`
	// Points are the ticket's story points; 0 when unestimated.
	Points float64 `json:"points,omitempty"`
	// Assignee is the display name of the person the ticket is assigned to; empty when unassigned.
	Assignee string `json:"assignee,omitempty"`
	// Team marks a teammate's or unassigned ticket, shown on request; it never needs the user.
	Team bool `json:"team,omitempty"`
	// Board names the team board whose active sprint holds the ticket, empty when it is on no team board.
	Board    string        `json:"board,omitempty"`
	Parent   Key           `json:"parent,omitempty"`
	URL      string        `json:"url"`
	PRs      []PullRequest `json:"prs,omitempty"`
	Subtasks []Ticket      `json:"subtasks,omitempty"`
}

func (t Ticket) Space() string { return t.Key.Space() }

type Reason string

const (
	ReasonCIFailing        Reason = "CI failing"
	ReasonChangesRequested Reason = "Changes requested"
	ReasonUnresolved       Reason = "Unresolved comments"
	ReasonConflicts        Reason = "Conflicts"
)

// Attention lists why an open ticket needs the user: failing CI or requested changes on any open PR.
func (t Ticket) Attention() []Reason {
	if t.Category == Done || t.Team {
		return nil
	}
	found := map[Reason]bool{}
	for _, pr := range t.PRs {
		for _, r := range pr.Attention() {
			found[r] = true
		}
	}
	var out []Reason
	for _, r := range reasons {
		if found[r] {
			out = append(out, r)
		}
	}
	return out
}

// reasons orders what needs the user, most urgent first.
var reasons = []Reason{ReasonCIFailing, ReasonChangesRequested, ReasonUnresolved, ReasonConflicts}

// Attention lists why an open pull request needs the user, most urgent first.
func (pr PullRequest) Attention() []Reason {
	if pr.State != PROpen {
		return nil
	}
	var out []Reason
	if pr.Checks == ChecksFail {
		out = append(out, ReasonCIFailing)
	}
	if pr.Review == ReviewChangesRequested {
		out = append(out, ReasonChangesRequested)
	}
	if pr.Unresolved > 0 {
		out = append(out, ReasonUnresolved)
	}
	if pr.Merge == MergeConflicts {
		out = append(out, ReasonConflicts)
	}
	return out
}

// Checks returns the most urgent check state among the ticket's open PRs.
func (t Ticket) Checks() CheckState {
	worst := ChecksNone
	for _, pr := range t.PRs {
		if pr.State == PROpen {
			worst = WorstChecks(worst, pr.Checks)
		}
	}
	return worst
}

// AttachPRs gives each ticket the PRs whose branch or title carries its key, once each. A PR naming
// several tickets belongs to each of them.
func AttachPRs(tickets []Ticket, prs []PullRequest) []Ticket {
	byKey := map[Key][]PullRequest{}
	seen := map[string]bool{}
	for _, pr := range prs {
		keys := append(BranchKeys(pr.Branch), KeysIn(strings.ToUpper(pr.Title))...)
		for _, k := range keys {
			id := fmt.Sprintf("%s|%s#%d", k, pr.Repo, pr.Number)
			if seen[id] {
				continue
			}
			seen[id] = true
			byKey[k] = append(byKey[k], pr)
		}
	}
	out := make([]Ticket, len(tickets))
	for i, t := range tickets {
		t.PRs = append([]PullRequest(nil), byKey[t.Key]...)
		sort.Slice(t.PRs, func(a, b int) bool {
			if t.PRs[a].Repo != t.PRs[b].Repo {
				return t.PRs[a].Repo < t.PRs[b].Repo
			}
			return t.PRs[a].Number < t.PRs[b].Number
		})
		out[i] = t
	}
	return out
}

// GroupSubtasks nests subtasks under their parent when the parent is present; orphans stay top-level.
func GroupSubtasks(tickets []Ticket) []Ticket {
	present := map[Key]bool{}
	for _, t := range tickets {
		present[t.Key] = true
	}
	children := map[Key][]Ticket{}
	var top []Ticket
	for _, t := range tickets {
		if t.Parent != "" && present[t.Parent] {
			children[t.Parent] = append(children[t.Parent], t)
			continue
		}
		top = append(top, t)
	}
	for i := range top {
		top[i].Subtasks = children[top[i].Key]
	}
	return top
}

// SplitPRs separates open pull requests from finished (merged or closed) ones, which lists summarize as a count.
func SplitPRs(prs []PullRequest) (open []PullRequest, merged, closed int) {
	for _, pr := range prs {
		switch pr.State {
		case PRMerged:
			merged++
		case PRClosed:
			closed++
		default:
			open = append(open, pr)
		}
	}
	return open, merged, closed
}

// FinishedSummary describes finished PRs briefly, such as "5 merged" or "2 merged · 1 closed".
func FinishedSummary(merged, closed int) string {
	var parts []string
	if merged > 0 {
		parts = append(parts, fmt.Sprintf("%d merged", merged))
	}
	if closed > 0 {
		parts = append(parts, fmt.Sprintf("%d closed", closed))
	}
	return strings.Join(parts, " · ")
}
