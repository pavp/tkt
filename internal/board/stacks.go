package board

import (
	"slices"
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

// KnownPRs lists every pull request of the snapshot: its tickets', then the review requests and everyone
// else's, these with the ticket their branch names.
func (s Snapshot) KnownPRs() []KnownPR {
	var out []KnownPR
	for _, t := range s.Tickets {
		for _, pr := range t.PRs {
			out = append(out, KnownPR{PullRequest: pr, Key: t.Key})
		}
	}
	for _, pr := range append(slices.Clone(s.Reviews), s.Others...) {
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

// stackName is the part its branches share, cut at a separator, or the bottom's branch when they share no
// more than their kind ("feat/").
func stackName(s []ticket.StackedPR) string {
	bottom := s[0].Branch
	common := bottom
	for _, p := range s[1:] {
		n := 0
		for n < len(common) && n < len(p.Branch) && common[n] == p.Branch[n] {
			n++
		}
		common = common[:n]
	}
	if common != bottom {
		if i := strings.LastIndexAny(common, "-_/."); i >= 0 {
			common = common[:i]
		} else {
			common = ""
		}
	}
	kind, _, _ := strings.Cut(bottom, "/")
	if common == "" || common == kind {
		return bottom
	}
	return common
}
