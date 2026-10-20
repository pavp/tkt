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
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/pavp/tkt/internal/board"
)

// Protocol is the API's protocol number, reported by /health and checked in the WebSocket hello.
const Protocol = 1

// SyncFunc fetches a fresh board, writing whatever cache the CLI keeps.
type SyncFunc func(ctx context.Context) (board.Snapshot, error)

type Options struct {
	// Dir is the data directory holding the token and daemon.json.
	Dir     string
	Version string
	Sync    SyncFunc
	// Interval is the time between syncs; POST /v1/sync asks for one at once.
	Interval time.Duration
	// Seed, when set, is served until the first sync finishes, so clients see the cached board at once.
	Seed *board.Snapshot
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

	mu   sync.Mutex
	snap board.Snapshot
	have bool

	syncNow chan struct{}
}

func New(o Options) (*Server, error) {
	tok, err := Token(o.Dir)
	if err != nil {
		return nil, err
	}
	s := &Server{opts: o, token: tok, syncNow: make(chan struct{}, 1)}
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
	defer os.Remove(infoPath(s.opts.Dir))

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	var loop sync.WaitGroup
	loop.Add(1)
	go func() {
		defer loop.Done()
		s.syncLoop(ctx)
	}()

	srv := &http.Server{Handler: s.Handler(), ReadHeaderTimeout: 10 * time.Second}
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

// syncOnce fetches a board; a failed sync keeps serving the last good one.
func (s *Server) syncOnce(ctx context.Context) {
	snap, err := s.opts.Sync(ctx)
	if err != nil {
		return
	}
	s.mu.Lock()
	s.snap, s.have = snap, true
	s.mu.Unlock()
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "version": s.opts.Version, "protocol": Protocol})
	})
	mux.HandleFunc("GET /v1/tickets", s.authed(s.tickets))
	mux.HandleFunc("POST /v1/sync", s.authed(s.requestSync))
	return mux
}

func (s *Server) authed(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		got, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
		if !ok || subtle.ConstantTimeCompare([]byte(got), []byte(s.token)) != 1 {
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
	writeJSON(w, http.StatusOK, snap)
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
