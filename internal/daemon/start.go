package daemon

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"mime"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"

	"github.com/pavp/tkt/internal/ticket"
)

// StartChoicesFunc reads what a Start of key asks before it runs in the Herdr named herdr ("desktop" or "classic").
type StartChoicesFunc func(ctx context.Context, key ticket.Key, herdr string) (StartChoices, error)

// StartFunc runs a Start of key with req's answers. It calls settled once it asks nothing more and only opening the
// agents remains, which can take minutes; a Start that only focuses an agent may return without calling it.
type StartFunc func(ctx context.Context, key ticket.Key, req StartRequest, settled func()) error

// ErrHerdrUnavailable is a Start in a Herdr that is not running.
var ErrHerdrUnavailable = errors.New("herdr unavailable")

// NeedsAnswerError is a Start that needs an answer its request did not give: "clone", "worktree", "repos" or "agent".
// A task's clone question also names the repository and the folder the clone would go into.
type NeedsAnswerError struct{ Need, Repo, Into string }

func (e NeedsAnswerError) Error() string { return "the start needs an answer for " + e.Need }

// StartChoices is what the board's Start modal asks: a worktree among several, else the repositories, and, when Opens,
// the agent to open.
type StartChoices struct {
	Herdr     string          `json:"herdr"`
	Opens     bool            `json:"opens"`
	Worktrees []StartWorktree `json:"worktrees"`
	Repos     []StartRepo     `json:"repos"`
	Agents    []string        `json:"agents"`
	LastAgent string          `json:"last_agent,omitempty"`
	// AgentsError says why no agent can be offered.
	AgentsError string `json:"agents_error,omitempty"`
}

type StartWorktree struct {
	Repo   string `json:"repo"`
	Branch string `json:"branch"`
	Path   string `json:"path"`
	// Agent is the status of the agent working in it, empty without one.
	Agent string `json:"agent,omitempty"`
}

type StartRepo struct {
	Name     string `json:"name"`
	LastUsed bool   `json:"last_used"`
	Checked  bool   `json:"checked"`
}

// StartRequest carries a Start's answers; Herdr is required, and Clone nil means the request did not answer it.
type StartRequest struct {
	Herdr    string   `json:"herdr"`
	Repos    []string `json:"repos,omitempty"`
	Worktree string   `json:"worktree,omitempty"`
	Agent    string   `json:"agent,omitempty"`
	Clone    *bool    `json:"clone,omitempty"`
}

// pathKey is the ticket key in the request's path, answering 400 when it is not one.
func pathKey(w http.ResponseWriter, r *http.Request) (ticket.Key, bool) {
	key := ticket.Key(r.PathValue("key"))
	return key, validKey(w, key)
}

func validHerdr(w http.ResponseWriter, name string) bool {
	if name != "desktop" && name != "classic" {
		writeError(w, http.StatusBadRequest, "bad_request", fmt.Sprintf("herdr must be desktop or classic, not %q", name))
		return false
	}
	return true
}

// writeJobError answers a job of kind, such as a Start, or its choices, that could not run.
func writeJobError(w http.ResponseWriter, kind string, err error) {
	var need NeedsAnswerError
	switch {
	case errors.As(err, &need):
		body := map[string]string{"type": "error", "code": "needs_answer", "needs": need.Need, "message": err.Error()}
		if need.Repo != "" {
			body["repo"], body["into"] = need.Repo, need.Into
		}
		writeJSON(w, http.StatusConflict, body)
	case errors.Is(err, ErrHerdrUnavailable):
		writeError(w, http.StatusServiceUnavailable, "herdr_unavailable", err.Error())
	default:
		writeError(w, http.StatusUnprocessableEntity, kind+"_failed", err.Error())
	}
}

func (s *Server) startChoices(w http.ResponseWriter, r *http.Request) {
	if s.opts.StartChoices == nil {
		writeError(w, http.StatusNotFound, "not_found", "this daemon starts no agents")
		return
	}
	key, ok := pathKey(w, r)
	herdr := r.URL.Query().Get("herdr")
	if !ok || !validHerdr(w, herdr) {
		return
	}
	ch, err := s.opts.StartChoices(r.Context(), key, herdr)
	if err != nil {
		writeJobError(w, "start", err)
		return
	}
	ch.Herdr = herdr
	ch.Worktrees, ch.Repos, ch.Agents = orNoneOf(ch.Worktrees), orNoneOf(ch.Repos), orNoneOf(ch.Agents)
	writeJSON(w, http.StatusOK, ch)
}

func orNoneOf[T any](xs []T) []T {
	if xs == nil {
		return []T{}
	}
	return xs
}

// mutating guards a route that changes something outside the daemon: besides the token, it takes only JSON, which a
// page cannot send across origins without a preflight, from no origin or the daemon's own, as the WebSocket does.
func (s *Server) mutating(next http.HandlerFunc) http.HandlerFunc {
	return s.authed(func(w http.ResponseWriter, r *http.Request) {
		if mt, _, err := mime.ParseMediaType(r.Header.Get("Content-Type")); err != nil || mt != "application/json" {
			writeError(w, http.StatusUnsupportedMediaType, "unsupported_media_type", "send the request as application/json")
			return
		}
		if o := r.Header.Get("Origin"); o != "" {
			u, err := url.Parse(o)
			if err != nil || !strings.EqualFold(u.Host, r.Host) {
				writeError(w, http.StatusForbidden, "forbidden_origin", "requests from another origin are refused")
				return
			}
		}
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next(rec, r)
		if s.opts.Log != nil {
			s.opts.Log("write %s %s → %d\n", r.Method, strconv.Quote(r.URL.Path), rec.status)
		}
	})
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(code int) {
	r.status = code
	r.ResponseWriter.WriteHeader(code)
}

func (s *Server) start(w http.ResponseWriter, r *http.Request) {
	if s.opts.Start == nil {
		writeError(w, http.StatusNotFound, "not_found", "this daemon starts no agents")
		return
	}
	key, ok := pathKey(w, r)
	if !ok {
		return
	}
	var req StartRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "the body is not a start request: "+err.Error())
		return
	}
	if !validHerdr(w, req.Herdr) {
		return
	}
	if req.Worktree != "" && len(req.Repos) > 0 {
		writeError(w, http.StatusBadRequest, "bad_request", "give a worktree or repositories, not both")
		return
	}
	s.runJob(w, r, job{kind: "start", field: "key", subject: string(key)}, func(ctx context.Context, settled func()) error {
		return s.opts.Start(ctx, key, req, settled)
	})
}

// job is a slow action a POST runs: one per subject at a time, answering 202 once it asks nothing more and reporting
// its end over the WebSocket as <kind>.finished or <kind>.failed with {id, <field>: subject, message}.
type job struct{ kind, field, subject string }

func (s *Server) runJob(w http.ResponseWriter, r *http.Request, j job, run func(ctx context.Context, settled func()) error) {
	busy := j.kind + ":" + j.subject
	s.mu.Lock()
	if s.running[busy] {
		s.mu.Unlock()
		writeError(w, http.StatusConflict, j.kind+"_running", fmt.Sprintf("a %s of %s is still running", j.kind, j.subject))
		return
	}
	s.running[busy] = true
	s.mu.Unlock()

	id := startID()
	// The job outlives this request once settled, so it ends with the daemon rather than with the request.
	ctx, cancel := context.WithCancel(context.WithoutCancel(r.Context()))
	settled, done := make(chan struct{}), make(chan error, 1)
	go func() {
		defer cancel()
		go func() {
			select {
			case <-s.quit:
				cancel()
			case <-ctx.Done():
			}
		}()
		var once sync.Once
		defer func() {
			if p := recover(); p != nil {
				done <- fmt.Errorf("the %s of %s failed: %v", j.kind, j.subject, p)
			}
		}()
		done <- run(ctx, func() { once.Do(func() { close(settled) }) })
	}()
	select {
	case err := <-done:
		s.endJob(busy)
		if err != nil {
			writeJobError(w, j.kind, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"id": id, "state": "finished"})
	case <-settled:
		s.mu.Lock()
		s.recordJob(id, jobEnd{Kind: j.kind, Subject: j.subject, State: "running"})
		s.mu.Unlock()
		writeJSON(w, http.StatusAccepted, map[string]string{"id": id, "state": "started"})
		go func() {
			err := <-done
			s.mu.Lock()
			defer s.mu.Unlock()
			delete(s.running, busy)
			if err != nil {
				s.recordJob(id, jobEnd{Kind: j.kind, Subject: j.subject, State: "failed", Message: err.Error()})
				s.publish(j.kind+".failed", map[string]string{"id": id, j.field: j.subject, "message": err.Error()})
				return
			}
			s.recordJob(id, jobEnd{Kind: j.kind, Subject: j.subject, State: "finished"})
			s.publish(j.kind+".finished", map[string]string{"id": id, j.field: j.subject})
		}()
	}
}

func (s *Server) endJob(busy string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.running, busy)
}

func startID() string {
	raw := make([]byte, 8)
	_, _ = rand.Read(raw)
	return hex.EncodeToString(raw)
}
