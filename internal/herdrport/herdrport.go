// Package herdrport holds what tkt's Herdr port speaks, so every Herdr adapter shares one set of types.
package herdrport

import (
	"encoding/json"
	"errors"
)

// Errors an adapter returns, matched with errors.Is, for the cases tkt handles rather than reports.
var (
	ErrNameTaken = errors.New("herdr: agent name already used")
	ErrPaneBusy  = errors.New("herdr: pane is busy")
	// ErrNotReady means Herdr started the agent but it is showing a question, such as Claude Code's folder trust prompt.
	ErrNotReady = errors.New("herdr: agent not ready")
	ErrBlocked  = errors.New("herdr: agent is blocked")
	// ErrNotNamed means the agent's name is not active yet, which lags behind starting it on some systems.
	ErrNotNamed = errors.New("herdr: not an active named agent")
)

type Worktree struct {
	CheckoutPath string
	RepoName     string
	RepoRoot     string
	Linked       bool
}

type Workspace struct {
	ID       string
	Label    string
	Tokens   map[string]string
	Worktree *Worktree
}

type Pane struct {
	ID          string
	WorkspaceID string
	Cwd         string
	Focused     bool
}

type Agent struct {
	PaneID      string
	WorkspaceID string
	Agent       string
	Name        string
	Status      string
	Cwd         string
	// Tokens are the values plugins reported on the agent's pane, such as the ticket tkt gave it.
	Tokens map[string]string
}

// Subscription names a Herdr event to follow; agent status changes are followed per pane.
type Subscription struct {
	Type   string
	PaneID string
}

// Event is one event Herdr streams; Data is its payload as the adapter received it.
type Event struct {
	Kind string
	Data json.RawMessage
}
