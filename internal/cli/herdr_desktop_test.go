package cli

import (
	"context"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/pavp/tkt/internal/herdrcore"
	"github.com/pavp/tkt/internal/herdrport"
)

// desktopHerdr fakes Herdr desktop as internal/cli sees it: each agent opens in a terminal of its own (t9) beside
// the pane it is given, layout calls fail with err once the window is closed, and nothing can notify.
type desktopHerdr struct {
	HerdrAPI // nil: a call no test expects panics
	panes    []herdrport.Pane
	agents   []herdrport.Agent
	wss      []herdrport.Workspace
	window   error
	calls    []string
}

func (d *desktopHerdr) did(f string, a ...any) { d.calls = append(d.calls, fmt.Sprintf(f, a...)) }

func (d *desktopHerdr) Panes(context.Context) ([]herdrport.Pane, error)   { return d.panes, nil }
func (d *desktopHerdr) Agents(context.Context) ([]herdrport.Agent, error) { return d.agents, nil }
func (d *desktopHerdr) Workspaces(context.Context) ([]herdrport.Workspace, error) {
	return d.wss, d.window
}
func (d *desktopHerdr) SplitPane(_ context.Context, pane, cwd string) (herdrport.Pane, error) {
	d.did("split %s", pane)
	return herdrport.Pane{ID: "t8", Cwd: cwd}, d.window
}
func (d *desktopHerdr) StartAgent(_ context.Context, pane, kind, name string, _ []string) error {
	d.did("start %s %s", name, pane)
	d.agents = append(d.agents, herdrport.Agent{PaneID: "t9", Agent: kind, Name: name, Status: "idle"})
	return nil
}
func (d *desktopHerdr) AgentPane(_ context.Context, name string) (string, error) {
	if i := slices.IndexFunc(d.agents, func(ag herdrport.Agent) bool { return ag.Name == name }); i >= 0 {
		return d.agents[i].PaneID, nil
	}
	return "", herdrcore.ErrNotFound
}
func (d *desktopHerdr) ReportPaneTokens(_ context.Context, pane, _ string, tokens map[string]*string) error {
	d.did("mark %s %s", pane, *tokens[ticketToken])
	return nil
}
func (d *desktopHerdr) FocusPane(_ context.Context, pane string) error {
	d.did("focus %s", pane)
	return d.window
}
func (d *desktopHerdr) FocusWorkspace(_ context.Context, id string) error {
	d.did("focus workspace %s", id)
	return d.window
}
func (d *desktopHerdr) ClosePane(_ context.Context, pane string) error {
	d.did("close %s", pane)
	return nil
}
func (d *desktopHerdr) Prompt(_ context.Context, target, _ string) error {
	d.did("prompt %s", target)
	return nil
}
func (d *desktopHerdr) Notify(context.Context, string, string) error {
	return herdrcore.ErrNotifyUnsupported
}

func desktopHarness(t *testing.T) (*harness, *desktopHerdr) {
	t.Helper()
	h, _ := workHarness(t)
	d := &desktopHerdr{}
	h.app.NewHerdr = func() HerdrAPI { return d }
	h.app.Sleep = func(context.Context, time.Duration) error { return nil }
	h.env["HC_TERMINAL_ID"] = "t5"
	return h, d
}

func TestHerdrStartOnDesktopMarksTheAgentsOwnTerminal(t *testing.T) {
	h, d := desktopHarness(t)
	web := h.clone(t, "web")
	if code := h.exec("herdr", "start", "DEMO-7", "--repo", "web", "--agent", "claude"); code != 0 {
		t.Fatalf("exit %d: %s", code, h.errOut)
	}
	want := []string{"split t5", "focus t8", "start claude t8", "close t8", "focus t9", "mark t9 DEMO-7", "prompt claude"}
	if !reflect.DeepEqual(d.calls, want) {
		t.Errorf("calls = %q\nwant    %q", d.calls, want)
	}
	if hos := h.app.loadState().Handoffs; len(hos) != 1 || hos[0].Pane != "t9" || hos[0].Clone != web {
		t.Errorf("handoffs = %+v", hos)
	}
}

func TestBoardTaskOnDesktopGoesToTheAgentsOwnTerminal(t *testing.T) {
	h, d := desktopHarness(t)
	d.panes = []herdrport.Pane{{ID: "t1", WorkspaceID: "sp1", Cwd: "/src/web"}}
	pane, err := h.app.startTaskAgent(context.Background(), d, "claude", "/src/web", "DEMO-7", "fix it")
	if err != nil || pane != "t9" {
		t.Fatalf("startTaskAgent = %q, %v", pane, err)
	}
	if want := []string{"split t1", "start claude t8", "close t8", "prompt claude"}; !reflect.DeepEqual(d.calls, want) {
		t.Errorf("calls = %q\nwant    %q", d.calls, want)
	}
}

func TestHerdrStartOnDesktopFindsTheSpaceByItsFolder(t *testing.T) {
	h, d := desktopHarness(t)
	h.clone(t, "web")
	h.run.out["git worktree list --porcelain"] = "worktree /src/web-DEMO-1\nHEAD a\nbranch refs/heads/feature/DEMO-1-filter\n"
	d.wss = []herdrport.Workspace{{ID: "sp3", Label: "web-DEMO-1", Folder: "/src/web-DEMO-1/"}}
	if code := h.exec("herdr", "start", "DEMO-1"); code != 0 {
		t.Fatalf("exit %d: %s", code, h.errOut)
	}
	if want := []string{"focus workspace sp3"}; !reflect.DeepEqual(d.calls, want) {
		t.Errorf("calls = %q, want %q", d.calls, want)
	}
}

func TestHerdrStartOnDesktopWithTheWindowClosed(t *testing.T) {
	h, d := desktopHarness(t)
	h.clone(t, "web")
	h.run.out["git worktree list --porcelain"] = "worktree /src/web-DEMO-1\nHEAD a\nbranch refs/heads/feature/DEMO-1-filter\n"
	d.window = herdrcore.ErrNoWindow
	d.agents = []herdrport.Agent{{PaneID: "t9", WorkspaceID: "sp3", Agent: "claude", Name: "claude", Cwd: "/src/web-DEMO-1"}}
	if code := h.exec("herdr", "start", "DEMO-1"); code != 0 {
		t.Fatalf("exit %d: %s", code, h.errOut)
	}
	if out := h.out.String(); !strings.Contains(out, "Focusing the agent of DEMO-1") || !strings.Contains(out, "! herdr desktop window is not open: open Herdr") {
		t.Errorf("output = %q", out)
	}
}

func TestHerdrOpenOnDesktopPrintsWhatItCannotNotify(t *testing.T) {
	h, _ := desktopHarness(t)
	if code := h.exec("herdr", "open", "https://github.com/example-org/web/pull/999"); code != 0 {
		t.Fatalf("exit %d: %s", code, h.errOut)
	}
	if !strings.Contains(h.out.String(), "No ticket found for https://github.com/example-org/web/pull/999") {
		t.Errorf("output = %q", h.out)
	}
}

func TestFixOnDesktopSendsToTheTicketsAgent(t *testing.T) {
	h, _, _ := fixHarness(t)
	d := &desktopHerdr{agents: []herdrport.Agent{{PaneID: "t9", WorkspaceID: "sp3", Agent: "claude", Name: "claude", Cwd: "/src/web-DEMO-1"}}}
	h.app.NewHerdr = func() HerdrAPI { return d }
	h.env["HC_TERMINAL_ID"] = "t5"
	h.clone(t, "web")
	h.run.out["git worktree list --porcelain"] = "worktree /src/web-DEMO-1\nHEAD a\nbranch refs/heads/feature/DEMO-1-filter\n"
	if code := h.exec("fix", "DEMO-1"); code != 0 {
		t.Fatalf("exit %d: %s", code, h.errOut)
	}
	if !slices.Contains(d.calls, "prompt t9") || !strings.Contains(h.out.String(), "✓ Sent DEMO-1's feedback to its agent") {
		t.Errorf("calls = %q, output = %q", d.calls, h.out)
	}
}

func TestAgentKindsOnDesktopAreTheInstalledKnownAgents(t *testing.T) {
	h, _ := desktopHarness(t)
	h.run.missing["herdr"] = true
	for _, k := range desktopKinds {
		h.run.missing[k] = k != "claude" && k != "opencode"
	}
	kinds, err := h.app.agentKinds(context.Background())
	if err != nil || !reflect.DeepEqual(kinds, []string{"claude", "opencode"}) {
		t.Errorf("agentKinds = %q, %v", kinds, err)
	}
	h.run.missing["claude"], h.run.missing["opencode"] = true, true
	if _, err := h.app.agentKinds(context.Background()); err == nil || !strings.Contains(err.Error(), "no agent Herdr desktop can start is installed; install one of: pi, claude") {
		t.Errorf("none installed: %v", err)
	}
}
