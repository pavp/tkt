package daemon

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/pavp/tkt/internal/board"
	"github.com/pavp/tkt/internal/ticket"
)

// fakeMoves answers the Jira side of Move and Assign and records what the daemon asked.
type fakeMoves struct {
	trs      []board.Transition
	users    []board.User
	err      error
	listed   []string
	moved    []string
	values   []board.Value
	comment  string
	assigned []string
	searched []string
}

func (f *fakeMoves) transitions(_ context.Context, key ticket.Key) ([]board.Transition, error) {
	f.listed = append(f.listed, string(key))
	return f.trs, f.err
}

func (f *fakeMoves) transition(_ context.Context, key ticket.Key, id string, values []board.Value, comment string) error {
	f.moved, f.values, f.comment = append(f.moved, string(key)+" "+id), values, comment
	return f.err
}

func (f *fakeMoves) search(_ context.Context, key ticket.Key, query string) ([]board.User, error) {
	f.searched = append(f.searched, string(key)+" "+query)
	return f.users, f.err
}

func (f *fakeMoves) assign(_ context.Context, key ticket.Key, me bool, accountID string) error {
	f.assigned = append(f.assigned, fmt.Sprintf("%s me=%v %s", key, me, accountID))
	return f.err
}

var demoTransitions = []board.Transition{
	{ID: "31", To: "In Review"},
	{ID: "32", To: "In Review"},
	{ID: "41", To: "Done", NeedsForm: true},
	{ID: "51", To: "Blocked", Fields: []board.Field{
		{ID: "customfield_10300", Name: "Reason", Kind: board.KindOption, Required: true, Options: []board.Option{{ID: "9001", Value: "Waiting on a decision"}}},
		{ID: "customfield_10301", Name: "Owner", Kind: board.KindUser},
	}},
}

func movesServer(t *testing.T, f *fakeMoves) *Server {
	t.Helper()
	seed := board.Snapshot{FetchedAt: fetched, Tickets: []ticket.Ticket{{Key: "DEMO-123", URL: "https://example.atlassian.net/browse/DEMO-123"}, {Key: "DEMO-124"}}}
	s, err := New(Options{Dir: t.TempDir(), Version: "1.2.3", Sync: (&fakeSync{}).sync, Interval: time.Hour, Seed: &seed,
		Transitions: f.transitions, Transition: f.transition, Users: f.search, Assign: f.assign})
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// rejected is Jira refusing a request the way the Jira client reports it.
type rejected struct {
	status int
	fields map[string]string
}

func (r rejected) Error() string { return fmt.Sprintf("jira: %d", r.status) }
func (r rejected) Rejection() (int, []string, map[string]string) {
	return r.status, []string{"Fix the form"}, r.fields
}

func syncAsked(s *Server) bool {
	select {
	case <-s.syncNow:
		return true
	default:
		return false
	}
}

func TestContractMoves(t *testing.T) {
	tests := []struct{ name, path, fixture string }{
		{"a ticket with a link to Jira", "/v1/tickets/DEMO-123/transitions", "transitions"},
		{"a ticket without one", "/v1/tickets/DEMO-124/transitions", "transitions-no-url"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := movesServer(t, &fakeMoves{trs: demoTransitions})
			assertContract(t, s.Handler(), tt.path, s.token, tt.fixture)
		})
	}
}

func TestMovesRefusals(t *testing.T) {
	tests := []struct {
		name, path string
		err        error
		code       int
		kind       string
	}{
		{"not a ticket key", "/v1/tickets/demo-123/transitions", nil, http.StatusBadRequest, "bad_key"},
		{"a ticket Jira does not know", "/v1/tickets/DEMO-999/transitions", rejected{status: http.StatusNotFound}, http.StatusNotFound, "not_found"},
		{"Jira fails", "/v1/tickets/DEMO-123/transitions", errors.New("jira GET: connection refused"), http.StatusBadGateway, "jira_failed"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := movesServer(t, &fakeMoves{err: tt.err})
			code, body := get(t, s.Handler(), http.MethodGet, tt.path, s.token)
			if code != tt.code || body["code"] != tt.kind {
				t.Errorf("GET %s = %d %v, want %d %s", tt.path, code, body, tt.code, tt.kind)
			}
		})
	}
	none, err := New(Options{Dir: t.TempDir(), Sync: (&fakeSync{}).sync, Interval: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/v1/tickets/DEMO-123/transitions", "/v1/tickets/DEMO-123/users?query=ada"} {
		if code, _ := get(t, none.Handler(), http.MethodGet, path, none.token); code != http.StatusNotFound {
			t.Errorf("%s without Jira = %d, want 404", path, code)
		}
	}
}

func TestMoveWritesTheFormAndRefreshesTheBoard(t *testing.T) {
	f := &fakeMoves{trs: demoTransitions}
	s := movesServer(t, f)
	code, body := post(t, s.Handler(), "/v1/tickets/DEMO-123/transitions", s.token, "application/json", "",
		`{"id":"51","values":{"customfield_10300":"9001","customfield_10301":"acc-ada"},"comment":"Waiting for keys"}`)
	if code != http.StatusOK || body["key"] != "DEMO-123" || body["to"] != "Blocked" {
		t.Fatalf("POST = %d %v, want 200 moved to Blocked", code, body)
	}
	want := []board.Value{{Field: "customfield_10300", Kind: board.KindOption, ID: "9001"}, {Field: "customfield_10301", Kind: board.KindUser, ID: "acc-ada"}}
	if !reflect.DeepEqual(f.moved, []string{"DEMO-123 51"}) || !reflect.DeepEqual(f.values, want) || f.comment != "Waiting for keys" {
		t.Errorf("moved %v with %+v, comment %q", f.moved, f.values, f.comment)
	}
	if !syncAsked(s) {
		t.Error("a move asks for a sync, as the board refreshes after one")
	}
}

func TestMoveRefusesWhatTheBoardWouldNotSend(t *testing.T) {
	tests := []struct {
		name, body string
		err        error
		code       int
		kind       string
	}{
		{"a transition Jira no longer offers", `{"id":"99"}`, nil, http.StatusConflict, "transition_unavailable"},
		{"a second transition to the same status", `{"id":"32"}`, nil, http.StatusConflict, "transition_unavailable"},
		{"one left to Jira's form", `{"id":"41"}`, nil, http.StatusConflict, "needs_jira_form"},
		{"a required field left empty", `{"id":"51","values":{"customfield_10301":"acc-ada"}}`, nil, http.StatusUnprocessableEntity, "missing_field"},
		{"a field the form lacks", `{"id":"31","values":{"customfield_1":"x"}}`, nil, http.StatusBadRequest, "bad_request"},
		{"an option the field lacks", `{"id":"51","values":{"customfield_10300":"1"}}`, nil, http.StatusBadRequest, "bad_request"},
		{"no transition", `{}`, nil, http.StatusBadRequest, "bad_request"},
		{"a malformed body", `{"id":`, nil, http.StatusBadRequest, "bad_request"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := &fakeMoves{trs: demoTransitions}
			s := movesServer(t, f)
			code, body := post(t, s.Handler(), "/v1/tickets/DEMO-123/transitions", s.token, "application/json", "", tt.body)
			if code != tt.code || body["code"] != tt.kind {
				t.Errorf("POST = %d %v, want %d %s", code, body, tt.code, tt.kind)
			}
			if len(f.moved) > 0 || syncAsked(s) {
				t.Errorf("a refused move reached Jira: %v", f.moved)
			}
		})
	}
	s := movesServer(t, &fakeMoves{trs: demoTransitions})
	if _, body := post(t, s.Handler(), "/v1/tickets/DEMO-123/transitions", s.token, "application/json", "", `{"id":"41"}`); body["url"] != "https://example.atlassian.net/browse/DEMO-123" {
		t.Errorf("a move left to Jira names where to open it: %v", body)
	}
}

func TestMoveShowsJiraRejections(t *testing.T) {
	f := &fakeMoves{trs: demoTransitions}
	s := movesServer(t, f)
	h := s.Handler()
	s.opts.Transition = func(context.Context, ticket.Key, string, []board.Value, string) error {
		return rejected{status: http.StatusBadRequest, fields: map[string]string{"resolution": "Resolution is required."}}
	}
	code, body := post(t, h, "/v1/tickets/DEMO-123/transitions", s.token, "application/json", "", `{"id":"31"}`)
	fields, _ := body["fields"].(map[string]any)
	msgs, _ := body["messages"].([]any)
	if code != http.StatusUnprocessableEntity || body["code"] != "jira_rejected" || fields["resolution"] != "Resolution is required." || len(msgs) != 1 || msgs[0] != "Fix the form" {
		t.Errorf("POST = %d %v, want 422 jira_rejected with Jira's messages", code, body)
	}
	if syncAsked(s) {
		t.Error("a rejected move asks for no sync")
	}
}

func TestMutatingMoveRoutesNeedJSONFromTheDaemonsOrigin(t *testing.T) {
	routes := []struct{ method, path, body string }{
		{http.MethodPost, "/v1/tickets/DEMO-123/transitions", `{"id":"31"}`},
		{http.MethodPut, "/v1/tickets/DEMO-123/assignee", `{"to":"me"}`},
	}
	for _, r := range routes {
		t.Run(r.method+" "+r.path, func(t *testing.T) {
			f := &fakeMoves{trs: demoTransitions}
			s := movesServer(t, f)
			cases := []struct {
				token, contentType, origin string
				code                       int
			}{
				{"", "application/json", "", http.StatusUnauthorized},
				{s.token, "text/plain", "", http.StatusUnsupportedMediaType},
				{s.token, "application/json", "https://evil.example.com", http.StatusForbidden},
			}
			for _, c := range cases {
				if code := send(t, s.Handler(), r.method, r.path, c.token, c.contentType, c.origin, r.body); code != c.code {
					t.Errorf("token=%t type=%s origin=%s = %d, want %d", c.token != "", c.contentType, c.origin, code, c.code)
				}
			}
			if len(f.moved)+len(f.assigned) > 0 {
				t.Errorf("a refused request reached Jira: %v %v", f.moved, f.assigned)
			}
		})
	}
}

func send(t *testing.T, h http.Handler, method, path, token, contentType, origin, body string) int {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	req.Header.Set("Content-Type", contentType)
	if origin != "" {
		req.Header.Set("Origin", origin)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec.Code
}

func TestContractUsers(t *testing.T) {
	f := &fakeMoves{users: []board.User{{ID: "acc-ada", Name: "Ada Lovelace"}, {ID: "acc-alan", Name: "Alan Turing"}}}
	s := movesServer(t, f)
	assertContract(t, s.Handler(), "/v1/tickets/DEMO-123/users?query=a+l", s.token, "users")
	if !reflect.DeepEqual(f.searched, []string{"DEMO-123 a l"}) {
		t.Errorf("searched %v", f.searched)
	}
}

func TestUsersAskJiraFromTwoLettersAndAtMostTwenty(t *testing.T) {
	var many []board.User
	for i := range 30 {
		many = append(many, board.User{ID: fmt.Sprintf("acc-%d", i), Name: "Ada"})
	}
	f := &fakeMoves{users: many}
	s := movesServer(t, f)
	for _, q := range []string{"", "a", " a "} {
		code, body := get(t, s.Handler(), http.MethodGet, "/v1/tickets/DEMO-123/users?query="+strings.ReplaceAll(q, " ", "+"), s.token)
		if users, _ := body["users"].([]any); code != http.StatusOK || users == nil || len(users) != 0 {
			t.Errorf("query %q = %d %v, want 200 with no users", q, code, body)
		}
	}
	if len(f.searched) != 0 {
		t.Errorf("searched Jira for fewer than two letters: %v", f.searched)
	}
	if _, body := get(t, s.Handler(), http.MethodGet, "/v1/tickets/DEMO-123/users?query=ad", s.token); len(body["users"].([]any)) != 20 {
		t.Errorf("users = %d, want 20", len(body["users"].([]any)))
	}
}

func TestAssignGivesTheTicketAndRefreshesTheBoardAndBacklog(t *testing.T) {
	tests := []struct{ name, body, call string }{
		{"to me", `{"to":"me"}`, "DEMO-123 me=true "},
		{"to someone found", `{"to":"user","account_id":"acc-ada"}`, "DEMO-123 me=false acc-ada"},
		{"to nobody", `{"to":"nobody"}`, "DEMO-123 me=false "},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := &fakeMoves{}
			s := movesServer(t, f)
			var forced []bool
			s.opts.Backlog = func(_ context.Context, force bool) (board.Backlog, error) {
				forced = append(forced, force)
				return board.Backlog{FetchedAt: fetched}, nil
			}
			code := send(t, s.Handler(), http.MethodPut, "/v1/tickets/DEMO-123/assignee", s.token, "application/json", "", tt.body)
			if code != http.StatusOK || !reflect.DeepEqual(f.assigned, []string{tt.call}) {
				t.Fatalf("PUT = %d, assigned %v, want 200 %s", code, f.assigned, tt.call)
			}
			if !syncAsked(s) {
				t.Error("an assignment asks for a sync, as the board refreshes after one")
			}
			get(t, s.Handler(), http.MethodGet, "/v1/backlog", s.token)
			get(t, s.Handler(), http.MethodGet, "/v1/backlog", s.token)
			if !reflect.DeepEqual(forced, []bool{true, false}) {
				t.Errorf("backlog loads forced %v, want the first after the assignment forced", forced)
			}
		})
	}
}

func TestAssignRefusals(t *testing.T) {
	tests := []struct {
		name, path, body string
		err              error
		code             int
		kind             string
	}{
		{"someone without an account", "/v1/tickets/DEMO-123/assignee", `{"to":"user"}`, nil, http.StatusBadRequest, "bad_request"},
		{"an account beside me", "/v1/tickets/DEMO-123/assignee", `{"to":"me","account_id":"acc-ada"}`, nil, http.StatusBadRequest, "bad_request"},
		{"an unknown target", "/v1/tickets/DEMO-123/assignee", `{"to":"team"}`, nil, http.StatusBadRequest, "bad_request"},
		{"not a ticket key", "/v1/tickets/DEMO/assignee", `{"to":"me"}`, nil, http.StatusBadRequest, "bad_key"},
		{"a ticket Jira does not know", "/v1/tickets/DEMO-999/assignee", `{"to":"me"}`, rejected{status: http.StatusNotFound}, http.StatusNotFound, "not_found"},
		{"Jira refuses the user", "/v1/tickets/DEMO-123/assignee", `{"to":"user","account_id":"acc-x"}`, rejected{status: http.StatusBadRequest, fields: map[string]string{"assignee": "User cannot be assigned."}}, http.StatusUnprocessableEntity, "jira_rejected"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := &fakeMoves{err: tt.err}
			s := movesServer(t, f)
			req := httptest.NewRequest(http.MethodPut, tt.path, strings.NewReader(tt.body))
			req.Header.Set("Authorization", "Bearer "+s.token)
			req.Header.Set("Content-Type", "application/json")
			rec := httptest.NewRecorder()
			s.Handler().ServeHTTP(rec, req)
			if rec.Code != tt.code || !strings.Contains(rec.Body.String(), `"code":"`+tt.kind+`"`) {
				t.Errorf("PUT = %d %s, want %d %s", rec.Code, rec.Body, tt.code, tt.kind)
			}
			if tt.err == nil && len(f.assigned) > 0 {
				t.Errorf("a refused assignment reached Jira: %v", f.assigned)
			}
			if syncAsked(s) {
				t.Error("a failed assignment asks for no sync")
			}
		})
	}
}
