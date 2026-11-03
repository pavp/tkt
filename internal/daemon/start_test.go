package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/pavp/tkt/internal/ticket"
)

func startServer(t *testing.T, choices StartChoicesFunc, start StartFunc) *Server {
	t.Helper()
	s, err := New(Options{Dir: t.TempDir(), Version: "1.2.3", Sync: (&fakeSync{}).sync, Interval: time.Hour, StartChoices: choices, Start: start})
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestContractStartChoices(t *testing.T) {
	tests := []struct {
		name, fixture string
		choices       StartChoices
	}{
		{"several worktrees", "start-worktrees", StartChoices{Worktrees: []StartWorktree{
			{Repo: "example-org/web", Branch: "feature/DEMO-7-a", Path: "/src/web-DEMO-7-a", Agent: "idle"},
			{Repo: "example-org/web", Branch: "feature/DEMO-7-b", Path: "/src/web-DEMO-7-b"},
		}, Agents: []string{"claude", "opencode"}, LastAgent: "opencode"}},
		{"the repositories", "start-repos", StartChoices{Opens: true, Repos: []StartRepo{
			{Name: "example-org/web", LastUsed: true, Checked: true}, {Name: "example-org/api"},
		}, Agents: []string{"claude", "opencode"}, LastAgent: "claude"}},
		{"no agent installed", "start-no-agent", StartChoices{Opens: true, AgentsError: "no agent Herdr desktop can start is installed; install one of: pi, claude"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var asked []string
			s := startServer(t, func(_ context.Context, key ticket.Key, herdr string) (StartChoices, error) {
				asked = append(asked, string(key), herdr)
				return tt.choices, nil
			}, nil)
			assertContract(t, s.Handler(), "/v1/tickets/DEMO-7/start?herdr=desktop", s.token, tt.fixture)
			if want := []string{"DEMO-7", "desktop"}; !reflect.DeepEqual(asked, want) {
				t.Errorf("asked %q, want %q", asked, want)
			}
		})
	}
}

func TestStartChoicesRefusals(t *testing.T) {
	tests := []struct {
		name, path string
		err        error
		code       int
		kind       string
	}{
		{"no Herdr named", "/v1/tickets/DEMO-7/start", nil, http.StatusBadRequest, "bad_request"},
		{"an unknown Herdr", "/v1/tickets/DEMO-7/start?herdr=tmux", nil, http.StatusBadRequest, "bad_request"},
		{"not a ticket key", "/v1/tickets/demo-7/start?herdr=classic", nil, http.StatusBadRequest, "bad_key"},
		{"the Herdr is not running", "/v1/tickets/DEMO-7/start?herdr=classic", fmt.Errorf("%w: Herdr is not running", ErrHerdrUnavailable), http.StatusServiceUnavailable, "herdr_unavailable"},
		{"the choices fail", "/v1/tickets/DEMO-7/start?herdr=classic", errors.New("space DEMO is not configured"), http.StatusUnprocessableEntity, "start_failed"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := startServer(t, func(context.Context, ticket.Key, string) (StartChoices, error) { return StartChoices{}, tt.err }, nil)
			code, body := get(t, s.Handler(), http.MethodGet, tt.path, s.token)
			if code != tt.code || body["code"] != tt.kind {
				t.Errorf("GET %s = %d %v, want %d %s", tt.path, code, body, tt.code, tt.kind)
			}
		})
	}
	s := startServer(t, nil, nil)
	if code, _ := get(t, s.Handler(), http.MethodGet, "/v1/tickets/DEMO-7/start?herdr=classic", s.token); code != http.StatusNotFound {
		t.Errorf("without start choices = %d, want 404", code)
	}
	if code, _ := get(t, s.Handler(), http.MethodGet, "/v1/tickets/DEMO-7/start?herdr=classic", ""); code != http.StatusUnauthorized {
		t.Errorf("without the token = %d, want 401", code)
	}
}

func post(t *testing.T, h http.Handler, path, token, contentType, origin, body string) (int, map[string]any) {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	if origin != "" {
		req.Header.Set("Origin", origin)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	var out map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	return rec.Code, out
}

func TestStartRefusesWhatItCannotRun(t *testing.T) {
	const ok = `{"herdr":"desktop"}`
	tests := []struct {
		name, path, contentType, origin, body string
		err                                   error
		code                                  int
		kind                                  string
	}{
		{"not JSON", "/v1/tickets/DEMO-7/start", "text/plain", "", ok, nil, http.StatusUnsupportedMediaType, "unsupported_media_type"},
		{"another origin", "/v1/tickets/DEMO-7/start", "application/json", "https://evil.example.com", ok, nil, http.StatusForbidden, "forbidden_origin"},
		{"a malformed body", "/v1/tickets/DEMO-7/start", "application/json", "", `{"herdr":`, nil, http.StatusBadRequest, "bad_request"},
		{"no Herdr named", "/v1/tickets/DEMO-7/start", "application/json", "", `{}`, nil, http.StatusBadRequest, "bad_request"},
		{"an unknown Herdr", "/v1/tickets/DEMO-7/start", "application/json", "", `{"herdr":"tmux"}`, nil, http.StatusBadRequest, "bad_request"},
		{"a worktree and repositories", "/v1/tickets/DEMO-7/start", "application/json", "", `{"herdr":"desktop","worktree":"/src/a","repos":["web"]}`, nil, http.StatusBadRequest, "bad_request"},
		{"not a ticket key", "/v1/tickets/DEMO7/start", "application/json", "", ok, nil, http.StatusBadRequest, "bad_key"},
		{"the Herdr is not running", "/v1/tickets/DEMO-7/start", "application/json", "", ok, fmt.Errorf("%w: Herdr desktop is not running", ErrHerdrUnavailable), http.StatusServiceUnavailable, "herdr_unavailable"},
		{"the start fails", "/v1/tickets/DEMO-7/start", "application/json; charset=utf-8", "", ok, errors.New("no repository to start in"), http.StatusUnprocessableEntity, "start_failed"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ran := false
			s := startServer(t, nil, func(context.Context, ticket.Key, StartRequest, func()) error { ran = true; return tt.err })
			code, body := post(t, s.Handler(), tt.path, s.token, tt.contentType, tt.origin, tt.body)
			if code != tt.code || body["code"] != tt.kind {
				t.Errorf("POST = %d %v, want %d %s", code, body, tt.code, tt.kind)
			}
			if ran != (tt.err != nil) {
				t.Errorf("start ran = %v", ran)
			}
		})
	}
	s := startServer(t, nil, func(context.Context, ticket.Key, StartRequest, func()) error { return nil })
	if code, _ := post(t, s.Handler(), "/v1/tickets/DEMO-7/start", "", "application/json", "", ok); code != http.StatusUnauthorized {
		t.Errorf("without the token = %d, want 401", code)
	}
	if code, _ := post(t, s.Handler(), "/v1/tickets/DEMO-7/start", s.token, "application/json", "http://example.com", ok); code != http.StatusOK {
		t.Errorf("from the daemon's own origin = %d, want 200", code)
	}
	none := startServer(t, nil, nil)
	if code, _ := post(t, none.Handler(), "/v1/tickets/DEMO-7/start", none.token, "application/json", "", ok); code != http.StatusNotFound {
		t.Errorf("without start = %d, want 404", code)
	}
}

func TestStartNamesTheMissingAnswer(t *testing.T) {
	for _, need := range []string{"clone", "worktree", "repos", "agent"} {
		t.Run(need, func(t *testing.T) {
			s := startServer(t, nil, func(context.Context, ticket.Key, StartRequest, func()) error {
				return fmt.Errorf("start: %w", NeedsAnswerError{Need: need})
			})
			code, body := post(t, s.Handler(), "/v1/tickets/DEMO-7/start", s.token, "application/json", "", `{"herdr":"classic"}`)
			if code != http.StatusConflict || body["code"] != "needs_answer" || body["needs"] != need {
				t.Errorf("POST = %d %v, want 409 needs_answer needs %s", code, body, need)
			}
		})
	}
}

func TestAStartThatPanicsFailsWithoutStoppingTheDaemon(t *testing.T) {
	s := startServer(t, nil, func(context.Context, ticket.Key, StartRequest, func()) error { panic("boom") })
	code, body := post(t, s.Handler(), "/v1/tickets/DEMO-7/start", s.token, "application/json", "", `{"herdr":"classic"}`)
	if code != http.StatusUnprocessableEntity || body["code"] != "start_failed" {
		t.Errorf("POST = %d %v, want 422 start_failed", code, body)
	}
}

func TestStartPassesTheAnswers(t *testing.T) {
	var got StartRequest
	var key ticket.Key
	s := startServer(t, nil, func(_ context.Context, k ticket.Key, req StartRequest, _ func()) error { key, got = k, req; return nil })
	code, body := post(t, s.Handler(), "/v1/tickets/DEMO-7/start", s.token, "application/json", "",
		`{"herdr":"desktop","repos":["example-org/web","example-org/api"],"agent":"opencode","clone":false}`)
	no := false
	if want := (StartRequest{Herdr: "desktop", Repos: []string{"example-org/web", "example-org/api"}, Agent: "opencode", Clone: &no}); key != "DEMO-7" || !reflect.DeepEqual(got, want) {
		t.Errorf("start %s %+v, want DEMO-7 %+v", key, got, want)
	}
	if code != http.StatusOK || body["state"] != "finished" || body["id"] == "" || body["id"] == nil {
		t.Errorf("a start done at once = %d %v, want 200 finished with an id", code, body)
	}
}

// startEvent waits for the next start event a client gets.
func startEvent(t *testing.T, cl *client) map[string]any {
	t.Helper()
	timeout := time.After(5 * time.Second)
	for {
		select {
		case msg := <-cl.out:
			var ev map[string]any
			if err := json.Unmarshal(msg, &ev); err != nil {
				t.Fatal(err)
			}
			if typ, _ := ev["type"].(string); strings.HasPrefix(typ, "start.") {
				return ev
			}
		case <-timeout:
			t.Fatal("no start event")
		}
	}
}

func TestALongStartAnswersAtOnceAndReportsItsEnd(t *testing.T) {
	tests := []struct {
		name, event string
		err         error
	}{
		{"it opens the agents", "start.finished", nil},
		{"it fails after settling", "start.failed", errors.New("the agent did not start")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			release := make(chan struct{})
			s := startServer(t, nil, func(ctx context.Context, _ ticket.Key, _ StartRequest, settled func()) error {
				settled()
				<-release
				return tt.err
			})
			cl := s.join()
			code, body := post(t, s.Handler(), "/v1/tickets/DEMO-7/start", s.token, "application/json", "", `{"herdr":"desktop","agent":"claude"}`)
			if code != http.StatusAccepted || body["state"] != "started" || body["id"] == nil {
				t.Fatalf("POST = %d %v, want 202 started with an id", code, body)
			}
			if again, b := post(t, s.Handler(), "/v1/tickets/DEMO-7/start", s.token, "application/json", "", `{"herdr":"desktop"}`); again != http.StatusConflict || b["code"] != "start_running" {
				t.Errorf("a second start of the ticket = %d %v, want 409 start_running", again, b)
			}
			close(release)
			ev := startEvent(t, cl)
			data, _ := ev["data"].(map[string]any)
			if ev["type"] != tt.event || data["id"] != body["id"] || data["key"] != "DEMO-7" {
				t.Errorf("event = %v, want %s for %v", ev, tt.event, body["id"])
			}
			if tt.err != nil && data["message"] != tt.err.Error() {
				t.Errorf("failure message = %v", data["message"])
			}
			if code, _ := post(t, s.Handler(), "/v1/tickets/DEMO-7/start", s.token, "application/json", "", `{"herdr":"desktop","agent":"claude"}`); code == http.StatusConflict {
				t.Errorf("a start after the last one ended is refused")
			}
		})
	}
}
