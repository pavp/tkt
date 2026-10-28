package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/pavp/tkt/internal/board"
	"github.com/pavp/tkt/internal/herdr"
	"github.com/pavp/tkt/internal/herdr/herdrtest"
	"github.com/pavp/tkt/internal/herdrplugin"
	"github.com/pavp/tkt/internal/ticket"
	"github.com/pavp/tkt/internal/tui"
	"github.com/pavp/tkt/internal/workspace"
)

func herdrHarness(t *testing.T) (*harness, *herdrtest.FakeServer, *[]launchCall) {
	t.Helper()
	h, calls := workHarness(t)
	f := herdrtest.Start(t)
	h.app.NewHerdr = func() HerdrAPI { return herdr.New(f.Path) }
	h.app.Executable = func() (string, error) { return "/opt/tkt/bin/tkt", nil }
	h.app.Sleep = func(context.Context, time.Duration) error { return nil }
	h.run.out["herdr --default-config"] = "[keys]\nprefix = \"ctrl+b\"\nnew_tab = \"prefix+c\"\n"
	return h, f, calls
}

func count(f *herdrtest.FakeServer, method string) int {
	n := 0
	for _, r := range f.Requests {
		if r["method"] == method {
			n++
		}
	}
	return n
}

func TestHerdrInstallWritesManifestAndLinks(t *testing.T) {
	h, f, _ := herdrHarness(t)
	if code := h.exec("herdr", "install"); code != 0 {
		t.Fatalf("exit %d: %s", code, h.errOut)
	}
	dir := filepath.Join(h.home, "data", "tkt", "herdr-plugin")
	b, err := os.ReadFile(filepath.Join(dir, "herdr-plugin.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `command = ["/opt/tkt/bin/tkt", "board"]`) {
		t.Errorf("manifest does not point at the binary:\n%s", b)
	}
	if got := f.Last("plugin.link"); got["path"] != dir || got["enabled"] != true {
		t.Errorf("link params = %v", got)
	}
	if !strings.Contains(h.out.String(), "Skipped keybindings") {
		t.Errorf("without answers the keys should be skipped:\n%s", h.out)
	}
}

func TestHerdrInstallAddsAndUninstallRemovesKeys(t *testing.T) {
	h, f, _ := herdrHarness(t)
	cfg := filepath.Join(h.home, ".config", "herdr", "config.toml")
	if err := os.MkdirAll(filepath.Dir(cfg), 0o755); err != nil {
		t.Fatal(err)
	}
	original := "[theme]\nname = \"catppuccin\"\n"
	if err := os.WriteFile(cfg, []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}
	h.app.Stdin = strings.NewReader("\n")
	if code := h.exec("herdr", "install"); code != 0 {
		t.Fatalf("exit %d: %s", code, h.errOut)
	}
	b, _ := os.ReadFile(cfg)
	if !strings.Contains(string(b), `command = "tkt.board"`) || !strings.Contains(string(b), `key = "prefix+i"`) {
		t.Errorf("config:\n%s", b)
	}
	if bak, _ := os.ReadFile(cfg + ".bak-tkt"); string(bak) != original {
		t.Errorf("backup = %q", bak)
	}
	if count(f, "server.reload_config") != 1 {
		t.Error("config not reloaded")
	}
	if !strings.Contains(h.errOut.String(), "Add Herdr keybindings prefix+d (tkt board in a pane), prefix+i (tkt ticket pane), prefix+t (tkt board)?") {
		t.Errorf("prompt = %q", h.errOut)
	}

	h.app.in = nil
	if code := h.exec("herdr", "install", "--keys"); code != 0 {
		t.Fatalf("reinstall exit %d", code)
	}
	if !strings.Contains(h.out.String(), "✓ Already bound:") {
		t.Errorf("reinstall should not duplicate keys:\n%s", h.out)
	}

	if code := h.exec("herdr", "uninstall"); code != 0 {
		t.Fatalf("uninstall exit %d: %s", code, h.errOut)
	}
	if b, _ := os.ReadFile(cfg); string(b) != original {
		t.Errorf("uninstall left:\n%s", b)
	}
	if count(f, "plugin.unlink") != 1 {
		t.Error("plugin not unlinked")
	}
}

func TestHerdrInstallNeverTakesUsedKeys(t *testing.T) {
	h, _, _ := herdrHarness(t)
	cfg := filepath.Join(h.home, ".config", "herdr", "config.toml")
	_ = os.MkdirAll(filepath.Dir(cfg), 0o755)
	_ = os.WriteFile(cfg, []byte("[keys]\nzoom = \"prefix+t\"\n"), 0o644)
	if code := h.exec("herdr", "install", "--keys"); code != 0 {
		t.Fatalf("exit %d: %s", code, h.errOut)
	}
	b, _ := os.ReadFile(cfg)
	if strings.Contains(string(b), `key = "prefix+t"`) || !strings.Contains(string(b), `key = "prefix+i"`) {
		t.Errorf("config:\n%s", b)
	}
	if !strings.Contains(h.out.String(), `! prefix+t is taken by "zoom"`) {
		t.Errorf("output = %q", h.out)
	}
	if code := h.exec("herdr", "install", "--no-keys"); code != 0 || strings.Contains(h.out.String(), "keybindings") {
		t.Errorf("--no-keys: exit %d, %q", code, h.out)
	}
}

func TestHerdrInstallReportsHerdrDown(t *testing.T) {
	h, _, _ := herdrHarness(t)
	h.app.NewHerdr = func() HerdrAPI { return herdr.New(filepath.Join(h.home, "no.sock")) }
	if code := h.exec("herdr", "install"); code == 0 || !strings.Contains(h.errOut.String(), "herdr is not running") {
		t.Errorf("exit %d, %q", code, h.errOut)
	}
}

func TestHerdrStartCreatesWorkspacesWithAgents(t *testing.T) {
	h, f, calls := herdrHarness(t)
	web := h.clone(t, "web")
	api := h.clone(t, "api")
	f.Results["workspace.create"] = `{"type":"workspace_created","workspace":{"workspace_id":"w9","label":"x"},"root_pane":{"pane_id":"w9:p1"},"tab":{}}`
	h.app.Stdin = strings.NewReader("1,2\n")
	if code := h.exec("herdr", "start", "DEMO-7"); code != 0 {
		t.Fatalf("exit %d: %s", code, h.errOut)
	}
	if n := count(f, "workspace.create"); n != 2 {
		t.Fatalf("workspaces created = %d", n)
	}
	var cwds []string
	for _, r := range f.Requests {
		if r["method"] == "workspace.create" {
			p := r["params"].(map[string]any)
			cwds = append(cwds, p["cwd"].(string)+"|"+p["label"].(string))
		}
	}
	if cwds[0] != web+"|DEMO-7 · web" || cwds[1] != api+"|DEMO-7 · api" {
		t.Errorf("workspaces = %v", cwds)
	}
	start := f.Last("agent.start")
	if start["kind"] != "claude" || start["name"] != "claude" || start["pane_id"] != "w9:p1" || len(start["args"].([]any)) != 0 {
		t.Errorf("agent.start = %v", start)
	}
	prompt := f.Last("agent.prompt")
	if text, _ := prompt["text"].(string); prompt["target"] != "claude" || !strings.Contains(text, "Work on Jira ticket DEMO-7") || !strings.Contains(text, "herdr worktree create") {
		t.Errorf("agent.prompt = %v", prompt)
	}
	if len(*calls) != 0 {
		t.Error("launched a terminal agent instead of Herdr")
	}
	if mark := f.Last("pane.report_metadata"); mark["pane_id"] != "w9:p1" || fmt.Sprint(mark["tokens"]) != "map[tkt_key:DEMO-7]" {
		t.Errorf("the agent's pane carries its ticket: %v", mark)
	}
}

func TestHerdrStartFocusesExistingWorktree(t *testing.T) {
	h, f, _ := herdrHarness(t)
	h.clone(t, "web")
	h.run.out["git worktree list --porcelain"] = "worktree /src/web-DEMO-1\nHEAD a\nbranch refs/heads/feature/DEMO-1-filter\n"
	f.Results["workspace.list"] = `{"type":"workspace_list","workspaces":[{"workspace_id":"w3","label":"web-DEMO-1","worktree":{"checkout_path":"/src/web-DEMO-1/"}}]}`
	if code := h.exec("herdr", "start", "DEMO-1"); code != 0 {
		t.Fatalf("exit %d: %s", code, h.errOut)
	}
	if got := f.Last("workspace.focus"); got["workspace_id"] != "w3" {
		t.Errorf("focus = %v", got)
	}
	if count(f, "agent.start") != 0 {
		t.Error("started a second agent")
	}
}

func TestHerdrStartFocusesTheAgentOfAWorktreeHerdrDidNotCreate(t *testing.T) {
	tests := []struct {
		name, agents string
	}{
		{"agent reports its folder", `[{"pane_id":"w5:p2","workspace_id":"w5","agent":"claude","cwd":"/src/web-DEMO-1/app"}]`},
		{"folder from its pane", `[{"pane_id":"w5:p2","workspace_id":"w5","agent":"claude"}]`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h, f, _ := herdrHarness(t)
			h.clone(t, "web")
			h.run.out["git worktree list --porcelain"] = "worktree /src/web-DEMO-1\nHEAD a\nbranch refs/heads/feature/DEMO-1-filter\n"
			f.Results["workspace.list"] = `{"type":"workspace_list","workspaces":[{"workspace_id":"w5","label":"web"}]}`
			f.Results["pane.list"] = `{"type":"pane_list","panes":[{"pane_id":"w5:p2","workspace_id":"w5","cwd":"/src/web-DEMO-1/"}]}`
			f.Results["agent.list"] = `{"type":"agent_list","agents":` + tt.agents + `}`
			if code := h.exec("herdr", "start", "DEMO-1"); code != 0 {
				t.Fatalf("exit %d: %s", code, h.errOut)
			}
			if got := f.Last("workspace.focus"); got["workspace_id"] != "w5" {
				t.Errorf("focus = %v", got)
			}
			if got := f.Last("pane.focus"); got["pane_id"] != "w5:p2" {
				t.Errorf("agent's pane not focused, so its tab may stay hidden: %v", got)
			}
			if count(f, "agent.start") != 0 || count(f, "workspace.create") != 0 {
				t.Errorf("started a second agent: agent.start %d, workspace.create %d", count(f, "agent.start"), count(f, "workspace.create"))
			}
		})
	}
}

func TestHerdrStartFocusesTheAgentsPaneInAHerdrWorktree(t *testing.T) {
	h, f, _ := herdrHarness(t)
	h.clone(t, "web")
	h.run.out["git worktree list --porcelain"] = "worktree /src/web-DEMO-1\nHEAD a\nbranch refs/heads/feature/DEMO-1-filter\n"
	f.Results["workspace.list"] = `{"type":"workspace_list","workspaces":[{"workspace_id":"w3","label":"web-DEMO-1","worktree":{"checkout_path":"/src/web-DEMO-1/"}}]}`
	f.Results["agent.list"] = `{"type":"agent_list","agents":[{"pane_id":"w3:p4","workspace_id":"w3","agent":"claude","cwd":"/src/web-DEMO-1"}]}`
	if code := h.exec("herdr", "start", "DEMO-1"); code != 0 {
		t.Fatalf("exit %d: %s", code, h.errOut)
	}
	if got := f.Last("workspace.focus"); got["workspace_id"] != "w3" {
		t.Errorf("focus = %v", got)
	}
	if got := f.Last("pane.focus"); got["pane_id"] != "w3:p4" {
		t.Errorf("pane.focus = %v", got)
	}
}

func TestHerdrStartPicksTheWorktreeWithTheAgent(t *testing.T) {
	tests := []struct {
		name, agents string
		wantAsk      bool
		wantWS       string
	}{
		{"one has an agent", `[{"pane_id":"w6:p1","workspace_id":"w6","agent":"claude","cwd":"/src/web-DEMO-1-b"}]`, false, "w6"},
		{"none has an agent", `[]`, true, ""},
		{"both have agents", `[{"pane_id":"w5:p1","workspace_id":"w5","agent":"claude","cwd":"/src/web-DEMO-1-a"},{"pane_id":"w6:p1","workspace_id":"w6","agent":"claude","cwd":"/src/web-DEMO-1-b"}]`, true, "w5"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h, f, _ := herdrHarness(t)
			h.clone(t, "web")
			h.run.out["git worktree list --porcelain"] = "worktree /src/web-DEMO-1-a\nHEAD a\nbranch refs/heads/feature/DEMO-1-a\n\nworktree /src/web-DEMO-1-b\nHEAD b\nbranch refs/heads/feature/DEMO-1-b\n"
			f.Results["agent.list"] = `{"type":"agent_list","agents":` + tt.agents + `}`
			f.Results["workspace.create"] = `{"type":"workspace_created","workspace":{"workspace_id":"w9","label":"x"},"root_pane":{"pane_id":"w9:p1"}}`
			h.app.Stdin = strings.NewReader("\n")
			if code := h.exec("herdr", "start", "DEMO-1"); code != 0 {
				t.Fatalf("exit %d: %s", code, h.errOut)
			}
			if asked := strings.Contains(h.errOut.String(), "Open which worktree?"); asked != tt.wantAsk {
				t.Errorf("asked = %v:\n%s", asked, h.errOut)
			}
			if tt.wantWS != "" {
				if got := f.Last("workspace.focus"); got["workspace_id"] != tt.wantWS {
					t.Errorf("focus = %v", got)
				}
			}
		})
	}
}

const twoWorktrees = "worktree /src/web-DEMO-1-a\nHEAD a\nbranch refs/heads/feature/DEMO-1-a\n\nworktree /src/web-DEMO-1-b\nHEAD b\nbranch refs/heads/feature/DEMO-1-b\n"

func TestStartOpensTheWorktreeAsked(t *testing.T) {
	h, f, calls := herdrHarness(t)
	h.clone(t, "web")
	h.run.out["git worktree list --porcelain"] = twoWorktrees
	f.Results["workspace.create"] = `{"type":"workspace_created","workspace":{"workspace_id":"w9","label":"x"},"root_pane":{"pane_id":"w9:p1"}}`
	if code := h.exec("herdr", "start", "DEMO-1", "--worktree", "/src/web-DEMO-1-b/"); code != 0 {
		t.Fatalf("exit %d: %s", code, h.errOut)
	}
	if strings.Contains(h.errOut.String(), "Open which worktree?") {
		t.Errorf("asked although --worktree was given:\n%s", h.errOut)
	}
	if got := f.Last("workspace.create"); got["cwd"] != "/src/web-DEMO-1-b" {
		t.Errorf("workspace.create = %v", got)
	}
	if code := h.exec("start", "DEMO-1", "--worktree", "/src/web-DEMO-1-a"); code != 0 || len(*calls) != 1 || (*calls)[0].dir != "/src/web-DEMO-1-a" {
		t.Errorf("tkt start --worktree: exit %d, launches %+v, %s", code, *calls, h.errOut)
	}
	if code := h.exec("herdr", "start", "DEMO-1", "--worktree", "/nope"); code == 0 || !strings.Contains(h.errOut.String(), "DEMO-1 has no worktree at /nope") {
		t.Errorf("unknown worktree: exit %d, %s", code, h.errOut)
	}
	for _, args := range [][]string{{"start"}, {"herdr", "start"}} {
		args = append(args, "DEMO-1", "--worktree", "/src/web-DEMO-1-a", "--repo", "web")
		if code := h.exec(args...); code == 0 || !strings.Contains(h.errOut.String(), "worktree") || !strings.Contains(h.errOut.String(), "repo") {
			t.Errorf("%v: exit %d, %s", args, code, h.errOut)
		}
	}
}

func TestStartChoices(t *testing.T) {
	agent := func(ws, cwd string) string {
		return `{"pane_id":"` + ws + `:p1","workspace_id":"` + ws + `","agent":"claude","agent_status":"working","cwd":"` + cwd + `"}`
	}
	tests := []struct {
		name, worktrees, agents string
		lastRepos               []string
		want                    tui.StartChoices
	}{
		{"one worktree", "worktree /src/web-DEMO-1-a\nHEAD a\nbranch refs/heads/feature/DEMO-1-a\n", `[]`, nil, tui.StartChoices{}},
		{"the agent tells", twoWorktrees, `[` + agent("w6", "/src/web-DEMO-1-b") + `]`, nil, tui.StartChoices{}},
		{"no agent tells", twoWorktrees, `[]`, nil, tui.StartChoices{Worktrees: []tui.WorktreeChoice{
			{Repo: "example-org/web", Branch: "feature/DEMO-1-a", Path: "/src/web-DEMO-1-a"}, {Repo: "example-org/web", Branch: "feature/DEMO-1-b", Path: "/src/web-DEMO-1-b"}}}},
		{"both have agents", twoWorktrees, `[` + agent("w5", "/src/web-DEMO-1-a") + `,` + agent("w6", "/src/web-DEMO-1-b") + `]`, nil, tui.StartChoices{Worktrees: []tui.WorktreeChoice{
			{Repo: "example-org/web", Branch: "feature/DEMO-1-a", Path: "/src/web-DEMO-1-a", Agent: "working"}, {Repo: "example-org/web", Branch: "feature/DEMO-1-b", Path: "/src/web-DEMO-1-b", Agent: "working"}}}},
		{"repositories, last used checked", "", `[]`, []string{"example-org/api"}, tui.StartChoices{Repos: []tui.RepoChoice{
			{Name: "example-org/web"}, {Name: "example-org/api", LastUsed: true, Checked: true}}}},
		{"repositories, first checked when none was used", "", `[]`, nil, tui.StartChoices{Repos: []tui.RepoChoice{
			{Name: "example-org/web", Checked: true}, {Name: "example-org/api"}}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h, f, _ := herdrHarness(t)
			h.clone(t, "web")
			h.run.out["git worktree list --porcelain"] = tt.worktrees
			f.Results["agent.list"] = `{"type":"agent_list","agents":` + tt.agents + `}`
			if tt.lastRepos != nil {
				st := h.app.loadState()
				st.LastRepos["DEMO"] = tt.lastRepos
				h.app.saveState(st)
			}
			c, err := h.app.LoadConfig()
			if err != nil {
				t.Fatal(err)
			}
			got, err := h.app.startChoices(context.Background(), c, "DEMO-1", h.app.NewHerdr())
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("got %+v\nwant %+v", got, tt.want)
			}
		})
	}
}

func TestHerdrStartIgnoresAgentsOutsideTheWorktree(t *testing.T) {
	h, f, _ := herdrHarness(t)
	h.clone(t, "web")
	h.run.out["git worktree list --porcelain"] = "worktree /src/web-DEMO-1\nHEAD a\nbranch refs/heads/feature/DEMO-1-filter\n"
	f.Results["workspace.create"] = `{"type":"workspace_created","workspace":{"workspace_id":"w9","label":"x"},"root_pane":{"pane_id":"w9:p1"}}`
	f.Results["agent.list"] = `{"type":"agent_list","agents":[{"pane_id":"w5:p2","workspace_id":"w5","agent":"claude","cwd":"/src/web-DEMO-10"}]}`
	if code := h.exec("herdr", "start", "DEMO-1"); code != 0 {
		t.Fatalf("exit %d: %s", code, h.errOut)
	}
	if got := f.Last("workspace.focus"); got["workspace_id"] == "w5" {
		t.Error("focused an agent of another folder")
	}
	if count(f, "agent.start") != 1 {
		t.Errorf("agent.start = %d, want the ticket's own agent", count(f, "agent.start"))
	}
}

func TestHerdrOpenBoardOrSetup(t *testing.T) {
	h, f, _ := herdrHarness(t)
	if code := h.exec("herdr", "open-board"); code != 0 {
		t.Fatalf("exit %d: %s", code, h.errOut)
	}
	if got := f.Last("plugin.pane.open"); got["entrypoint"] != "board" || got["plugin_id"] != "tkt" {
		t.Errorf("pane = %v", got)
	}
	os.Remove(h.app.configPath)
	h.env["HERDR_PLUGIN_ID"] = "tkt-dev"
	h.exec("herdr", "open-board")
	if got := f.Last("plugin.pane.open"); got["entrypoint"] != "setup" || got["plugin_id"] != "tkt-dev" {
		t.Errorf("unconfigured pane = %v", got)
	}
}

func TestHerdrInstallMovesTheBoardPaneOffItsOldKey(t *testing.T) {
	h, _, _ := herdrHarness(t)
	cfg := filepath.Join(h.home, ".config", "herdr", "config.toml")
	_ = os.MkdirAll(filepath.Dir(cfg), 0o755)
	old := herdrplugin.AddBlock("", []herdrplugin.Binding{herdrplugin.DefaultBindings[0], herdrplugin.DefaultBindings[2], {Key: "prefix+alt+t", Action: "tkt.board-pane", Description: "tkt board in a pane"}})
	_ = os.WriteFile(cfg, []byte(old), 0o644)
	h.run.out["herdr --default-config"] = "[keys]\n"
	if code := h.exec("herdr", "install", "--keys"); code != 0 {
		t.Fatalf("exit %d: %s", code, h.errOut)
	}
	got, _ := os.ReadFile(cfg)
	if strings.Contains(string(got), "prefix+alt+t") || !strings.Contains(string(got), `key = "prefix+d"`) {
		t.Errorf("config:\n%s", got)
	}
	if !strings.Contains(h.out.String(), "Moved tkt board in a pane off prefix+alt+t") {
		t.Errorf("output %q", h.out)
	}
}

func TestHerdrOpenBoardInAPane(t *testing.T) {
	h, f, _ := herdrHarness(t)
	if code := h.exec("herdr", "open-board-pane"); code != 0 {
		t.Fatalf("exit %d: %s", code, h.errOut)
	}
	if got := f.Last("plugin.pane.open"); got["entrypoint"] != "board-split" || got["plugin_id"] != "tkt" || got["focus"] != true {
		t.Errorf("the board's pane opens focused: %v", got)
	}
	os.Remove(h.app.configPath)
	h.exec("herdr", "open-board-pane")
	if got := f.Last("plugin.pane.open"); got["entrypoint"] != "setup" {
		t.Errorf("unconfigured pane = %v", got)
	}
}

func TestHerdrOpenBoardBringsTheOpenBoardToTheCurrentWorkspace(t *testing.T) {
	cases := []struct {
		name     string
		recorded string
		closeErr string
		focus    string
		close    string
		opens    int
	}{
		{name: "no board open", opens: 1},
		{name: "board open in this workspace", recorded: "w2:p9 w2", focus: "w2:p9"},
		{name: "board open in another workspace", recorded: "w1:p9 w1", close: "w1:p9", opens: 1},
		{name: "recorded board already gone", recorded: "w1:p9 w1", closeErr: "pane not found", close: "w1:p9", opens: 1},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h, f, _ := herdrHarness(t)
			h.env["HERDR_WORKSPACE_ID"] = "w2"
			if c.recorded != "" {
				if err := os.MkdirAll(h.app.Env.DataDir(), 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(h.app.Env.DataDir(), "board-pane"), []byte(c.recorded), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			if c.closeErr != "" {
				f.Errors["plugin.pane.close"] = c.closeErr
			}
			if code := h.exec("herdr", "open-board"); code != 0 {
				t.Fatalf("exit %d: %s", code, h.errOut)
			}
			if got, _ := f.Last("plugin.pane.focus")["pane_id"].(string); got != c.focus {
				t.Errorf("focused %q, want %q", got, c.focus)
			}
			if got, _ := f.Last("plugin.pane.close")["pane_id"].(string); got != c.close {
				t.Errorf("closed %q, want %q", got, c.close)
			}
			if n := count(f, "plugin.pane.open"); n != c.opens {
				t.Fatalf("opened %d boards, want %d", n, c.opens)
			}
			if c.opens > 0 {
				if got := f.Last("plugin.pane.open"); got["entrypoint"] != "board" || got["workspace_id"] != nil {
					t.Errorf("a popup opens on the active pane, Herdr rejects a workspace_id: %v", got)
				}
			}
		})
	}
}

func TestBoardPaneRecordLastsWhileTheBoardRuns(t *testing.T) {
	h, _, _ := herdrHarness(t)
	h.env["HERDR_ENV"] = "1"
	h.env["HERDR_PANE_ID"] = "w3:p2"
	h.env["HERDR_WORKSPACE_ID"] = "w3"
	file := filepath.Join(h.app.Env.DataDir(), "board-pane")
	release := h.app.claimBoardPane()
	if b, err := os.ReadFile(file); err != nil || string(b) != "w3:p2 w3" {
		t.Fatalf("record = %q, %v", b, err)
	}
	if fi, err := os.Stat(file); err != nil || fi.Mode().Perm() != 0o600 {
		t.Errorf("record mode = %v, %v", fi.Mode().Perm(), err)
	}
	release()
	if _, err := os.Stat(file); !os.IsNotExist(err) {
		t.Errorf("record should be gone after the board closes: %v", err)
	}
}

func TestBoardPaneReleaseKeepsANewerBoardsRecord(t *testing.T) {
	h, _, _ := herdrHarness(t)
	h.env["HERDR_ENV"] = "1"
	h.env["HERDR_PANE_ID"] = "w3:p2"
	h.env["HERDR_WORKSPACE_ID"] = "w3"
	release := h.app.claimBoardPane()
	file := filepath.Join(h.app.Env.DataDir(), "board-pane")
	if err := os.WriteFile(file, []byte("w4:p1 w4"), 0o600); err != nil {
		t.Fatal(err)
	}
	release()
	if b, _ := os.ReadFile(file); string(b) != "w4:p1 w4" {
		t.Errorf("record = %q, the newer board's record must stay", b)
	}
}

func TestHerdrRefreshLabelsTicketWorkspaces(t *testing.T) {
	h, f, _ := herdrHarness(t)
	f.Results["workspace.list"] = `{"type":"workspace_list","workspaces":[
		{"workspace_id":"w1","label":"web-DEMO-1","worktree":{"checkout_path":"/src/web-DEMO-1"}},
		{"workspace_id":"w2","label":"dotfiles"}]}`
	f.Results["agent.list"] = `{"type":"agent_list","agents":[
		{"pane_id":"w1:p1","workspace_id":"w1","agent":"claude","agent_status":"idle"},
		{"pane_id":"w1:p2","workspace_id":"w1","agent":"claude","agent_status":"working"},
		{"pane_id":"w1:p3","workspace_id":"w1","agent_status":"unknown"}]}`
	h.run.out["git rev-parse --abbrev-ref HEAD"] = "feature/DEMO-1-filter\n"
	if code := h.exec("herdr", "refresh"); code != 0 {
		t.Fatalf("exit %d: %s", code, h.errOut)
	}
	got := f.Last("workspace.report_metadata")
	if got["workspace_id"] != "w1" || got["source"] != "plugin:tkt" || got["ttl_ms"] != float64(3600000) {
		t.Errorf("report = %v", got)
	}
	tokens := got["tokens"].(map[string]any)
	want := map[string]any{"tkt": nil, "tkt_alert": "✕ CI · #812"}
	if !reflect.DeepEqual(tokens, want) {
		t.Errorf("tokens = %v, want %v", tokens, want)
	}
	if count(f, "workspace.report_metadata") != 1 {
		t.Error("labelled a workspace without a ticket")
	}
	if !strings.Contains(h.out.String(), "Updated 1 Herdr workspace") {
		t.Errorf("output = %q", h.out)
	}
}

func TestHerdrRestoreUsesCacheOnly(t *testing.T) {
	h, f, _ := herdrHarness(t)
	f.Results["workspace.list"] = `{"type":"workspace_list","workspaces":[{"workspace_id":"w1","worktree":{"checkout_path":"/src/web-DEMO-1"}}]}`
	f.Results["pane.list"] = `{"type":"pane_list","panes":[{"pane_id":"w1:p1","workspace_id":"w1","cwd":"/src/web-DEMO-1"}]}`
	h.run.out["git rev-parse --abbrev-ref HEAD"] = "feature/DEMO-1-filter\n"
	if code := h.exec("herdr", "restore"); code != 0 || count(f, "workspace.report_metadata") != 0 {
		t.Fatalf("restore without cache: exit %d, reports %d", code, count(f, "workspace.report_metadata"))
	}
	if err := h.app.cache().Save(board.Snapshot{Tickets: []ticket.Ticket{{Key: "DEMO-1", Status: "In Progress"}}}); err != nil {
		t.Fatal(err)
	}
	h.jira.searchErr = os.ErrDeadlineExceeded
	if code := h.exec("herdr", "restore"); code != 0 {
		t.Fatalf("exit %d: %s", code, h.errOut)
	}
	if label := f.Last("workspace.report_metadata")["tokens"].(map[string]any)["tkt"]; label != "In Progress" {
		t.Errorf("label = %v", label)
	}
}

func TestHerdrOnWorktreeLinksKeyedBranches(t *testing.T) {
	h, _, _ := herdrHarness(t)
	h.inCheckout("feature/DEMO-7-export", "/src/web-DEMO-7", "git@github.com:example-org/web.git")
	h.env["HERDR_PLUGIN_EVENT_JSON"] = `{"event":"worktree.created","data":{"worktree":{"path":"/src/web-DEMO-7","branch":"feature/DEMO-7-export"}}}`
	if code := h.exec("herdr", "on-worktree"); code != 0 {
		t.Fatalf("exit %d: %s", code, h.errOut)
	}
	links, _ := h.app.links().Load()
	if len(links) != 1 || links[0].Source != workspace.SourceHook || links[0].Key != "DEMO-7" {
		t.Errorf("links = %+v", links)
	}

	h.inCheckout("spike", "/src/web-spike", "git@github.com:example-org/web.git")
	h.env["HERDR_PLUGIN_EVENT_JSON"] = `{"workspace":{"worktree":{"checkout_path":"/src/web-spike"}}}`
	if code := h.exec("herdr", "on-worktree"); code != 0 || !strings.Contains(h.errOut.String(), `branch "spike" in /src/web-spike carries no ticket key; not linked`) {
		t.Errorf("unkeyed: exit %d, %q", code, h.errOut)
	}
	if links, _ := h.app.links().Load(); len(links) != 1 {
		t.Errorf("unkeyed branch linked: %+v", links)
	}
}

func TestWorktreePath(t *testing.T) {
	for raw, want := range map[string]string{
		`{"worktree":{"path":"/a"}}`:                                 "/a",
		`{"data":{"workspace":{"worktree":{"checkout_path":"/b"}}}}`: "/b",
		`{"path":"/not-a-worktree"}`:                                 "",
		`not json`:                                                   "",
	} {
		if got := worktreePath(raw); got != want {
			t.Errorf("worktreePath(%s) = %q, want %q", raw, got, want)
		}
	}
}

func TestHerdrOpenLinks(t *testing.T) {
	h, f, _ := herdrHarness(t)
	h.env["HERDR_PLUGIN_CLICKED_URL"] = "https://example.atlassian.net/browse/DEMO-7"
	if code := h.exec("herdr", "open"); code != 0 {
		t.Fatalf("exit %d: %s", code, h.errOut)
	}
	got := f.Last("plugin.pane.open")
	if got["entrypoint"] != "ticket-popup" || got["env"].(map[string]any)["TKT_KEY"] != "DEMO-7" {
		t.Errorf("pane = %v", got)
	}

	snap := board.Snapshot{Tickets: []ticket.Ticket{{Key: "DEMO-1", PRs: []ticket.PullRequest{{URL: "https://github.com/example-org/web/pull/812"}}}}}
	if err := h.app.cache().Save(snap); err != nil {
		t.Fatal(err)
	}
	h.exec("herdr", "open", "https://github.com/example-org/web/pull/812/files")
	if got := f.Last("plugin.pane.open"); got["env"].(map[string]any)["TKT_KEY"] != "DEMO-1" {
		t.Errorf("PR link pane = %v", got)
	}

	h.exec("herdr", "open", "https://github.com/example-org/web/pull/999")
	if got := f.Last("notification.show"); !strings.Contains(got["body"].(string), "No ticket found") {
		t.Errorf("notification = %v", got)
	}
}

func TestHerdrOpenTicketPane(t *testing.T) {
	h, f, _ := herdrHarness(t)
	h.env["HERDR_WORKSPACE_ID"] = "w1"
	f.Results["workspace.list"] = `{"type":"workspace_list","workspaces":[{"workspace_id":"w1","worktree":{"checkout_path":"/src/web-DEMO-1"}}]}`
	f.Results["pane.list"] = `{"type":"pane_list","panes":[{"pane_id":"w1:p1","workspace_id":"w1","cwd":"/src/web-DEMO-1"}]}`
	h.run.out["git rev-parse --abbrev-ref HEAD"] = "feature/DEMO-1-filter\n"
	if code := h.exec("herdr", "open-ticket"); code != 0 {
		t.Fatalf("exit %d: %s", code, h.errOut)
	}
	got := f.Last("plugin.pane.open")
	if got["entrypoint"] != "ticket" || got["target_pane_id"] != "w1:p1" || got["cwd"] != "/src/web-DEMO-1" || got["env"].(map[string]any)["TKT_KEY"] != "DEMO-1" {
		t.Errorf("pane = %v", got)
	}
	h.run.out["git rev-parse --abbrev-ref HEAD"] = "main\n"
	h.exec("herdr", "open-ticket")
	if got := f.Last("notification.show"); got == nil || !strings.Contains(got["body"].(string), "carries no ticket key") {
		t.Errorf("notification = %v", got)
	}
}

func TestHerdrOpenTicketPaneWithoutWorktree(t *testing.T) {
	h, f, _ := herdrHarness(t)
	h.env["HERDR_WORKSPACE_ID"] = "w1"
	f.Results["workspace.list"] = `{"type":"workspace_list","workspaces":[{"workspace_id":"w1"},{"workspace_id":"w2"}]}`
	f.Results["pane.list"] = `{"type":"pane_list","panes":[
		{"pane_id":"w2:p1","workspace_id":"w2","cwd":"/src/api","focused":true},
		{"pane_id":"w1:p1","workspace_id":"w1","cwd":"/src/notes"},
		{"pane_id":"w1:p2","workspace_id":"w1","cwd":"/src/web-DEMO-1","focused":true}]}`
	h.run.out["git rev-parse --abbrev-ref HEAD"] = "refactor/demo-1-filter\n"
	if code := h.exec("herdr", "open-ticket"); code != 0 {
		t.Fatalf("exit %d: %s", code, h.errOut)
	}
	got := f.Last("plugin.pane.open")
	if got["cwd"] != "/src/web-DEMO-1" || got["target_pane_id"] != "w1:p2" || got["workspace_id"] != nil || got["env"].(map[string]any)["TKT_KEY"] != "DEMO-1" {
		t.Errorf("pane = %v", got)
	}
}

func TestHerdrTicketPopupDeclines(t *testing.T) {
	h, f, _ := herdrHarness(t)
	h.env["TKT_KEY"] = "DEMO-7"
	h.app.Stdin = strings.NewReader("n\n")
	if code := h.exec("herdr", "ticket-popup"); code != 0 {
		t.Fatalf("exit %d: %s", code, h.errOut)
	}
	if !strings.Contains(h.out.String(), "DEMO-7") || count(f, "workspace.create") != 0 {
		t.Errorf("output %q, creates %d", h.out, count(f, "workspace.create"))
	}
}

func TestShowUsesTKTKey(t *testing.T) {
	h, _, _ := herdrHarness(t)
	h.env["TKT_KEY"] = "DEMO-7"
	if code := h.exec("show"); code != 0 || !strings.Contains(h.out.String(), "DEMO-7") {
		t.Errorf("exit %d: %s", code, h.errOut)
	}
}

func TestBoardOffersSetupWhenUnconfigured(t *testing.T) {
	h, _, _ := herdrHarness(t)
	os.Remove(h.app.configPath)
	if code := h.exec("board"); code != 0 || !strings.Contains(h.errOut.String(), "tkt isn't set up") {
		t.Errorf("exit %d, %q", code, h.errOut)
	}
}

func TestBoardOffersToConnectJiraWithoutAToken(t *testing.T) {
	h := newHarness(t, baseConfig)
	if code := h.exec("board"); code != 0 || !strings.Contains(h.errOut.String(), "No Jira token for dev@example.com · press Enter to connect Jira") {
		t.Errorf("exit %d, %q", code, h.errOut)
	}
}

func TestEnsureJiraTokenConnectsBeforeTheBoard(t *testing.T) {
	h := newHarness(t, baseConfig)
	h.app.Stdin = strings.NewReader("\ntok-123\n")
	c, err := h.app.LoadConfig()
	if err != nil {
		t.Fatal(err)
	}
	ok, err := h.app.ensureJiraToken(context.Background(), c)
	if err != nil || !ok {
		t.Fatalf("ensureJiraToken = %v, %v; stderr %q", ok, err, h.errOut)
	}
	if h.creds["jira:example.atlassian.net:dev@example.com"] != "tok-123" {
		t.Errorf("creds = %v", h.creds)
	}
	h.creds["jira:example.atlassian.net:dev@example.com"] = "stored"
	if ok, err := h.app.ensureJiraToken(context.Background(), c); err != nil || !ok || strings.Count(h.errOut.String(), "press Enter to connect Jira") != 1 {
		t.Errorf("a stored token should open the board without asking: %v, %v", ok, err)
	}
}

func herdrConfig(t *testing.T, h *harness, content string) string {
	t.Helper()
	cfg := filepath.Join(h.home, ".config", "herdr", "config.toml")
	if err := os.MkdirAll(filepath.Dir(cfg), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cfg, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return cfg
}

func TestHerdrInstallAddsAndUninstallRemovesSidebarRows(t *testing.T) {
	h, f, _ := herdrHarness(t)
	original := "[theme]\nname = \"catppuccin\"\n"
	cfg := herdrConfig(t, h, original)
	h.app.Stdin = strings.NewReader("n\n\n")
	if code := h.exec("herdr", "install"); code != 0 {
		t.Fatalf("exit %d: %s", code, h.errOut)
	}
	b, _ := os.ReadFile(cfg)
	if !strings.Contains(string(b), `token = "$tkt_alert"`) || strings.Contains(string(b), "tkt_agent") || strings.Contains(string(b), "tkt.board") {
		t.Errorf("config:\n%s", b)
	}
	if !strings.Contains(h.errOut.String(), "Show each ticket and its agent in Herdr's sidebar?") || !strings.Contains(h.out.String(), "✓ Added tkt's ticket and agent lines to Herdr's sidebar") {
		t.Errorf("stderr %q, stdout %q", h.errOut, h.out)
	}
	if count(f, "server.reload_config") != 2 {
		t.Error("config should be reloaded before (to learn its warnings) and after writing the rows")
	}
	if code := h.exec("herdr", "uninstall"); code != 0 {
		t.Fatalf("uninstall exit %d: %s", code, h.errOut)
	}
	if b, _ := os.ReadFile(cfg); string(b) != original {
		t.Errorf("uninstall left:\n%s", b)
	}
}

func TestHerdrInstallLeavesTheUsersSidebarRowsAlone(t *testing.T) {
	h, _, _ := herdrHarness(t)
	own := "[ui.sidebar.spaces]\nrows = [[\"workspace\"]]\n"
	cfg := herdrConfig(t, h, own)
	if code := h.exec("herdr", "install", "--no-keys", "--sidebar"); code != 0 {
		t.Fatalf("exit %d: %s", code, h.errOut)
	}
	if b, _ := os.ReadFile(cfg); string(b) != own {
		t.Errorf("own rows rewritten:\n%s", b)
	}
	if !strings.Contains(h.out.String(), "you configure [ui.sidebar.spaces] yourself") || !strings.Contains(h.out.String(), `token = "$tkt"`) {
		t.Errorf("should show the rows to add:\n%s", h.out)
	}
}

func TestHerdrInstallUndoesSidebarRowsHerdrRejects(t *testing.T) {
	h, f, _ := herdrHarness(t)
	original := "[theme]\nname = \"catppuccin\"\n"
	cfg := herdrConfig(t, h, original)
	f.Results["server.reload_config"] = `{"type":"config_reload","status":"failed","diagnostics":["ui.sidebar.spaces.rows: unknown token"]}`
	if code := h.exec("herdr", "install", "--no-keys", "--sidebar"); code != 0 {
		t.Fatalf("exit %d: %s", code, h.errOut)
	}
	if b, _ := os.ReadFile(cfg); string(b) != original {
		t.Errorf("rejected rows should be undone:\n%s", b)
	}
	if !strings.Contains(h.out.String(), "Herdr rejected the sidebar rows (ui.sidebar.spaces.rows: unknown token); your config is back as it was") {
		t.Errorf("stdout = %q", h.out)
	}
}

func TestBoardRefreshCarriesAgentStatesInsideHerdr(t *testing.T) {
	h, f, _ := herdrHarness(t)
	h.env["HERDR_ENV"] = "1"
	f.Results["workspace.list"] = `{"type":"workspace_list","workspaces":[
		{"workspace_id":"w1","label":"web-DEMO-1","worktree":{"checkout_path":"/src/web-DEMO-1"}}]}`
	f.Results["agent.list"] = `{"type":"agent_list","agents":[{"pane_id":"w1:p1","workspace_id":"w1","agent":"claude","agent_status":"blocked"}]}`
	h.run.out["git rev-parse --abbrev-ref HEAD"] = "feature/DEMO-1-filter\n"
	c, err := h.app.LoadConfig()
	if err != nil {
		t.Fatal(err)
	}
	b := &boardActions{a: h.app, c: c, ctx: context.Background()}
	msg, ok := b.Refresh()().(tui.SnapshotMsg)
	if !ok || msg.Err != nil {
		t.Fatalf("refresh = %+v", msg)
	}
	if got := msg.Snap.Agents["DEMO-1"]; got != "blocked" {
		t.Errorf("agents = %v", msg.Snap.Agents)
	}
}

func TestBoardAgentsReadsStatesForTheCachedBoardWithoutFetching(t *testing.T) {
	h, f, _ := herdrHarness(t)
	h.env["HERDR_ENV"] = "1"
	f.Results["workspace.list"] = `{"type":"workspace_list","workspaces":[
		{"workspace_id":"w1","label":"web-DEMO-1","worktree":{"checkout_path":"/src/web-DEMO-1"}}]}`
	f.Results["agent.list"] = `{"type":"agent_list","agents":[{"pane_id":"w1:p1","workspace_id":"w1","agent":"claude","agent_status":"working"}]}`
	h.run.out["git rev-parse --abbrev-ref HEAD"] = "feature/DEMO-1-filter\n"
	c, err := h.app.LoadConfig()
	if err != nil {
		t.Fatal(err)
	}
	cached := board.Snapshot{FetchedAt: time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC), Tickets: []ticket.Ticket{{Key: "DEMO-1", Category: ticket.InProgress}}}
	b := &boardActions{a: h.app, c: c, ctx: context.Background()}
	msg, ok := b.Agents(cached)().(tui.AgentsMsg)
	if !ok {
		t.Fatalf("agents = %+v", msg)
	}
	if !msg.FetchedAt.Equal(cached.FetchedAt) || msg.Agents["DEMO-1"] != "working" {
		t.Errorf("agents = %+v", msg)
	}
	if n := count(f, "workspace.report_metadata"); n != 0 {
		t.Errorf("the cached board must not rewrite the sidebar, got %d reports", n)
	}
}

func TestHerdrInstallKeepsSidebarRowsDespiteUnrelatedWarnings(t *testing.T) {
	h, f, _ := herdrHarness(t)
	cfg := herdrConfig(t, h, "[toast]\nenabled = true\n")
	f.Results["server.reload_config"] = `{"type":"config_reload","status":"partial","diagnostics":["[toast] is not a known section"]}`
	if code := h.exec("herdr", "install", "--no-keys", "--sidebar"); code != 0 {
		t.Fatalf("exit %d: %s", code, h.errOut)
	}
	if b, _ := os.ReadFile(cfg); !strings.Contains(string(b), `token = "$tkt"`) {
		t.Errorf("a warning that was there before should not undo the rows:\n%s\n%s", b, h.out)
	}
}

func TestHerdrInstallUndoesSidebarRowsThatAddWarnings(t *testing.T) {
	h, f, _ := herdrHarness(t)
	original := "[toast]\nenabled = true\n"
	cfg := herdrConfig(t, h, original)
	f.Results["server.reload_config"] = `{"type":"config_reload","status":"partial","diagnostics":["[toast] is not a known section"]}`
	h.app.NewHerdr = func() HerdrAPI { return reloadSequence{HerdrAPI: herdr.New(f.Path), f: f} }
	if code := h.exec("herdr", "install", "--no-keys", "--sidebar"); code != 0 {
		t.Fatalf("exit %d: %s", code, h.errOut)
	}
	if b, _ := os.ReadFile(cfg); string(b) != original {
		t.Errorf("rows that add a warning should be undone:\n%s", b)
	}
	if !strings.Contains(h.out.String(), "ui.sidebar.spaces.rows: unknown style") {
		t.Errorf("stdout = %q", h.out)
	}
}

// reloadSequence makes the second reload (after tkt writes its rows) report a new warning.
type reloadSequence struct {
	HerdrAPI
	f *herdrtest.FakeServer
}

func (r reloadSequence) ReloadConfig(ctx context.Context) error {
	if count(r.f, "server.reload_config") == 1 {
		r.f.Results["server.reload_config"] = `{"type":"config_reload","status":"partial","diagnostics":["[toast] is not a known section","ui.sidebar.spaces.rows: unknown style"]}`
	}
	return r.HerdrAPI.ReloadConfig(ctx)
}

func TestHerdrRefreshLabelsWorkspacesOpenedOnAPlainCheckout(t *testing.T) {
	h, f, _ := herdrHarness(t)
	f.Results["workspace.list"] = `{"type":"workspace_list","workspaces":[{"workspace_id":"w9","label":"live test"}]}`
	f.Results["pane.list"] = `{"type":"pane_list","panes":[
		{"pane_id":"w9:p1","workspace_id":"w9","cwd":"/src/web"},
		{"pane_id":"w8:p1","workspace_id":"w8","cwd":"/elsewhere"}]}`
	f.Results["agent.list"] = `{"type":"agent_list","agents":[]}`
	h.run.out["git rev-parse --abbrev-ref HEAD"] = "feature/DEMO-1-filter\n"
	if code := h.exec("herdr", "refresh"); code != 0 {
		t.Fatalf("exit %d: %s", code, h.errOut)
	}
	got := f.Last("workspace.report_metadata")
	if got == nil || got["workspace_id"] != "w9" {
		t.Fatalf("a workspace on a plain checkout should be labelled from its pane's folder: %v", got)
	}
	if tok := got["tokens"].(map[string]any)["tkt_alert"]; tok != "✕ CI · #812" {
		t.Errorf("tokens = %v", got["tokens"])
	}
}

func TestHerdrManifestHasNoAgentEvent(t *testing.T) {
	h, _, _ := herdrHarness(t)
	h.exec("herdr", "install", "--no-keys", "--no-sidebar")
	b, _ := os.ReadFile(filepath.Join(h.app.Env.DataDir(), "herdr-plugin", herdrplugin.FileName))
	if strings.Contains(string(b), "pane.agent_status_changed") || strings.Contains(string(b), "on-agent") {
		t.Errorf("Herdr shows agent states itself; the plugin should not follow them:\n%s", b)
	}
}

func TestHerdrStartNamesAgentsClaudeAndNumbersTakenNames(t *testing.T) {
	h, f, _ := herdrHarness(t)
	f.Results["workspace.create"] = `{"type":"workspace_created","workspace":{"workspace_id":"w9","label":"x"},"root_pane":{"pane_id":"w9:p1"}}`
	f.Errors["agent.start"] = "agent name claude is already used; candidates: pane_id=w1:p1"
	h.app.NewHerdr = func() HerdrAPI { return takenOnce{HerdrAPI: herdr.New(f.Path), f: f} }
	if _, err := h.app.startInWorkspace(context.Background(), h.app.NewHerdr(), "claude", "/src/web", "DEMO-7 · web", "prompt", "", true); err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, r := range f.Requests {
		if r["method"] == "agent.start" {
			names = append(names, r["params"].(map[string]any)["name"].(string))
		}
	}
	if strings.Join(names, ",") != "claude,claude-2" {
		t.Errorf("names tried = %v", names)
	}
}

func TestHerdrStartReusesOpenWorkspaceWithoutAgent(t *testing.T) {
	tests := []struct {
		name      string
		agents    string
		startErr  string
		splitErr  string
		wantReuse bool
		wantPane  string
	}{
		{name: "idle workspace", agents: `[]`, wantReuse: true, wantPane: "w4:p1"},
		{name: "agent already there", agents: `[{"pane_id":"w4:p2","workspace_id":"w4","agent":"claude"}]`, wantPane: "w9:p1"},
		{name: "pane busy gets a split", agents: `[]`, startErr: "pane is not at an interactive shell prompt", wantReuse: true, wantPane: "w4:p3"},
		{name: "split refused", agents: `[]`, startErr: "pane is not at an interactive shell prompt", splitErr: "no room", wantPane: "w9:p1"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h, f, _ := herdrHarness(t)
			f.Results["workspace.create"] = `{"type":"workspace_created","workspace":{"workspace_id":"w9","label":"x"},"root_pane":{"pane_id":"w9:p1"}}`
			f.Results["pane.list"] = `{"type":"pane_list","panes":[{"pane_id":"w3:p1","workspace_id":"w3","cwd":"/src/api"},{"pane_id":"w4:p1","workspace_id":"w4","cwd":"/src/web/"}]}`
			f.Results["agent.list"] = `{"type":"agent_list","agents":` + tt.agents + `}`
			f.Results["pane.split"] = `{"type":"pane_info","pane":{"pane_id":"w4:p3","workspace_id":"w4","cwd":"/src/web"}}`
			if tt.splitErr != "" {
				f.Errors["pane.split"] = tt.splitErr
			}
			h.app.NewHerdr = func() HerdrAPI { return herdr.New(f.Path) }
			if tt.startErr != "" {
				f.Errors["agent.start"] = tt.startErr
				h.app.NewHerdr = func() HerdrAPI { return takenOnce{HerdrAPI: herdr.New(f.Path), f: f} }
			}
			if _, err := h.app.startInWorkspace(context.Background(), h.app.NewHerdr(), "claude", "/src/web", "DEMO-7 · web", "Work on DEMO-7", "", true); err != nil {
				t.Fatal(err)
			}
			wantPane := tt.wantPane
			if tt.startErr != "" {
				if got := f.Last("pane.split"); got["target_pane_id"] != "w4:p1" || got["cwd"] != "/src/web" {
					t.Errorf("pane.split = %v", got)
				}
			}
			if tt.wantReuse {
				if got := f.Last("workspace.focus"); got["workspace_id"] != "w4" {
					t.Errorf("focus = %v", got)
				}
			}
			if created := count(f, "workspace.create") == 1; created == tt.wantReuse {
				t.Errorf("workspace.create calls = %d", count(f, "workspace.create"))
			}
			if got := f.Last("agent.start"); got["pane_id"] != wantPane {
				t.Errorf("agent.start = %v", got)
			}
			if got := f.Last("agent.prompt"); got["target"] != "claude" {
				t.Errorf("agent.prompt = %v", got)
			}
		})
	}
}

func TestHerdrStartBranchesInPlaceOnlyInAFreeCheckout(t *testing.T) {
	tests := []struct {
		name    string
		agents  string
		status  string
		head    string
		inPlace bool
	}{
		{name: "free workspace, clean checkout on its default branch", agents: `[]`, head: "main", inPlace: true},
		{name: "local changes", agents: `[]`, status: " M src/app.ts", head: "main"},
		{name: "on another branch", agents: `[]`, head: "feature/DEMO-1-filter"},
		{name: "workspace has an agent", agents: `[{"pane_id":"w4:p2","workspace_id":"w4","agent":"claude"}]`, head: "main"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h, f, _ := herdrHarness(t)
			f.Results["workspace.create"] = `{"type":"workspace_created","workspace":{"workspace_id":"w9","label":"x"},"root_pane":{"pane_id":"w9:p1"}}`
			f.Results["pane.list"] = `{"type":"pane_list","panes":[{"pane_id":"w4:p1","workspace_id":"w4","cwd":"/src/web"}]}`
			f.Results["agent.list"] = `{"type":"agent_list","agents":` + tt.agents + `}`
			h.run.out["git status --porcelain"] = tt.status
			h.run.out["git rev-parse --abbrev-ref HEAD"] = tt.head + "\n"
			h.run.out["git symbolic-ref --short refs/remotes/origin/HEAD"] = "origin/main\n"
			if _, err := h.app.startInWorkspace(context.Background(), h.app.NewHerdr(), "claude", "/src/web", "DEMO-7 · web", "worktree prompt", "branch prompt", true); err != nil {
				t.Fatal(err)
			}
			want := "worktree prompt"
			if tt.inPlace {
				want = "branch prompt"
			}
			if got := f.Last("agent.prompt"); got["text"] != want {
				t.Errorf("prompt = %v, want %q", got["text"], want)
			}
		})
	}
}

func TestHerdrStartRetriesPromptUntilAgentNameIsActive(t *testing.T) {
	h, f, _ := herdrHarness(t)
	f.Results["workspace.create"] = `{"type":"workspace_created","workspace":{"workspace_id":"w9","label":"x"},"root_pane":{"pane_id":"w9:p1"}}`
	f.Errors["agent.prompt"] = "agent_not_ready: agent claude is not an active named agent"
	var slept []time.Duration
	h.app.Sleep = func(_ context.Context, d time.Duration) error {
		slept = append(slept, d)
		if len(slept) == 3 {
			delete(f.Errors, "agent.prompt")
		}
		return nil
	}
	if _, err := h.app.startInWorkspace(context.Background(), h.app.NewHerdr(), "claude", "/src/web", "DEMO-7 · web", "Work on DEMO-7", "", true); err != nil {
		t.Fatal(err)
	}
	if n := count(f, "agent.prompt"); n != 4 || len(slept) != 3 {
		t.Errorf("prompts = %d, sleeps = %v", n, slept)
	}
	if out := h.out.String(); !strings.Contains(out, "✓ claude started in workspace DEMO-7 · web (w9)") {
		t.Errorf("output = %q", out)
	}
}

func TestHerdrStartHandsPromptToUserWhenHerdrRefusesIt(t *testing.T) {
	h, f, _ := herdrHarness(t)
	f.Results["workspace.create"] = `{"type":"workspace_created","workspace":{"workspace_id":"w9","label":"x"},"root_pane":{"pane_id":"w9:p1"}}`
	f.Errors["agent.prompt"] = "agent_not_ready: agent claude is not an active named agent"
	if _, err := h.app.startInWorkspace(context.Background(), h.app.NewHerdr(), "claude", "/src/web", "DEMO-7 · web", "Work on DEMO-7", "", true); err != nil {
		t.Fatal(err)
	}
	if out := h.out.String(); !strings.Contains(out, "did not get its first prompt") || !strings.Contains(out, "give it this prompt:\n\nWork on DEMO-7") {
		t.Errorf("output = %q", out)
	}
}

func TestHerdrStartWaitsForBlockedAgentBeforePrompting(t *testing.T) {
	tests := []struct {
		name       string
		waitErr    string
		wantPrompt bool
	}{
		{name: "user answers", wantPrompt: true},
		{name: "nobody answers", waitErr: "timeout: agent did not reach idle"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h, f, _ := herdrHarness(t)
			f.Results["workspace.create"] = `{"type":"workspace_created","workspace":{"workspace_id":"w9","label":"x"},"root_pane":{"pane_id":"w9:p1"}}`
			f.Errors["agent.start"] = "agent_not_ready: claude is blocked"
			if tt.waitErr != "" {
				f.Errors["agent.wait"] = tt.waitErr
			}
			if _, err := h.app.startInWorkspace(context.Background(), h.app.NewHerdr(), "claude", "/src/web", "DEMO-7 · web", "Work on DEMO-7", "", true); err != nil {
				t.Fatal(err)
			}
			if got := f.Last("agent.wait"); got["target"] != "claude" || got["until"].([]any)[0] != "idle" {
				t.Errorf("agent.wait = %v", got)
			}
			out := h.out.String()
			if !strings.Contains(out, "answer it there") {
				t.Errorf("output = %q", out)
			}
			if got := f.Last("agent.prompt"); (got["text"] == "Work on DEMO-7") != tt.wantPrompt {
				t.Errorf("agent.prompt = %v", got)
			}
			if !tt.wantPrompt && !strings.Contains(out, "give it this prompt:\n\nWork on DEMO-7") {
				t.Errorf("prompt not handed to the user: %q", out)
			}
		})
	}
}

// takenOnce lets the first agent.start fail with agent_name_taken and the next ones succeed.
type takenOnce struct {
	HerdrAPI
	f *herdrtest.FakeServer
}

func (o takenOnce) StartAgent(ctx context.Context, paneID, kind, name string, args []string) error {
	err := o.HerdrAPI.StartAgent(ctx, paneID, kind, name, args)
	delete(o.f.Errors, "agent.start")
	return err
}

func TestAReleaseBringsTheInstalledHerdrPluginUpToDate(t *testing.T) {
	old := Version
	t.Cleanup(func() { Version = old })
	h, f, _ := herdrHarness(t)
	dir := filepath.Join(h.app.Env.DataDir(), "herdr-plugin")
	manifest := filepath.Join(dir, herdrplugin.FileName)
	cfg := filepath.Join(h.home, ".config", "herdr", "config.toml")
	_ = os.MkdirAll(filepath.Dir(cfg), 0o755)
	_ = os.MkdirAll(dir, 0o700)
	_ = os.WriteFile(manifest, []byte(herdrplugin.Render("/opt/tkt/bin/tkt", "0.12.0")), 0o600)
	accepted := herdrplugin.AddBlock("", []herdrplugin.Binding{herdrplugin.DefaultBindings[0], {Key: "prefix+alt+t", Action: "tkt.board-pane", Description: "tkt board in a pane"}})
	_ = os.WriteFile(cfg, []byte(accepted), 0o644)

	Version = "dev"
	h.app.syncHerdrPlugin(context.Background())
	if got, _ := os.ReadFile(manifest); !strings.Contains(string(got), `version = "0.12.0"`) || count(f, "plugin.link") != 0 {
		t.Fatalf("a dev build leaves the plugin alone")
	}

	Version = "0.12.3"
	h.app.syncHerdrPlugin(context.Background())
	if got, _ := os.ReadFile(manifest); !strings.Contains(string(got), `version = "0.12.3"`) || count(f, "plugin.link") != 1 {
		t.Errorf("the manifest follows the release and Herdr reads it again: links %d", count(f, "plugin.link"))
	}
	got, _ := os.ReadFile(cfg)
	if strings.Contains(string(got), "prefix+alt+t") || !strings.Contains(string(got), `key = "prefix+d"`) || strings.Contains(string(got), `key = "prefix+i"`) {
		t.Errorf("the moved shortcut follows, a never accepted one is not added:\n%s", got)
	}
	if bak, _ := os.ReadFile(cfg + ".bak-tkt"); string(bak) != accepted {
		t.Error("the config is backed up first")
	}
	if notice := h.app.takeHerdrNotice(); !strings.Contains(notice, "prefix+i (tkt ticket pane)") || !strings.Contains(notice, "tkt herdr install") {
		t.Errorf("notice %q", notice)
	}
	if again := h.app.takeHerdrNotice(); again != "" {
		t.Errorf("the notice shows once: %q", again)
	}
	h.app.syncHerdrPlugin(context.Background())
	if count(f, "plugin.link") != 1 || h.app.takeHerdrNotice() != "" {
		t.Error("an up to date plugin is left alone, and a shortcut is announced once")
	}
}

func TestNoHerdrPluginNothingToSync(t *testing.T) {
	old := Version
	t.Cleanup(func() { Version = old })
	Version = "0.12.3"
	h, f, _ := herdrHarness(t)
	h.app.syncHerdrPlugin(context.Background())
	if count(f, "plugin.link") != 0 {
		t.Error("tkt installs the plugin only when asked")
	}
	if _, err := os.Stat(filepath.Join(h.app.Env.DataDir(), "herdr-plugin")); err == nil {
		t.Error("no plugin folder appears")
	}
}

func TestHerdrPluginSyncRetriesItsShortcutsAndRunsAtHerdrsStart(t *testing.T) {
	old := Version
	t.Cleanup(func() { Version = old })
	Version = "0.12.4"
	h, f, _ := herdrHarness(t)
	dir := filepath.Join(h.app.Env.DataDir(), "herdr-plugin")
	manifest := filepath.Join(dir, herdrplugin.FileName)
	cfg := filepath.Join(h.home, ".config", "herdr", "config.toml")
	_ = os.MkdirAll(filepath.Dir(cfg), 0o755)
	_ = os.MkdirAll(dir, 0o700)
	_ = os.WriteFile(manifest, []byte(herdrplugin.Render("/opt/tkt/bin/tkt", "0.12.0")), 0o600)
	_ = os.WriteFile(cfg, []byte(herdrplugin.AddBlock("", []herdrplugin.Binding{{Key: "prefix+alt+t", Action: "tkt.board-pane", Description: "tkt board in a pane"}})), 0o644)
	defaults := h.run.out["herdr --default-config"]
	delete(h.run.out, "herdr --default-config")
	h.app.syncHerdrPlugin(context.Background())
	if got, _ := os.ReadFile(cfg); !strings.Contains(string(got), "prefix+alt+t") {
		t.Fatal("Herdr's defaults unreadable, the shortcuts wait")
	}
	h.run.out["herdr --default-config"] = defaults
	if code := h.exec("herdr", "restore"); code != 0 {
		t.Fatalf("exit %d: %s", code, h.errOut)
	}
	if got, _ := os.ReadFile(cfg); strings.Contains(string(got), "prefix+alt+t") || !strings.Contains(string(got), `key = "prefix+d"`) {
		t.Errorf("Herdr's start retries the shortcuts:\n%s", got)
	}
	if got, _ := os.ReadFile(manifest); !strings.Contains(string(got), `version = "0.12.4"`) || count(f, "plugin.link") != 1 {
		t.Errorf("the manifest is rewritten once: links %d", count(f, "plugin.link"))
	}
}

func TestAnAgentMarkedWithATicketBelongsToItOnAnyBranch(t *testing.T) {
	h, f, _ := herdrHarness(t)
	h.env["HERDR_ENV"] = "1"
	f.Results["workspace.list"] = `{"type":"workspace_list","workspaces":[{"workspace_id":"w5","label":"cloud-web"}]}`
	f.Results["pane.list"] = `{"type":"pane_list","panes":[{"pane_id":"w5:p1","workspace_id":"w5","cwd":"/src/web"}]}`
	f.Results["agent.list"] = `{"type":"agent_list","agents":[{"pane_id":"w5:p1","workspace_id":"w5","agent":"claude","agent_status":"working","cwd":"/src/web","tokens":{"tkt_key":"DEMO-5"}}]}`
	h.run.out["git rev-parse --abbrev-ref HEAD"] = "trunk"
	c, err := h.app.LoadConfig()
	if err != nil {
		t.Fatal(err)
	}
	agents := h.app.agentStatesQuietly(context.Background(), c, board.Snapshot{})
	if agents["DEMO-5"] == "" {
		t.Errorf("the pane's mark names the ticket when the branch does not: %v", agents)
	}
}
