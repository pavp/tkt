package daemon

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/pavp/tkt/internal/board"
)

// fakeState keeps the board state in memory, as board-state.json would, and counts its saves.
type fakeState struct {
	st    board.SavedState
	saves int
}

func (f *fakeState) load() board.SavedState { return f.st }

func (f *fakeState) save(st board.SavedState) error {
	f.st, f.saves = st, f.saves+1
	return nil
}

func stateServer(t *testing.T, f *fakeState) *Server {
	t.Helper()
	s, err := New(Options{Dir: t.TempDir(), Version: "1.2.3", Sync: (&fakeSync{}).sync, Interval: time.Hour, SavedState: f.load, SaveState: f.save})
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func patchState(s *Server, body, origin string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPatch, "/v1/board/state", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+s.token)
	req.Header.Set("Content-Type", "application/json")
	if origin != "" {
		req.Header.Set("Origin", origin)
	}
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	return rec
}

func TestContractBoardState(t *testing.T) {
	off := false
	tests := []struct {
		name, fixture string
		st            board.SavedState
	}{
		{"nothing saved yet", "board-state-default", board.SavedState{}},
		{"a saved state", "board-state", board.SavedState{HiddenRepos: []string{"example-org/api"}, HiddenBoards: []string{"Core"}, Collapsed: []string{"backlog:", "mine:example-org/web"},
			View: board.ViewList, Team: true, TeamPRs: &off, BacklogMerged: true}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := stateServer(t, &fakeState{st: tt.st})
			assertContract(t, s.Handler(), "/v1/board/state", s.token, tt.fixture)
		})
	}
}

func TestPatchBoardStateChangesOnlyTheFieldsSent(t *testing.T) {
	on := true
	f := &fakeState{st: board.SavedState{HiddenRepos: []string{"example-org/api"}, Collapsed: []string{"sprint:12"}, View: board.ViewColumns}}
	s := stateServer(t, f)
	rec := patchState(s, `{"team":true,"hidden_boards":["Web","Core","Web"],"view":"list","team_prs":true}`, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("patch = %d %s", rec.Code, rec.Body)
	}
	want := board.SavedState{HiddenRepos: []string{"example-org/api"}, HiddenBoards: []string{"Core", "Web"}, Collapsed: []string{"sprint:12"}, View: board.ViewList, Team: true, TeamPRs: &on}
	if !reflect.DeepEqual(f.st, want) || f.saves != 1 {
		t.Errorf("saved %d times: %+v, want %+v", f.saves, f.st, want)
	}
	wantBody := `{"view":"list","team":true,"hidden_boards":["Core","Web"],"hidden_repos":["example-org/api"],"collapsed":["sprint:12"],"team_prs":true,"backlog_merged":false}` + "\n"
	if rec.Body.String() != wantBody {
		t.Errorf("body = %s, want %s", rec.Body, wantBody)
	}

	if rec := patchState(s, `{"hidden_boards":[],"collapsed":[]}`, ""); rec.Code != http.StatusOK || f.st.HiddenBoards != nil || f.st.Collapsed != nil {
		t.Errorf("emptying lists = %d %s, saved %+v", rec.Code, rec.Body, f.st)
	}
}

func TestPatchBoardStateRefusesWhatTheBoardDoesNotRemember(t *testing.T) {
	tests := []struct {
		name, body, origin, contentType string
		code                            int
		message                         string
	}{
		{"an unknown field", `{"colour":"blue"}`, "", "application/json", http.StatusBadRequest, `unknown field "colour"`},
		{"an unknown view", `{"view":"grid"}`, "", "application/json", http.StatusBadRequest, `view must be columns or list, not "grid"`},
		{"a boolean as text", `{"team":"true"}`, "", "application/json", http.StatusBadRequest, "team must be true or false"},
		{"a null", `{"backlog_merged":null}`, "", "application/json", http.StatusBadRequest, "backlog_merged must be true or false"},
		{"a list of numbers", `{"hidden_repos":[1]}`, "", "application/json", http.StatusBadRequest, "hidden_repos must be a list of names"},
		{"an empty name", `{"hidden_boards":["Web",""]}`, "", "application/json", http.StatusBadRequest, "hidden_boards must be a list of names"},
		{"not an object", `["team"]`, "", "application/json", http.StatusBadRequest, "the body is not a board state"},
		{"a second value", `{"team":true}{"team":false}`, "", "application/json", http.StatusBadRequest, "the body is not a board state"},
		{"not JSON", `{"team":true}`, "", "text/plain", http.StatusUnsupportedMediaType, "send the request as application/json"},
		{"another origin", `{"team":true}`, "http://evil.example", "application/json", http.StatusForbidden, "requests from another origin are refused"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := &fakeState{st: board.SavedState{View: board.ViewList}}
			s := stateServer(t, f)
			req := httptest.NewRequest(http.MethodPatch, "/v1/board/state", strings.NewReader(tt.body))
			req.Header.Set("Authorization", "Bearer "+s.token)
			req.Header.Set("Content-Type", tt.contentType)
			if tt.origin != "" {
				req.Header.Set("Origin", tt.origin)
			}
			rec := httptest.NewRecorder()
			s.Handler().ServeHTTP(rec, req)
			var body struct{ Code, Message string }
			_ = json.Unmarshal(rec.Body.Bytes(), &body)
			if rec.Code != tt.code || !strings.Contains(body.Message, tt.message) {
				t.Errorf("patch = %d %s, want %d %q", rec.Code, rec.Body, tt.code, tt.message)
			}
			if f.saves != 0 || f.st.View != board.ViewList {
				t.Errorf("a refused patch saved %d times: %+v", f.saves, f.st)
			}
		})
	}
}

func TestBoardStateRoutes(t *testing.T) {
	s := newServer(t, &fakeSync{}, nil)
	if code, body := get(t, s.Handler(), http.MethodGet, "/v1/board/state", s.token); code != http.StatusNotFound || body["code"] != "not_found" {
		t.Errorf("state without a store = %d %v", code, body)
	}
	if rec := patchState(s, `{"team":true}`, ""); rec.Code != http.StatusNotFound {
		t.Errorf("patch without a store = %d %s", rec.Code, rec.Body)
	}
	s = stateServer(t, &fakeState{})
	if code, _ := get(t, s.Handler(), http.MethodGet, "/v1/board/state", ""); code != http.StatusUnauthorized {
		t.Errorf("state without the token = %d", code)
	}
	if code, _ := get(t, s.Handler(), http.MethodPatch, "/v1/board/state", ""); code != http.StatusUnauthorized {
		t.Errorf("patch without the token = %d", code)
	}
}

func TestPatchBoardStateTellsTheOtherClients(t *testing.T) {
	f := &fakeState{}
	d, err := New(Options{Dir: t.TempDir(), Version: "1.2.3", Sync: (&scriptedSync{}).sync, Interval: time.Hour, Seed: &board.Snapshot{FetchedAt: fetched}, SavedState: f.load, SaveState: f.save})
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(d.Handler())
	t.Cleanup(srv.Close)
	c := joined(t, d, srv)
	next(t, c)
	if rec := patchState(d, `{"backlog_merged":true}`, ""); rec.Code != http.StatusOK {
		t.Fatalf("patch = %d %s", rec.Code, rec.Body)
	}
	if e := next(t, c); e.Type != "state.updated" || !strings.Contains(string(e.Data), `"backlog_merged":true`) {
		t.Errorf("event = %s %s", e.Type, e.Data)
	}
}
