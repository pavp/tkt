package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
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
	// Workspace is set when tkt opened a workspace only for the setup agent, Host being the pane it opened with.
	Workspace string    `json:"workspace,omitempty"`
	Host      string    `json:"host,omitempty"`
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
		msg := fmt.Sprintf("could not close the setup agent of %s in pane %s: %v", ho.Key, ho.Pane, err)
		fmt.Fprintf(a.Stderr, "tkt: %s\n", msg)
		_ = h.Notify(ctx, "tkt", msg)
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
	name, err := a.startAgent(ctx, h, pane, ho.Kind)
	if err != nil && !notReady(err) {
		return err
	}
	markAgent(ctx, h, pane, ho.Key)
	_ = h.FocusWorkspace(ctx, ws)
	_ = h.FocusPane(ctx, pane)
	prompted, err := a.promptAgent(ctx, h, name, "workspace "+ws, workspace.HandoffPrompt(ho.Key, ho.Summary, ho.Clone), err)
	if err == nil && !prompted {
		err = fmt.Errorf("the agent in %s did not get its prompt", pane)
	}
	return err
}

// closeSetupAgent closes the setup agent's pane, then the workspace tkt opened for it once nothing but the pane it
// opened with is left, and leaves the pane alone once another agent runs there.
func closeSetupAgent(ctx context.Context, h HerdrAPI, ho handoff) error {
	agents, err := h.Agents(ctx)
	if err != nil {
		return err
	}
	if slices.ContainsFunc(agents, func(ag herdr.Agent) bool { return ag.PaneID == ho.Pane && ag.Name != ho.Agent }) {
		return fmt.Errorf("another agent runs there now")
	}
	if err := h.ClosePane(ctx, ho.Pane); err != nil {
		return err
	}
	if ho.Workspace == "" {
		return nil
	}
	panes, err := h.Panes(ctx)
	if err != nil {
		return err
	}
	left := slices.DeleteFunc(panes, func(p herdr.Pane) bool { return p.WorkspaceID != ho.Workspace || p.ID == ho.Pane })
	if len(left) != 1 || left[0].ID != ho.Host {
		return nil
	}
	if err := h.CloseWorkspace(ctx, ho.Workspace); err != nil {
		return fmt.Errorf("its workspace %s stays: %w", ho.Workspace, err)
	}
	return nil
}

// eventWorkspace is the id of the workspace a worktree event opened: the shallowest "workspace" in the payload,
// since a worktree's own details may name its source checkout's workspace further down.
func eventWorkspace(raw string) string {
	var v any
	if json.Unmarshal([]byte(raw), &v) != nil {
		return ""
	}
	level := []any{v}
	for len(level) > 0 {
		var next []any
		for _, x := range level {
			m, ok := x.(map[string]any)
			if !ok {
				continue
			}
			if w, ok := m["workspace"].(map[string]any); ok {
				if id, ok := w["workspace_id"].(string); ok && id != "" {
					return id
				}
			}
			for _, k := range slices.Sorted(maps.Keys(m)) {
				next = append(next, m[k])
			}
		}
		level = next
	}
	return ""
}
