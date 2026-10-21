package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/pavp/tkt/internal/board"
	"github.com/pavp/tkt/internal/ticket"
)

// scriptedSync answers each sync with the next board it holds, or err.
type scriptedSync struct {
	mu     sync.Mutex
	boards []board.Snapshot
	err    error
}

func (s *scriptedSync) sync(context.Context, func(board.Snapshot)) (board.Snapshot, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.err != nil {
		return board.Snapshot{}, s.err
	}
	if len(s.boards) == 0 {
		return board.Snapshot{}, errors.New("no board scripted")
	}
	b := s.boards[0]
	if len(s.boards) > 1 {
		s.boards = s.boards[1:]
	}
	return b, nil
}

type event struct {
	Seq  int             `json:"seq"`
	Type string          `json:"type"`
	TS   string          `json:"ts"`
	Data json.RawMessage `json:"data"`
}

func dial(t *testing.T, srv *httptest.Server, hello string) *websocket.Conn {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	c, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(srv.URL, "http")+"/v1/ws", nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.CloseNow() })
	if hello != "" {
		if err := c.Write(ctx, websocket.MessageText, []byte(hello)); err != nil {
			t.Fatal(err)
		}
	}
	return c
}

// joined dials and says hello, returning once the daemon counts the client, so no event is missed.
func joined(t *testing.T, d *Server, srv *httptest.Server) *websocket.Conn {
	t.Helper()
	before := d.clientCount()
	c := dial(t, srv, hello(d.token))
	waitFor(t, func() bool { return d.clientCount() > before })
	return c
}

func next(t *testing.T, c *websocket.Conn) event {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_, b, err := c.Read(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var e event
	if err := json.Unmarshal(b, &e); err != nil {
		t.Fatalf("%s: %v", b, err)
	}
	return e
}

func hello(token string) string {
	return `{"type":"hello","token":"` + token + `","protocol":1}`
}

func wsServer(t *testing.T, s *scriptedSync, seed *board.Snapshot) (*Server, *httptest.Server) {
	t.Helper()
	d, err := New(Options{Dir: t.TempDir(), Version: "1.2.3", Sync: s.sync, Interval: time.Hour, Seed: seed})
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(d.Handler())
	t.Cleanup(srv.Close)
	return d, srv
}

func ticketBoard(at time.Time, tickets ...ticket.Ticket) board.Snapshot {
	return board.Snapshot{FetchedAt: at, Tickets: tickets}
}

func TestWebSocketRejectsAWrongHelloWith4401(t *testing.T) {
	for name, msg := range map[string]string{
		"wrong token":    hello("nope"),
		"wrong protocol": `{"type":"hello","token":"TOKEN","protocol":9}`,
		"not a hello":    `{"type":"subscribe"}`,
	} {
		t.Run(name, func(t *testing.T) {
			d, srv := wsServer(t, &scriptedSync{}, &board.Snapshot{FetchedAt: fetched})
			c := dial(t, srv, strings.Replace(msg, "TOKEN", d.token, 1))
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			_, _, err := c.Read(ctx)
			if got := websocket.CloseStatus(err); got != 4401 {
				t.Errorf("close status = %d (%v), want 4401", got, err)
			}
		})
	}
}

func TestWebSocketRejectsForeignOrigins(t *testing.T) {
	_, srv := wsServer(t, &scriptedSync{}, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_, resp, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(srv.URL, "http")+"/v1/ws", &websocket.DialOptions{
		HTTPHeader: http.Header{"Origin": {"https://evil.example.com"}},
	})
	if err == nil || resp == nil || resp.StatusCode != http.StatusForbidden {
		t.Errorf("a foreign origin was accepted: %v %v", resp, err)
	}
}

func TestWebSocketSendsTheSnapshotThenDeltas(t *testing.T) {
	a := ticket.Ticket{Key: "DEMO-1", Status: "To Do"}
	b := ticket.Ticket{Key: "DEMO-2", Status: "To Do"}
	seed := ticketBoard(fetched, a, b)
	moved := a
	moved.Status = "In Progress"
	s := &scriptedSync{boards: []board.Snapshot{ticketBoard(fetched.Add(time.Minute), moved)}}
	d, srv := wsServer(t, s, &seed)
	c := joined(t, d, srv)

	first := next(t, c)
	var snap board.Snapshot
	if first.Seq != 1 || first.Type != "snapshot" || json.Unmarshal(first.Data, &snap) != nil || len(snap.Tickets) != 2 {
		t.Fatalf("first event = %+v", first)
	}
	if _, err := time.Parse(time.RFC3339, first.TS); err != nil {
		t.Errorf("ts %q: %v", first.TS, err)
	}

	d.syncOnce(context.Background())
	var got []string
	for range 4 {
		e := next(t, c)
		got = append(got, e.Type)
		if e.Seq != len(got)+1 {
			t.Errorf("%s has seq %d, want %d", e.Type, e.Seq, len(got)+1)
		}
		switch e.Type {
		case "ticket.updated":
			var tk ticket.Ticket
			if json.Unmarshal(e.Data, &tk) != nil || tk.Key != "DEMO-1" || tk.Status != "In Progress" {
				t.Errorf("updated = %s", e.Data)
			}
		case "ticket.removed":
			if !strings.Contains(string(e.Data), `"DEMO-2"`) {
				t.Errorf("removed = %s", e.Data)
			}
		}
	}
	want := []string{"sync.started", "ticket.updated", "ticket.removed", "sync.finished"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("events = %v, want %v", got, want)
	}
}

func TestWebSocketReportsAFailedSync(t *testing.T) {
	s := &scriptedSync{err: errors.New("jira down")}
	d, srv := wsServer(t, s, &board.Snapshot{FetchedAt: fetched})
	c := joined(t, d, srv)
	next(t, c)
	d.syncOnce(context.Background())
	if e := next(t, c); e.Type != "sync.started" {
		t.Fatalf("got %s", e.Type)
	}
	if e := next(t, c); e.Type != "sync.failed" || !strings.Contains(string(e.Data), "jira down") {
		t.Errorf("got %s %s", e.Type, e.Data)
	}
}

func TestWebSocketResendsTheSnapshotWhenMoreThanTicketsChange(t *testing.T) {
	a := ticket.Ticket{Key: "DEMO-1"}
	seed := ticketBoard(fetched, a)
	changed := ticketBoard(fetched.Add(time.Minute), a)
	changed.Reviews = []ticket.PullRequest{{Repo: "example-org/web", Number: 820}}
	d, srv := wsServer(t, &scriptedSync{boards: []board.Snapshot{changed}}, &seed)
	c := joined(t, d, srv)
	next(t, c)
	d.syncOnce(context.Background())
	var got []string
	for range 3 {
		got = append(got, next(t, c).Type)
	}
	if strings.Join(got, ",") != "sync.started,snapshot,sync.finished" {
		t.Errorf("events = %v", got)
	}
}

func TestWebSocketWaitsForTheFirstBoard(t *testing.T) {
	s := &scriptedSync{boards: []board.Snapshot{ticketBoard(fetched, ticket.Ticket{Key: "DEMO-1"})}}
	d, srv := wsServer(t, s, nil)
	c := joined(t, d, srv)
	d.syncOnce(context.Background())
	if e := next(t, c); e.Type != "sync.started" {
		t.Fatalf("got %s", e.Type)
	}
	if e := next(t, c); e.Type != "snapshot" || e.Seq != 2 {
		t.Errorf("the first board arrives as a snapshot: %+v", e)
	}
}

func TestWebSocketClosesASilentClientWith4401(t *testing.T) {
	d, err := New(Options{Dir: t.TempDir(), Version: "1.2.3", Sync: (&scriptedSync{}).sync, Interval: time.Hour, HelloTimeout: 50 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(d.Handler())
	t.Cleanup(srv.Close)
	c := dial(t, srv, "")
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_, _, err = c.Read(ctx)
	if got := websocket.CloseStatus(err); got != 4401 {
		t.Errorf("close status = %d (%v), want 4401", got, err)
	}
}

func TestSlowClientIsDroppedAfterItsBufferFills(t *testing.T) {
	d, _ := wsServer(t, &scriptedSync{}, &board.Snapshot{FetchedAt: fetched})
	cl := d.join()
	d.mu.Lock()
	for range clientBuffer + 5 {
		d.publish("sync.started", struct{}{})
	}
	d.mu.Unlock()
	if n := d.clientCount(); n != 0 {
		t.Errorf("%d clients left, the slow one should be dropped", n)
	}
	got := 0
	for range cl.out {
		got++
	}
	if got != clientBuffer {
		t.Errorf("the dropped client kept %d queued events, want %d", got, clientBuffer)
	}
	d.leave(cl)
}

func TestStoppingTheDaemonClosesItsClients(t *testing.T) {
	s := &scriptedSync{boards: []board.Snapshot{ticketBoard(fetched)}}
	d, err := New(Options{Dir: t.TempDir(), Version: "1.2.3", Sync: s.sync, Interval: time.Hour, Seed: &board.Snapshot{FetchedAt: fetched}})
	if err != nil {
		t.Fatal(err)
	}
	ln, err := Listen(0)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- d.Serve(ctx, ln) }()
	srv := &httptest.Server{URL: "http://" + ln.Addr().String()}
	c := joined(t, d, srv)
	cancel()
	<-done
	rctx, rcancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer rcancel()
	for {
		if _, _, err := c.Read(rctx); err != nil {
			if websocket.CloseStatus(err) != websocket.StatusGoingAway {
				t.Errorf("the client should be told the daemon went away: %v", err)
			}
			break
		}
	}
	waitFor(t, func() bool { return d.clientCount() == 0 })
}

func TestEventsAndTicketsLeaveOutTheDevPanelCache(t *testing.T) {
	seed := ticketBoard(fetched, ticket.Ticket{Key: "DEMO-1"})
	seed.Panels = map[string]board.DevPanel{"DEMO-1": {Asked: fetched}}
	d, srv := wsServer(t, &scriptedSync{}, &seed)
	c := joined(t, d, srv)
	if e := next(t, c); strings.Contains(string(e.Data), "panels") {
		t.Errorf("snapshot event carries the panel cache: %s", e.Data)
	}
	if _, body := get(t, d.Handler(), http.MethodGet, "/v1/tickets", d.token); body["panels"] != nil {
		t.Errorf("/v1/tickets carries the panel cache: %v", body["panels"])
	}
}

func TestSetAgentsSendsAgentUpdatedOnlyOnChange(t *testing.T) {
	d, srv := wsServer(t, &scriptedSync{}, &board.Snapshot{FetchedAt: fetched})
	c := joined(t, d, srv)
	next(t, c)
	d.SetAgents(map[string]string{"DEMO-1": "working"})
	d.SetAgents(map[string]string{"DEMO-1": "working"})
	d.SetAgents(map[string]string{"DEMO-1": "blocked"})
	for _, want := range []string{"working", "blocked"} {
		e := next(t, c)
		if e.Type != "agent.updated" || !strings.Contains(string(e.Data), `"DEMO-1":"`+want+`"`) {
			t.Errorf("event = %s %s, want agent.updated with %s", e.Type, e.Data, want)
		}
	}
	if snap, ok := d.Board(); !ok || snap.Agents["DEMO-1"] != "blocked" {
		t.Errorf("board agents = %v", snap.Agents)
	}
	if _, body := get(t, d.Handler(), http.MethodGet, "/v1/tickets", d.token); body["agents"] == nil {
		t.Errorf("/v1/tickets should carry the agents: %v", body)
	}
}

func TestSyncWithNewAgentStatesSendsAgentUpdated(t *testing.T) {
	seed := ticketBoard(fetched, ticket.Ticket{Key: "DEMO-1"})
	synced := ticketBoard(fetched.Add(time.Minute), ticket.Ticket{Key: "DEMO-1"})
	synced.Agents = map[string]string{"DEMO-1": "idle"}
	d, srv := wsServer(t, &scriptedSync{boards: []board.Snapshot{synced}}, &seed)
	c := joined(t, d, srv)
	next(t, c)
	d.syncOnce(context.Background())
	var got []string
	for range 3 {
		got = append(got, next(t, c).Type)
	}
	if strings.Join(got, ",") != "sync.started,agent.updated,sync.finished" {
		t.Errorf("events = %v", got)
	}
}

func TestSyncWithoutAgentStatesKeepsTheKnownOnes(t *testing.T) {
	seed := ticketBoard(fetched, ticket.Ticket{Key: "DEMO-1"})
	d, _ := wsServer(t, &scriptedSync{boards: []board.Snapshot{ticketBoard(fetched.Add(time.Minute), ticket.Ticket{Key: "DEMO-1"})}}, &seed)
	d.SetAgents(map[string]string{"DEMO-1": "working"})
	d.syncOnce(context.Background())
	if snap, _ := d.Board(); snap.Agents["DEMO-1"] != "working" {
		t.Errorf("a sync that could not read Herdr dropped the agents: %v", snap.Agents)
	}
}
