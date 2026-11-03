package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/pavp/tkt/internal/herdrport"
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
	// Desktop is set when Herdr desktop ran the Start: its panes mean nothing to Herdr 0.9.5, nor the reverse.
	Desktop bool `json:"desktop,omitempty"`
}

const handoffTTL = time.Hour

// takeHandoff removes and returns the pending handoff for key in repo that Herdr desktop (or Herdr 0.9.5) saved,
// dropping stale ones on the way.
func (a *App) takeHandoff(key ticket.Key, repo string, desktop bool) (handoff, bool) {
	st := a.loadState()
	var found handoff
	ok := false
	kept := st.Handoffs[:0]
	for _, ho := range st.Handoffs {
		switch {
		case a.Now().Sub(ho.At) > handoffTTL:
		case !ok && ho.Key == key && strings.EqualFold(ho.Repo, repo) && ho.Desktop == desktop:
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
	if a.handedOff(ctx, h, ho, a.startHandoff(ctx, h, ho, ws)) {
		a.closeHandedOff(ctx, h, ho)
	}
}

// handedOff reports whether the new agent started, telling the user otherwise; Herdr desktop cannot notify, so
// there the printed line is the message.
func (a *App) handedOff(ctx context.Context, h HerdrAPI, ho handoff, err error) bool {
	if err == nil {
		return true
	}
	a.tellHandoff(ctx, h, fmt.Sprintf("could not hand %s to an agent in its worktree (%v); its agent stays in pane %s", ho.Key, err, ho.Pane))
	return false
}

func (a *App) closeHandedOff(ctx context.Context, h HerdrAPI, ho handoff) {
	if err := closeSetupAgent(ctx, h, ho, func(string, ...any) {}); err != nil {
		a.tellHandoff(ctx, h, fmt.Sprintf("could not close the setup agent of %s in pane %s: %v", ho.Key, ho.Pane, err))
	}
}

func (a *App) tellHandoff(ctx context.Context, h HerdrAPI, msg string) {
	fmt.Fprintf(a.Stderr, "tkt: %s\n", msg)
	_ = h.Notify(ctx, "tkt", msg)
}

// agentSpaces is a Herdr that opens a space with an agent already running and its first message (Herdr desktop).
// It has no worktree command nor worktree event, so its setup agent creates the worktree with git and `tkt link`
// completes the handoff.
type agentSpaces interface {
	OpenAgentSpace(ctx context.Context, folder, name, kind, task string) (herdrport.Workspace, herdrport.Pane, error)
	RunDetached(ctx context.Context, cwd string, argv []string) error
}

// closeJob is what the detached closer gets: the handoff, and the new agent to bring forward once the setup agent
// is gone, since closing it can move the window's focus.
type closeJob struct {
	Handoff handoff `json:"handoff"`
	Space   string  `json:"space"`
	Pane    string  `json:"pane"`
}

// desktopSetupPrompt is workspace.SetupPrompt for Herdr desktop, which cannot create worktrees: the setup agent
// creates it with git and registers it, which hands the ticket on.
func desktopSetupPrompt(key ticket.Key, summary string) string {
	return fmt.Sprintf(`Prepare Jira ticket %[1]s: %[2]s

Another agent does the work; your only job is to create its worktree.
1. Following this repository's own conventions for branch names, base branch and tooling, choose the branch and its base. The branch name must contain %[1]s.
2. Create the worktree with git where this repository's rules put worktrees; when they say nothing, use ~/.herdr/worktrees/<repository folder name>/<branch with every / replaced by ->.
3. Register it from inside the new worktree: tkt link %[1]s
4. Stop there. tkt starts an agent in a new space at the worktree and closes this one.`, key, summary)
}

// handOffFromLink completes a pending Start handoff for key in repo once its setup agent ran `tkt link` in the
// worktree at dir, on Herdr desktop. The setup agent runs this very command, so a closer herdr-core runs closes it.
func (a *App) handOffFromLink(ctx context.Context, key ticket.Key, repo, dir string) {
	// The Herdr 0.9.5 hook never sees the main checkout, which herdr worktree create cannot return; here the setup
	// agent may link it, so the Start keeps waiting for a real worktree instead of taking the checkout.
	for _, ho := range a.loadState().Handoffs {
		if ho.Desktop && ho.Key == key && strings.EqualFold(ho.Repo, repo) && filepath.Clean(ho.Clone) == filepath.Clean(dir) {
			fmt.Fprintf(a.Stderr, "tkt: %s is the main checkout, and the main checkout stays free: create a separate worktree for %s with git and run `tkt link %s` there; the Start keeps waiting\n", dir, key, key)
			return
		}
	}
	ho, ok := a.takeHandoff(key, repo, true)
	if !ok {
		return
	}
	h := a.NewHerdr()
	s, ok := h.(agentSpaces)
	if !ok {
		a.handedOff(ctx, h, ho, errors.New("this Herdr cannot open a space with an agent"))
		return
	}
	pane, err := a.startSpaceHandoff(ctx, h, s, ho, dir)
	if !a.handedOff(ctx, h, ho, err) {
		return
	}
	b, err := json.Marshal(closeJob{Handoff: ho, Space: pane.WorkspaceID, Pane: pane.ID})
	exe := ""
	if err == nil {
		exe, err = a.Executable()
	}
	if err == nil {
		data := a.Env.DataDir()
		err = s.RunDetached(ctx, data, []string{"sh", "-c", `exec "$0" herdr close-setup "$1" >>"$2" 2>&1`, exe, string(b), filepath.Join(data, "handoff.log")})
	}
	if err != nil {
		a.tellHandoff(ctx, h, fmt.Sprintf("could not close the setup agent of %s in pane %s: %v", ho.Key, ho.Pane, err))
		return
	}
	fmt.Fprintf(a.Stderr, "tkt: %s goes on in a new space at %s; this setup agent closes now\n", ho.Key, dir)
}

// startSpaceHandoff names the new space after the worktree's folder, which tells it from the setup agent's space.
func (a *App) startSpaceHandoff(ctx context.Context, h HerdrAPI, s agentSpaces, ho handoff, dir string) (herdrport.Pane, error) {
	label := string(ho.Key) + " · " + filepath.Base(dir)
	_, pane, err := s.OpenAgentSpace(ctx, dir, label, ho.Kind, workspace.HandoffPrompt(ho.Key, ho.Summary, ho.Clone))
	if err != nil {
		return pane, err
	}
	markAgent(ctx, h, pane.ID, ho.Key)
	focusAgent(ctx, h, pane)
	return pane, nil
}

func focusAgent(ctx context.Context, h HerdrAPI, pane herdrport.Pane) error {
	return errors.Join(h.FocusWorkspace(ctx, pane.WorkspaceID), h.FocusPane(ctx, pane.ID))
}

// closeSetup is the detached half of handOffFromLink, logging each step to handoff.log. It waits for `tkt link` to
// return first, as closing the setup agent ends the terminal it runs in, then brings the new agent forward again.
func (a *App) closeSetup(ctx context.Context, raw string) error {
	var job closeJob
	if err := json.Unmarshal([]byte(raw), &job); err != nil || job.Handoff.Pane == "" {
		return fmt.Errorf("not a handoff: %q", raw)
	}
	ho := job.Handoff
	logf := func(format string, args ...any) {
		a.printf("%s close-setup %s: %s\n", a.Now().Format(time.RFC3339), ho.Key, fmt.Sprintf(format, args...))
	}
	logf("closing the setup agent %s (space %q, first pane %q)", ho.Pane, ho.Workspace, ho.Host)
	if err := a.Sleep(ctx, 2*time.Second); err != nil {
		return err
	}
	// herdr-core's environment may not say which Herdr runs it, and only Herdr desktop starts this closer.
	h := a.NewHerdr()
	if a.herdrOf != nil {
		h = a.herdrOf(herdrDesktop)
	}
	if err := closeSetupAgent(ctx, h, ho, logf); err != nil {
		logf("failed: %v", err)
		a.tellHandoff(ctx, h, fmt.Sprintf("could not close the setup agent of %s in pane %s: %v", ho.Key, ho.Pane, err))
	}
	if job.Pane != "" {
		if err := focusAgent(ctx, h, herdrport.Pane{ID: job.Pane, WorkspaceID: job.Space}); err != nil {
			logf("could not focus %s in %s: %v", job.Pane, job.Space, err)
		} else {
			logf("focused %s in %s", job.Pane, job.Space)
		}
	}
	return nil
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
	var root []herdrport.Pane
	for _, p := range panes {
		if p.WorkspaceID == ws {
			root = append(root, p)
		}
	}
	if len(root) != 1 || slices.ContainsFunc(agents, func(ag herdrport.Agent) bool { return ag.PaneID == root[0].ID }) {
		return fmt.Errorf("workspace %s has no single empty pane to start the agent in", ws)
	}
	name, pane, err := a.startAgent(ctx, h, root[0].ID, ho.Kind)
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
func closeSetupAgent(ctx context.Context, h HerdrAPI, ho handoff, logf func(string, ...any)) error {
	agents, err := h.Agents(ctx)
	if err != nil {
		return err
	}
	if slices.ContainsFunc(agents, func(ag herdrport.Agent) bool { return ag.PaneID == ho.Pane && ag.Name != ho.Agent }) {
		return fmt.Errorf("another agent runs there now")
	}
	if err := h.ClosePane(ctx, ho.Pane); err != nil {
		return err
	}
	logf("closed pane %s", ho.Pane)
	if ho.Workspace == "" {
		return nil
	}
	panes, err := h.Panes(ctx)
	if err != nil {
		return err
	}
	left := slices.DeleteFunc(panes, func(p herdrport.Pane) bool { return p.WorkspaceID != ho.Workspace || p.ID == ho.Pane })
	if len(left) != 1 || left[0].ID != ho.Host {
		return nil
	}
	if err := h.CloseWorkspace(ctx, ho.Workspace); err != nil {
		return fmt.Errorf("its workspace %s stays: %w", ho.Workspace, err)
	}
	logf("closed space %s", ho.Workspace)
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
