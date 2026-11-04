package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/pavp/tkt/internal/board"
	"github.com/pavp/tkt/internal/jira"
	"github.com/pavp/tkt/internal/ticket"
)

var update = flag.Bool("update", false, "rewrite the contract fixtures under testdata/contract/v1")

// contractBoard is a fictitious board with the user's tickets in every column, a subtask in another column
// than its parent, and team tickets on two team boards.
func contractBoard() board.Snapshot {
	return board.Snapshot{
		FetchedAt: fetched,
		Tickets: []ticket.Ticket{
			{Key: "DEMO-1", Type: "Story", Summary: "Filter accounts", Status: "In Progress", Category: ticket.InProgress, Points: 3, Assignee: "Ada Lovelace", URL: "https://example.atlassian.net/browse/DEMO-1",
				PRs:      []ticket.PullRequest{{Repo: "example-org/api", Number: 12, Title: "Filter accounts", URL: "https://github.com/example-org/api/pull/12", State: ticket.PROpen, Branch: "feature/DEMO-1-filter", Checks: ticket.ChecksFail, Yours: true}},
				Subtasks: []ticket.Ticket{{Key: "DEMO-4", Type: "Sub-task", Summary: "Write the query", Status: "To Do", Category: ticket.ToDo, Parent: "DEMO-1", URL: "https://example.atlassian.net/browse/DEMO-4"}}},
			{Key: "DEMO-2", Type: "Bug", Summary: "Fix the login", Status: "To Do", Category: ticket.ToDo, Points: 1, Assignee: "Ada Lovelace", URL: "https://example.atlassian.net/browse/DEMO-2"},
			{Key: "DEMO-3", Type: "Task", Summary: "Ship the docs", Status: "Done", Category: ticket.Done, Assignee: "Ada Lovelace", URL: "https://example.atlassian.net/browse/DEMO-3"},
			{Key: "DEMO-7", Type: "Story", Summary: "Export reports", Status: "In Review", Category: ticket.InReview, Points: 5, Assignee: "Alan Turing", Team: true, Board: "Web", URL: "https://example.atlassian.net/browse/DEMO-7"},
			{Key: "DEMO-8", Type: "Task", Summary: "Tune the cache", Status: "To Do", Category: ticket.ToDo, Team: true, Board: "Core", URL: "https://example.atlassian.net/browse/DEMO-8"},
		},
		Agents: map[string]string{"DEMO-1": "working", "DEMO-7": "idle"},
	}
}

func TestContractBoard(t *testing.T) {
	tests := []struct {
		name, fixture, query string
		state                board.ViewState
	}{
		{"the user's tickets only", "board", "", board.ViewState{}},
		{"the team's tickets too", "board-team", "?team=true", board.ViewState{}},
		{"a hidden board", "board-hidden-board", "?team=true&hidden_boards=Web", board.ViewState{}},
		{"the saved board state", "board-team", "", board.ViewState{Team: true}},
		{"a query over the saved state", "board", "?team=false&hidden_boards=", board.ViewState{Team: true, HiddenBoards: []string{"Core"}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			seed := contractBoard()
			s, err := New(Options{Dir: t.TempDir(), Version: "1.2.3", Sync: (&fakeSync{}).sync, Interval: time.Hour, Seed: &seed, State: func() board.ViewState { return tt.state }, Now: func() time.Time { return seed.FetchedAt.Add(3 * time.Minute) }})
			if err != nil {
				t.Fatal(err)
			}
			assertContract(t, s.Handler(), "/v1/board"+tt.query, s.token, tt.fixture)
		})
	}
}

func TestBoardReportsTheSync(t *testing.T) {
	release := make(chan struct{})
	started := make(chan struct{})
	syncs := 0
	sync := func(context.Context, func(board.Snapshot)) (board.Snapshot, error) {
		syncs++
		if syncs == 1 {
			close(started)
			<-release
			return board.Snapshot{}, errors.New("jira: 503 Service Unavailable")
		}
		return board.Snapshot{FetchedAt: fetched.Add(time.Minute)}, nil
	}
	seed := board.Snapshot{FetchedAt: fetched}
	s, err := New(Options{Dir: t.TempDir(), Version: "1.2.3", Sync: sync, Interval: time.Hour, Seed: &seed})
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() { s.syncOnce(context.Background()); close(done) }()
	<-started
	if _, body := get(t, s.Handler(), http.MethodGet, "/v1/board", s.token); body["syncing"] != true || body["sync_error"] != nil {
		t.Errorf("during a sync: %v", body)
	}
	close(release)
	<-done
	if _, body := get(t, s.Handler(), http.MethodGet, "/v1/board", s.token); body["syncing"] != nil || body["sync_error"] != "jira: 503 Service Unavailable" || body["fetched_at"] != fetched.Format(time.RFC3339) {
		t.Errorf("after a failed sync: %v", body)
	}
	s.syncOnce(context.Background())
	if _, body := get(t, s.Handler(), http.MethodGet, "/v1/board", s.token); body["syncing"] != nil || body["sync_error"] != nil {
		t.Errorf("after a good sync: %v", body)
	}
}

func TestBoardBeforeAnyBoardIsUnavailable(t *testing.T) {
	s := newServer(t, &fakeSync{}, nil)
	code, body := get(t, s.Handler(), http.MethodGet, "/v1/board", s.token)
	if code != http.StatusServiceUnavailable || body["code"] != "not_synced" {
		t.Errorf("board before a sync = %d %v", code, body)
	}
}

func TestContractTickets(t *testing.T) {
	seed := contractBoard()
	s := newServer(t, &fakeSync{}, &seed)
	assertContract(t, s.Handler(), "/v1/tickets", s.token, "tickets")
}

// assertContract compares an endpoint's JSON to its fixture, which the Herdr desktop mod's tests read.
func assertContract(t *testing.T, h http.Handler, path, token, name string) {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET %s = %d %s", path, rec.Code, rec.Body)
	}
	file := filepath.Join("testdata", "contract", "v1", name+".json")
	if *update {
		if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(file, rec.Body.Bytes(), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(file)
	if err != nil {
		t.Fatalf("%v (run go test ./internal/daemon -run Contract -update)", err)
	}
	if !bytes.Equal(rec.Body.Bytes(), want) {
		t.Errorf("GET %s differs from %s:\n got %s\nwant %s", path, file, rec.Body, want)
	}
}

func TestTheBoardSaysItsOpenSprintsAsTheTUIsContextLine(t *testing.T) {
	seed := contractBoard()
	seed.Sprints = []jira.Sprint{
		{Space: "DEMO", Name: "Sprint 12", End: fetched.Add(60 * time.Hour)},
		{Space: "OPS", Name: "Ops 3", End: fetched.Add(-time.Hour)},
		{Space: "DEMO", Name: "Kanban"},
	}
	s, err := New(Options{Dir: t.TempDir(), Version: "1.2.3", Sync: (&fakeSync{}).sync, Interval: time.Hour, Seed: &seed})
	if err != nil {
		t.Fatal(err)
	}
	_, body := get(t, s.Handler(), http.MethodGet, "/v1/board", s.token)
	got, _ := json.Marshal(body["sprints"])
	if want := `["DEMO · Sprint 12 · 3d left","OPS · Ops 3 · overdue","DEMO · Kanban"]`; string(got) != want {
		t.Errorf("sprints = %s, want %s", got, want)
	}
}
