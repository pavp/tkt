package herdrcore_test

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/pavp/tkt/internal/herdrcore"
	"github.com/pavp/tkt/internal/herdrport"
)

func idsClient(hc herdrcore.Runner, app herdrcore.Runner) *herdrcore.Client {
	c := herdrcore.NewWithApp(hc, app)
	herdrcore.SetNewID(c, func(prefix string) string { return prefix + "new" })
	herdrcore.SetSettle(c, 0, 3)
	return c
}

func appCmds(app *fakeApp) []string {
	var out []string
	for _, c := range app.calls {
		out = append(out, strings.Join(c, " "))
	}
	return out
}

var (
	appStatus  = ok(`{"space":"sp1","focused_pane":"p77","errors":[]}`)
	appRemote  = herdrcore.Output{Stderr: []byte("herdr-app: cannot connect to /home/dev/.config/herdr-app/app.sock\n"), Code: 1}
	ranShell   = `{"jsonrpc":"2.0","id":1,"result":{"address":"t11","name":null,"cwd":"/home/dev/code/api","state":"live","labels":{}}}`
	runNewArgs = []string{"run", "--bg", "--json", "--no-parent", "--cwd", "/home/dev/code/api", "--label", "herdr.space=spnew", "--label", "herdr.tab=tbnew"}
)

func TestCreateWorkspaceStartsAShellLabelledWithANewSpace(t *testing.T) {
	tests := []struct {
		name, label string
		app         []herdrcore.Output
		wantApp     []doWant
	}{
		{"names the space where the window is reachable", "DEMO-123 api", []herdrcore.Output{appStatus},
			[]doWant{{"space.rename", map[string]any{"id": "spnew", "name": "DEMO-123 api"}}}},
		{"leaves an unnamed space to Herdr's name", "", nil, nil},
		{"works where the window cannot be reached", "DEMO-123 api", []herdrcore.Output{appRemote}, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			hc := &seqHC{outs: []string{ran}}
			app := &fakeApp{outs: tt.app}
			ws, pane, err := idsClient(hc, app).CreateWorkspace(context.Background(), "/home/dev/code/api", tt.label, true)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(ws, herdrport.Workspace{ID: "spnew", Label: tt.label}) || pane != (herdrport.Pane{ID: "t12", WorkspaceID: "spnew", Cwd: "/home/dev/code/api"}) {
				t.Errorf("CreateWorkspace = %+v, %+v", ws, pane)
			}
			if got := hcArgs(hc); !reflect.DeepEqual(got, [][]string{runNewArgs}) {
				t.Errorf("hc calls = %q", got)
			}
			checkDos(t, app.calls, tt.wantApp)
		})
	}
}

// The space opens on a shell with the agent beside it, as Herdr's own worktree spaces do, so closing the agent leaves
// the space (a worktree's, nested under its repository's) in the sidebar.
func TestOpenAgentSpaceOpensAShellThenTheAgentBesideIt(t *testing.T) {
	task := "Work on DEMO-123\nthen tkt link DEMO-123"
	tests := []struct {
		name, kind string
		hcOuts     []string
		wantArgv   []string
		wantSend   bool
	}{
		{"claude gets its task as its first message", "claude", []string{ranShell, ran}, []string{"claude", task}, false},
		{"codex too, with what it needs under hc", "codex", []string{ranShell, ran}, []string{"codex", "--no-daemon", task}, false},
		{"another agent is prompted once ready", "pi", []string{ranShell, ran, got("t12", status("idle"), placed), got("t12", status("idle"), placed), rpc(`{"started":true}`)}, []string{"pi"}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			hc := &seqHC{outs: tt.hcOuts}
			app := &fakeApp{outs: []herdrcore.Output{appStatus}}
			ws, pane, err := idsClient(hc, app).OpenAgentSpace(context.Background(), "/home/dev/code/api", "DEMO-123 · api", tt.kind, task)
			if err != nil {
				t.Fatal(err)
			}
			if ws.ID != "spnew" || pane != (herdrport.Pane{ID: "t12", WorkspaceID: "spnew", Cwd: "/home/dev/code/api"}) {
				t.Errorf("OpenAgentSpace = %+v, %+v", ws, pane)
			}
			calls := hcArgs(hc)
			if len(calls) < 2 || !reflect.DeepEqual(calls[0], runNewArgs) {
				t.Errorf("first hc run = %q\nwant           %q (the space's shell)", calls, runNewArgs)
			}
			if want := append(append(append([]string{}, runNewArgs...), "--"), tt.wantArgv...); len(calls) < 2 || !reflect.DeepEqual(calls[1], want) {
				t.Errorf("second hc run = %q\nwant            %q (the agent, in the same space and tab)", calls, want)
			}
			sent := len(hc.calls) > 0 && strings.Contains(hc.calls[len(hc.calls)-1].stdin, `"terminal.send"`)
			if sent != tt.wantSend {
				t.Errorf("sent the task through hc: %v, calls %+v", sent, hc.calls)
			}
			checkDos(t, app.calls, []doWant{{"space.rename", map[string]any{"id": "spnew", "name": "DEMO-123 · api"}}})
		})
	}
}

const terminalGet = `{"jsonrpc":"2.0","id":1,"result":{"address":"t7","name":"claude","cwd":"/home/dev/code/web","labels":{"herdr.space":"sp1","herdr.tab":"tb1"}}}`

func TestSplitPaneOpensAShellInThePanesSpaceThroughHC(t *testing.T) {
	tests := []struct {
		name, cwd, want string
	}{
		{"in the folder asked", "/home/dev/code/web-DEMO-123", "/home/dev/code/web-DEMO-123"},
		{"in the pane's folder when none is asked", "", "/home/dev/code/web"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			hc := &seqHC{outs: []string{terminalGet, ran}}
			app := &fakeApp{}
			pane, err := idsClient(hc, app).SplitPane(context.Background(), "t7", tt.cwd)
			if err != nil {
				t.Fatal(err)
			}
			if pane != (herdrport.Pane{ID: "t12", WorkspaceID: "sp1", Cwd: tt.want}) {
				t.Errorf("SplitPane = %+v", pane)
			}
			want := []string{"run", "--bg", "--json", "--no-parent", "--cwd", tt.want, "--label", "herdr.space=sp1", "--label", "herdr.tab=tb1"}
			if calls := hcArgs(hc); len(calls) != 2 || !reflect.DeepEqual(calls[1], want) {
				t.Errorf("hc calls = %q", calls)
			}
			if len(app.calls) != 0 {
				t.Errorf("herdr-app calls = %q", app.calls)
			}
		})
	}
}

func TestClosePaneEndsAndRemovesTheTerminalThroughHC(t *testing.T) {
	tests := []struct {
		name    string
		hcOuts  []string
		wantErr error
		wantHC  [][]string
	}{
		{"ends then removes it", []string{`{"address":"t7","state":"finished"}`, `{"removed":"t7"}`}, nil,
			[][]string{{"kill", "--json", "t7"}, {"rm", "--json", "t7"}}},
		{"one still ending is removed once it ended", []string{`{"address":"t7","state":"live"}`, hcErr("live"), `{"removed":"t7"}`}, nil,
			[][]string{{"kill", "--json", "t7"}, {"rm", "--json", "t7"}, {"rm", "--json", "t7"}}},
		{"an unknown terminal", []string{hcErr("not_found")}, herdrcore.ErrNotFound, [][]string{{"kill", "--json", "t7"}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			hc := &seqHC{outs: tt.hcOuts}
			app := &fakeApp{}
			err := idsClient(hc, app).ClosePane(context.Background(), "t7")
			if tt.wantErr == nil && err != nil || tt.wantErr != nil && !errors.Is(err, tt.wantErr) {
				t.Errorf("ClosePane = %v, want %v", err, tt.wantErr)
			}
			if got := hcArgs(hc); !reflect.DeepEqual(got, tt.wantHC) {
				t.Errorf("hc calls = %q, want %q", got, tt.wantHC)
			}
			if len(app.calls) != 0 {
				t.Errorf("herdr-app calls = %q", app.calls)
			}
		})
	}
}

func TestCloseWorkspaceEndsEveryTerminalOfTheSpace(t *testing.T) {
	list := `{"jsonrpc":"2.0","id":1,"result":{"terminals":[
{"address":"t3","cwd":"/home/dev/code/web","state":"live","labels":{"herdr.space":"sp1","herdr.tab":"tb1"}},
{"address":"t7","cwd":"/home/dev/code/web","state":"live","labels":{"herdr.space":"sp1","herdr.tab":"tb1"}},
{"address":"t9","cwd":"/home/dev/code/api","state":"live","labels":{"herdr.space":"sp2"}},
{"address":"wsl-dev/t4","cwd":"/home/dev/code","state":"live","labels":{"herdr.space":"sp1"}}]}}`
	tests := []struct {
		name   string
		app    []herdrcore.Output
		wantDo []doWant
	}{
		{"and closes what the window still shows of it", []herdrcore.Output{appStatus, {Stdout: []byte(`{"error":"no space sp1"}`), Code: 1}},
			[]doWant{{"space.close", map[string]any{"id": "sp1", "confirmed": true}}}},
		{"where the window cannot be reached", []herdrcore.Output{appRemote}, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			hc := &seqHC{outs: []string{list, `{}`, `{"removed":"t3"}`, `{}`, `{"removed":"t7"}`}}
			app := &fakeApp{outs: tt.app}
			if err := idsClient(hc, app).CloseWorkspace(context.Background(), "sp1"); err != nil {
				t.Fatal(err)
			}
			want := [][]string{{"connect"}, {"kill", "--json", "t3"}, {"rm", "--json", "t3"}, {"kill", "--json", "t7"}, {"rm", "--json", "t7"}}
			if got := hcArgs(hc); !reflect.DeepEqual(got, want) {
				t.Errorf("hc calls = %q\nwant       %q", got, want)
			}
			checkDos(t, app.calls, tt.wantDo)
		})
	}
}

func TestWindowActionsWhereTheWindowCannotBeReached(t *testing.T) {
	for _, out := range []herdrcore.Output{appRemote, {Stderr: []byte(noWindow), Code: 1}} {
		app := &fakeApp{outs: []herdrcore.Output{out}}
		c := idsClient(newFake(), app)
		ctx := context.Background()
		for _, err := range []error{c.FocusWorkspace(ctx, "sp1"), c.FocusPane(ctx, "t7"), c.FocusWorkspace(ctx, "sp2")} {
			if !errors.Is(err, herdrcore.ErrNoWindow) {
				t.Errorf("window action = %v, want ErrNoWindow", err)
			}
		}
		if got := appCmds(app); !reflect.DeepEqual(got, []string{"status"}) {
			t.Errorf("herdr-app calls = %q, want one status", got)
		}
	}
	if err := herdrcore.NewWith(newFake()).FocusPane(context.Background(), "t7"); !errors.Is(err, herdrcore.ErrNoWindow) {
		t.Errorf("without herdr-app: %v", err)
	}
}

func TestFocusWaitsForTheWindowToShowANewSpace(t *testing.T) {
	notYet := herdrcore.Output{Stdout: []byte(`{"error":"no space spnew"}`), Code: 1}
	app := &fakeApp{outs: []herdrcore.Output{appStatus, notYet, ok(`{}`)}}
	if err := idsClient(newFake(), app).FocusWorkspace(context.Background(), "spnew"); err != nil {
		t.Fatal(err)
	}
	checkDos(t, app.calls, []doWant{{"space.select", map[string]any{"id": "spnew"}}, {"space.select", map[string]any{"id": "spnew"}}})
}

// When the agent cannot start beside it, the space's lone shell goes too, so no empty space is left named after the
// ticket.
func TestOpenAgentSpaceRemovesItsShellWhenTheAgentFails(t *testing.T) {
	hc := &seqHC{outs: []string{ranShell, rpcErr("io"), `{}`, `{}`}}
	c := idsClient(hc, &fakeApp{outs: []herdrcore.Output{appStatus}})
	herdrcore.SetSettle(c, 0, 1)
	if _, _, err := c.OpenAgentSpace(context.Background(), "/home/dev/code/api", "DEMO-123 · api", "claude", "task"); err == nil {
		t.Fatal("OpenAgentSpace succeeded with the agent failing")
	}
	calls := hcArgs(hc)
	if !slices.ContainsFunc(calls, func(a []string) bool { return len(a) > 0 && a[0] == "kill" && slices.Contains(a, "t11") }) {
		t.Errorf("hc calls = %q, want the shell t11 killed", calls)
	}
}
