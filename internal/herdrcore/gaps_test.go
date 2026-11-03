package herdrcore_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/pavp/tkt/internal/herdrcore"
)

func hcArgs(f *seqHC) [][]string {
	var args [][]string
	for _, c := range f.calls {
		args = append(args, runArgs(c))
	}
	return args
}

func TestPanesMarkTheWindowsFocusedPane(t *testing.T) {
	tests := []struct {
		name    string
		outs    []herdrcore.Output
		focused string
	}{
		{"the focused pane's terminal", []herdrcore.Output{ok(`{"space":"sp1","tab":"tb1","focused_pane":"p77","errors":[]}`), ok(appLs)}, "t7"},
		{"none while the window is closed", []herdrcore.Output{{Stderr: []byte(noWindow), Code: 1}}, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			hc := newFake()
			hc.out = herdrcore.Output{Stdout: []byte(terminalList)}
			panes, err := herdrcore.NewWithApp(hc, &fakeApp{outs: tt.outs}).Panes(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			var focused []string
			for _, p := range panes {
				if p.Focused {
					focused = append(focused, p.ID)
				}
			}
			if len(panes) != 3 || strings.Join(focused, ",") != tt.focused {
				t.Errorf("Panes = %+v, want %q focused", panes, tt.focused)
			}
		})
	}
}

func TestPanesAndAgentsSkipFinishedTerminals(t *testing.T) {
	list := `{"jsonrpc":"2.0","id":1,"result":{"terminals":[
{"address":"t1","cwd":"/home/dev/code/a","state":"finished","status":{"app":"claude-code","state":"idle"}},
{"address":"t2","cwd":"/home/dev/code/b","state":"live","status":{"app":"codex","state":"idle"}}]}}`
	c := herdrcore.NewWith(&seqHC{outs: []string{list, list}})
	panes, err := c.Panes(context.Background())
	if err != nil || len(panes) != 1 || panes[0].ID != "t2" {
		t.Errorf("Panes = %+v, %v", panes, err)
	}
	agents, err := c.Agents(context.Background())
	if err != nil || len(agents) != 1 || agents[0].PaneID != "t2" {
		t.Errorf("Agents = %+v, %v", agents, err)
	}
}

func TestAgentPaneIsTheAgentsOwnTerminal(t *testing.T) {
	hc := &seqHC{outs: []string{got("t12", status("idle"), placed)}}
	pane, err := herdrcore.NewWith(hc).AgentPane(context.Background(), "claude-2")
	if err != nil || pane != "t12" {
		t.Errorf("AgentPane = %q, %v", pane, err)
	}
	if len(hc.calls) != 1 || !strings.Contains(hc.calls[0].stdin, `"terminal":"claude-2"`) {
		t.Errorf("calls = %+v", hc.calls)
	}
}

func TestNoWindowErrorSaysToOpenHerdr(t *testing.T) {
	c := herdrcore.NewWithApp(newFake(), &fakeApp{outs: []herdrcore.Output{{Stderr: []byte(noWindow), Code: 1}}})
	err := c.FocusWorkspace(context.Background(), "sp1")
	if !errors.Is(err, herdrcore.ErrNoWindow) || !strings.HasPrefix(err.Error(), "herdr desktop window is not open: open Herdr") {
		t.Errorf("FocusWorkspace = %v", err)
	}
}
