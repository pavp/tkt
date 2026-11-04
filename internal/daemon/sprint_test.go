package daemon

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"testing"
	"time"

	"github.com/pavp/tkt/internal/board"
	"github.com/pavp/tkt/internal/ticket"
)

func sprintServer(t *testing.T, moved *[]string) *Server {
	t.Helper()
	s, err := New(Options{Dir: t.TempDir(), Version: "1.2.3", Sync: (&fakeSync{}).sync, Interval: time.Hour,
		Backlog: func(context.Context, bool) (board.Backlog, error) { return contractBacklog(), nil },
		MoveToSprint: func(_ context.Context, key ticket.Key, sprint int) error {
			*moved = append(*moved, string(key)+"→"+strconv.Itoa(sprint))
			return nil
		}})
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestATicketMovesOnlyToAnActiveSprintOfTheBacklog(t *testing.T) {
	tests := []struct {
		name, body string
		code       int
		moved      int
	}{
		{"an active sprint", `{"sprint":11}`, http.StatusOK, 1},
		{"a future sprint", `{"sprint":21}`, http.StatusConflict, 0},
		{"a sprint the backlog does not have", `{"sprint":99}`, http.StatusConflict, 0},
		{"no sprint", `{}`, http.StatusBadRequest, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var moved []string
			s := sprintServer(t, &moved)
			code, body := post(t, s.Handler(), "/v1/tickets/DEMO-40/sprint", s.token, "application/json", "", tt.body)
			if code != tt.code || len(moved) != tt.moved {
				t.Errorf("POST = %d %v, moved %v; want %d with %d moves", code, body, moved, tt.code, tt.moved)
			}
		})
	}
}

func TestTheBacklogSaysWhichGroupsAreActiveSprints(t *testing.T) {
	var moved []string
	s := sprintServer(t, &moved)
	_, body := get(t, s.Handler(), http.MethodGet, "/v1/backlog", s.token)
	groups, _ := body["groups"].([]any)
	active := map[string]bool{}
	for _, g := range groups {
		m := g.(map[string]any)
		active[m["group"].(string)], _ = m["active"].(bool)
	}
	if !active["sprint:11"] || active["sprint:21"] {
		t.Errorf("active groups = %v, want sprint:11 only", active)
	}
}

func TestTheBacklogListsItsActiveSprintsWhateverTheGrouping(t *testing.T) {
	var moved []string
	s := sprintServer(t, &moved)
	for _, path := range []string{"/v1/backlog?merged=false", "/v1/backlog?merged=true"} {
		_, body := get(t, s.Handler(), http.MethodGet, path, s.token)
		got, _ := json.Marshal(body["active_sprints"])
		if want := `[{"board":"Web","id":11,"name":"Sprint 12"}]`; string(got) != want {
			t.Errorf("%s active_sprints = %s, want %s", path, got, want)
		}
	}
}
