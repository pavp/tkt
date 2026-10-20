package daemon

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/pavp/tkt/internal/board"
	"github.com/pavp/tkt/internal/ticket"
)

func running(t *testing.T, s *scriptedSync, seed *board.Snapshot) (*Server, *Client) {
	t.Helper()
	dir := t.TempDir()
	d, err := New(Options{Dir: dir, Version: "1.2.3", Sync: s.sync, Interval: time.Hour, Seed: seed})
	if err != nil {
		t.Fatal(err)
	}
	ln, err := Listen(0)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go func() { _ = d.Serve(ctx, ln) }()
	waitFor(t, func() bool { _, err := ReadInfo(dir); return err == nil })
	c, err := Dial(dir, Info{Port: ln.Addr().(*net.TCPAddr).Port})
	if err != nil {
		t.Fatal(err)
	}
	return d, c
}

func TestClientReadsTheBoardAndAsksForSyncs(t *testing.T) {
	s := &scriptedSync{boards: []board.Snapshot{ticketBoard(fetched.Add(time.Minute), ticket.Ticket{Key: "DEMO-2"})}}
	seed := ticketBoard(fetched, ticket.Ticket{Key: "DEMO-1"})
	d, c := running(t, s, &seed)
	snap, err := c.Tickets(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(snap.Tickets) == 0 {
		t.Fatalf("tickets = %+v", snap)
	}
	if err := c.Sync(context.Background()); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { b, _ := d.Board(); return len(b.Tickets) == 1 && b.Tickets[0].Key == "DEMO-2" })
}

func TestClientWithoutABoardYetSaysSo(t *testing.T) {
	_, c := running(t, &scriptedSync{err: context.DeadlineExceeded}, nil)
	if _, err := c.Tickets(context.Background()); err != ErrNotSynced {
		t.Errorf("err = %v, want ErrNotSynced", err)
	}
}

func TestClientFollowsEvents(t *testing.T) {
	seed := ticketBoard(fetched, ticket.Ticket{Key: "DEMO-1"})
	d, c := running(t, &scriptedSync{}, &seed)
	got := make(chan Event, 8)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- c.Events(ctx, func(e Event) { got <- e }) }()
	if e := <-got; e.Type != "snapshot" || e.Seq != 1 {
		t.Fatalf("first event = %+v", e)
	}
	d.SetAgents(map[string]string{"DEMO-1": "working"})
	select {
	case e := <-got:
		if e.Type != "agent.updated" || e.Seq != 2 {
			t.Errorf("event = %+v", e)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no agent.updated")
	}
	cancel()
	if err := <-done; err == nil {
		t.Error("Events should return once ctx ends")
	}
}

func TestDialNeedsTheTokenFile(t *testing.T) {
	if _, err := Dial(t.TempDir(), Info{Port: 1}); err == nil {
		t.Error("dialing without a token file should fail rather than create one")
	}
}
