package daemon

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/pavp/tkt/internal/board"
	"github.com/pavp/tkt/internal/jira"
	"github.com/pavp/tkt/internal/ticket"
)

// contractPRs adds to contractBoard review requests, one from a bot, and others' pull requests, one the user
// opened for a ticket not theirs.
func contractPRs() board.Snapshot {
	snap := contractBoard()
	snap.Tickets[1].PRs = []ticket.PullRequest{{Repo: "example-org/web", Number: 30, Title: "Fix the login", URL: "https://github.com/example-org/web/pull/30", State: ticket.PROpen, Branch: "fix/DEMO-2-login", Review: ticket.ReviewApproved, Approvals: 2, Yours: true}}
	snap.Reviews = []ticket.PullRequest{
		{Repo: "example-org/api", Number: 15, Title: "Rate limit the API", URL: "https://github.com/example-org/api/pull/15", State: ticket.PROpen, Author: "grace"},
		{Repo: "example-org/web", Number: 31, Title: "Bump the bundler", URL: "https://github.com/example-org/web/pull/31", State: ticket.PROpen, Author: "dependabot", AuthorBot: true},
	}
	snap.Others = []ticket.PullRequest{
		{Repo: "example-org/api", Number: 16, Title: "Tidy the handlers", URL: "https://github.com/example-org/api/pull/16", State: ticket.PROpen, Author: "alan", Draft: true},
		{Repo: "example-org/web", Number: 33, Title: "Export reports", URL: "https://github.com/example-org/web/pull/33", State: ticket.PROpen, Branch: "feature/DEMO-7-export", Checks: ticket.ChecksPending, Yours: true},
	}
	return snap
}

func TestContractPRs(t *testing.T) {
	tests := []struct {
		name, fixture, query string
		state                board.ListState
	}{
		{"mine and the review requests", "prs", "", board.ListState{}},
		{"every open pull request", "prs-team", "?team_prs=true", board.ListState{}},
		{"bots shown", "prs-bots", "?bots=true", board.ListState{}},
		{"a hidden repository", "prs-hidden-repo", "?hidden_repos=example-org/web", board.ListState{}},
		{"the saved state", "prs-saved", "", board.ListState{TeamPRs: true, Collapsed: []string{"mine:example-org/api"}}},
		{"a query over the saved state", "prs", "?team_prs=false&hidden_repos=", board.ListState{TeamPRs: true, HiddenRepos: []string{"example-org/web"}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			seed := contractPRs()
			s, err := New(Options{Dir: t.TempDir(), Version: "1.2.3", Sync: (&fakeSync{}).sync, Interval: time.Hour, Seed: &seed,
				Lists: func() board.ListState { return tt.state }, Repos: func() []string { return []string{"example-org/api", "example-org/docs"} }})
			if err != nil {
				t.Fatal(err)
			}
			assertContract(t, s.Handler(), "/v1/prs"+tt.query, s.token, tt.fixture)
		})
	}
}

func TestPRsBeforeAnyBoardIsUnavailable(t *testing.T) {
	s := newServer(t, &fakeSync{}, nil)
	code, body := get(t, s.Handler(), http.MethodGet, "/v1/prs", s.token)
	if code != http.StatusServiceUnavailable || body["code"] != "not_synced" {
		t.Errorf("pull requests before a sync = %d %v", code, body)
	}
}

// contractBacklog is a fictitious backlog of two team boards sharing a sprint name, with a sprint each and
// tickets in no sprint.
func contractBacklog() board.Backlog {
	start, end := time.Date(2026, 10, 26, 0, 0, 0, 0, time.UTC), time.Date(2026, 11, 6, 0, 0, 0, 0, time.UTC)
	return board.Backlog{
		FetchedAt: fetched, HasPoints: true, Me: "Ada Lovelace",
		Sprints: []board.BacklogSprint{
			{Sprint: jira.Sprint{ID: 11, Name: "Sprint 12", State: "active", Start: start, End: end}, BoardName: "Web", Tickets: []ticket.Ticket{
				{Key: "DEMO-20", Type: "Story", Summary: "Search orders", Status: "In Progress", Category: ticket.InProgress, Points: 5, Board: "Web", URL: "https://example.atlassian.net/browse/DEMO-20"},
				{Key: "DEMO-21", Type: "Task", Summary: "Log the searches", Status: "To Do", Category: ticket.ToDo, Board: "Web", URL: "https://example.atlassian.net/browse/DEMO-21"},
			}},
			{Sprint: jira.Sprint{ID: 21, Name: "Sprint 12", State: "future", Start: end}, BoardName: "Core", Tickets: []ticket.Ticket{
				{Key: "DEMO-30", Type: "Story", Summary: "Shard the cache", Status: "Done", Category: ticket.Done, Points: 3, Board: "Core", URL: "https://example.atlassian.net/browse/DEMO-30"},
			}},
		},
		Backlog: []ticket.Ticket{{Key: "DEMO-40", Type: "Bug", Summary: "Fix the footer", Status: "To Do", Category: ticket.ToDo, Points: 2, Board: "Web", URL: "https://example.atlassian.net/browse/DEMO-40"}},
	}
}

func TestContractBacklog(t *testing.T) {
	tests := []struct {
		name, fixture, query string
		view                 board.ViewState
		lists                board.ListState
	}{
		{"one group per sprint and board", "backlog", "", board.ViewState{}, board.ListState{}},
		{"merged by sprint name", "backlog-merged", "?merged=true", board.ViewState{}, board.ListState{}},
		{"a hidden board", "backlog-hidden-board", "?hidden_boards=Core", board.ViewState{}, board.ListState{}},
		{"the saved state", "backlog-saved", "", board.ViewState{HiddenBoards: []string{"Core"}}, board.ListState{BacklogMerged: true, Collapsed: []string{"backlog:", "sprint:Sprint 12"}}},
		{"a query over the saved state", "backlog", "?merged=false&hidden_boards=", board.ViewState{HiddenBoards: []string{"Core"}}, board.ListState{BacklogMerged: true}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s, err := New(Options{Dir: t.TempDir(), Version: "1.2.3", Sync: (&fakeSync{}).sync, Interval: time.Hour,
				State: func() board.ViewState { return tt.view }, Lists: func() board.ListState { return tt.lists },
				Backlog: func(context.Context, bool) (board.Backlog, error) { return contractBacklog(), nil }})
			if err != nil {
				t.Fatal(err)
			}
			assertContract(t, s.Handler(), "/v1/backlog"+tt.query, s.token, tt.fixture)
		})
	}
}

func TestBacklogLoads(t *testing.T) {
	older := contractBacklog()
	tests := []struct {
		name, query string
		bl          board.Backlog
		err         error
		wantForce   bool
		wantCode    int
		wantError   string
	}{
		{"from the loader", "", older, nil, false, http.StatusOK, ""},
		{"forced by refresh", "?refresh=true", older, nil, true, http.StatusOK, ""},
		{"the last copy when Jira fails", "?refresh=true", older, errors.New("jira: 503 Service Unavailable"), true, http.StatusOK, "jira: 503 Service Unavailable"},
		{"nothing when Jira fails without a copy", "", board.Backlog{}, errors.New("jira: 401 Unauthorized"), false, http.StatusBadGateway, "jira: 401 Unauthorized"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var gotForce bool
			s, err := New(Options{Dir: t.TempDir(), Version: "1.2.3", Sync: (&fakeSync{}).sync, Interval: time.Hour,
				Backlog: func(_ context.Context, force bool) (board.Backlog, error) { gotForce = force; return tt.bl, tt.err }})
			if err != nil {
				t.Fatal(err)
			}
			code, body := get(t, s.Handler(), http.MethodGet, "/v1/backlog"+tt.query, s.token)
			if code != tt.wantCode || gotForce != tt.wantForce {
				t.Fatalf("GET = %d %v, force %v", code, body, gotForce)
			}
			switch {
			case code == http.StatusBadGateway:
				if body["code"] != "backlog_failed" || body["message"] != tt.wantError {
					t.Errorf("error body = %v", body)
				}
			case tt.wantError != "":
				if body["error"] != tt.wantError || body["fetched_at"] != fetched.Format(time.RFC3339) {
					t.Errorf("last copy = %v", body)
				}
			case body["error"] != nil:
				t.Errorf("unexpected error: %v", body)
			}
		})
	}
}

func TestBacklogSaysWhenNoTeamBoardIsSet(t *testing.T) {
	for _, none := range []bool{true, false} {
		s, err := New(Options{Dir: t.TempDir(), Version: "1.2.3", Sync: (&fakeSync{}).sync, Interval: time.Hour,
			Backlog: func(context.Context, bool) (board.Backlog, error) { return board.Backlog{NoTeamBoards: none}, nil }})
		if err != nil {
			t.Fatal(err)
		}
		code, body := get(t, s.Handler(), http.MethodGet, "/v1/backlog", s.token)
		if got, _ := body["no_team_boards"].(bool); code != http.StatusOK || got != none {
			t.Errorf("no team boards %v: GET = %d %v", none, code, body)
		}
	}
}

func TestBacklogLoadsOneAtATime(t *testing.T) {
	var running, most atomic.Int32
	s, err := New(Options{Dir: t.TempDir(), Version: "1.2.3", Sync: (&fakeSync{}).sync, Interval: time.Hour,
		Backlog: func(context.Context, bool) (board.Backlog, error) {
			n := running.Add(1)
			defer running.Add(-1)
			for m := most.Load(); n > m && !most.CompareAndSwap(m, n); m = most.Load() {
			}
			time.Sleep(20 * time.Millisecond)
			return contractBacklog(), nil
		}})
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for range 4 {
		wg.Go(func() {
			if code, body := get(t, s.Handler(), http.MethodGet, "/v1/backlog", s.token); code != http.StatusOK {
				t.Errorf("GET = %d %v", code, body)
			}
		})
	}
	wg.Wait()
	if most.Load() != 1 {
		t.Errorf("%d backlog loads ran at once, want 1", most.Load())
	}
}

func TestBooleanQueriesAreStrict(t *testing.T) {
	seed := contractPRs()
	s, err := New(Options{Dir: t.TempDir(), Version: "1.2.3", Sync: (&fakeSync{}).sync, Interval: time.Hour, Seed: &seed,
		Backlog: func(context.Context, bool) (board.Backlog, error) { return contractBacklog(), nil }})
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct{ path, param string }{
		{"/v1/board?team=1", "team"},
		{"/v1/board?team=True", "team"},
		{"/v1/board?team=yes", "team"},
		{"/v1/board?team=", "team"},
		{"/v1/prs?team_prs=on", "team_prs"},
		{"/v1/prs?bots=1", "bots"},
		{"/v1/backlog?merged=yes", "merged"},
		{"/v1/backlog?refresh=1", "refresh"},
	}
	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			code, body := get(t, s.Handler(), http.MethodGet, tt.path, s.token)
			msg, _ := body["message"].(string)
			if code != http.StatusBadRequest || body["type"] != "error" || body["code"] != "bad_request" || !strings.Contains(msg, tt.param) {
				t.Errorf("GET %s = %d %v", tt.path, code, body)
			}
		})
	}
	for _, path := range []string{"/v1/board?team=true", "/v1/board?team=false", "/v1/prs?bots=false", "/v1/backlog?merged=true"} {
		if code, body := get(t, s.Handler(), http.MethodGet, path, s.token); code != http.StatusOK {
			t.Errorf("GET %s = %d %v", path, code, body)
		}
	}
}
