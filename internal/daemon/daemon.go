// Package daemon keeps the board synced in one background process and serves it over a local API, so
// the board, tkt list and the Herdr plugin read fresh data without each asking Jira and GitHub.
package daemon

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"maps"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/pavp/tkt/internal/board"
	"github.com/pavp/tkt/internal/ticket"
)

// Protocol is the API's protocol number, reported by /health and checked in the WebSocket hello.
const Protocol = 1

// SyncFunc fetches a fresh board, writing whatever cache the CLI keeps. It may hand partial the board as
// Jira tells it before GitHub answers, which the daemon serves and announces until the full one arrives.
type SyncFunc func(ctx context.Context, partial func(board.Snapshot)) (board.Snapshot, error)

// DetailFunc loads one ticket with its description and comments; known is the board's copy of the ticket,
// when the board holds it.
type DetailFunc func(ctx context.Context, key ticket.Key, known *ticket.Ticket) (any, error)

type Options struct {
	// Dir is the data directory holding the token and daemon.json.
	Dir     string
	Version string
	Sync    SyncFunc
	// Interval is the time between syncs; POST /v1/sync asks for one at once.
	Interval time.Duration
	// Seed, when set, is served until the first sync finishes, so clients see the cached board at once.
	Seed         *board.Snapshot // HelloTimeout bounds the wait for a WebSocket client's hello; zero means ten seconds.
	HelloTimeout time.Duration
	// Detail serves GET /v1/tickets/{key}; nil answers that route with not found.
	Detail DetailFunc
}

// Info is what daemon.json records for clients: where the daemon listens and which process it is.
type Info struct {
	Port    int    `json:"port"`
	PID     int    `json:"pid"`
	Version string `json:"version"`
}

type Server struct {
	opts  Options
	token string

	// mu guards the board and the clients, so a client joining between two events sees each once.
	mu      sync.Mutex
	snap    board.Snapshot
	have    bool
	clients map[*client]struct{}

	syncNow chan struct{}
	// quit closes when Serve stops, so WebSocket handlers, which outlive the HTTP server's shutdown,
	// close their clients too.
	quit     chan struct{}
	quitOnce sync.Once
}

func New(o Options) (*Server, error) {
	tok, err := Token(o.Dir)
	if err != nil {
		return nil, err
	}
	if o.HelloTimeout == 0 {
		o.HelloTimeout = 10 * time.Second
	}
	s := &Server{opts: o, token: tok, syncNow: make(chan struct{}, 1), clients: map[*client]struct{}{}, quit: make(chan struct{})}
	if o.Seed != nil {
		s.snap, s.have = *o.Seed, true
	}
	return s, nil
}

// Token returns the daemon's 256-bit token, creating it on first use readable by the owner only.
func Token(dir string) (string, error) {
	path := filepath.Join(dir, "token")
	if b, err := os.ReadFile(path); err == nil && len(strings.TrimSpace(string(b))) == 64 {
		return strings.TrimSpace(string(b)), nil
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	tok := hex.EncodeToString(raw)
	if err := os.WriteFile(path, []byte(tok), 0o600); err != nil {
		return "", err
	}
	return tok, nil
}

// Listen binds the loopback interface only, so nothing outside this machine reaches the daemon.
func Listen(port int) (net.Listener, error) {
	return net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
}

func infoPath(dir string) string { return filepath.Join(dir, "daemon.json") }

// Healthy reports whether a tkt daemon of this protocol answers on port, and its pid, so a caller can
// tell it from another daemon that took the port of one that died.
func Healthy(ctx context.Context, port int) (int, bool) {
	ctx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, fmt.Sprintf("http://127.0.0.1:%d/health", port), nil)
	if err != nil {
		return 0, false
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0, false
	}
	defer resp.Body.Close()
	var h struct {
		OK       bool `json:"ok"`
		Protocol int  `json:"protocol"`
		PID      int  `json:"pid"`
	}
	if resp.StatusCode != http.StatusOK || json.NewDecoder(resp.Body).Decode(&h) != nil || !h.OK || h.Protocol != Protocol {
		return 0, false
	}
	return h.PID, true
}

// ReadInfo reads where a running daemon listens.
func ReadInfo(dir string) (Info, error) {
	var info Info
	b, err := os.ReadFile(infoPath(dir))
	if err != nil {
		return info, err
	}
	return info, json.Unmarshal(b, &info)
}

// Serve syncs and answers requests on ln until ctx ends, recording itself in daemon.json meanwhile.
func (s *Server) Serve(ctx context.Context, ln net.Listener) error {
	info, err := json.Marshal(Info{Port: ln.Addr().(*net.TCPAddr).Port, PID: os.Getpid(), Version: s.opts.Version})
	if err != nil {
		return err
	}
	if err := os.WriteFile(infoPath(s.opts.Dir), info, 0o600); err != nil {
		return err
	}
	defer func() {
		// Another daemon may have started meanwhile and written its own record; leave that one.
		if info, err := ReadInfo(s.opts.Dir); err == nil && info.PID == os.Getpid() {
			os.Remove(infoPath(s.opts.Dir))
		}
	}()

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	var loop sync.WaitGroup
	loop.Add(1)
	go func() {
		defer loop.Done()
		s.syncLoop(ctx)
	}()

	defer s.quitOnce.Do(func() { close(s.quit) })
	srv := &http.Server{Handler: s.Handler(), ReadHeaderTimeout: 10 * time.Second}
	srv.RegisterOnShutdown(func() { s.quitOnce.Do(func() { close(s.quit) }) })
	errc := make(chan error, 1)
	go func() { errc <- srv.Serve(ln) }()
	select {
	case err := <-errc:
		cancel()
		loop.Wait()
		return err
	case <-ctx.Done():
	}
	shutdown, done := context.WithTimeout(context.Background(), 5*time.Second)
	defer done()
	err = srv.Shutdown(shutdown)
	loop.Wait()
	return err
}

func (s *Server) syncLoop(ctx context.Context) {
	tick := time.NewTicker(s.opts.Interval)
	defer tick.Stop()
	for {
		s.syncOnce(ctx)
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		case <-s.syncNow:
		}
	}
}

// syncOnce fetches a board and tells clients what changed; a failed sync keeps serving the last good one.
func (s *Server) syncOnce(ctx context.Context) {
	s.mu.Lock()
	s.publish("sync.started", struct{}{})
	before, hadBefore := s.snap, s.have
	s.mu.Unlock()
	painted := false
	snap, err := s.opts.Sync(ctx, func(p board.Snapshot) {
		s.mu.Lock()
		defer s.mu.Unlock()
		if p.Agents == nil {
			p.Agents = s.snap.Agents
		}
		s.snap, s.have, painted = p, true, true
		s.publish("snapshot", public(p))
	})
	s.mu.Lock()
	defer s.mu.Unlock()
	if err != nil {
		// A partial board must not outlive its failed sync: the last full one comes back.
		if painted && hadBefore {
			s.snap = before
			s.publish("snapshot", public(before))
		}
		s.publish("sync.failed", map[string]string{"message": err.Error()})
		return
	}
	old, had := s.snap, s.have
	if snap.Agents == nil {
		// The sync could not read Herdr; the agents it last reported still stand.
		snap.Agents = old.Agents
	}
	s.snap, s.have = snap, true
	s.publishChanges(old, had, snap)
	if had && !maps.Equal(old.Agents, snap.Agents) {
		s.publish("agent.updated", map[string]any{"agents": snap.Agents})
	}
	s.publish("sync.finished", map[string]any{"fetched_at": snap.FetchedAt})
}

// Board returns the board the daemon serves and whether it has one yet.
func (s *Server) Board() (board.Snapshot, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.snap, s.have
}

// SetAgents records the agent state of each ticket, as Herdr reports it between syncs, and tells clients
// when it changed.
func (s *Server) SetAgents(agents map[string]string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if maps.Equal(s.snap.Agents, agents) {
		return
	}
	s.snap.Agents = agents
	s.publish("agent.updated", map[string]any{"agents": agents})
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "version": s.opts.Version, "protocol": Protocol, "pid": os.Getpid()})
	})
	mux.HandleFunc("GET /v1/tickets", s.authed(s.tickets))
	mux.HandleFunc("GET /v1/tickets/{key}", s.authed(s.ticket))
	mux.HandleFunc("POST /v1/sync", s.authed(s.requestSync))
	mux.HandleFunc("GET /v1/ws", s.ws)
	return mux
}

func (s *Server) validToken(got string) bool {
	return subtle.ConstantTimeCompare([]byte(got), []byte(s.token)) == 1
}

func (s *Server) authed(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		got, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
		if !ok || !s.validToken(got) {
			writeError(w, http.StatusUnauthorized, "unauthorized", "missing or wrong token")
			return
		}
		next(w, r)
	}
}

func (s *Server) tickets(w http.ResponseWriter, _ *http.Request) {
	s.mu.Lock()
	snap, have := s.snap, s.have
	s.mu.Unlock()
	if !have {
		writeError(w, http.StatusServiceUnavailable, "not_synced", "the first sync has not finished")
		return
	}
	writeJSON(w, http.StatusOK, public(snap))
}

func (s *Server) ticket(w http.ResponseWriter, r *http.Request) {
	if s.opts.Detail == nil {
		writeError(w, http.StatusNotFound, "not_found", "this daemon serves no ticket details")
		return
	}
	key := ticket.Key(r.PathValue("key"))
	if keys := ticket.KeysIn(string(key)); len(keys) != 1 || keys[0] != key {
		writeError(w, http.StatusBadRequest, "bad_key", fmt.Sprintf("%q is not a ticket key like DEMO-123", key))
		return
	}
	s.mu.Lock()
	snap, have := s.snap, s.have
	s.mu.Unlock()
	var known *ticket.Ticket
	if t, ok := snap.Find(key); have && ok {
		known = &t
	}
	d, err := s.opts.Detail(r.Context(), key, known)
	if err != nil {
		writeError(w, http.StatusBadGateway, "detail_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, d)
}

// public leaves out the development-panel cache, which only the sync needs.
func public(snap board.Snapshot) board.Snapshot {
	snap.Panels = nil
	return snap
}

func (s *Server) requestSync(w http.ResponseWriter, _ *http.Request) {
	select {
	case s.syncNow <- struct{}{}:
	default:
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"ok": true})
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, code int, kind, msg string) {
	writeJSON(w, code, map[string]string{"type": "error", "code": kind, "message": msg})
}
