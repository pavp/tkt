package daemon

import (
	"context"
	"errors"
	"maps"
	"net/http"
	"testing"
	"time"

	"github.com/pavp/tkt/internal/board"
	"github.com/pavp/tkt/internal/ticket"
)

func TestTicketContextIsTheCopiedMarkdown(t *testing.T) {
	ctxText := func(_ context.Context, key ticket.Key) (string, error) {
		if key == "DEMO-9" {
			return "", errors.New("jira: DEMO-9 not found")
		}
		return "# " + string(key) + " · Filter accounts\n", nil
	}
	s, err := New(Options{Dir: t.TempDir(), Version: "1.2.3", Sync: (&fakeSync{}).sync, Interval: time.Hour, Context: ctxText})
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name, path, token string
		code              int
		want              map[string]any
	}{
		{"a ticket", "/v1/tickets/DEMO-1/context", s.token, http.StatusOK, map[string]any{"key": "DEMO-1", "text": "# DEMO-1 · Filter accounts\n"}},
		{"no token", "/v1/tickets/DEMO-1/context", "", http.StatusUnauthorized, nil},
		{"not a key", "/v1/tickets/demo-1/context", s.token, http.StatusBadRequest, map[string]any{"type": "error", "code": "bad_key", "message": `"demo-1" is not a ticket key like DEMO-123`}},
		{"jira fails", "/v1/tickets/DEMO-9/context", s.token, http.StatusBadGateway, map[string]any{"type": "error", "code": "detail_failed", "message": "jira: DEMO-9 not found"}},
	}
	for _, tt := range tests {
		code, body := get(t, s.Handler(), http.MethodGet, tt.path, tt.token)
		if code != tt.code || (tt.want != nil && !maps.Equal(body, tt.want)) {
			t.Errorf("%s: %d %v", tt.name, code, body)
		}
	}
	s = newServer(t, &fakeSync{}, nil)
	if code, body := get(t, s.Handler(), http.MethodGet, "/v1/tickets/DEMO-1/context", s.token); code != http.StatusNotFound || body["code"] != "not_found" {
		t.Errorf("context without a builder = %d %v", code, body)
	}
}

func TestTicketDetailHearsTheTicketsAgent(t *testing.T) {
	seed := &board.Snapshot{FetchedAt: fetched, Tickets: []ticket.Ticket{{Key: "DEMO-1"}}, Agents: map[string]string{"DEMO-1": "working"}}
	agents := map[string]string{}
	detail := func(_ context.Context, key ticket.Key, _ *ticket.Ticket, agent string) (any, error) {
		agents[string(key)] = agent
		return map[string]string{"key": string(key)}, nil
	}
	s, err := New(Options{Dir: t.TempDir(), Version: "1.2.3", Sync: (&fakeSync{}).sync, Interval: time.Hour, Seed: seed, Detail: detail})
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"DEMO-1", "DEMO-2"} {
		if code, body := get(t, s.Handler(), http.MethodGet, "/v1/tickets/"+key, s.token); code != http.StatusOK {
			t.Errorf("%s = %d %v", key, code, body)
		}
	}
	if want := map[string]string{"DEMO-1": "working", "DEMO-2": ""}; !maps.Equal(agents, want) {
		t.Errorf("agents handed to the loader = %v, want %v", agents, want)
	}
}
