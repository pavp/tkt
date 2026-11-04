package cli

import (
	"context"
	"errors"
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
	texts    []string
	spaceErr error
	startErr error
	waitErr  error
	runErr   error
	detached [][]string
	// agentsErr fails Agents, the first call the closer makes.
	agentsErr error
	// paneErr fails AgentPane, so the agent's own terminal cannot be found.
	paneErr error
}

func (d *desktopHerdr) did(f string, a ...any) { d.calls = append(d.calls, fmt.Sprintf(f, a...)) }

func (d *desktopHerdr) Panes(context.Context) ([]herdrport.Pane, error) { return d.panes, nil }
func (d *desktopHerdr) Agents(context.Context) ([]herdrport.Agent, error) {
	return d.agents, d.agentsErr
}
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
	return d.startErr
}
func (d *desktopHerdr) WaitAgent(_ context.Context, target, status string, _ time.Duration) error {
	d.did("wait %s %s", target, status)
	return d.waitErr
}
func (d *desktopHerdr) AgentPane(_ context.Context, name string) (string, error) {
	if d.paneErr != nil {
		return "", d.paneErr
	}
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
func (d *desktopHerdr) Prompt(_ context.Context, target, text string) error {
	d.did("prompt %s", target)
	d.texts = append(d.texts, text)
	return nil
}
func (d *desktopHerdr) OpenAgentSpace(_ context.Context, folder, name, kind, task string) (herdrport.Workspace, herdrport.Pane, error) {
	d.did("space %q %s %s", name, folder, kind)
	d.texts = append(d.texts, task)
	return herdrport.Workspace{ID: "sp20", Label: name}, herdrport.Pane{ID: "t20", WorkspaceID: "sp20", Cwd: folder}, d.spaceErr
}
func (d *desktopHerdr) RunDetached(_ context.Context, cwd string, argv []string) error {
	d.did("detach %s", cwd)
	d.detached = append(d.detached, argv)
	return d.runErr
}
func (d *desktopHerdr) CreateWorkspace(_ context.Context, cwd, label string, _ bool) (herdrport.Workspace, herdrport.Pane, error) {
	d.did("workspace %q %s", label, cwd)
	return herdrport.Workspace{ID: "sp1", Label: label}, herdrport.Pane{ID: "t1", WorkspaceID: "sp1", Cwd: cwd}, nil
}
func (d *desktopHerdr) CloseWorkspace(_ context.Context, id string) error {
	d.did("close workspace %s", id)
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
	want := []string{fmt.Sprintf("workspace %q %s", "", web), "focus workspace sp1", "focus t1", "start claude t1", "focus t9", "mark t9 DEMO-7", "prompt claude"}
	if !reflect.DeepEqual(d.calls, want) {
		t.Errorf("calls = %q\nwant    %q", d.calls, want)
	}
	if hos := h.app.loadState().Handoffs; len(hos) != 1 || hos[0].Pane != "t9" || hos[0].Clone != web || !hos[0].Desktop {
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

func TestHerdrStartOnDesktopWaitsForTheAnswerToTheAgentsQuestion(t *testing.T) {
	tests := []struct {
		name       string
		waitErr    error
		wantPrompt bool
		wantOut    string
	}{
		{"answered", nil, true, "✓ claude started in"},
		{"not answered in time", herdrcore.ErrTimeout, false, "! It did not get ready in time; give it this prompt:"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h, d := desktopHarness(t)
			h.clone(t, "web")
			d.startErr, d.waitErr = herdrport.ErrNotReady, tt.waitErr
			if code := h.exec("herdr", "start", "DEMO-7", "--repo", "web", "--agent", "claude"); code != 0 {
				t.Fatalf("exit %d: %s", code, h.errOut)
			}
			out := h.out.String()
			if n := strings.Count(out, "is asking a question; answer it there and the prompt follows"); n != 1 {
				t.Errorf("told the user %d times: %q", n, out)
			}
			if !strings.Contains(out, tt.wantOut) || slices.Contains(d.calls, "prompt claude") != tt.wantPrompt || !slices.Contains(d.calls, "wait claude idle") {
				t.Errorf("calls = %q, output = %q", d.calls, out)
			}
		})
	}
}

// Herdr desktop nests a worktree's space under its repository's space only while a space at the main checkout is open,
// so the setup agent opens in that space, which tkt opens with a shell first when there is none and never closes.
func TestHerdrStartOnDesktopOpensARepoSpaceWithAShellFirst(t *testing.T) {
	h, d := desktopHarness(t)
	delete(h.env, "HC_TERMINAL_ID")
	web := h.clone(t, "web")
	if code := h.exec("herdr", "start", "DEMO-7", "--repo", "web", "--agent", "claude"); code != 0 {
		t.Fatalf("exit %d: %s", code, h.errOut)
	}
	if want := fmt.Sprintf("workspace %q %s", "", web); len(d.calls) == 0 || d.calls[0] != want {
		t.Errorf("calls = %q, want first %q", d.calls, want)
	}
	if slices.Contains(d.calls, "close t1") || !slices.Contains(d.calls, "start claude t1") {
		t.Errorf("calls = %q", d.calls)
	}
	hos := h.app.loadState().Handoffs
	if len(hos) != 1 || hos[0].Workspace != "" || hos[0].Host != "" || hos[0].Pane != "t9" {
		t.Fatalf("handoffs = %+v", hos)
	}
	d.calls, d.panes = nil, []herdrport.Pane{{ID: "t1", WorkspaceID: "sp1", Cwd: web}, {ID: "t9", WorkspaceID: "sp1", Cwd: web}}
	if err := closeSetupAgent(context.Background(), d, hos[0], func(string, ...any) {}); err != nil || !reflect.DeepEqual(d.calls, []string{"close t9"}) {
		t.Errorf("closing the setup agent: %v, calls %q", err, d.calls)
	}
}

func TestHerdrStartOnDesktopOpensTheSetupAgentInTheRepoSpace(t *testing.T) {
	h, d := desktopHarness(t)
	web := h.clone(t, "web")
	d.panes = []herdrport.Pane{{ID: "t2", Cwd: web}, {ID: "t3", WorkspaceID: "sp4", Cwd: web}}
	if code := h.exec("herdr", "start", "DEMO-7", "--repo", "web", "--agent", "claude"); code != 0 {
		t.Fatalf("exit %d: %s", code, h.errOut)
	}
	want := []string{"focus workspace sp4", "focus t3", "start claude t3", "focus t9", "mark t9 DEMO-7", "prompt claude"}
	if !reflect.DeepEqual(d.calls, want) {
		t.Errorf("calls = %q\nwant    %q", d.calls, want)
	}
}

func TestHerdrStartOnDesktopNeverHandsTheRepoShellToTheCloser(t *testing.T) {
	h, d := desktopHarness(t)
	web := h.clone(t, "web")
	d.panes = []herdrport.Pane{{ID: "u5", WorkspaceID: "sp4", Cwd: web}}
	d.paneErr = errors.New("hc get failed")
	code := h.exec("herdr", "start", "DEMO-7", "--repo", "web", "--agent", "claude")
	if code == 0 || slices.Contains(d.calls, "mark u5 DEMO-7") || len(h.app.loadState().Handoffs) != 0 {
		t.Errorf("exit %d, calls %q, handoffs %+v", code, d.calls, h.app.loadState().Handoffs)
	}
	if !strings.Contains(h.errOut.String(), "could not find the setup agent's terminal") {
		t.Errorf("stderr = %q", h.errOut)
	}
}

const windowNote = "cannot reach the Herdr desktop window from this machine"

func TestHerdrStartOnDesktopWithoutTheWindowTellsOnceAndGoesOn(t *testing.T) {
	h, d := desktopHarness(t)
	h.clone(t, "web")
	d.window = herdrcore.ErrNoWindow
	if code := h.exec("herdr", "start", "DEMO-7", "--repo", "web", "--agent", "claude"); code != 0 {
		t.Fatalf("exit %d: %s", code, h.errOut)
	}
	if n := strings.Count(h.out.String()+h.errOut.String(), windowNote); n != 1 || !slices.Contains(d.calls, "prompt claude") {
		t.Errorf("told %d times, calls %q, output %q", n, d.calls, h.out)
	}
}

func TestFixOnDesktopFindsTheUnnamedHandoffAgent(t *testing.T) {
	h, _, _ := fixHarness(t)
	d := &desktopHerdr{agents: []herdrport.Agent{{PaneID: "t22", WorkspaceID: "sp20", Agent: "claude", Cwd: "/src/web-DEMO-1", Tokens: map[string]string{ticketToken: "DEMO-1"}}}}
	h.app.NewHerdr = func() HerdrAPI { return d }
	h.env["HC_TERMINAL_ID"] = "t5"
	h.clone(t, "web")
	h.run.out["git worktree list --porcelain"] = "worktree /src/web-DEMO-1\nHEAD a\nbranch refs/heads/feature/DEMO-1-filter\n"
	if code := h.exec("fix", "DEMO-1"); code != 0 {
		t.Fatalf("exit %d: %s", code, h.errOut)
	}
	if !slices.Contains(d.calls, "prompt t22") {
		t.Errorf("calls = %q, output = %q", d.calls, h.out)
	}
}

func TestAgentKindsOnDesktopPreferTheAgentsHerdrDesktopKnows(t *testing.T) {
	h, _ := desktopHarness(t)
	h.run.missing["herdr"] = true
	if err := h.app.saveAgentKinds([]string{"opencode", "aider", "claude"}); err != nil {
		t.Fatal(err)
	}
	h.run.missing["aider"] = true
	kinds, err := h.app.agentKinds(context.Background())
	if err != nil || !reflect.DeepEqual(kinds, []string{"opencode", "claude"}) {
		t.Errorf("agentKinds = %q, %v; want Herdr desktop's installed agents in its order", kinds, err)
	}
	if err := h.app.saveAgentKinds([]string{}); err != nil {
		t.Fatal(err)
	}
	if kinds, err := h.app.agentKinds(context.Background()); err != nil || len(kinds) < 2 || kinds[0] != "pi" {
		t.Errorf("with no saved agents = %q, %v; want the fixed list", kinds, err)
	}
}

func TestAgentKindsOnDesktopFallBackWhenNoSavedAgentIsInstalled(t *testing.T) {
	h, _ := desktopHarness(t)
	h.run.missing["herdr"] = true
	if err := h.app.saveAgentKinds([]string{"onlymissing"}); err != nil {
		t.Fatal(err)
	}
	h.run.missing["onlymissing"] = true
	if kinds, err := h.app.agentKinds(context.Background()); err != nil || len(kinds) == 0 || kinds[0] != "pi" {
		t.Errorf("agentKinds = %q, %v; want the fixed list's installed agents", kinds, err)
	}
}
