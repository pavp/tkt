package board

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
