package board

import (
	"fmt"
	"slices"
	"strings"

	"github.com/pavp/tkt/internal/ticket"
)

// ListState is what the PRs tab and the Backlog show, as the user last left them.
type ListState struct {
	HiddenRepos []string
	// Collapsed lists the folded groups of both tabs, such as "mine:owner/name" or "sprint:12"; the Backlog's
	// sprintless group is folded unless listed.
	Collapsed []string
	// TeamPRs lists everyone else's open pull requests too; off, the PRs tab narrows to the review requests.
	TeamPRs       bool
	BacklogMerged bool
}

// PRFilter is what the PRs tab leaves of a snapshot's pull requests.
type PRFilter struct {
	HiddenRepos map[string]bool
	// Team lists everyone else's open pull requests beside the review requests.
	Team bool
	// Bots keeps the pull requests opened by bots.
	Bots bool
}

func (f PRFilter) repoHidden(repo string) bool { return repo != "" && f.HiddenRepos[repo] }

// PRRow is one pull request of the PRs tab: the user's, with its ticket when it has one, a review request, or,
// with Other set, someone else's or one the user opened for a ticket not theirs.
type PRRow struct {
	PR     *ticket.PullRequest
	Ticket *ticket.Ticket
	Other  bool
}

func (r PRRow) Review() bool { return r.Ticket == nil && !r.Other }

// Own covers the user's pull requests, those of their tickets and those they opened for a ticket not theirs.
func (r PRRow) Own() bool { return r.Ticket != nil || r.Other && r.PR.Yours }

// PRGroup is one group of the PRs tab, such as "Mine · owner/name", by its key, such as "mine:owner/name".
type PRGroup struct {
	Group, Title string
	Rows         []PRRow
	// Needs counts the rows that need the user: review requests and the user's pull requests asking for action.
	Needs int
}

// MyPRs lists the open pull requests of the user's tickets by repository, repositories with one that needs
// the user first, then by name, and within each repository those that need the user first.
func MyPRs(snap Snapshot, f PRFilter) []PRRow {
	var out []PRRow
	// Jira can link one pull request to several tickets; it is listed once, under the ticket its branch names.
	at := map[string]int{}
	named := func(r PRRow) bool { return slices.Contains(ticket.BranchKeys(r.PR.Branch), r.Ticket.Key) }
	for _, t := range Sorted(snap.Tickets) {
		if t.Team {
			continue
		}
		for _, pr := range t.PRs {
			if pr.State != ticket.PROpen || f.repoHidden(pr.Repo) {
				continue
			}
			r := PRRow{PR: &pr, Ticket: &t}
			id := fmt.Sprintf("%s#%d", strings.ToLower(pr.Repo), pr.Number)
			if i, ok := at[id]; ok {
				if named(r) && !named(out[i]) {
					out[i] = r
				}
				continue
			}
			at[id] = len(out)
			out = append(out, r)
		}
	}
	for _, pr := range snap.Others {
		id := fmt.Sprintf("%s#%d", strings.ToLower(pr.Repo), pr.Number)
		if _, ok := at[id]; ok || !pr.Yours || pr.State != ticket.PROpen || f.repoHidden(pr.Repo) {
			continue
		}
		at[id] = len(out)
		out = append(out, PRRow{PR: &pr, Other: true})
	}
	urgent := map[string]bool{}
	for _, r := range out {
		urgent[r.PR.Repo] = urgent[r.PR.Repo] || NeedsYou(*r.PR)
	}
	slices.SortStableFunc(out, func(a, b PRRow) int {
		if c := boolRank(urgent[b.PR.Repo]) - boolRank(urgent[a.PR.Repo]); c != 0 {
			return c
		}
		if c := strings.Compare(a.PR.Repo, b.PR.Repo); c != 0 {
			return c
		}
		return boolRank(NeedsYou(*b.PR)) - boolRank(NeedsYou(*a.PR))
	})
	return out
}

// Reviews are the review requests the PRs tab lists, those of hidden repositories left out.
func Reviews(snap Snapshot, f PRFilter) []ticket.PullRequest {
	reviews := snap.Reviews
	if !f.Bots {
		reviews = snap.WithoutBots().Reviews
	}
	return slices.DeleteFunc(slices.Clone(reviews), func(pr ticket.PullRequest) bool { return f.repoHidden(pr.Repo) })
}

// Others are the open pull requests of everyone else, listed while Team is on, filtered as reviews are.
func Others(snap Snapshot, f PRFilter) []ticket.PullRequest {
	others := snap.Others
	if !f.Bots {
		others = snap.WithoutBots().Others
	}
	return slices.DeleteFunc(slices.Clone(others), func(pr ticket.PullRequest) bool { return pr.Yours || f.repoHidden(pr.Repo) })
}

// HiddenBots counts the review requests and others' pull requests opened by bots.
func HiddenBots(snap Snapshot) int {
	shown := snap.WithoutBots()
	return len(snap.Reviews) + len(snap.Others) - len(shown.Reviews) - len(shown.Others)
}

// PRGroups lays the PRs tab out: the user's pull requests per repository, then the review requests per
// repository; with Team on, review requests join everyone else's pull requests in their repository's group.
func PRGroups(snap Snapshot, f PRFilter) []PRGroup {
	var out []PRGroup
	add := func(group, title string, rows []PRRow) {
		g := PRGroup{Group: group, Title: title, Rows: rows}
		for _, r := range rows {
			if r.Review() || r.Own() && NeedsYou(*r.PR) {
				g.Needs++
			}
		}
		out = append(out, g)
	}
	mine := MyPRs(snap, f)
	for i := 0; i < len(mine); {
		j, repo := i, mine[i].PR.Repo
		for j < len(mine) && mine[j].PR.Repo == repo {
			j++
		}
		title := "Mine"
		if repo != "" {
			title += " · " + repo
		}
		add("mine:"+repo, title, mine[i:j])
		i = j
	}
	var rest []PRRow
	for _, pr := range Reviews(snap, f) {
		rest = append(rest, PRRow{PR: &pr})
	}
	group, title := "review:", "Review requested · "
	if f.Team {
		for _, pr := range Others(snap, f) {
			rest = append(rest, PRRow{PR: &pr, Other: true})
		}
		group, title = "team:", ""
	}
	slices.SortStableFunc(rest, func(a, b PRRow) int { return strings.Compare(a.PR.Repo, b.PR.Repo) })
	for i := 0; i < len(rest); {
		j, repo := i, rest[i].PR.Repo
		for j < len(rest) && rest[j].PR.Repo == repo {
			j++
		}
		add(group+repo, title+repo, rest[i:j])
		i = j
	}
	return out
}

// Repos counts, per repository the board knows (the configured ones and any with pull requests), its open pull
// requests and review requests; the Repos filter offers them.
func Repos(snap Snapshot, configured []string) map[string]int {
	repos := map[string]int{}
	add := func(pr ticket.PullRequest) {
		if pr.Repo == "" {
			return
		}
		if _, ok := repos[pr.Repo]; !ok {
			repos[pr.Repo] = 0
		}
		if pr.State == ticket.PROpen || pr.State == "" {
			repos[pr.Repo]++
		}
	}
	for _, t := range snap.Tickets {
		for _, pr := range t.PRs {
			add(pr)
		}
	}
	for _, pr := range snap.Reviews {
		add(pr)
	}
	for _, r := range configured {
		add(ticket.PullRequest{Repo: r, State: ticket.PRMerged})
	}
	return repos
}

func NeedsYou(pr ticket.PullRequest) bool { return len(pr.Attention()) > 0 }

func boolRank(b bool) int {
	if b {
		return 1
	}
	return 0
}

// PRStatus words where one of the user's pull requests stands: what needs the user first, then whether it
// can merge, then the review, as the CI mark beside it already shows a running check.
func PRStatus(pr ticket.PullRequest) string {
	switch {
	case pr.Checks == ticket.ChecksFail:
		return string(ticket.ReasonCIFailing)
	case pr.Review == ticket.ReviewChangesRequested:
		return string(ticket.ReasonChangesRequested)
	case pr.Merge == ticket.MergeConflicts:
		return string(ticket.ReasonConflicts)
	case pr.Unresolved > 0:
		return fmt.Sprintf("%d unresolved", pr.Unresolved)
	case pr.Draft:
		return "Draft"
	case pr.Merge == ticket.MergeReady:
		return "Ready to merge"
	case pr.Review == ticket.ReviewApproved:
		return "Approved"
	case pr.Review == ticket.ReviewRequired:
		return "Waiting review"
	case pr.Checks == ticket.ChecksPending:
		return "CI running"
	}
	return "Open"
}

// Folded reports whether a group's rows are hidden: those collapsed, except the Backlog's sprintless group,
// folded until collapsed records it open.
func Folded(collapsed map[string]bool, group string) bool {
	return collapsed[group] != (group == BacklogGroupKey)
}

// Status is the row's standing in the PRs tab: a review request, else where the pull request stands.
func (r PRRow) Status() string {
	if r.Review() {
		return "Review requested"
	}
	return PRStatus(*r.PR)
}
