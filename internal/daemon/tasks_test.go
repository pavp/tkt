package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/pavp/tkt/internal/board"
	"github.com/pavp/tkt/internal/ticket"
)

func taskBoard() board.Snapshot {
	snap := contractBoard()
	snap.Reviews = []ticket.PullRequest{{Repo: "example-org/web", Number: 820, Title: "Add pagination", Author: "alex", URL: "https://github.com/example-org/web/pull/820", State: ticket.PROpen}}
	snap.Others = []ticket.PullRequest{
		{Repo: "example-org/api", Number: 40, Title: "Bump the client", Author: "grace", URL: "https://github.com/example-org/api/pull/40", State: ticket.PROpen},
		{Repo: "example-org/api", Number: 41, Title: "DEMO-9 Speed up exports", Branch: "speed-up-exports", Author: "ada", URL: "https://github.com/example-org/api/pull/41", State: ticket.PROpen, Unresolved: 1, Yours: true},
		{Repo: "example-org/api", Number: 42, Title: "Tidy the docs", Branch: "tidy", Author: "ada", URL: "https://github.com/example-org/api/pull/42", State: ticket.PROpen, Unresolved: 1, Yours: true},
	}
	return snap
}

func tasksServer(t *testing.T, agents TaskAgentsFunc, task TaskFunc) *Server {
	t.Helper()
	seed := taskBoard()
	s, err := New(Options{Dir: t.TempDir(), Version: "1.2.3", Sync: (&fakeSync{}).sync, Interval: time.Hour, Seed: &seed, TaskAgents: agents, Task: task})
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func taskAgents() TaskAgents {
	return TaskAgents{
		Phrase: "DEMO-1's feedback",
		Agents: []OpenAgent{
			{Name: "claude", Pane: "t1", Status: "idle", Folder: "/src/api", Workspace: "api", Repo: "example-org/api", Branch: "feature/DEMO-1-filter", Ticket: "DEMO-1", SameRepo: true},
			{Name: "claude-3", Pane: "t3", Status: "working", Folder: "/src/web", Workspace: "web", Repo: "example-org/web", Branch: "main"},
		},
		Kinds: []string{"claude", "opencode"}, LastAgent: "opencode",
	}
}

func TestContractTaskAgents(t *testing.T) {
	tests := []struct {
		name, path, fixture string
		want                Task
	}{
		{"a ticket's feedback", "/v1/tasks/agents?herdr=desktop&ticket=DEMO-1", "task-agents", Task{Key: "DEMO-1"}},
		{"a review request", "/v1/tasks/agents?herdr=desktop&repo=example-org/web&number=820", "task-agents-review", Task{Review: &taskBoard().Reviews[0]}},
		{"the feedback of a pull request of the user's", "/v1/tasks/agents?herdr=desktop&feedback=true&repo=example-org/api&number=41", "task-agents-feedback",
			Task{Key: "DEMO-9", Known: &ticket.Ticket{Key: "DEMO-9", Summary: "DEMO-9 Speed up exports", PRs: []ticket.PullRequest{taskBoard().Others[1]}}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var got Task
			var herdr string
			s := tasksServer(t, func(_ context.Context, task Task, h string) (TaskAgents, error) {
				got, herdr = task, h
				out := taskAgents()
				switch {
				case task.Review != nil:
					out.Phrase = "the review of #820"
					out.Agents = out.Agents[1:]
				case task.Key == "DEMO-9":
					out.Phrase = "DEMO-9's feedback"
					out.Agents = out.Agents[:1]
				}
				return out, nil
			}, nil)
			assertContract(t, s.Handler(), tt.path, s.token, tt.fixture)
			if tt.want.Key == "DEMO-1" {
				if got.Key != "DEMO-1" || got.Known == nil || got.Known.Summary != "Filter accounts" || got.Review != nil {
					t.Errorf("task %+v: the board's copy of the ticket goes with it", got)
				}
			} else if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("task %+v, want the board's pull request %+v", got, tt.want)
			}
			if herdr != "desktop" {
				t.Errorf("herdr %q", herdr)
			}
		})
	}
}

func TestTaskAgentsRefusals(t *testing.T) {
	tests := []struct {
		name, path string
		err        error
		code       int
		kind       string
	}{
		{"no Herdr named", "/v1/tasks/agents?ticket=DEMO-1", nil, http.StatusBadRequest, "bad_request"},
		{"an unknown Herdr", "/v1/tasks/agents?herdr=tmux&ticket=DEMO-1", nil, http.StatusBadRequest, "bad_request"},
		{"no task", "/v1/tasks/agents?herdr=desktop", nil, http.StatusBadRequest, "bad_request"},
		{"a ticket and a pull request", "/v1/tasks/agents?herdr=desktop&ticket=DEMO-1&repo=example-org/web&number=820", nil, http.StatusBadRequest, "bad_request"},
		{"not a ticket key", "/v1/tasks/agents?herdr=desktop&ticket=demo-1", nil, http.StatusBadRequest, "bad_key"},
		{"not a pull request number", "/v1/tasks/agents?herdr=desktop&repo=example-org/web&number=x", nil, http.StatusBadRequest, "bad_request"},
		{"a pull request the board lacks", "/v1/tasks/agents?herdr=desktop&repo=example-org/web&number=999", nil, http.StatusNotFound, "not_found"},
		{"feedback naming no pull request", "/v1/tasks/agents?herdr=desktop&feedback=true", nil, http.StatusBadRequest, "bad_request"},
		{"feedback not saying yes or no", "/v1/tasks/agents?herdr=desktop&feedback=maybe&repo=example-org/api&number=41", nil, http.StatusBadRequest, "bad_request"},
		{"a ticket and a pull request's feedback", "/v1/tasks/agents?herdr=desktop&ticket=DEMO-1&feedback=true&repo=example-org/api&number=41", nil, http.StatusBadRequest, "bad_request"},
		{"the feedback of someone else's pull request", "/v1/tasks/agents?herdr=desktop&feedback=true&repo=example-org/api&number=40", nil, http.StatusBadRequest, "bad_request"},
		{"the feedback of a pull request naming no ticket", "/v1/tasks/agents?herdr=desktop&feedback=true&repo=example-org/api&number=42", nil, http.StatusBadRequest, "bad_request"},
		{"the feedback of a pull request the board lacks", "/v1/tasks/agents?herdr=desktop&feedback=true&repo=example-org/api&number=999", nil, http.StatusNotFound, "not_found"},
		{"the Herdr is not running", "/v1/tasks/agents?herdr=classic&ticket=DEMO-1", fmt.Errorf("%w: Herdr is not running", ErrHerdrUnavailable), http.StatusServiceUnavailable, "herdr_unavailable"},
		{"the listing fails", "/v1/tasks/agents?herdr=classic&ticket=DEMO-1", errors.New("herdr: socket closed"), http.StatusUnprocessableEntity, "task_failed"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := tasksServer(t, func(context.Context, Task, string) (TaskAgents, error) { return TaskAgents{}, tt.err }, nil)
			code, body := get(t, s.Handler(), http.MethodGet, tt.path, s.token)
			if code != tt.code || body["code"] != tt.kind {
				t.Errorf("GET %s = %d %v, want %d %s", tt.path, code, body, tt.code, tt.kind)
			}
		})
	}
	s := tasksServer(t, nil, nil)
	if code, _ := get(t, s.Handler(), http.MethodGet, "/v1/tasks/agents?herdr=classic&ticket=DEMO-1", s.token); code != http.StatusNotFound {
		t.Errorf("without task agents = %d, want 404", code)
	}
	if code, _ := get(t, s.Handler(), http.MethodGet, "/v1/tasks/agents?herdr=classic&ticket=DEMO-1", ""); code != http.StatusUnauthorized {
		t.Errorf("without the token = %d, want 401", code)
	}
	none := newServer(t, &fakeSync{}, nil)
	none.opts.TaskAgents = func(context.Context, Task, string) (TaskAgents, error) { return TaskAgents{}, nil }
	if code, body := get(t, none.Handler(), http.MethodGet, "/v1/tasks/agents?herdr=classic&repo=example-org/web&number=820", none.token); code != http.StatusServiceUnavailable || body["code"] != "not_synced" {
		t.Errorf("a review before the first board = %d %v, want 503 not_synced", code, body)
	}
}

func TestTaskRefusesWhatItCannotRun(t *testing.T) {
	const ok = `{"herdr":"desktop","ticket":"DEMO-1","to":"own"}`
	tests := []struct {
		name, contentType, origin, body string
		err                             error
		code                            int
		kind                            string
	}{
		{"not JSON", "text/plain", "", ok, nil, http.StatusUnsupportedMediaType, "unsupported_media_type"},
		{"another origin", "application/json", "https://evil.example.com", ok, nil, http.StatusForbidden, "forbidden_origin"},
		{"a malformed body", "application/json", "", `{"herdr":`, nil, http.StatusBadRequest, "bad_request"},
		{"no Herdr named", "application/json", "", `{"ticket":"DEMO-1","to":"own"}`, nil, http.StatusBadRequest, "bad_request"},
		{"no task", "application/json", "", `{"herdr":"desktop","to":"new"}`, nil, http.StatusBadRequest, "bad_request"},
		{"a ticket and a review", "application/json", "", `{"herdr":"desktop","ticket":"DEMO-1","review":{"repo":"example-org/web","number":820},"to":"new"}`, nil, http.StatusBadRequest, "bad_request"},
		{"a review and a pull request's feedback", "application/json", "", `{"herdr":"desktop","review":{"repo":"example-org/web","number":820},"feedback":{"repo":"example-org/api","number":41},"to":"new"}`, nil, http.StatusBadRequest, "bad_request"},
		{"feedback without its pull request's number", "application/json", "", `{"herdr":"desktop","feedback":{"repo":"example-org/api"},"to":"new"}`, nil, http.StatusBadRequest, "bad_request"},
		{"the feedback of someone else's pull request", "application/json", "", `{"herdr":"desktop","feedback":{"repo":"example-org/api","number":40},"to":"new"}`, nil, http.StatusBadRequest, "bad_request"},
		{"not a ticket key", "application/json", "", `{"herdr":"desktop","ticket":"DEMO1","to":"new"}`, nil, http.StatusBadRequest, "bad_key"},
		{"no destination", "application/json", "", `{"herdr":"desktop","ticket":"DEMO-1"}`, nil, http.StatusBadRequest, "bad_request"},
		{"an unknown destination", "application/json", "", `{"herdr":"desktop","ticket":"DEMO-1","to":"shell"}`, nil, http.StatusBadRequest, "bad_request"},
		{"a review to its own agent", "application/json", "", `{"herdr":"desktop","review":{"repo":"example-org/web","number":820},"to":"own"}`, nil, http.StatusBadRequest, "bad_request"},
		{"an open agent without its pane", "application/json", "", `{"herdr":"desktop","ticket":"DEMO-1","to":"open"}`, nil, http.StatusBadRequest, "bad_request"},
		{"a pane for a new agent", "application/json", "", `{"herdr":"desktop","ticket":"DEMO-1","to":"new","pane":"t1"}`, nil, http.StatusBadRequest, "bad_request"},
		{"an agent kind for an open agent", "application/json", "", `{"herdr":"desktop","ticket":"DEMO-1","to":"open","pane":"t1","agent":"claude"}`, nil, http.StatusBadRequest, "bad_request"},
		{"a clone for its own agent", "application/json", "", `{"herdr":"desktop","ticket":"DEMO-1","to":"own","clone":true}`, nil, http.StatusBadRequest, "bad_request"},
		{"a pull request the board lacks", "application/json", "", `{"herdr":"desktop","review":{"repo":"example-org/web","number":999},"to":"new"}`, nil, http.StatusNotFound, "not_found"},
		{"the Herdr is not running", "application/json", "", ok, fmt.Errorf("%w: Herdr desktop is not running", ErrHerdrUnavailable), http.StatusServiceUnavailable, "herdr_unavailable"},
		{"the task fails", "application/json", "", ok, errors.New("DEMO-1 has no worktree yet: start it first"), http.StatusUnprocessableEntity, "task_failed"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ran := false
			s := tasksServer(t, nil, func(context.Context, Task, TaskRequest, func()) error { ran = true; return tt.err })
			code, body := post(t, s.Handler(), "/v1/tasks", s.token, tt.contentType, tt.origin, tt.body)
			if code != tt.code || body["code"] != tt.kind {
				t.Errorf("POST = %d %v, want %d %s", code, body, tt.code, tt.kind)
			}
			if ran != (tt.err != nil) {
				t.Errorf("task ran = %v", ran)
			}
		})
	}
	s := tasksServer(t, nil, func(context.Context, Task, TaskRequest, func()) error { return nil })
	if code, _ := post(t, s.Handler(), "/v1/tasks", "", "application/json", "", ok); code != http.StatusUnauthorized {
		t.Errorf("without the token = %d, want 401", code)
	}
	none := tasksServer(t, nil, nil)
	if code, _ := post(t, none.Handler(), "/v1/tasks", none.token, "application/json", "", ok); code != http.StatusNotFound {
		t.Errorf("without tasks = %d, want 404", code)
	}
}

func TestTaskPassesTheTaskFromTheBoard(t *testing.T) {
	yes := true
	tests := []struct {
		name, body string
		want       TaskRequest
		review     *ticket.PullRequest
	}{
		{"a ticket's feedback to a new agent", `{"herdr":"desktop","ticket":"DEMO-1","to":"new","agent":"opencode","clone":true}`,
			TaskRequest{Herdr: "desktop", Ticket: "DEMO-1", To: "new", Agent: "opencode", Clone: &yes}, nil},
		{"anyone's pull request to an open agent", `{"herdr":"classic","review":{"repo":"example-org/api","number":40},"to":"open","pane":"w1:p1"}`,
			TaskRequest{Herdr: "classic", Review: &TaskReview{Repo: "example-org/api", Number: 40}, To: "open", Pane: "w1:p1"}, &taskBoard().Others[0]},
		{"one of the user's pull requests", `{"herdr":"classic","review":{"repo":"example-org/api","number":12},"to":"new"}`,
			TaskRequest{Herdr: "classic", Review: &TaskReview{Repo: "example-org/api", Number: 12}, To: "new"}, &taskBoard().Tickets[0].PRs[0]},
	}
	feedback := []struct {
		name, body string
		want       TaskRequest
	}{
		{"to its ticket's own agent", `{"herdr":"desktop","feedback":{"repo":"example-org/api","number":41},"to":"own"}`,
			TaskRequest{Herdr: "desktop", Feedback: &TaskReview{Repo: "example-org/api", Number: 41}, To: "own"}},
		{"to an open agent", `{"herdr":"classic","feedback":{"repo":"example-org/api","number":41},"to":"open","pane":"w1:p1"}`,
			TaskRequest{Herdr: "classic", Feedback: &TaskReview{Repo: "example-org/api", Number: 41}, To: "open", Pane: "w1:p1"}},
	}
	for _, tt := range feedback {
		t.Run("a pull request's feedback "+tt.name, func(t *testing.T) {
			var got TaskRequest
			var task Task
			s := tasksServer(t, nil, func(_ context.Context, tk Task, req TaskRequest, _ func()) error { task, got = tk, req; return nil })
			code, body := post(t, s.Handler(), "/v1/tasks", s.token, "application/json", "", tt.body)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("request %+v, want %+v", got, tt.want)
			}
			want := Task{Key: "DEMO-9", Known: &ticket.Ticket{Key: "DEMO-9", Summary: "DEMO-9 Speed up exports", PRs: []ticket.PullRequest{taskBoard().Others[1]}}}
			if !reflect.DeepEqual(task, want) {
				t.Errorf("task %+v, want DEMO-9 holding that pull request alone", task)
			}
			if code != http.StatusOK || body["state"] != "finished" {
				t.Errorf("POST = %d %v, want 200 finished", code, body)
			}
		})
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var got TaskRequest
			var task Task
			s := tasksServer(t, nil, func(_ context.Context, tk Task, req TaskRequest, _ func()) error { task, got = tk, req; return nil })
			code, body := post(t, s.Handler(), "/v1/tasks", s.token, "application/json", "", tt.body)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("request %+v, want %+v", got, tt.want)
			}
			if tt.review != nil && (task.Review == nil || !reflect.DeepEqual(*task.Review, *tt.review) || task.Key != "") {
				t.Errorf("task %+v, want the board's pull request %+v", task, tt.review)
			}
			if tt.review == nil && (task.Key != "DEMO-1" || task.Known == nil || task.Known.PRs[0].Number != 12) {
				t.Errorf("task %+v, want DEMO-1 with the board's copy", task)
			}
			if code != http.StatusOK || body["state"] != "finished" || body["id"] == nil {
				t.Errorf("a task done at once = %d %v, want 200 finished with an id", code, body)
			}
		})
	}
}

func TestTaskAsksBeforeCloning(t *testing.T) {
	s := tasksServer(t, nil, func(context.Context, Task, TaskRequest, func()) error {
		return NeedsAnswerError{Need: "clone", Repo: "example-org/api", Into: "/src"}
	})
	code, body := post(t, s.Handler(), "/v1/tasks", s.token, "application/json", "", `{"herdr":"desktop","ticket":"DEMO-1","to":"new","agent":"claude"}`)
	if code != http.StatusConflict || body["code"] != "needs_answer" || body["needs"] != "clone" || body["repo"] != "example-org/api" || body["into"] != "/src" {
		t.Errorf("POST = %d %v, want 409 needs_answer clone naming the repository and the folder", code, body)
	}
}

func TestATaskThatPanicsFailsWithoutStoppingTheDaemon(t *testing.T) {
	s := tasksServer(t, nil, func(context.Context, Task, TaskRequest, func()) error { panic("boom") })
	code, body := post(t, s.Handler(), "/v1/tasks", s.token, "application/json", "", `{"herdr":"classic","ticket":"DEMO-1","to":"own"}`)
	if code != http.StatusUnprocessableEntity || body["code"] != "task_failed" {
		t.Errorf("POST = %d %v, want 422 task_failed", code, body)
	}
}

func TestALongTaskAnswersAtOnceAndReportsItsEnd(t *testing.T) {
	tests := []struct {
		name, body, subject, event string
		err                        error
	}{
		{"a fix opens its agent", `{"herdr":"desktop","ticket":"DEMO-1","to":"new","agent":"claude","clone":true}`, "DEMO-1", "task.finished", nil},
		{"a review fails after settling", `{"herdr":"desktop","review":{"repo":"example-org/web","number":820},"to":"new","agent":"claude"}`, "example-org/web#820", "task.failed", errors.New("the agent did not start")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			release := make(chan struct{})
			s := tasksServer(t, nil, func(_ context.Context, _ Task, _ TaskRequest, settled func()) error {
				settled()
				<-release
				return tt.err
			})
			cl := s.join()
			code, body := post(t, s.Handler(), "/v1/tasks", s.token, "application/json", "", tt.body)
			if code != http.StatusAccepted || body["state"] != "started" || body["id"] == nil {
				t.Fatalf("POST = %d %v, want 202 started with an id", code, body)
			}
			if again, b := post(t, s.Handler(), "/v1/tasks", s.token, "application/json", "", tt.body); again != http.StatusConflict || b["code"] != "task_running" {
				t.Errorf("a second task of the same work = %d %v, want 409 task_running", again, b)
			}
			close(release)
			ev := jobEvent(t, cl, "task.")
			data, _ := ev["data"].(map[string]any)
			if ev["type"] != tt.event || data["id"] != body["id"] || data["task"] != tt.subject {
				t.Errorf("event = %v, want %s for %v on %s", ev, tt.event, body["id"], tt.subject)
			}
			if tt.err != nil && data["message"] != tt.err.Error() {
				t.Errorf("failure message = %v", data["message"])
			}
		})
	}
}

// jobEvent waits for the next event of a job whose type starts with prefix, such as "task.".
func jobEvent(t *testing.T, cl *client, prefix string) map[string]any {
	t.Helper()
	timeout := time.After(5 * time.Second)
	for {
		select {
		case msg := <-cl.out:
			var ev map[string]any
			if err := json.Unmarshal(msg, &ev); err != nil {
				t.Fatal(err)
			}
			if typ, _ := ev["type"].(string); strings.HasPrefix(typ, prefix) {
				return ev
			}
		case <-timeout:
			t.Fatalf("no %s event", prefix)
		}
	}
}
