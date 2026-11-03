package herdrcore_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/pavp/tkt/internal/herdrcore"
	"github.com/pavp/tkt/internal/herdrport"
)

type call struct {
	stdin string
	args  []string
}

type fakeHC struct {
	out    herdrcore.Output
	calls  []call
	stream *io.PipeReader
	feed   *io.PipeWriter
}

func (f *fakeHC) Run(_ context.Context, stdin []byte, args ...string) (herdrcore.Output, error) {
	f.calls = append(f.calls, call{string(stdin), args})
	return f.out, nil
}

func (f *fakeHC) Stream(ctx context.Context, args ...string) (io.ReadCloser, error) {
	f.calls = append(f.calls, call{"", args})
	context.AfterFunc(ctx, func() { f.feed.Close() })
	return f.stream, nil
}

func newFake() *fakeHC {
	r, w := io.Pipe()
	return &fakeHC{stream: r, feed: w}
}

const terminalList = `{"jsonrpc":"2.0","id":1,"result":{"hidden_finished":0,"machines":[],"terminals":[
{"address":"t3","name":null,"cwd":"/home/dev/code/web","state":"live","status":null,"labels":{"herdr.space":"sp1","herdr.tab":"tb1","where":"web / 1"}},
{"address":"t7","name":"claude","cwd":"/home/dev/code/web-DEMO-123","state":"live","status":{"app":"claude-code","state":"working"},"labels":{"herdr.space":"sp2","herdr.tab":"tb2","tkt_key":"DEMO-123"}},
{"address":"t9","name":null,"cwd":"/home/dev/code/api","state":"live","status":{"app":"codex","state":"blocked"}}
]}}`

func TestPanesAndAgents(t *testing.T) {
	f := newFake()
	f.out = herdrcore.Output{Stdout: []byte(terminalList)}
	c := herdrcore.NewWith(f)
	ctx := context.Background()

	panes, err := c.Panes(ctx)
	if err != nil {
		t.Fatal(err)
	}
	wantPanes := []herdrport.Pane{
		{ID: "t3", WorkspaceID: "sp1", Cwd: "/home/dev/code/web"},
		{ID: "t7", WorkspaceID: "sp2", Cwd: "/home/dev/code/web-DEMO-123"},
		{ID: "t9", Cwd: "/home/dev/code/api"},
	}
	if !reflect.DeepEqual(panes, wantPanes) {
		t.Errorf("Panes = %+v", panes)
	}
	if got := f.calls[0]; !reflect.DeepEqual(got.args, []string{"connect"}) || !strings.Contains(got.stdin, `"method":"terminal.list"`) {
		t.Errorf("list call = %+v", got)
	}

	agents, err := c.Agents(ctx)
	if err != nil {
		t.Fatal(err)
	}
	wantAgents := []herdrport.Agent{
		{PaneID: "t7", WorkspaceID: "sp2", Agent: "claude-code", Name: "claude", Status: "working", Cwd: "/home/dev/code/web-DEMO-123",
			Tokens: map[string]string{"herdr.space": "sp2", "herdr.tab": "tb2", "tkt_key": "DEMO-123"}},
		{PaneID: "t9", Agent: "codex", Status: "blocked", Cwd: "/home/dev/code/api"},
	}
	if !reflect.DeepEqual(agents, wantAgents) {
		t.Errorf("Agents = %+v", agents)
	}

	terms, err := c.Terminals(ctx)
	if err != nil || len(terms) != 3 || terms[1].Tab != "tb2" || terms[1].Space != "sp2" {
		t.Errorf("Terminals = %+v, %v", terms, err)
	}
}

func TestReportPaneTokens(t *testing.T) {
	key := "DEMO-123"
	empty := ""
	tests := []struct {
		name   string
		tokens map[string]*string
		want   []string
	}{
		{"sets a value", map[string]*string{"tkt_key": &key}, []string{"label", "t7", "tkt_key=DEMO-123"}},
		{"removes with nil", map[string]*string{"tkt_key": nil}, []string{"label", "t7", "tkt_key="}},
		{"sorts keys", map[string]*string{"tkt_alert": nil, "tkt": &empty}, []string{"label", "t7", "tkt=", "tkt_alert="}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFake()
			if err := herdrcore.NewWith(f).ReportPaneTokens(context.Background(), "t7", "tkt", tt.tokens); err != nil {
				t.Fatal(err)
			}
			if len(f.calls) != 1 || !reflect.DeepEqual(f.calls[0].args, tt.want) {
				t.Errorf("calls = %+v, want %v", f.calls, tt.want)
			}
		})
	}
	f := newFake()
	if err := herdrcore.NewWith(f).ReportPaneTokens(context.Background(), "t7", "tkt", nil); err != nil || len(f.calls) != 0 {
		t.Errorf("no tokens: calls = %+v, %v", f.calls, err)
	}
}

func TestErrors(t *testing.T) {
	tests := []struct {
		name     string
		out      herdrcore.Output
		sentinel error
		text     string
	}{
		{"name taken", herdrcore.Output{Code: 3, Stderr: []byte(`{"error":{"kind":"name_taken","message":"name \"claude\" is taken"}}`)}, herdrport.ErrNameTaken, "name_taken"},
		{"blocked", herdrcore.Output{Code: 3, Stderr: []byte(`{"error":{"kind":"blocked","message":"t7 needs a person"}}`)}, herdrport.ErrBlocked, "needs a person"},
		{"not found", herdrcore.Output{Code: 3, Stderr: []byte(`{"error":{"hint":"see hc ls --all","kind":"not_found","message":"no terminal \"t99\""}}`)}, herdrcore.ErrNotFound, "no terminal"},
		{"plain stderr", herdrcore.Output{Code: 1, Stderr: []byte("core crashed\n")}, nil, "core crashed"},
	}
	key := "DEMO-123"
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFake()
			f.out = tt.out
			err := herdrcore.NewWith(f).ReportPaneTokens(context.Background(), "t7", "tkt", map[string]*string{"tkt_key": &key})
			if err == nil || !strings.Contains(err.Error(), tt.text) {
				t.Fatalf("err = %v, want it to mention %q", err, tt.text)
			}
			if tt.sentinel != nil && !errors.Is(err, tt.sentinel) {
				t.Errorf("err = %v, want %v", err, tt.sentinel)
			}
		})
	}

	f := newFake()
	f.out = herdrcore.Output{Stdout: []byte(`{"jsonrpc":"2.0","id":1,"error":{"code":-32000,"message":"core not reachable","data":{"kind":"unreachable"}}}`)}
	if _, err := herdrcore.NewWith(f).Agents(context.Background()); !errors.Is(err, herdrcore.ErrNotRunning) {
		t.Errorf("list error = %v, want ErrNotRunning", err)
	}
}

func TestNewWithoutHC(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	if _, err := herdrcore.New(); !errors.Is(err, herdrcore.ErrNotInstalled) {
		t.Errorf("New = %v, want ErrNotInstalled", err)
	}
}

const snapshot = `{"channel":null,"seq":10,"terminals":[{"address":"t3","status":null},{"address":"t7","status":{"app":"claude-code","state":"idle"}}]}`

func ev(typ, term, data string) string {
	return `{"jsonrpc":"2.0","method":"event","params":{"seq":11,"terminal":"` + term + `","type":"` + typ + `","data":` + data + `}}`
}

func TestWatch(t *testing.T) {
	all := []herdrport.Subscription{
		{Type: "pane.created"}, {Type: "pane.closed"}, {Type: "pane.exited"}, {Type: "pane.agent_detected"},
		{Type: "pane.agent_status_changed", PaneID: "t7"},
	}
	tests := []struct {
		name  string
		subs  []herdrport.Subscription
		lines []string
		want  []string
		args  []string
	}{
		{"created", all, []string{ev("created", "t12", `{}`)}, []string{"pane.created t12"}, nil},
		{"removed is closed", all, []string{ev("removed", "t3", `{}`)}, []string{"pane.closed t3"}, nil},
		{"finished and lost are exited", all, []string{ev("finished", "t3", `{"exit_code":0}`), ev("lost", "t7", `{}`)}, []string{"pane.exited t3", "pane.exited t7"}, nil},
		{"status of a followed agent", all, []string{ev("status", "t7", `{"status":{"app":"claude-code","state":"working"}}`)}, []string{"pane.agent_status_changed t7"}, nil},
		{"status of an unfollowed pane is dropped", all, []string{ev("status", "t9", `{"status":null}`)}, nil, nil},
		{"first status is a detected agent", all, []string{ev("status", "t3", `{"status":{"app":"codex","state":"idle"}}`)}, []string{"pane.agent_detected t3"}, nil},
		{"unrelated facts are dropped", all, []string{ev("labels", "t7", `{"labels":{}}`), ev("turn", "t7", `{"turn":2}`), `{"jsonrpc":"2.0","method":"heartbeat","params":{"seq":11}}`}, nil, nil},
		{"unsubscribed kinds are dropped", []herdrport.Subscription{{Type: "pane.closed"}}, []string{ev("created", "t12", `{}`), ev("removed", "t12", `{}`)}, []string{"pane.closed t12"}, nil},
		{"missed diffs the fresh snapshot", all, []string{`{"jsonrpc":"2.0","method":"missed","params":{"seq":20,"terminals":[{"address":"t7","status":{"app":"claude-code","state":"done"}},{"address":"t12","status":null}]}}`},
			[]string{"pane.agent_status_changed t7", "pane.created t12", "pane.closed t3"}, nil},
		{"one pane filters by terminal", []herdrport.Subscription{{Type: "pane.agent_status_changed", PaneID: "t7"}}, []string{ev("status", "t7", `{"status":null}`)},
			[]string{"pane.agent_status_changed t7"}, []string{"events", "--json", "--terminal", "t7"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			lines, want := tt.lines, tt.want
			if slices.Contains(tt.subs, herdrport.Subscription{Type: "pane.closed"}) {
				lines = append(slices.Clone(lines), ev("removed", "tz", `{}`))
				want = append(slices.Clone(want), "pane.closed tz")
			}
			f := newFake()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			got := make(chan string, 16)
			done := make(chan error, 1)
			go func() {
				done <- herdrcore.NewWith(f).Watch(ctx, tt.subs, func(e herdrport.Event) {
					var d struct {
						PaneID string `json:"pane_id"`
					}
					_ = json.Unmarshal(e.Data, &d)
					got <- e.Kind + " " + d.PaneID
				})
			}()
			go func() {
				for _, l := range append([]string{snapshot}, lines...) {
					if _, err := io.WriteString(f.feed, l+"\n"); err != nil {
						return
					}
				}
			}()
			var seen []string
			for range want {
				select {
				case s := <-got:
					seen = append(seen, s)
				case <-time.After(2 * time.Second):
					t.Fatalf("events = %v, want %v", seen, want)
				}
			}
			cancel()
			if err := <-done; !errors.Is(err, context.Canceled) {
				t.Errorf("Watch = %v, want context.Canceled", err)
			}
			close(got)
			for s := range got {
				seen = append(seen, s)
			}
			if !reflect.DeepEqual(seen, want) {
				t.Errorf("events = %v, want %v", seen, want)
			}
			if tt.args != nil && !reflect.DeepEqual(f.calls[0].args, tt.args) {
				t.Errorf("args = %v, want %v", f.calls[0].args, tt.args)
			}
			if tt.args == nil && !reflect.DeepEqual(f.calls[0].args, []string{"events", "--json"}) {
				t.Errorf("args = %v", f.calls[0].args)
			}
		})
	}
}

func TestWatchStreamEnds(t *testing.T) {
	f := newFake()
	go func() {
		_, _ = io.WriteString(f.feed, snapshot+"\n")
		f.feed.Close()
	}()
	err := herdrcore.NewWith(f).Watch(context.Background(), []herdrport.Subscription{{Type: "pane.created"}}, func(herdrport.Event) {})
	if err == nil || errors.Is(err, context.Canceled) {
		t.Errorf("Watch = %v, want an error for the ended stream", err)
	}
}
