package cli

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/pavp/tkt/internal/board"
	"github.com/pavp/tkt/internal/config"
	"github.com/pavp/tkt/internal/credstore"
	"github.com/pavp/tkt/internal/herdr"
	"github.com/pavp/tkt/internal/herdr/herdrtest"
	"github.com/pavp/tkt/internal/herdrport"
	"github.com/pavp/tkt/internal/jira"
	"github.com/pavp/tkt/internal/ticket"
)

// watchCounter wraps a Herdr and counts the subscriptions still open on it.
type watchCounter struct {
	HerdrAPI
	open *atomic.Int32
}

func (w watchCounter) Watch(ctx context.Context, subs []herdrport.Subscription, on func(herdrport.Event)) error {
	w.open.Add(1)
	defer w.open.Add(-1)
	return w.HerdrAPI.Watch(ctx, subs, on)
}

type bothWorld struct {
	h                *harness
	classic, desktop *herdrtest.FakeServer
	upClassic        atomic.Bool
	upDesktop        atomic.Bool
	probes           atomic.Int32
	open             atomic.Int32
}

// bothHerdrs is Herdr 0.9.5 running an agent on DEMO-1 and Herdr desktop one on DEMO-2, on the same pane id.
func bothHerdrs(t *testing.T) *bothWorld {
	t.Helper()
	w := &bothWorld{}
	w.h, w.classic = agentWorld(t, "working")
	w.desktop = herdrtest.Start(t)
	w.desktop.Results["workspace.list"] = `{"type":"workspace_list","workspaces":[{"workspace_id":"w1","label":"api"}]}`
	setDesktopAgent(w.desktop, "blocked")
	w.h.app.HerdrRunning = func(context.Context) (bool, bool) {
		w.probes.Add(1)
		return w.upClassic.Load(), w.upDesktop.Load()
	}
	w.h.app.herdrOf = func(f herdrFlavor) HerdrAPI {
		path := w.classic.Path
		if f == herdrDesktop {
			path = w.desktop.Path
		}
		return watchCounter{herdr.New(path), &w.open}
	}
	return w
}

func setDesktopAgent(f *herdrtest.FakeServer, status string) {
	f.SetResult("agent.list", `{"type":"agent_list","agents":[{"pane_id":"w1:p1","workspace_id":"w1","agent":"codex","agent_status":"`+status+`","tokens":{"tkt_key":"DEMO-2"}}]}`)
}

func (w *bothWorld) follow(t *testing.T, rec agentBoard) (recheck chan time.Time, stop func()) {
	t.Helper()
	recheck = make(chan time.Time)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { w.h.app.followHerdrs(ctx, rec, recheck); close(done) }()
	return recheck, func() {
		t.Helper()
		cancel()
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			t.Fatal("followHerdrs kept running after cancel")
		}
		if n := w.open.Load(); n != 0 {
			t.Errorf("%d Herdr subscriptions still open after cancel", n)
		}
	}
}

func twoTicketBoard() *agentRecorder {
	return &agentRecorder{snap: board.Snapshot{Tickets: []ticket.Ticket{{Key: "DEMO-1", Status: "In Progress"}, {Key: "DEMO-2", Status: "In Progress"}}}}
}

func TestDaemonFollowsBothHerdrs(t *testing.T) {
	w := bothHerdrs(t)
	w.upClassic.Store(true)
	w.upDesktop.Store(true)
	rec := twoTicketBoard()
	_, stop := w.follow(t, rec)

	eventually(t, "agent states from both Herdrs", func() bool {
		a := rec.last()
		return a["DEMO-1"] == "working" && a["DEMO-2"] == "blocked"
	})
	eventually(t, "a subscription on each Herdr", func() bool { return w.classic.Watchers() > 0 && w.desktop.Watchers() > 0 })

	setDesktopAgent(w.desktop, "done")
	w.desktop.Emit(`{"event":"pane.agent_status_changed","data":{"pane_id":"w1:p1","workspace_id":"w1","agent_status":"done"}}`)
	eventually(t, "the desktop agent's new state", func() bool { return rec.last()["DEMO-2"] == "done" })
	if got := rec.last()["DEMO-1"]; got != "working" {
		t.Errorf("an event on Herdr desktop's pane w1:p1 changed Herdr's DEMO-1 to %q", got)
	}
	if n := w.probes.Load(); n != 1 {
		t.Errorf("probed the Herdrs %d times between rechecks, want 1", n)
	}
	stop()

	if count(w.classic, "workspace.report_metadata") == 0 {
		t.Error("Herdr 0.9.5's sidebar was not written")
	}
	if n := count(w.desktop, "workspace.report_metadata"); n != 0 {
		t.Errorf("sent sidebar tokens to Herdr desktop %d times", n)
	}
}

func TestDaemonPicksUpAndDropsHerdrsOnRecheck(t *testing.T) {
	w := bothHerdrs(t)
	w.upClassic.Store(true)
	rec := twoTicketBoard()
	recheck, stop := w.follow(t, rec)
	defer stop()

	eventually(t, "Herdr's agent", func() bool { return rec.last()["DEMO-1"] == "working" })
	if _, ok := rec.last()["DEMO-2"]; ok || w.desktop.Watchers() != 0 {
		t.Fatalf("followed Herdr desktop before it ran: %v", rec.last())
	}

	w.upDesktop.Store(true)
	recheck <- time.Now()
	eventually(t, "Herdr desktop started later", func() bool {
		a := rec.last()
		return a["DEMO-1"] == "working" && a["DEMO-2"] == "blocked"
	})

	w.upClassic.Store(false)
	recheck <- time.Now()
	eventually(t, "Herdr's agents dropped once it quit", func() bool {
		a := rec.last()
		_, ok := a["DEMO-1"]
		return !ok && a["DEMO-2"] == "blocked"
	})
	if n := w.probes.Load(); n != 3 {
		t.Errorf("probed %d times, want once at start and once per recheck (3)", n)
	}
}

func TestDaemonSyncReadsBothHerdrs(t *testing.T) {
	w := bothHerdrs(t)
	w.upClassic.Store(true)
	w.upDesktop.Store(true)
	h := w.h
	c, err := h.app.LoadConfig()
	if err != nil {
		t.Fatal(err)
	}
	site, _ := config.SiteURL(c.Jira.Site)
	h.creds[credstore.JiraKey(site, c.Jira.Email)] = "token"
	h.app.NewGitHub = func(*config.Config) GitHubAPI { return &fakeGitHub{} }
	h.app.Now = func() time.Time { return time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC) }
	h.jira.issues = []jira.Issue{
		{ID: "1", Key: "DEMO-1", Status: "In Progress", StatusCategory: "indeterminate"},
		{ID: "2", Key: "DEMO-2", Status: "In Progress", StatusCategory: "indeterminate"},
	}
	for range 2 {
		snap, err := h.app.daemonSync(context.Background(), nil)
		if err != nil {
			t.Fatal(err)
		}
		if snap.Agents["DEMO-1"] != "working" || snap.Agents["DEMO-2"] != "blocked" {
			t.Errorf("agents = %v, want both Herdrs' agents", snap.Agents)
		}
	}
	if n := w.probes.Load(); n != 1 {
		t.Errorf("probed %d times across two syncs, want 1", n)
	}
	if count(w.classic, "workspace.report_metadata") == 0 {
		t.Error("Herdr 0.9.5's sidebar was not written")
	}
	if n := count(w.desktop, "workspace.report_metadata"); n != 0 {
		t.Errorf("sent sidebar tokens to Herdr desktop %d times", n)
	}
}
