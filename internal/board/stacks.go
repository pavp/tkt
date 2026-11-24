package board

import (
	"slices"
	"strconv"
	"strings"

	"github.com/pavp/tkt/internal/ticket"
)

// PRStack is a stack of pull requests as the boards show it: named, with its worst status, bottom first.
type PRStack struct {
	Name   string     `json:"name"`
	Status string     `json:"status"`
	PRs    []StackRow `json:"prs"`
}

// StackRow is one pull request of a stack with where it stands.
type StackRow struct {
	ticket.StackedPR
	Status string `json:"status"`
	// Ticket is the key of the ticket the pull request belongs to, when it is known.
	Ticket ticket.Key `json:"ticket,omitempty"`
}

// KnownPR is a pull request the board knows, with the key of the ticket it belongs to, empty for none.
type KnownPR struct {
	ticket.PullRequest
	Key ticket.Key
}

// KnownPRs lists every pull request of the snapshot once: its tickets', then the review requests and everyone
// else's, these with the ticket their branch names; a team ticket's pull request is also among everyone else's.
func (s Snapshot) KnownPRs() []KnownPR {
	var out []KnownPR
	seen := map[string]bool{}
	first := func(pr ticket.PullRequest) bool {
		id := pr.Repo + "#" + strconv.Itoa(pr.Number)
		defer func() { seen[id] = true }()
		return !seen[id]
	}
	for _, t := range s.Tickets {
		for _, pr := range t.PRs {
			if first(pr) {
				out = append(out, KnownPR{PullRequest: pr, Key: t.Key})
			}
		}
	}
	for _, pr := range append(slices.Clone(s.Reviews), s.Others...) {
		if !first(pr) {
			continue
		}
		k := KnownPR{PullRequest: pr}
		if keys := ticket.BranchKeys(pr.Branch); len(keys) > 0 {
			k.Key = keys[0]
		}
		out = append(out, k)
	}
	return out
}

// worse lists the statuses from the one that most needs acting on; a stack shows the first any of its pull
// requests has.
var worse = []string{string(ticket.ReasonCIFailing), string(ticket.ReasonChangesRequested), string(ticket.ReasonConflicts),
	"unresolved", "Draft", "CI running", "Waiting review", "Open", "Approved", "Ready to merge", "Closed", "Merged"}

func rank(status string) int {
	for i, s := range worse {
		if status == s || strings.HasSuffix(status, " "+s) {
			return i
		}
	}
	return len(worse)
}

// PRStacks groups prs into stacks as ticket.Stacks does, with each one's name, status and rows' statuses.
func PRStacks(prs []ticket.PullRequest) []PRStack { return PRStacksWith("", prs, nil) }

// PRStacksWith is PRStacks of key's pull requests prs, completed with those of known that share a stack with
// them, so a stack split across tickets shows whole; only the stacks holding one of prs are kept.
func PRStacksWith(key ticket.Key, prs []ticket.PullRequest, known []KnownPR) []PRStack {
	type id struct {
		repo string
		n    int
	}
	pool := slices.Clone(prs)
	keys := map[id]ticket.Key{}
	for _, p := range prs {
		keys[id{p.Repo, p.Number}] = key
	}
	for _, k := range known {
		if _, ok := keys[id{k.Repo, k.Number}]; !ok {
			keys[id{k.Repo, k.Number}] = k.Key
			pool = append(pool, k.PullRequest)
		}
	}
	own := func(s []ticket.StackedPR) bool {
		return slices.ContainsFunc(s, func(p ticket.StackedPR) bool {
			return slices.ContainsFunc(prs, func(o ticket.PullRequest) bool { return o.Repo == p.Repo && o.Number == p.Number })
		})
	}
	var out []PRStack
	for _, s := range ticket.Stacks(pool) {
		if !own(s) {
			continue
		}
		st := PRStack{PRs: make([]StackRow, len(s))}
		for i, p := range s {
			st.PRs[i] = StackRow{StackedPR: p, Status: stackStatus(p.PullRequest), Ticket: keys[id{p.Repo, p.Number}]}
			if i == 0 || rank(st.PRs[i].Status) < rank(st.Status) {
				st.Status = st.PRs[i].Status
			}
		}
		st.Name = stackName(s)
		out = append(out, st)
	}
	return out
}

func stackStatus(pr ticket.PullRequest) string {
	switch pr.State {
	case ticket.PRMerged:
		return "Merged"
	case ticket.PRClosed:
		return "Closed"
	}
	return PRStatus(pr)
}

// stackName is the part its branches share, cut at a separator and with their kind ("feat/") when they all have
// the same one, or the bottom's branch when they share no more than their kind.
func stackName(s []ticket.StackedPR) string {
	bottom := s[0].Branch
	kind, common, found := strings.Cut(bottom, "/")
	if !found {
		kind, common = "", bottom
	}
	sameKind := true
	for _, p := range s[1:] {
		k, rest, ok := strings.Cut(p.Branch, "/")
		if !ok {
			k, rest = "", p.Branch
		}
		sameKind = sameKind && k == kind
		n := 0
		for n < len(common) && n < len(rest) && common[n] == rest[n] {
			n++
		}
		if n < len(common) {
			common = common[:n]
			if i := strings.LastIndexAny(common, "-_/."); i >= 0 {
				common = common[:i]
			} else {
				common = ""
			}
		}
	}
	switch {
	case common == "":
		return bottom
	case sameKind && kind != "":
		return kind + "/" + common
	}
	return common
}

// StackedRow is a row of the PRs tab with the stack it belongs to, nil when it stands alone, and the number of
// the pull request it sits on.
type StackedRow struct {
	PRRow
	Stack *PRStack
	After int
}

// StackedRows orders a group's rows so each stack's rows follow each other from its bottom, where the stack's
// first row was; rows standing alone keep their place.
func StackedRows(rows []PRRow) []StackedRow {
	prs := make([]ticket.PullRequest, len(rows))
	at := map[string]int{}
	for i, r := range rows {
		prs[i] = *r.PR
		at[r.PR.Repo+"#"+strconv.Itoa(r.PR.Number)] = i
	}
	type placed struct {
		first int
		stack PRStack
	}
	var stacks []placed
	for _, s := range PRStacks(prs) {
		first := len(rows)
		for _, p := range s.PRs {
			first = min(first, at[p.Repo+"#"+strconv.Itoa(p.Number)])
		}
		stacks = append(stacks, placed{first, s})
	}
	slices.SortStableFunc(stacks, func(a, b placed) int { return a.first - b.first })
	var out []StackedRow
	for _, p := range stacks {
		s := p.stack
		for _, sp := range s.PRs {
			r := StackedRow{PRRow: rows[at[sp.Repo+"#"+strconv.Itoa(sp.Number)]], After: sp.After}
			if len(s.PRs) > 1 {
				r.Stack = &s
			}
			out = append(out, r)
		}
	}
	return out
}
