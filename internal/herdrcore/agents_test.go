package herdrcore_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/pavp/tkt/internal/herdrcore"
	"github.com/pavp/tkt/internal/herdrport"
)

// seqHC answers each Run with the next output and streams events, then holds the stream open until ctx ends
// when hold is set.
type seqHC struct {
	outs   []string
	calls  []call
	events []string
	hold   bool
}

func (f *seqHC) Run(_ context.Context, stdin []byte, args ...string) (herdrcore.Output, error) {
	f.calls = append(f.calls, call{string(stdin), args})
	if len(f.outs) == 0 {
		return herdrcore.Output{Code: 1, Stderr: []byte("unexpected hc call")}, nil
	}
	o := f.outs[0]
	f.outs = f.outs[1:]
	if strings.HasPrefix(o, "!") {
		return herdrcore.Output{Code: 3, Stderr: []byte(o[1:])}, nil
	}
	return herdrcore.Output{Stdout: []byte(o)}, nil
}

func (f *seqHC) Stream(ctx context.Context, args ...string) (io.ReadCloser, error) {
	f.calls = append(f.calls, call{"", args})
	r, w := io.Pipe()
	lines, hold := f.events, f.hold
	go func() {
		for _, l := range lines {
			if _, err := io.WriteString(w, l+"\n"); err != nil {
				return
			}
		}
		if hold {
			<-ctx.Done()
		}
		w.Close()
	}()
	return r, nil
}

func hcErr(kind string) string {
	return `!{"error":{"kind":"` + kind + `","message":"` + kind + ` from hc"}}`
}

func got(addr, status, labels string) string {
	return `{"jsonrpc":"2.0","id":1,"result":{"address":"` + addr + `","name":"claude","cwd":"/home/dev/code/web-DEMO-123","state":"live","status":` + status + `,"labels":` + labels + `}}`
}

func status(state string) string {
	return `{"app":"claude-code","state":"` + state + `"}`
}

const (
	placed     = `{"herdr.space":"sp1","herdr.tab":"tb1","where":"web / 1"}`
	ran        = `{"address":"t12","name":"claude","argv":["claude"],"cwd":"/home/dev/code/web-DEMO-123","state":"live","labels":{}}`
	snapshot12 = `{"seq":1,"terminals":[{"address":"t12","status":null}]}`
)

func TestStartAgent(t *testing.T) {
	where := []string{"run", "--bg", "--json", "--no-parent", "--name", "claude", "--cwd", "/home/dev/code/web-DEMO-123"}
	inSpace := append(append([]string{}, where...), "--label", "herdr.space=sp1", "--label", "herdr.tab=tb1")
	tests := []struct {
		name    string
		kind    string
		args    []string
		outs    []string
		events  []string
		wantErr error
		wantRun []string
	}{
		{"claude beside the pane, in its space and tab", "claude", nil,
			[]string{got("t3", "null", placed), ran, got("t12", "null", "{}")},
			[]string{snapshot12, ev("status", "t12", `{"status":`+status("working")+`}`), ev("status", "t12", `{"status":`+status("idle")+`}`)},
			nil, append(append([]string{}, inSpace...), "--", "claude")},
		{"codex runs without its daemon, before its args", "codex", []string{"--model", "demo"},
			[]string{got("t3", "null", placed), ran, got("t12", status("idle"), "{}")}, nil,
			nil, append(append([]string{}, inSpace...), "--", "codex", "--no-daemon", "--model", "demo")},
		{"a pane outside any space places nothing", "claude", nil,
			[]string{got("t3", "null", "{}"), ran, got("t12", status("done"), "{}")}, nil,
			nil, append(append([]string{}, where...), "--", "claude")},
		{"a question at startup is not ready", "claude", nil,
			[]string{got("t3", "null", placed), ran, got("t12", "null", "{}")},
			[]string{snapshot12, ev("status", "t12", `{"status":`+status("blocked")+`}`)},
			herdrport.ErrNotReady, append(append([]string{}, inSpace...), "--", "claude")},
		{"an agent that exits while starting", "claude", nil,
			[]string{got("t3", "null", placed), ran, got("t12", "null", "{}")},
			[]string{snapshot12, ev("finished", "t12", `{"exit_code":1}`)},
			herdrcore.ErrExited, append(append([]string{}, inSpace...), "--", "claude")},
		{"a taken name", "claude", nil,
			[]string{got("t3", "null", placed), hcErr("name_taken")}, nil,
			herdrport.ErrNameTaken, append(append([]string{}, inSpace...), "--", "claude")},
		{"an unknown pane", "claude", nil, []string{hcErr("not_found")}, nil, herdrcore.ErrNotFound, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := &seqHC{outs: tt.outs, events: tt.events}
			err := herdrcore.NewWith(f).StartAgent(context.Background(), "t3", tt.kind, "claude", tt.args)
			if !errors.Is(err, tt.wantErr) || (tt.wantErr == nil) != (err == nil) {
				t.Fatalf("StartAgent = %v, want %v", err, tt.wantErr)
			}
			if c := f.calls[0]; !reflect.DeepEqual(c.args, []string{"connect"}) || !strings.Contains(c.stdin, `"method":"terminal.get"`) || !strings.Contains(c.stdin, `"terminal":"t3"`) {
				t.Errorf("pane lookup = %+v", c)
			}
			if tt.wantRun == nil {
				return
			}
			if !reflect.DeepEqual(f.calls[1].args, tt.wantRun) {
				t.Errorf("run args = %q\nwant       %q", f.calls[1].args, tt.wantRun)
			}
			if len(f.calls) > 3 && !reflect.DeepEqual(f.calls[3].args, []string{"events", "--json", "--terminal", "t12"}) {
				t.Errorf("wait args = %q", f.calls[3].args)
			}
		})
	}
}

func TestWaitAgent(t *testing.T) {
	tests := []struct {
		name    string
		status  string
		timeout time.Duration
		outs    []string
		events  []string
		hold    bool
		wantErr error
		streams bool
	}{
		{"already idle", "idle", time.Minute, []string{got("t12", status("idle"), "{}")}, nil, false, nil, false},
		{"done is ready like idle", "idle", time.Minute, []string{got("t12", status("done"), "{}")}, nil, false, nil, false},
		{"idle once a question is answered", "idle", time.Minute, []string{got("t12", status("blocked"), "{}")},
			[]string{snapshot12, ev("status", "t12", `{"status":`+status("blocked")+`}`), ev("status", "t12", `{"status":`+status("idle")+`}`)}, false, nil, true},
		{"idle once a question hc shows without status is answered", "idle", time.Minute, []string{got("t12", "null", "{}")},
			[]string{snapshot12, ev("status", "t12", `{"status":`+status("idle")+`}`)}, false, nil, true},
		{"working", "working", time.Minute, []string{got("t12", status("idle"), "{}")},
			[]string{ev("status", "t12", `{"status":`+status("working")+`}`)}, false, nil, true},
		{"a missed stream resyncs", "idle", time.Minute, []string{got("t12", status("working"), "{}")},
			[]string{`{"jsonrpc":"2.0","method":"missed","params":{"seq":9,"terminals":[{"address":"t12","status":` + status("idle") + `}]}}`}, false, nil, true},
		{"the agent exits", "idle", time.Minute, []string{got("t12", status("working"), "{}")},
			[]string{ev("lost", "t12", `{}`)}, false, herdrcore.ErrExited, true},
		{"the agent is removed from the snapshot", "idle", time.Minute, []string{got("t12", status("working"), "{}")},
			[]string{`{"seq":1,"terminals":[]}`}, false, herdrcore.ErrExited, true},
		{"timeout", "idle", 50 * time.Millisecond, []string{got("t12", status("working"), "{}")},
			[]string{snapshot12}, true, herdrcore.ErrTimeout, true},
		{"unknown agent", "idle", time.Minute, []string{hcErr("not_found")}, nil, false, herdrcore.ErrNotFound, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := &seqHC{outs: tt.outs, events: tt.events, hold: tt.hold}
			err := herdrcore.NewWith(f).WaitAgent(context.Background(), "claude", tt.status, tt.timeout)
			if !errors.Is(err, tt.wantErr) || (tt.wantErr == nil) != (err == nil) {
				t.Fatalf("WaitAgent = %v, want %v", err, tt.wantErr)
			}
			if c := f.calls[0]; !strings.Contains(c.stdin, `"method":"terminal.get"`) || !strings.Contains(c.stdin, `"terminal":"claude"`) {
				t.Errorf("lookup = %+v", c)
			}
			if streamed := len(f.calls) > 1; streamed != tt.streams {
				t.Fatalf("calls = %+v, want streaming %v", f.calls, tt.streams)
			}
			if tt.streams && !reflect.DeepEqual(f.calls[1].args, []string{"events", "--json", "--terminal", "t12"}) {
				t.Errorf("stream args = %q", f.calls[1].args)
			}
		})
	}
}

func TestWaitAgentCanceled(t *testing.T) {
	f := &seqHC{outs: []string{got("t12", status("working"), "{}")}, hold: true}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if err := herdrcore.NewWith(f).WaitAgent(ctx, "claude", "idle", time.Minute); !errors.Is(err, context.DeadlineExceeded) || errors.Is(err, herdrcore.ErrTimeout) {
		t.Errorf("WaitAgent = %v, want the caller's context error", err)
	}
}

func rpc(result string) string { return `{"jsonrpc":"2.0","id":1,"result":` + result + `}` }

func rpcErr(kind string) string {
	return `{"jsonrpc":"2.0","id":1,"error":{"code":1,"message":"` + kind + ` from hc","data":{"kind":"` + kind + `"}}}`
}

// Claude Code's folder trust question shows in hc as a live terminal with no status at all, never as blocked; Enter
// there picks "No, exit".
func TestStartAgentWithoutStatusIsAskingAQuestion(t *testing.T) {
	tests := []struct {
		name    string
		outs    []string
		events  []string
		wantErr error
	}{
		{"no status past the grace is not ready", []string{got("t3", "null", placed), ran, got("t12", "null", "{}"), got("t12", "null", "{}")},
			[]string{snapshot12}, herdrport.ErrNotReady},
		{"a status within the grace keeps waiting until ready", []string{got("t3", "null", placed), ran, got("t12", "null", "{}"), got("t12", status("working"), "{}"), got("t12", status("working"), "{}")},
			[]string{snapshot12}, context.DeadlineExceeded},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := &seqHC{outs: tt.outs, events: tt.events, hold: true}
			c := herdrcore.NewWith(f)
			herdrcore.SetQuestionAfter(c, 30*time.Millisecond)
			ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
			defer cancel()
			err := c.StartAgent(ctx, "t3", "claude", "claude", nil)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("StartAgent = %v, want %v", err, tt.wantErr)
			}
			for _, call := range f.calls {
				if len(call.args) > 0 && call.args[0] == "keys" || strings.Contains(call.stdin, "terminal.send") {
					t.Errorf("typed into the agent: %+v", call)
				}
			}
		})
	}
}

func TestPrompt(t *testing.T) {
	send := []string{"connect"}
	ready := got("t12", status("idle"), "{}")
	tests := []struct {
		name    string
		outs    []string
		wantErr error
		want    [][]string
	}{
		{"sent and started", []string{ready, rpc(`{"started":true,"status":"idle","turn":3}`)}, nil, [][]string{send, send}},
		{"a long paste left unsent gets Enter again", []string{ready, rpc(`{"started":false,"status":"idle","turn":3}`), got("t12", status("idle"), "{}"), `{"status":"working","turn":3}`},
			nil, [][]string{send, send, {"connect"}, {"keys", "--json", "t12", "enter"}}},
		{"a question is never answered by that Enter", []string{ready, rpc(`{"started":false,"status":"idle","turn":3}`), got("t12", status("blocked"), "{}")},
			nil, [][]string{send, send, {"connect"}}},
		{"nor is a question hc shows without status", []string{ready, rpc(`{"started":false,"status":"idle","turn":3}`), got("t12", "null", "{}")},
			nil, [][]string{send, send, {"connect"}}},
		{"an agent without status gets nothing: it may be asking whether to trust the folder", []string{got("t12", "null", "{}")},
			herdrport.ErrBlocked, [][]string{send}},
		{"a blocked agent refuses", []string{ready, rpcErr("blocked")}, herdrport.ErrBlocked, [][]string{send, send}},
		{"a finished agent", []string{ready, rpcErr("finished")}, herdrcore.ErrExited, [][]string{send, send}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := &seqHC{outs: tt.outs}
			err := herdrcore.NewWith(f).Prompt(context.Background(), "claude", "- fix the DEMO-123 tests")
			if !errors.Is(err, tt.wantErr) || (tt.wantErr == nil) != (err == nil) {
				t.Fatalf("Prompt = %v, want %v", err, tt.wantErr)
			}
			var args [][]string
			for _, c := range f.calls {
				args = append(args, c.args)
			}
			if !reflect.DeepEqual(args, tt.want) {
				t.Errorf("calls = %q\nwant    %q", args, tt.want)
			}
			if len(f.calls) < 2 {
				return
			}
			// Sent without a caller, the prompt arrives as typed, not wrapped as a report from tkt's terminal.
			var req struct {
				Method string         `json:"method"`
				Params map[string]any `json:"params"`
			}
			if err := json.Unmarshal([]byte(f.calls[1].stdin), &req); err != nil || req.Method != "terminal.send" ||
				!reflect.DeepEqual(req.Params, map[string]any{"terminal": "claude", "text": "- fix the DEMO-123 tests"}) {
				t.Errorf("send request = %s", f.calls[1].stdin)
			}
		})
	}
}

func TestAgentKinds(t *testing.T) {
	list := `{"jsonrpc":"2.0","id":1,"result":{"terminals":[
{"address":"t1","cwd":"/home/dev/code/a","status":{"app":"claude-code","state":"idle"}},
{"address":"t2","cwd":"/home/dev/code/b","status":{"app":"codex","state":"idle"}},
{"address":"t3","cwd":"/home/dev/code/c","status":{"app":"pi","state":"done"}}]}}`
	agents, err := herdrcore.NewWith(&seqHC{outs: []string{list}}).Agents(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var kinds []string
	for _, a := range agents {
		kinds = append(kinds, a.Agent)
	}
	if want := []string{"claude", "codex", "pi"}; !reflect.DeepEqual(kinds, want) {
		t.Errorf("kinds = %q, want %q", kinds, want)
	}
}

func TestRunDetached(t *testing.T) {
	f := &seqHC{outs: []string{ran}}
	if err := herdrcore.NewWith(f).RunDetached(context.Background(), "/home/dev/.local/share/tkt", []string{"sh", "-c", "exit 0"}); err != nil {
		t.Fatal(err)
	}
	want := []string{"run", "--bg", "--json", "--no-parent", "--rm", "--cwd", "/home/dev/.local/share/tkt", "--", "sh", "-c", "exit 0"}
	if len(f.calls) != 1 || !reflect.DeepEqual(f.calls[0].args, want) {
		t.Errorf("calls = %+v\nwant %q", f.calls, want)
	}
}
