package cli

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/pavp/tkt/internal/board"
	"github.com/pavp/tkt/internal/config"
	"github.com/pavp/tkt/internal/credstore"
	"github.com/pavp/tkt/internal/ticket"
	"github.com/pavp/tkt/internal/tui"
)

func daemonBoard() *board.Snapshot {
	return &board.Snapshot{FetchedAt: time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC), Tickets: []ticket.Ticket{{Key: "DEMO-9", Summary: "Served by the daemon", Status: "To Do", Category: ticket.ToDo}}}
}

func TestListReadsThroughTheDaemon(t *testing.T) {
	h, f := startHarness(t)
	f.board = daemonBoard()
	if code := h.exec("list"); code != 0 {
		t.Fatalf("exit %d: %s", code, h.errOut)
	}
	if !strings.Contains(h.out.String(), "DEMO-9") || h.jira.jql != "" {
		t.Errorf("list should come from the daemon without asking Jira (jql %q):\n%s", h.jira.jql, h.out)
	}
	if f.started.Load() != 1 {
		t.Errorf("list should start the daemon, started %d", f.started.Load())
	}
}

func TestListFallsBackWhenTheDaemonCannotHelp(t *testing.T) {
	cases := []struct {
		name  string
		setup func(*harness, *fakeDaemons)
	}{
		{"it cannot start", func(h *harness, _ *fakeDaemons) {
			h.app.StartDetached = func(string, []string, string) (int, error) { return 0, errors.New("no fork") }
		}},
		{"it has no board yet", func(*harness, *fakeDaemons) {}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h, f := startHarness(t)
			c.setup(h, f)
			cfg, err := h.app.LoadConfig()
			if err != nil {
				t.Fatal(err)
			}
			site, _ := config.SiteURL(cfg.Jira.Site)
			h.creds[credstore.JiraKey(site, cfg.Jira.Email)] = "token"
			h.app.NewGitHub = func(*config.Config) GitHubAPI { return &fakeGitHub{} }
			h.jira.issues = nil
			if code := h.exec("list"); code != 0 {
				t.Fatalf("exit %d: %s", code, h.errOut)
			}
			if h.jira.jql == "" {
				t.Error("list should ask Jira itself")
			}
		})
	}
}

func TestListCachedNeverStartsTheDaemon(t *testing.T) {
	h, f := startHarness(t)
	h.exec("list", "--cached")
	if f.started.Load() != 0 {
		t.Errorf("--cached started %d daemons", f.started.Load())
	}
}

func TestHerdrRestoreStartsTheDaemon(t *testing.T) {
	h, f := startHarness(t)
	h.exec("herdr", "restore")
	if f.started.Load() != 1 {
		t.Errorf("the Herdr startup hook should start the daemon, started %d", f.started.Load())
	}
}

func TestBoardFollowsTheDaemon(t *testing.T) {
	h, f := startHarness(t)
	f.board = daemonBoard()
	c, err := h.app.LoadConfig()
	if err != nil {
		t.Fatal(err)
	}
	b := &boardActions{a: h.app, c: c, ctx: context.Background()}
	var mu sync.Mutex
	var got []tea.Msg
	send := func(m tea.Msg) {
		mu.Lock()
		defer mu.Unlock()
		got = append(got, m)
	}
	received := func() int {
		mu.Lock()
		defer mu.Unlock()
		return len(got)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { h.app.followDaemon(ctx, b, send); close(done) }()
	eventually(t, "the daemon's first board", func() bool { return received() >= 1 })
	f.server().SetAgents(map[string]string{"DEMO-9": "working"})
	eventually(t, "the board after agent.updated", func() bool { return received() >= 2 })
	mu.Lock()
	last := got[len(got)-1].(tui.SnapshotMsg)
	mu.Unlock()
	if !last.Quiet || last.Snap.Agents["DEMO-9"] != "working" || last.Snap.Tickets[0].Key != "DEMO-9" {
		t.Errorf("last message = %+v", last)
	}
	msg := b.Refresh()().(tui.SnapshotMsg)
	if msg.Err != nil || len(msg.Snap.Tickets) != 1 || msg.Snap.Tickets[0].Key != "DEMO-9" || h.jira.jql != "" {
		t.Errorf("refresh should read the daemon: %+v (jql %q)", msg, h.jira.jql)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("followDaemon kept running")
	}
}
