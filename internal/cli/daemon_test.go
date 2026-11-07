package cli

import (
	"context"
	"errors"
	"github.com/pavp/tkt/internal/board"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/pavp/tkt/internal/config"
	"github.com/pavp/tkt/internal/credstore"
	"github.com/pavp/tkt/internal/jira"
	"github.com/pavp/tkt/internal/ticket"
	"github.com/pavp/tkt/internal/tui"
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

func TestDaemonBoardFollowsTheSavedBoardState(t *testing.T) {
	h := newHarness(t, baseConfig)
	if got := h.app.boardViewState(); got.Team || len(got.HiddenBoards) != 0 {
		t.Errorf("without a saved state = %+v", got)
	}
	b := &boardActions{a: h.app}
	if msg := b.SaveState(tui.BoardState{Team: true, HiddenBoards: []string{"Web"}, HiddenRepos: []string{"example-org/api"}})(); msg != nil {
		t.Fatalf("save = %v", msg)
	}
	if got := h.app.boardViewState(); !got.Team || !slices.Equal(got.HiddenBoards, []string{"Web"}) {
		t.Errorf("saved state = %+v", got)
	}
}

func TestDaemonListsFollowTheSavedBoardState(t *testing.T) {
	no, yes := false, true
	tests := []struct {
		name  string
		saved tui.BoardState
		want  board.ListState
	}{
		{"nothing saved", tui.BoardState{}, board.ListState{}},
		{"team pull requests follow Team while unset", tui.BoardState{Team: true}, board.ListState{TeamPRs: true}},
		{"team pull requests set apart from Team", tui.BoardState{Team: true, TeamPRs: &no}, board.ListState{}},
		{"every list choice", tui.BoardState{HiddenRepos: []string{"example-org/web"}, Collapsed: []string{"mine:example-org/api", "backlog:"}, TeamPRs: &yes, BacklogMerged: true},
			board.ListState{HiddenRepos: []string{"example-org/web"}, Collapsed: []string{"backlog:", "mine:example-org/api"}, TeamPRs: true, BacklogMerged: true}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newHarness(t, baseConfig)
			if msg := (&boardActions{a: h.app}).SaveState(tt.saved)(); msg != nil {
				t.Fatalf("save = %v", msg)
			}
			got := h.app.boardListState()
			if !slices.Equal(got.HiddenRepos, tt.want.HiddenRepos) || !slices.Equal(slices.Sorted(slices.Values(got.Collapsed)), tt.want.Collapsed) || got.TeamPRs != tt.want.TeamPRs || got.BacklogMerged != tt.want.BacklogMerged {
				t.Errorf("lists = %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestDaemonBacklogKeepsToItsCopy(t *testing.T) {
	now := time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name    string
		age     time.Duration
		force   bool
		wantErr bool
	}{
		{"a fresh copy without asking Jira", board.BacklogEvery - time.Second, false, false},
		{"an old copy beside Jira's failure", board.BacklogEvery, false, true},
		{"the copy beside Jira's failure when forced", 0, true, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newHarness(t, baseConfig)
			h.app.Now = func() time.Time { return now }
			copyAt := now.Add(-tt.age)
			if err := h.app.backlogCache().Save(board.Backlog{FetchedAt: copyAt, Backlog: []ticket.Ticket{{Key: "DEMO-40"}}}); err != nil {
				t.Fatal(err)
			}
			bl, err := h.app.daemonBacklog(context.Background(), tt.force)
			if (err != nil) != tt.wantErr || !bl.FetchedAt.Equal(copyAt) || len(bl.Backlog) != 1 {
				t.Errorf("backlog = %+v, %v", bl, err)
			}
		})
	}
	h := newHarness(t, baseConfig)
	if bl, err := h.app.daemonBacklog(context.Background(), false); err == nil || !bl.FetchedAt.IsZero() {
		t.Errorf("without a copy and without Jira = %+v, %v", bl, err)
	}
}

func TestDaemonReposAreTheConfiguredOnes(t *testing.T) {
	h := newHarness(t, baseConfig)
	if got := h.app.daemonRepos(); !slices.Equal(got, []string{"example-org/web"}) {
		t.Errorf("repos = %v", got)
	}
	if got := newHarness(t, "").app.daemonRepos(); len(got) != 0 {
		t.Errorf("repos without a configuration = %v", got)
	}
}

func TestDaemonSyncLogsAnErrorPageAsOneShortLine(t *testing.T) {
	h := daemonHarness(t)
	page := "504 <!DOCTYPE html>\n<html>\n  <head><title>Unicorn!</title></head>\n" + strings.Repeat("<p>base64 image data</p>\n", 200)
	h.app.NewGitHub = func(*config.Config) GitHubAPI { return &fakeGitHub{err: errors.New(page)} }
	if _, err := h.app.daemonSync(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(strings.TrimSpace(h.errOut.String()), "\n") {
		if len(line) > 400 {
			t.Errorf("a log line stays short, got %d bytes: %.120s…", len(line), line)
		}
	}
	if !strings.Contains(h.errOut.String(), "sync: pull requests unavailable: 504 <!DOCTYPE html> <html> <head><title>Unicorn!</title></head>") {
		t.Errorf("the line keeps the start of the error on one line:\n%.400s", h.errOut.String())
	}
}

func TestDaemonSyncLogsHowLongASlowSyncTook(t *testing.T) {
	h := daemonHarness(t)
	start := time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)
	for _, took := range []time.Duration{2 * time.Second, 7500 * time.Millisecond} {
		calls := 0
		h.app.Now = func() time.Time {
			calls++
			if calls == 1 {
				return start
			}
			return start.Add(took)
		}
		h.errOut.Reset()
		if _, err := h.app.daemonSync(context.Background(), nil); err != nil {
			t.Fatal(err)
		}
		logged := strings.Contains(h.errOut.String(), "sync: took 7.5s")
		if took < 5*time.Second && strings.Contains(h.errOut.String(), "sync: took") {
			t.Errorf("a quick sync leaves no line:\n%s", h.errOut.String())
		}
		if took >= 5*time.Second && !logged {
			t.Errorf("a slow sync says how long it took:\n%s", h.errOut.String())
		}
	}
}
