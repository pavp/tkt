package cli

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/pavp/tkt/internal/board"
	"github.com/pavp/tkt/internal/herdr"
	"github.com/pavp/tkt/internal/herdr/herdrtest"
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
	if !strings.Contains(h.errOut.String(), "Add Herdr keybindings prefix+i (tkt ticket pane), prefix+t (tkt board)?") {
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
	if start["kind"] != "claude" || start["pane_id"] != "w9:p1" || !strings.Contains(start["args"].([]any)[0].(string), "Work on Jira ticket DEMO-7") {
		t.Errorf("agent.start = %v", start)
	}
	if len(*calls) != 0 {
		t.Error("launched a terminal agent instead of Herdr")
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
	want := map[string]any{"tkt": nil, "tkt_alert": "DEMO-1 · ✕ CI · #812", "tkt_agent": "● claude working", "tkt_agent_alert": nil, "tkt_agent_idle": nil}
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
	if label := f.Last("workspace.report_metadata")["tokens"].(map[string]any)["tkt"]; label != "DEMO-1 · In Progress" {
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
	h.run.out["git rev-parse --abbrev-ref HEAD"] = "feature/DEMO-1-filter\n"
	if code := h.exec("herdr", "open-ticket"); code != 0 {
		t.Fatalf("exit %d: %s", code, h.errOut)
	}
	got := f.Last("plugin.pane.open")
	if got["entrypoint"] != "ticket" || got["cwd"] != "/src/web-DEMO-1" || got["env"].(map[string]any)["TKT_KEY"] != "DEMO-1" {
		t.Errorf("pane = %v", got)
	}
	h.run.out["git rev-parse --abbrev-ref HEAD"] = "main\n"
	h.exec("herdr", "open-ticket")
	if got := f.Last("notification.show"); got == nil || !strings.Contains(got["body"].(string), "carries no ticket key") {
		t.Errorf("notification = %v", got)
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
	if !strings.Contains(string(b), `token = "$tkt_agent"`) || strings.Contains(string(b), "tkt.board") {
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
	if tok := got["tokens"].(map[string]any)["tkt_agent_idle"]; tok != "no agent" {
		t.Errorf("tokens = %v", got["tokens"])
	}
}
