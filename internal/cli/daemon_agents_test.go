package cli

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/pavp/tkt/internal/board"
	"github.com/pavp/tkt/internal/config"
	"github.com/pavp/tkt/internal/credstore"
	"github.com/pavp/tkt/internal/herdr/herdrtest"
	"github.com/pavp/tkt/internal/jira"
	"github.com/pavp/tkt/internal/ticket"
)

// agentRecorder stands in for the daemon: it serves a board and records the agents set on it.
type agentRecorder struct {
	mu   sync.Mutex
	snap board.Snapshot
	sets []map[string]string
}

func (r *agentRecorder) Board() (board.Snapshot, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.snap, true
}

func (r *agentRecorder) SetAgents(a map[string]string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.sets = append(r.sets, a)
}

func (r *agentRecorder) last() map[string]string {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.sets) == 0 {
		return nil
	}
	return r.sets[len(r.sets)-1]
}

func agentWorld(t *testing.T, status string) (*harness, *herdrtest.FakeServer) {
	t.Helper()
	h, f, _ := herdrHarness(t)
	f.Results["workspace.list"] = `{"type":"workspace_list","workspaces":[{"workspace_id":"w1","label":"web-DEMO-1","worktree":{"checkout_path":"/src/web-DEMO-1"}}]}`
	setAgentStatus(f, status)
	h.run.out["git rev-parse --abbrev-ref HEAD"] = "feature/DEMO-1-filter\n"
	return h, f
}

func setAgentStatus(f *herdrtest.FakeServer, status string) {
	f.SetResult("agent.list", `{"type":"agent_list","agents":[{"pane_id":"w1:p1","workspace_id":"w1","agent":"claude","agent_status":"`+status+`"}]}`)
}

func eventually(t *testing.T, what string, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestDaemonFollowsHerdrAgentEvents(t *testing.T) {
	h, f := agentWorld(t, "working")
	rec := &agentRecorder{snap: board.Snapshot{Tickets: []ticket.Ticket{{Key: "DEMO-1", Status: "In Progress"}}}}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { h.app.watchAgents(ctx, rec); close(done) }()

	eventually(t, "the first agent states", func() bool { return rec.last()["DEMO-1"] == "working" })
	eventually(t, "the subscription", func() bool { return f.Watchers() > 0 })
	subs := f.Last("events.subscribe")["subscriptions"].([]any)
	perPane := false
	for _, s := range subs {
		m := s.(map[string]any)
		if m["type"] == "pane.agent_status_changed" && m["pane_id"] == "w1:p1" {
			perPane = true
		}
	}
	if !perPane {
		t.Errorf("no per-pane agent subscription in %v", subs)
	}
	if count(f, "workspace.report_metadata") == 0 {
		t.Error("the sidebar was not written")
	}

	setAgentStatus(f, "blocked")
	f.Emit(`{"event":"pane.agent_status_changed","data":{"pane_id":"w1:p1","workspace_id":"w1","agent_status":"blocked"}}`)
	eventually(t, "the blocked state", func() bool { return rec.last()["DEMO-1"] == "blocked" })

	f.DropWatchers()
	eventually(t, "a new subscription after Herdr dropped it", func() bool { return f.Watchers() > 0 })

	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("watchAgents kept running after cancel")
	}
}

func TestDaemonSyncCarriesAgentStates(t *testing.T) {
	h, _ := agentWorld(t, "idle")
	c, err := h.app.LoadConfig()
	if err != nil {
		t.Fatal(err)
	}
	site, _ := config.SiteURL(c.Jira.Site)
	h.creds[credstore.JiraKey(site, c.Jira.Email)] = "token"
	h.app.NewGitHub = func(*config.Config) GitHubAPI { return &fakeGitHub{} }
	h.app.Now = func() time.Time { return time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC) }
	h.jira.issues = []jira.Issue{{ID: "1", Key: "DEMO-1", Status: "In Progress", StatusCategory: "indeterminate"}}
	snap, err := h.app.daemonSync(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if snap.Agents["DEMO-1"] != "idle" {
		t.Errorf("agents = %v", snap.Agents)
	}
}

// emptyBoard is a daemon that has not finished its first sync.
type emptyBoard struct{ agentRecorder }

func (e *emptyBoard) Board() (board.Snapshot, bool) { return board.Snapshot{}, false }

func TestAgentsBeforeTheFirstSyncLeaveTheSidebarAlone(t *testing.T) {
	h, f := agentWorld(t, "working")
	rec := &emptyBoard{}
	if _, err := h.app.syncAgents(context.Background(), rec); err != nil {
		t.Fatal(err)
	}
	if n := count(f, "workspace.report_metadata"); n != 0 {
		t.Errorf("wrote the sidebar %d times without a board", n)
	}
	if rec.last()["DEMO-1"] != "working" {
		t.Errorf("agents should still be read: %v", rec.last())
	}
}

func TestWatchRetryWait(t *testing.T) {
	cases := []struct {
		wait, lived, want time.Duration
	}{
		{watchRetry, time.Second, 2 * watchRetry},
		{watchRetryMax, time.Second, watchRetryMax},
		{watchRetryMax, 2 * time.Minute, watchRetry},
	}
	for _, c := range cases {
		if got := nextWatchWait(c.wait, c.lived); got != c.want {
			t.Errorf("after %v that lived %v: wait %v, want %v", c.wait, c.lived, got, c.want)
		}
	}
}
