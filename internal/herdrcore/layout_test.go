package herdrcore_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"reflect"
	"testing"
	"time"

	"github.com/pavp/tkt/internal/herdrcore"
	"github.com/pavp/tkt/internal/herdrport"
)

// fakeApp answers each herdr-app run with the next queued output, and records what it was asked.
type fakeApp struct {
	outs  []herdrcore.Output
	calls [][]string
}

func (f *fakeApp) Run(_ context.Context, _ []byte, args ...string) (herdrcore.Output, error) {
	f.calls = append(f.calls, args)
	if len(f.outs) == 0 {
		return herdrcore.Output{Stdout: []byte(`{}`)}, nil
	}
	out := f.outs[0]
	f.outs = f.outs[1:]
	return out, nil
}

func (f *fakeApp) Stream(context.Context, ...string) (io.ReadCloser, error) {
	return nil, errors.New("herdr-app does not stream")
}

func ok(s string) herdrcore.Output { return herdrcore.Output{Stdout: []byte(s)} }

const appLs = `{"spaces":[
{"id":"sp1","name":"DEMO-123 login","folder":"/home/dev/code","machine":null,"active":false,"tabs":[
 {"id":"tb1","name":"1","active":true,"settled":false,"terminals":[
  {"terminal":"t3","pane":"p31","status":null,"app":null,"name":null,"sub_agents":[]},
  {"terminal":"t7","pane":"p77","status":"working","app":"claude-code","name":"claude","sub_agents":[]}]}]},
{"id":"sp2","name":"","folder":"/home/dev/code/api","machine":null,"active":true,"tabs":[
 {"id":"tb2","name":"1","active":true,"settled":false,"terminals":[
  {"terminal":"t9","pane":"p90","status":null,"app":null,"name":null,"sub_agents":[]}]}]}
],"recent":[]}`

const noWindow = "herdr-app: no Herdr window at /home/dev/.config/herdr-app/app.sock\n"

// doCall decodes a recorded `herdr-app do <command> <json>` into its command and arguments.
func doCall(t *testing.T, args []string) (string, map[string]any) {
	t.Helper()
	if len(args) != 3 || args[0] != "do" {
		t.Fatalf("not a do call: %q", args)
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(args[2]), &m); err != nil {
		t.Fatalf("do %s args %q: %v", args[1], args[2], err)
	}
	return args[1], m
}

type doWant struct {
	cmd  string
	args map[string]any
}

func checkDos(t *testing.T, calls [][]string, want []doWant) {
	t.Helper()
	var got []doWant
	for _, c := range calls {
		if c[0] != "do" {
			continue
		}
		cmd, m := doCall(t, c)
		got = append(got, doWant{cmd, m})
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("do calls = %+v, want %+v", got, want)
	}
}

func TestWorkspaces(t *testing.T) {
	app := &fakeApp{outs: []herdrcore.Output{ok(appLs)}}
	wss, err := herdrcore.NewWithApp(newFake(), app).Workspaces(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	want := []herdrport.Workspace{{ID: "sp1", Label: "DEMO-123 login"}, {ID: "sp2", Label: "api"}}
	if !reflect.DeepEqual(wss, want) {
		t.Errorf("Workspaces = %+v, want %+v", wss, want)
	}
	if !reflect.DeepEqual(app.calls, [][]string{{"ls"}}) {
		t.Errorf("calls = %q", app.calls)
	}
}

func TestCreateWorkspace(t *testing.T) {
	tests := []struct {
		name     string
		outs     []herdrcore.Output
		wantPane herdrport.Pane
		wantLs   bool
	}{
		{"reads the new terminal from the reply", []herdrcore.Output{ok(`{"space":"sp2","tab":"tb2","pane":"p90","terminal":"t9"}`)},
			herdrport.Pane{ID: "t9", WorkspaceID: "sp2", Cwd: "/home/dev/code/api"}, false},
		{"finds the terminal in ls when the reply names only the space", []herdrcore.Output{ok(`{"space":"sp2"}`), ok(appLs)},
			herdrport.Pane{ID: "t9", WorkspaceID: "sp2", Cwd: "/home/dev/code/api"}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			app := &fakeApp{outs: tt.outs}
			ws, pane, err := herdrcore.NewWithApp(newFake(), app).CreateWorkspace(context.Background(), "/home/dev/code/api", "DEMO-123 api", false)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(ws, herdrport.Workspace{ID: "sp2", Label: "DEMO-123 api"}) || pane != tt.wantPane {
				t.Errorf("CreateWorkspace = %+v, %+v", ws, pane)
			}
			checkDos(t, app.calls, []doWant{{"space.new", map[string]any{"name": "DEMO-123 api", "folder": "/home/dev/code/api", "machine": ""}}})
			if got := len(app.calls) == 2 && app.calls[1][0] == "ls"; got != tt.wantLs {
				t.Errorf("calls = %q", app.calls)
			}
		})
	}
}

func TestLayoutCommands(t *testing.T) {
	ctx := context.Background()
	tests := []struct {
		name string
		act  func(c *herdrcore.Client) error
		outs []herdrcore.Output
		want []doWant
	}{
		{"focus workspace selects the space", func(c *herdrcore.Client) error { return c.FocusWorkspace(ctx, "sp1") }, nil,
			[]doWant{{"space.select", map[string]any{"id": "sp1"}}}},
		{"focus pane focuses the terminal's pane", func(c *herdrcore.Client) error { return c.FocusPane(ctx, "t7") }, nil,
			[]doWant{{"pane.focus", map[string]any{"terminal": "t7"}}}},
		{"close pane closes the pane showing the terminal", func(c *herdrcore.Client) error { return c.ClosePane(ctx, "t7") }, []herdrcore.Output{ok(appLs)},
			[]doWant{{"pane.close", map[string]any{"pane": "p77"}}}},
		{"close workspace closes the space without asking", func(c *herdrcore.Client) error { return c.CloseWorkspace(ctx, "sp1") }, nil,
			[]doWant{{"space.close", map[string]any{"id": "sp1", "confirmed": true}}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			app := &fakeApp{outs: tt.outs}
			if err := tt.act(herdrcore.NewWithApp(newFake(), app)); err != nil {
				t.Fatal(err)
			}
			checkDos(t, app.calls, tt.want)
		})
	}
}

func TestClosePaneNotShown(t *testing.T) {
	app := &fakeApp{outs: []herdrcore.Output{ok(appLs)}}
	err := herdrcore.NewWithApp(newFake(), app).ClosePane(context.Background(), "t42")
	if !errors.Is(err, herdrcore.ErrNotFound) || len(app.calls) != 1 {
		t.Errorf("ClosePane = %v, calls %q", err, app.calls)
	}
}

const terminalGet = `{"jsonrpc":"2.0","id":1,"result":{"address":"t7","name":"claude","cwd":"/home/dev/code/web","labels":{"herdr.space":"sp1","herdr.tab":"tb1"}}}`

func TestSplitPane(t *testing.T) {
	tests := []struct {
		name, cwd  string
		wantFolder map[string]any
	}{
		{"opens beside the pane in the folder asked", "/home/dev/code/web-DEMO-123", map[string]any{"folder": "/home/dev/code/web-DEMO-123", "place": "right"}},
		{"keeps the pane's folder when none is asked", "", map[string]any{"folder": "/home/dev/code/web", "place": "right"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			hc := newFake()
			hc.out = herdrcore.Output{Stdout: []byte(terminalGet)}
			app := &fakeApp{outs: []herdrcore.Output{ok(`{}`), ok(`{"pane":"p12","terminal":"t12"}`)}}
			pane, err := herdrcore.NewWithApp(hc, app).SplitPane(context.Background(), "t7", tt.cwd)
			if err != nil {
				t.Fatal(err)
			}
			if pane != (herdrport.Pane{ID: "t12", WorkspaceID: "sp1", Cwd: tt.wantFolder["folder"].(string)}) {
				t.Errorf("SplitPane = %+v", pane)
			}
			checkDos(t, app.calls, []doWant{{"pane.focus", map[string]any{"terminal": "t7"}}, {"terminal.open", tt.wantFolder}})
		})
	}
}

func TestLayoutErrors(t *testing.T) {
	ctx := context.Background()
	tests := []struct {
		name     string
		out      herdrcore.Output
		sentinel error
	}{
		{"window closed", herdrcore.Output{Stderr: []byte(noWindow), Code: 1}, herdrcore.ErrNoWindow},
		{"command refused", herdrcore.Output{Stdout: []byte(`{ "error": "no space sp9" }`), Code: 1}, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := herdrcore.NewWithApp(newFake(), &fakeApp{outs: []herdrcore.Output{tt.out, tt.out}})
			if _, err := c.Workspaces(ctx); err == nil || tt.sentinel != nil && !errors.Is(err, tt.sentinel) {
				t.Errorf("Workspaces = %v", err)
			}
			err := c.FocusWorkspace(ctx, "sp9")
			if err == nil || tt.sentinel != nil && !errors.Is(err, tt.sentinel) {
				t.Errorf("FocusWorkspace = %v", err)
			}
			if tt.sentinel == nil && err.Error() != "herdr-app space.select: no space sp9" {
				t.Errorf("FocusWorkspace = %q", err)
			}
		})
	}
	if err := herdrcore.NewWith(newFake()).FocusWorkspace(ctx, "sp1"); !errors.Is(err, herdrcore.ErrNoWindow) {
		t.Errorf("without herdr-app: %v", err)
	}
}

func TestWorkspaceMetadataAndNotify(t *testing.T) {
	app := &fakeApp{}
	c := herdrcore.NewWithApp(newFake(), app)
	ctx := context.Background()
	line := "DEMO-123 In Progress"
	if err := c.ReportWorkspaceTokens(ctx, "sp1", "plugin:tkt", map[string]*string{"tkt_ticket": &line}, time.Hour); err != nil {
		t.Errorf("ReportWorkspaceTokens = %v", err)
	}
	if err := c.Notify(ctx, "tkt", "done"); !errors.Is(err, errors.ErrUnsupported) {
		t.Errorf("Notify = %v", err)
	}
	if len(app.calls) != 0 {
		t.Errorf("calls = %q", app.calls)
	}
}
