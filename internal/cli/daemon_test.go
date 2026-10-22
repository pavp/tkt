package cli

import (
	"context"
	"errors"
	"github.com/pavp/tkt/internal/board"
	"strings"
	"testing"
	"time"

	"github.com/pavp/tkt/internal/config"
	"github.com/pavp/tkt/internal/credstore"
	"github.com/pavp/tkt/internal/jira"
)

func daemonHarness(t *testing.T) *harness {
	t.Helper()
	h := newHarness(t, baseConfig)
	c, err := h.app.LoadConfig()
	if err != nil {
		t.Fatal(err)
	}
	site, _ := config.SiteURL(c.Jira.Site)
	h.creds[credstore.JiraKey(site, c.Jira.Email)] = "token"
	h.app.NewGitHub = func(*config.Config) GitHubAPI { return &fakeGitHub{} }
	h.app.Now = func() time.Time { return time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC) }
	h.jira.issues = []jira.Issue{{ID: "1", Key: c.Spaces[0].Key + "-1", Status: "To Do", StatusCategory: "new"}}
	return h
}

func TestDaemonSyncFetchesAndCachesTheBoard(t *testing.T) {
	h := daemonHarness(t)
	snap, err := h.app.daemonSync(context.Background(), nil)
	if err != nil || len(snap.Tickets) != 1 {
		t.Fatalf("sync = %+v, %v", snap, err)
	}
	cached, ok, err := h.app.cache().Load()
	if err != nil || !ok || len(cached.Tickets) != 1 {
		t.Errorf("the sync should write the cache: %+v %v %v", cached, ok, err)
	}
}

func TestDaemonSyncLogsEachNewWarningOnce(t *testing.T) {
	h := daemonHarness(t)
	h.app.NewGitHub = func(*config.Config) GitHubAPI { return &fakeGitHub{err: errors.New("secondary rate limit")} }
	for range 2 {
		if _, err := h.app.daemonSync(context.Background(), nil); err != nil {
			t.Fatal(err)
		}
	}
	log := h.errOut.String()
	if n := strings.Count(log, "sync: pull requests unavailable: secondary rate limit"); n != 1 {
		t.Errorf("a warning should reach the daemon's log once while it lasts, got %d:\n%s", n, log)
	}
	if !strings.Contains(log, "2026-10-20T12:00:00Z sync: ") {
		t.Errorf("each line says when:\n%s", log)
	}
}

func TestDaemonSyncReportsAFailedFetch(t *testing.T) {
	h := daemonHarness(t)
	if _, err := h.app.daemonSync(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	h.jira.searchErr = errors.New("jira down")
	if _, err := h.app.daemonSync(context.Background(), nil); err == nil {
		t.Error("a failed fetch must fail the sync, so the daemon keeps its last board")
	}
}

func TestDaemonCommandIsListed(t *testing.T) {
	h := newHarness(t, baseConfig)
	if code := h.exec("daemon", "--help"); code != 0 || !strings.Contains(h.out.String(), "run") {
		t.Errorf("exit %d: %s", code, h.out)
	}
}

func TestDaemonSyncHandsThePartialBoardOn(t *testing.T) {
	h := daemonHarness(t)
	var partials []board.Snapshot
	snap, err := h.app.daemonSync(context.Background(), func(p board.Snapshot) { partials = append(partials, p) })
	if err != nil || snap.Partial || len(partials) != 1 || !partials[0].Partial || len(partials[0].Tickets) != 1 {
		t.Errorf("sync = %+v, %v; partials %+v", snap, err, partials)
	}
}
