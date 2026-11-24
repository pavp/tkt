package board

import (
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
func PRStacks(prs []ticket.PullRequest) []PRStack {
	var out []PRStack
	for _, s := range ticket.Stacks(prs) {
		st := PRStack{PRs: make([]StackRow, len(s))}
		for i, p := range s {
			st.PRs[i] = StackRow{StackedPR: p, Status: stackStatus(p.PullRequest)}
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
