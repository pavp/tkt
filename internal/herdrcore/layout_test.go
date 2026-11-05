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
	want := []herdrport.Workspace{{ID: "sp1", Label: "DEMO-123 login", Folder: "/home/dev/code"}, {ID: "sp2", Label: "api", Folder: "/home/dev/code/api"}}
	if !reflect.DeepEqual(wss, want) {
		t.Errorf("Workspaces = %+v, want %+v", wss, want)
	}
	if !reflect.DeepEqual(app.calls, [][]string{{"ls"}}) {
		t.Errorf("calls = %q", app.calls)
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
			herdrcore.SetSettle(c, 0, 1)
			if _, err := c.Workspaces(ctx); tt.sentinel == nil && err == nil {
				t.Errorf("Workspaces = %v, want the window's refusal", err)
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

// A machine of the core without a Herdr window of its own (WSL, reached from a window on another computer) still has
// spaces: its terminals carry them as labels, so tkt finds them there rather than failing to tell tickets' agents.
func TestWorkspacesWithoutAWindowComeFromTheTerminals(t *testing.T) {
	f := newFake()
	f.out = herdrcore.Output{Stdout: []byte(terminalList)}
	c := herdrcore.NewWithApp(f, &fakeApp{outs: []herdrcore.Output{{Stderr: []byte(noWindow), Code: 1}}})
	wss, err := c.Workspaces(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	want := []herdrport.Workspace{{ID: "sp1", Label: "web", Folder: "/home/dev/code/web"}, {ID: "sp2", Label: "web-DEMO-123", Folder: "/home/dev/code/web-DEMO-123"}}
	if !reflect.DeepEqual(wss, want) {
		t.Errorf("Workspaces = %+v, want %+v", wss, want)
	}
}
