package board

import "slices"

const (
	ViewColumns = "columns"
	ViewList    = "list"
)

// SavedState is what the board remembers between openings, in board-state.json, which the TUI and the daemon's
// PATCH /v1/board/state both write.
type SavedState struct {
	// HiddenRepos lists the repositories ("owner/name") whose pull requests the PRs filter hides.
	HiddenRepos []string `json:"hidden_repos,omitempty"`
	// HiddenBoards lists the team boards whose tickets the board hides outside the PRs filter.
	HiddenBoards []string `json:"hidden_boards,omitempty"`
	// Collapsed lists the PRs filter's collapsed groups, such as "mine:owner/name" or "review:owner/name".
	Collapsed []string `json:"collapsed,omitempty"`
	// View is the view the user last chose, ViewColumns or ViewList.
	View string `json:"view,omitempty"`
	// Team asks the sync for the team's tickets too, those assigned to others or to nobody.
	Team bool `json:"team,omitempty"`
	// TeamPRs shows everyone else's open pull requests in the PRs filter; unset, it follows Team, as
	// one toggle did both before.
	TeamPRs *bool `json:"team_prs,omitempty"`
	// BacklogMerged groups the backlog's sprints by name instead of one group per sprint and board.
	BacklogMerged bool `json:"backlog_merged,omitempty"`
}

// TeamPRsShown reports whether the PRs filter shows everyone else's pull requests, following Team while unset.
func (s SavedState) TeamPRsShown() bool {
	if s.TeamPRs != nil {
		return *s.TeamPRs
	}
	return s.Team
}

// MergeSaved is mine saved over theirs, the file as another program left it since base was read: each field
// mine did not change from base keeps theirs, and the lists keep the other side's additions and removals.
func MergeSaved(base, mine, theirs SavedState) SavedState {
	out := theirs
	out.HiddenRepos = mergeSet(base.HiddenRepos, mine.HiddenRepos, theirs.HiddenRepos)
	out.HiddenBoards = mergeSet(base.HiddenBoards, mine.HiddenBoards, theirs.HiddenBoards)
	out.Collapsed = mergeSet(base.Collapsed, mine.Collapsed, theirs.Collapsed)
	if mine.View != base.View {
		out.View = mine.View
	}
	if mine.Team != base.Team {
		out.Team = mine.Team
	}
	if mine.TeamPRsShown() != base.TeamPRsShown() || (mine.TeamPRs == nil) != (base.TeamPRs == nil) {
		out.TeamPRs = mine.TeamPRs
	}
	if mine.BacklogMerged != base.BacklogMerged {
		out.BacklogMerged = mine.BacklogMerged
	}
	return out
}

func mergeSet(base, mine, theirs []string) []string {
	in := func(list []string, v string) bool { return slices.Contains(list, v) }
	var out []string
	for _, v := range theirs {
		if !in(base, v) || in(mine, v) {
			out = append(out, v)
		}
	}
	for _, v := range mine {
		if !in(base, v) && !in(out, v) {
			out = append(out, v)
		}
	}
	slices.Sort(out)
	return out
}
