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
type NeedsAnswerError struct{ Need string }

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
	if keys := ticket.KeysIn(string(key)); len(keys) != 1 || keys[0] != key {
		writeError(w, http.StatusBadRequest, "bad_key", fmt.Sprintf("%q is not a ticket key like DEMO-123", key))
		return "", false
	}
	return key, true
}

func validHerdr(w http.ResponseWriter, name string) bool {
	if name != "desktop" && name != "classic" {
		writeError(w, http.StatusBadRequest, "bad_request", fmt.Sprintf("herdr must be desktop or classic, not %q", name))
		return false
	}
	return true
}

// writeStartError answers a Start, or its choices, that could not run.
func writeStartError(w http.ResponseWriter, err error) {
	var need NeedsAnswerError
	switch {
	case errors.As(err, &need):
		writeJSON(w, http.StatusConflict, map[string]string{"type": "error", "code": "needs_answer", "needs": need.Need, "message": err.Error()})
	case errors.Is(err, ErrHerdrUnavailable):
		writeError(w, http.StatusServiceUnavailable, "herdr_unavailable", err.Error())
	default:
		writeError(w, http.StatusUnprocessableEntity, "start_failed", err.Error())
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
		writeStartError(w, err)
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
		next(w, r)
	})
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
	s.mu.Lock()
	if s.starting[key] {
		s.mu.Unlock()
		writeError(w, http.StatusConflict, "start_running", fmt.Sprintf("a start of %s is still running", key))
		return
	}
	s.starting[key] = true
	s.mu.Unlock()

	id := startID()
	// The Start outlives this request once settled, so it ends with the daemon rather than with the request.
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
		done <- s.opts.Start(ctx, key, req, func() { once.Do(func() { close(settled) }) })
	}()
	select {
	case err := <-done:
		s.endStart(key)
		if err != nil {
			writeStartError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"id": id, "state": "finished"})
	case <-settled:
		writeJSON(w, http.StatusAccepted, map[string]string{"id": id, "state": "started"})
		go func() {
			err := <-done
			s.mu.Lock()
			defer s.mu.Unlock()
			delete(s.starting, key)
			if err != nil {
				s.publish("start.failed", map[string]string{"id": id, "key": string(key), "message": err.Error()})
				return
			}
			s.publish("start.finished", map[string]string{"id": id, "key": string(key)})
		}()
	}
}

func (s *Server) endStart(key ticket.Key) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.starting, key)
}

func startID() string {
	raw := make([]byte, 8)
	_, _ = rand.Read(raw)
	return hex.EncodeToString(raw)
}
