package herdr_test

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/pavp/tkt/internal/herdr"
	"github.com/pavp/tkt/internal/herdr/herdrtest"
)

func TestSocketPath(t *testing.T) {
	env := map[string]string{}
	get := func(k string) string { return env[k] }
	if got := herdr.SocketPath(get, "/home/u"); got != "/home/u/.config/herdr/herdr.sock" {
		t.Errorf("default = %s", got)
	}
	env["XDG_CONFIG_HOME"] = "/x"
	if got := herdr.SocketPath(get, "/home/u"); got != "/x/herdr/herdr.sock" {
		t.Errorf("xdg = %s", got)
	}
	env["HERDR_SOCKET_PATH"] = "/run/h.sock"
	if got := herdr.SocketPath(get, "/home/u"); got != "/run/h.sock" {
		t.Errorf("env = %s", got)
	}
}

func TestCallsAndDecoding(t *testing.T) {
	f := herdrtest.Start(t)
	f.Results["workspace.list"] = `{"type":"workspace_list","workspaces":[{"workspace_id":"w1","label":"web","tokens":{"tkt":"x"},"worktree":{"checkout_path":"/src/web-DEMO-1","repo_name":"web","repo_root":"/src/web","is_linked_worktree":true}}]}`
	f.Results["workspace.create"] = `{"type":"workspace_created","workspace":{"workspace_id":"w2","label":"api"},"root_pane":{"pane_id":"w2:p1","cwd":"/src/api"},"tab":{}}`
	f.Results["agent.list"] = `{"type":"agent_list","agents":[{"pane_id":"w1:p1","workspace_id":"w1","agent":"claude","agent_status":"working","cwd":"/src/web-DEMO-1"}]}`
	c := herdr.New(f.Path)
	ctx := context.Background()

	ws, err := c.Workspaces(ctx)
	if err != nil || len(ws) != 1 || ws[0].Worktree == nil || ws[0].Worktree.CheckoutPath != "/src/web-DEMO-1" {
		t.Fatalf("Workspaces = %+v, %v", ws, err)
	}
	w, p, err := c.CreateWorkspace(ctx, "/src/api", "api", true)
	if err != nil || w.ID != "w2" || p.ID != "w2:p1" {
		t.Fatalf("CreateWorkspace = %+v %+v %v", w, p, err)
	}
	if got := f.Last("workspace.create"); got["cwd"] != "/src/api" || got["focus"] != true {
		t.Errorf("create params = %v", got)
	}
	agents, err := c.Agents(ctx)
	if err != nil || len(agents) != 1 || agents[0].Status != "working" {
		t.Fatalf("Agents = %+v, %v", agents, err)
	}
	if err := c.StartAgent(ctx, "w2:p1", "claude", "DEMO-1", []string{"do it"}); err != nil {
		t.Fatal(err)
	}
	if got := f.Last("agent.start"); got["kind"] != "claude" || got["pane_id"] != "w2:p1" || got["args"].([]any)[0] != "do it" {
		t.Errorf("start params = %v", got)
	}
	if err := c.WaitAgent(ctx, "w2:p1", "idle", 2*time.Minute); err != nil {
		t.Fatal(err)
	}
	if got := f.Last("agent.wait"); got["target"] != "w2:p1" || got["until"].([]any)[0] != "idle" || got["timeout_ms"] != float64(120000) {
		t.Errorf("wait params = %v", got)
	}
	f.Results["pane.split"] = `{"type":"pane_info","pane":{"pane_id":"w2:p2","workspace_id":"w2","cwd":"/src/web"}}`
	if p, err := c.SplitPane(ctx, "w2:p1", "/src/web"); err != nil || p.ID != "w2:p2" {
		t.Fatalf("SplitPane = %+v, %v", p, err)
	}
	if got := f.Last("pane.split"); got["target_pane_id"] != "w2:p1" || got["direction"] != "right" || got["cwd"] != "/src/web" {
		t.Errorf("split params = %v", got)
	}
	label := "DEMO-1 · CI failing"
	if err := c.ReportWorkspaceTokens(ctx, "w1", "plugin:tkt", map[string]*string{"tkt": &label, "old": nil}, 30*time.Minute); err != nil {
		t.Fatal(err)
	}
	got := f.Last("workspace.report_metadata")
	tokens := got["tokens"].(map[string]any)
	if tokens["tkt"] != label || tokens["old"] != nil || got["ttl_ms"] != float64(1800000) || got["source"] != "plugin:tkt" {
		t.Errorf("report params = %v", got)
	}
	if err := c.OpenPluginPane(ctx, herdr.PaneOpen{PluginID: "tkt", Entrypoint: "board", Placement: "popup"}); err != nil {
		t.Fatal(err)
	}
	if got := f.Last("plugin.pane.open"); got["entrypoint"] != "board" || got["placement"] != "popup" {
		t.Errorf("pane params = %v", got)
	}
	if err := c.Prompt(ctx, "w1:p1", "fix CI"); err != nil {
		t.Fatal(err)
	}
	if got := f.Last("agent.prompt"); got["target"] != "w1:p1" || got["text"] != "fix CI" {
		t.Errorf("prompt params = %v", got)
	}
}

func TestErrors(t *testing.T) {
	f := herdrtest.Start(t)
	f.Errors["agent.start"] = "pane is not at a shell prompt"
	err := herdr.New(f.Path).StartAgent(context.Background(), "p", "claude", "x", nil)
	var re *herdr.RPCError
	if !errors.As(err, &re) || !strings.Contains(err.Error(), "pane is not at a shell prompt") {
		t.Errorf("err = %v", err)
	}
	if err := herdr.New(filepath.Join(t.TempDir(), "none")).Notify(context.Background(), "t", "b"); !errors.Is(err, herdr.ErrNotRunning) {
		t.Errorf("missing socket err = %v", err)
	}
}

func TestLargeResponse(t *testing.T) {
	f := herdrtest.Start(t)
	big := strings.Repeat("x", 200_000)
	f.Results["agent.list"] = `{"type":"agent_list","agents":[{"pane_id":"p","name":"` + big + `"}]}`
	agents, err := herdr.New(f.Path).Agents(context.Background())
	if err != nil || len(agents) != 1 || len(agents[0].Name) != len(big) {
		t.Fatalf("large response: %v", err)
	}
}
