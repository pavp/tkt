package tui

// StartChoices is what a Start must ask before it runs: several worktrees to choose from, or the
// repositories to start in. Both empty means Start can run without asking.
type StartChoices struct {
	Worktrees []WorktreeChoice
	Repos     []RepoChoice
}

// WorktreeChoice is one worktree of the ticket; Agent is the status of the agent working in it, if any.
type WorktreeChoice struct {
	Branch, Path, Agent string
}

// RepoChoice is one repository of the ticket's space; Checked ones are offered preselected.
type RepoChoice struct {
	Name              string
	LastUsed, Checked bool
}

// StartOptions carry the answers to StartChoices into Start.
type StartOptions struct {
	Repos    []string
	Worktree string
}
