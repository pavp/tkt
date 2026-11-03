package workspace

// StartChoices is what a Start must ask before it runs: several worktrees to choose from, or the
// repositories to start in. Both empty means Start can run without asking.
type StartChoices struct {
	Worktrees []WorktreeChoice
	Repos     []RepoChoice
	// Opens says Start opens an agent rather than going to one already running, so it asks which.
	Opens bool
}

// WorktreeChoice is one worktree of the ticket; Agent is the status of the agent working in it, if any.
type WorktreeChoice struct {
	Repo, Branch, Path, Agent string
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
	// Agent is the agent kind to open, empty for Start to pick the only one installed.
	Agent string
	// Clone answers whether to clone a repository with no local clone; nil asks.
	Clone *bool
}

// NeedsAnswer is a Start that stopped at a question no terminal can answer: Need names it.
type NeedsAnswer struct{ Need string }

const (
	NeedClone    = "clone"
	NeedWorktree = "worktree"
	NeedRepos    = "repos"
	NeedAgent    = "agent"
)

func (e NeedsAnswer) Error() string { return "start needs an answer for " + e.Need }
