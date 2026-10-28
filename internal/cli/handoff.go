package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/pavp/tkt/internal/herdr"
	"github.com/pavp/tkt/internal/ticket"
	"github.com/pavp/tkt/internal/workspace"
)

// handoff is a Start waiting for the worktree its setup agent creates, so tkt can give the ticket to an agent born
// in that worktree: an agent cannot change its own folder or workspace.
type handoff struct {
	Key     ticket.Key `json:"key"`
	Summary string     `json:"summary"`
	Repo    string     `json:"repo"`
	Clone   string     `json:"clone"`
	Pane    string     `json:"pane"`
	Agent   string     `json:"agent"`
	Kind    string     `json:"kind"`
	// Workspace is set when tkt opened a workspace only for the setup agent.
	Workspace string    `json:"workspace,omitempty"`
	At        time.Time `json:"at"`
}

const handoffTTL = time.Hour

// takeHandoff removes and returns the pending handoff for key in repo, dropping stale ones on the way.
func (a *App) takeHandoff(key ticket.Key, repo string) (handoff, bool) {
	st := a.loadState()
	var found handoff
	ok := false
	kept := st.Handoffs[:0]
	for _, ho := range st.Handoffs {
		switch {
		case a.Now().Sub(ho.At) > handoffTTL:
		case !ok && ho.Key == key && strings.EqualFold(ho.Repo, repo):
			found, ok = ho, true
		default:
			kept = append(kept, ho)
		}
	}
	if ok || len(kept) != len(st.Handoffs) {
		st.Handoffs = kept
		a.saveState(st)
	}
	return found, ok
}

// handOff starts the setup agent's kind in the worktree workspace's root pane, then closes the setup agent. On any
// failure the setup agent stays and the user is told, since a half-done handoff must never lose the only agent.
func (a *App) handOff(ctx context.Context, ho handoff, ws string) {
	h := a.NewHerdr()
	if err := a.startHandoff(ctx, h, ho, ws); err != nil {
		msg := fmt.Sprintf("could not hand %s to an agent in its worktree (%v); its agent stays in pane %s", ho.Key, err, ho.Pane)
		fmt.Fprintf(a.Stderr, "tkt: %s\n", msg)
		_ = h.Notify(ctx, "tkt", msg)
		return
	}
	if err := closeSetupAgent(ctx, h, ho); err != nil {
		fmt.Fprintf(a.Stderr, "tkt: could not close the setup agent in pane %s: %v\n", ho.Pane, err)
	}
}

func (a *App) startHandoff(ctx context.Context, h HerdrAPI, ho handoff, ws string) error {
	if ws == "" {
		return errors.New("the worktree event names no workspace")
	}
	panes, err := h.Panes(ctx)
	if err != nil {
		return err
	}
	agents, err := h.Agents(ctx)
	if err != nil {
		return err
	}
	var root []herdr.Pane
	for _, p := range panes {
		if p.WorkspaceID == ws {
			root = append(root, p)
		}
	}
	if len(root) != 1 || slices.ContainsFunc(agents, func(ag herdr.Agent) bool { return ag.PaneID == root[0].ID }) {
		return fmt.Errorf("workspace %s has no single empty pane to start the agent in", ws)
	}
	pane := root[0].ID
	name, err := startAgent(ctx, h, pane, ho.Kind)
	if err != nil && !notReady(err) {
		return err
	}
	markAgent(ctx, h, pane, ho.Key)
	_ = h.FocusWorkspace(ctx, ws)
	_ = h.FocusPane(ctx, pane)
	return a.promptStarted(ctx, h, name, "workspace "+ws, workspace.HandoffPrompt(ho.Key, ho.Summary, ho.Clone), err)
}

// closeSetupAgent closes the workspace tkt opened for the setup agent while the agent's pane is all it holds, else
// only that pane, and leaves a pane alone once another agent runs there.
func closeSetupAgent(ctx context.Context, h HerdrAPI, ho handoff) error {
	agents, err := h.Agents(ctx)
	if err != nil {
		return err
	}
	if slices.ContainsFunc(agents, func(ag herdr.Agent) bool { return ag.PaneID == ho.Pane && ag.Name != ho.Agent }) {
		return fmt.Errorf("another agent runs there now")
	}
	if ho.Workspace != "" {
		panes, err := h.Panes(ctx)
		if err != nil {
			return err
		}
		alone := !slices.ContainsFunc(panes, func(p herdr.Pane) bool { return p.WorkspaceID == ho.Workspace && p.ID != ho.Pane })
		if alone {
			return h.CloseWorkspace(ctx, ho.Workspace)
		}
	}
	return h.ClosePane(ctx, ho.Pane)
}

// eventWorkspace is the id of the workspace a worktree event opened.
func eventWorkspace(raw string) string {
	var v any
	if json.Unmarshal([]byte(raw), &v) != nil {
		return ""
	}
	var walk func(any) string
	walk = func(x any) string {
		m, ok := x.(map[string]any)
		if !ok {
			return ""
		}
		if w, ok := m["workspace"].(map[string]any); ok {
			if id, ok := w["workspace_id"].(string); ok && id != "" {
				return id
			}
		}
		for _, child := range m {
			if id := walk(child); id != "" {
				return id
			}
		}
		return ""
	}
	return walk(v)
}
