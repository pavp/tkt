package cli

import (
	"reflect"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/pavp/tkt/internal/ticket"
	"github.com/pavp/tkt/internal/tui"
)

func TestStartAsksWhichOfTheInstalledAgentsAndRemembersIt(t *testing.T) {
	h, calls := workHarness(t)
	h.clone(t, "web")
	h.run.out["herdr agent start --help"] = herdrHelp("pi", "claude", "codex", "opencode")
	h.run.missing["codex"] = true
	h.app.Stdin = strings.NewReader("2\n")
	if code := h.exec("start", "DEMO-7", "--repo", "web"); code != 0 {
		t.Fatalf("exit %d: %s", code, h.errOut)
	}
	if len(*calls) != 1 || (*calls)[0].name != "claude" {
		t.Fatalf("launches = %+v", *calls)
	}
	for _, want := range []string{"  1) pi\n", "  2) claude\n", "  3) opencode\n", "Open which agent? [1] "} {
		if !strings.Contains(h.errOut.String(), want) {
			t.Errorf("menu missing %q: %q", want, h.errOut)
		}
	}
	if strings.Contains(h.errOut.String(), "codex") {
		t.Errorf("offered an agent that is not installed: %q", h.errOut)
	}
	if got := h.app.loadState().LastAgent; got != "claude" {
		t.Errorf("last agent = %q", got)
	}

	*calls = nil
	h.app.in = nil
	h.app.Stdin = strings.NewReader("\n")
	if code := h.exec("start", "DEMO-7", "--repo", "web"); code != 0 {
		t.Fatalf("exit %d: %s", code, h.errOut)
	}
	if !strings.Contains(h.errOut.String(), "  2) claude  (last used)\n") || !strings.Contains(h.errOut.String(), "Open which agent? [2] ") ||
		len(*calls) != 1 || (*calls)[0].name != "claude" {
		t.Errorf("last used not preselected: %q %+v", h.errOut, *calls)
	}
}

func TestStartOpensTheOnlyInstalledAgentWithoutAsking(t *testing.T) {
	h, calls := workHarness(t)
	h.clone(t, "web")
	h.run.out["herdr agent start --help"] = herdrHelp("claude", "opencode")
	h.run.missing["claude"] = true
	if code := h.exec("start", "DEMO-7", "--repo", "web"); code != 0 {
		t.Fatalf("exit %d: %s", code, h.errOut)
	}
	if len(*calls) != 1 || (*calls)[0].name != "opencode" || strings.Contains(h.errOut.String(), "which agent") {
		t.Errorf("launches = %+v, asked %q", *calls, h.errOut)
	}
}

func TestStartAgentFlagPicksTheAgent(t *testing.T) {
	h, calls := workHarness(t)
	h.clone(t, "web")
	h.run.out["herdr agent start --help"] = herdrHelp("claude", "opencode")
	if code := h.exec("start", "DEMO-7", "--repo", "web", "--agent", "opencode"); code != 0 {
		t.Fatalf("exit %d: %s", code, h.errOut)
	}
	if len(*calls) != 1 || (*calls)[0].name != "opencode" || strings.Contains(h.errOut.String(), "which agent") {
		t.Errorf("launches = %+v, asked %q", *calls, h.errOut)
	}
	if got := h.app.loadState().LastAgent; got != "opencode" {
		t.Errorf("last agent = %q", got)
	}

	*calls = nil
	h.run.missing["codex"] = true
	if code := h.exec("start", "DEMO-7", "--repo", "web", "--agent", "codex"); code == 0 || len(*calls) != 0 ||
		!strings.Contains(h.errOut.String(), "agent codex is not installed (not found in PATH)") {
		t.Errorf("exit %d, launches %+v, %q", code, *calls, h.errOut)
	}
}

func TestStartOpensNoAgentWhenItCannotTellWhichAreInstalled(t *testing.T) {
	tests := []struct {
		name    string
		setup   func(h *harness)
		wantErr string
	}{
		{"without Herdr", func(h *harness) { h.run.missing["herdr"] = true },
			"cannot tell which agents are installed: tkt learns them from Herdr (`herdr agent start --help`), which is not in PATH"},
		{"Herdr lists none", func(h *harness) { h.run.out["herdr agent start --help"] = "Usage: herdr agent start <NAME>\n" },
			"cannot tell which agents Herdr can start: `herdr agent start --help` lists none; update Herdr"},
		{"none installed", func(h *harness) {
			h.run.out["herdr agent start --help"] = herdrHelp("claude", "codex")
			h.run.missing["claude"], h.run.missing["codex"] = true, true
		}, "no agent Herdr can start is installed; install one of: claude, codex"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h, calls := workHarness(t)
			h.clone(t, "web")
			tt.setup(h)
			if code := h.exec("start", "DEMO-7", "--repo", "web"); code == 0 || len(*calls) != 0 || !strings.Contains(h.errOut.String(), tt.wantErr) {
				t.Errorf("exit %d, launches %+v, stderr %q", code, *calls, h.errOut)
			}
		})
	}
}

func TestStartOutsideHerdrCopiesThePromptForTheAgent(t *testing.T) {
	h, calls := workHarness(t)
	h.clone(t, "web")
	h.run.out["wl-copy"] = ""
	if code := h.exec("start", "DEMO-7", "--repo", "web"); code != 0 {
		t.Fatalf("exit %d: %s", code, h.errOut)
	}
	if len(*calls) != 1 || len((*calls)[0].args) != 0 {
		t.Fatalf("launches = %+v", *calls)
	}
	copied := h.run.stdin["wl-copy"]
	if !strings.Contains(copied, "Work on Jira ticket DEMO-7: Export users") {
		t.Errorf("copied %q", copied)
	}
	if out := h.out.String(); !strings.Contains(out, "Opening claude in ") || !strings.Contains(out, "its first prompt is on the clipboard: paste it there\n\n"+copied) {
		t.Errorf("output:\n%s", out)
	}
}

func TestHerdrStartOpensTheAgentAsked(t *testing.T) {
	h, f, _ := herdrHarness(t)
	h.clone(t, "web")
	h.run.out["herdr agent start --help"] = herdrHelp("claude", "opencode")
	f.Results["workspace.create"] = `{"type":"workspace_created","workspace":{"workspace_id":"w9","label":"x"},"root_pane":{"pane_id":"w9:p1"},"tab":{}}`
	if code := h.exec("herdr", "start", "DEMO-7", "--repo", "web", "--agent", "opencode"); code != 0 {
		t.Fatalf("exit %d: %s", code, h.errOut)
	}
	if start := f.Last("agent.start"); start["kind"] != "opencode" || start["name"] != "opencode" {
		t.Errorf("agent.start = %v", start)
	}
	if prompt := f.Last("agent.prompt"); prompt["target"] != "opencode" {
		t.Errorf("agent.prompt = %v", prompt)
	}
	if out := h.out.String(); !strings.Contains(out, "✓ opencode started in workspace DEMO-7 · web") || strings.Contains(out, "Claude") {
		t.Errorf("output:\n%s", out)
	}
}

func TestNewAgentOpensTheLastAgentChosen(t *testing.T) {
	b, h, f := agentsHarness(t)
	web := h.clone(t, "web")
	h.run.out["herdr agent start --help"] = herdrHelp("claude", "opencode")
	st := h.app.loadState()
	st.LastAgent = "opencode"
	h.app.saveState(st)
	f.Results["agent.list"] = `{"type":"agent_list","agents":[]}`
	f.Results["pane.list"] = `{"type":"pane_list","panes":[]}`
	f.Results["workspace.create"] = `{"type":"workspace_created","workspace":{"workspace_id":"w9","label":"x"},"root_pane":{"pane_id":"w9:p1"}}`
	pr := ticket.PullRequest{Repo: "example-org/web", Number: 820, URL: "https://github.com/example-org/web/pull/820"}
	if msg := b.NewAgentTask(tui.AgentTask{Review: &pr})(); msg != (tea.QuitMsg{}) {
		t.Fatalf("msg %v", msg)
	}
	if start := f.Last("agent.start"); start["kind"] != "opencode" || f.Last("workspace.create")["cwd"] != web {
		t.Errorf("agent.start = %v", start)
	}

	h.run.out["herdr agent start --help"] = "nothing"
	msg := b.NewAgentTask(tui.AgentTask{Review: &pr})()
	if got, _ := msg.(tui.StatusMsg); !strings.Contains(string(got), "cannot tell which agents Herdr can start") {
		t.Errorf("msg %v", msg)
	}
}

func TestDoctorNamesTheInstalledAgents(t *testing.T) {
	h := newHarness(t, baseConfig)
	h.run.out["herdr agent start --help"] = herdrHelp("pi", "claude", "codex")
	h.run.missing["codex"] = true
	h.exec("doctor")
	if out := h.out.String(); !strings.Contains(out, "✓ agents") || !strings.Contains(out, "pi, claude") || strings.Contains(out, "codex") {
		t.Errorf("doctor output:\n%s", out)
	}
}

func TestBoardLearnsTheInstalledAgents(t *testing.T) {
	b, h, _ := agentsHarness(t)
	h.run.out["herdr agent start --help"] = herdrHelp("pi", "claude", "codex")
	h.run.missing["codex"] = true
	st := h.app.loadState()
	st.LastAgent = "claude"
	h.app.saveState(st)
	got := b.AgentKinds()()
	if want := (tui.AgentKindsMsg{Kinds: []string{"pi", "claude"}, Last: "claude"}); !reflect.DeepEqual(got, want) {
		t.Errorf("got %+v, want %+v", got, want)
	}
	h.run.missing["herdr"] = true
	if got, _ := b.AgentKinds()().(tui.AgentKindsMsg); got.Err == nil || got.Kinds != nil {
		t.Errorf("without Herdr: %+v", got)
	}
}

func TestNewAgentOpensTheAgentChosenAndRemembersIt(t *testing.T) {
	b, h, f := agentsHarness(t)
	h.clone(t, "web")
	h.run.out["herdr agent start --help"] = herdrHelp("claude", "opencode")
	f.Results["agent.list"] = `{"type":"agent_list","agents":[]}`
	f.Results["pane.list"] = `{"type":"pane_list","panes":[]}`
	f.Results["workspace.create"] = `{"type":"workspace_created","workspace":{"workspace_id":"w9","label":"x"},"root_pane":{"pane_id":"w9:p1"}}`
	pr := ticket.PullRequest{Repo: "example-org/web", Number: 820, URL: "https://github.com/example-org/web/pull/820"}
	if msg := b.NewAgentTask(tui.AgentTask{Review: &pr, Kind: "opencode"})(); msg != (tea.QuitMsg{}) {
		t.Fatalf("msg %v", msg)
	}
	if start := f.Last("agent.start"); start["kind"] != "opencode" {
		t.Errorf("agent.start = %v", start)
	}
	if got := h.app.loadState().LastAgent; got != "opencode" {
		t.Errorf("last agent = %q", got)
	}
}
