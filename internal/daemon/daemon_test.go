package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"maps"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/pavp/tkt/internal/board"
	"github.com/pavp/tkt/internal/ticket"
)

var fetched = time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)

// fakeSync counts syncs and answers with a board holding one ticket per sync, or err.
type fakeSync struct {
	calls atomic.Int32
	err   error
}

func (f *fakeSync) sync(context.Context, func(board.Snapshot)) (board.Snapshot, error) {
	n := f.calls.Add(1)
	if f.err != nil {
		return board.Snapshot{}, f.err
	}
	return board.Snapshot{FetchedAt: fetched.Add(time.Duration(n) * time.Minute), Tickets: []ticket.Ticket{{Key: "DEMO-1"}}}, nil
}

func newServer(t *testing.T, f *fakeSync, seed *board.Snapshot) *Server {
	t.Helper()
	s, err := New(Options{Dir: t.TempDir(), Version: "1.2.3", Sync: f.sync, Interval: time.Hour, Seed: seed})
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func get(t *testing.T, h http.Handler, method, path, token string) (int, map[string]any) {
	t.Helper()
	req := httptest.NewRequest(method, path, nil)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	var body map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	return rec.Code, body
}

func TestHealthNeedsNoTokenAndSaysLittle(t *testing.T) {
	s := newServer(t, &fakeSync{}, nil)
	code, body := get(t, s.Handler(), http.MethodGet, "/health", "")
	if code != http.StatusOK || body["ok"] != true || body["version"] != "1.2.3" || body["protocol"] != float64(Protocol) || body["pid"] != float64(os.Getpid()) || len(body) != 4 {
		t.Errorf("health = %d %v", code, body)
	}
}

func TestV1RoutesNeedTheToken(t *testing.T) {
	s := newServer(t, &fakeSync{}, &board.Snapshot{FetchedAt: fetched})
	cases := []struct {
		name, method, path, token string
		want                      int
	}{
		{"no token", http.MethodGet, "/v1/tickets", "", http.StatusUnauthorized},
		{"wrong token", http.MethodGet, "/v1/tickets", "nope", http.StatusUnauthorized},
		{"sync without token", http.MethodPost, "/v1/sync", "", http.StatusUnauthorized},
		{"right token", http.MethodGet, "/v1/tickets", s.token, http.StatusOK},
		{"unknown route", http.MethodGet, "/v1/nothing", s.token, http.StatusNotFound},
		{"wrong method", http.MethodPost, "/v1/tickets", s.token, http.StatusMethodNotAllowed},
	}
	for _, c := range cases {
		code, body := get(t, s.Handler(), c.method, c.path, c.token)
		if code != c.want {
			t.Errorf("%s: %d %v, want %d", c.name, code, body, c.want)
		}
		if code == http.StatusUnauthorized && (body["type"] != "error" || body["code"] != "unauthorized") {
			t.Errorf("%s: error body = %v", c.name, body)
		}
	}
}

func TestTicketsServesTheSeedThenEachSync(t *testing.T) {
	f := &fakeSync{}
	s := newServer(t, f, &board.Snapshot{FetchedAt: fetched})
	_, body := get(t, s.Handler(), http.MethodGet, "/v1/tickets", s.token)
	if body["fetched_at"] != fetched.Format(time.RFC3339) {
		t.Errorf("before any sync the cached board is served: %v", body)
	}
	s.syncOnce(context.Background())
	_, body = get(t, s.Handler(), http.MethodGet, "/v1/tickets", s.token)
	if body["fetched_at"] != fetched.Add(time.Minute).Format(time.RFC3339) {
		t.Errorf("after a sync its board is served: %v", body)
	}
}

func TestTicketDetail(t *testing.T) {
	seed := &board.Snapshot{FetchedAt: fetched, Tickets: []ticket.Ticket{{Key: "DEMO-1", Summary: "Filter accounts"}}}
	var gotKnown *ticket.Ticket
	detail := func(_ context.Context, key ticket.Key, known *ticket.Ticket) (any, error) {
		gotKnown = known
		if key == "DEMO-9" {
			return nil, errors.New("jira: DEMO-9 not found")
		}
		return map[string]string{"key": string(key)}, nil
	}
	s, err := New(Options{Dir: t.TempDir(), Version: "1.2.3", Sync: (&fakeSync{}).sync, Interval: time.Hour, Seed: seed, Detail: detail})
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name, path, token string
		code              int
		want              map[string]any
		known             bool
	}{
		{"a board ticket", "/v1/tickets/DEMO-1", s.token, http.StatusOK, map[string]any{"key": "DEMO-1"}, true},
		{"a ticket off the board", "/v1/tickets/DEMO-2", s.token, http.StatusOK, map[string]any{"key": "DEMO-2"}, false},
		{"no token", "/v1/tickets/DEMO-1", "", http.StatusUnauthorized, nil, false},
		{"not a key", "/v1/tickets/demo-1", s.token, http.StatusBadRequest, map[string]any{"type": "error", "code": "bad_key", "message": `"demo-1" is not a ticket key like DEMO-123`}, false},
		{"jira fails", "/v1/tickets/DEMO-9", s.token, http.StatusBadGateway, map[string]any{"type": "error", "code": "detail_failed", "message": "jira: DEMO-9 not found"}, false},
	}
	for _, tt := range tests {
		gotKnown = nil
		code, body := get(t, s.Handler(), http.MethodGet, tt.path, tt.token)
		if code != tt.code || (tt.want != nil && !maps.Equal(body, tt.want)) {
			t.Errorf("%s: %d %v", tt.name, code, body)
		}
		if (gotKnown != nil) != tt.known || (tt.known && gotKnown.Summary != "Filter accounts") {
			t.Errorf("%s: known = %+v", tt.name, gotKnown)
		}
	}
}

func TestTicketDetailWithoutALoaderIsNotFound(t *testing.T) {
	s := newServer(t, &fakeSync{}, &board.Snapshot{FetchedAt: fetched})
	if code, body := get(t, s.Handler(), http.MethodGet, "/v1/tickets/DEMO-1", s.token); code != http.StatusNotFound || body["code"] != "not_found" {
		t.Errorf("detail = %d %v", code, body)
	}
}

func TestTicketsBeforeAnyBoardIsUnavailable(t *testing.T) {
	s := newServer(t, &fakeSync{}, nil)
	code, body := get(t, s.Handler(), http.MethodGet, "/v1/tickets", s.token)
	if code != http.StatusServiceUnavailable || body["code"] != "not_synced" {
		t.Errorf("tickets = %d %v", code, body)
	}
}

func TestAFailedSyncKeepsTheLastBoard(t *testing.T) {
	f := &fakeSync{}
	s := newServer(t, f, nil)
	s.syncOnce(context.Background())
	f.err = errors.New("jira down")
	s.syncOnce(context.Background())
	_, body := get(t, s.Handler(), http.MethodGet, "/v1/tickets", s.token)
	if body["fetched_at"] != fetched.Add(time.Minute).Format(time.RFC3339) {
		t.Errorf("a failed sync replaced the board: %v", body)
	}
}

func TestTokenIsCreatedOnceWithOwnerOnlyAccess(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "tkt")
	first, err := Token(dir)
	if err != nil {
		t.Fatal(err)
	}
	second, err := Token(dir)
	if err != nil || second != first || len(first) != 64 {
		t.Errorf("token %q then %q (%v)", first, second, err)
	}
	for path, want := range map[string]os.FileMode{dir: 0o700, filepath.Join(dir, "token"): 0o600} {
		if fi, err := os.Stat(path); err != nil || fi.Mode().Perm() != want {
			t.Errorf("%s mode = %v, want %v (%v)", path, fi.Mode().Perm(), want, err)
		}
	}
}

func TestServeListensOnLoopbackSyncsAndRecordsItself(t *testing.T) {
	f := &fakeSync{}
	dir := t.TempDir()
	s, err := New(Options{Dir: dir, Version: "1.2.3", Sync: f.sync, Interval: 20 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	ln, err := Listen(0)
	if err != nil {
		t.Fatal(err)
	}
	if host, _, _ := net.SplitHostPort(ln.Addr().String()); host != "127.0.0.1" {
		t.Fatalf("listening on %s, want 127.0.0.1 only", ln.Addr())
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- s.Serve(ctx, ln) }()
	waitFor(t, func() bool { return f.calls.Load() >= 2 })
	info, err := ReadInfo(dir)
	if err != nil || info.Port != ln.Addr().(*net.TCPAddr).Port || info.PID != os.Getpid() || info.Version != "1.2.3" {
		t.Fatalf("info = %+v, %v", info, err)
	}
	if fi, err := os.Stat(filepath.Join(dir, "daemon.json")); err != nil || fi.Mode().Perm() != 0o600 {
		t.Errorf("daemon.json mode = %v, %v", fi.Mode().Perm(), err)
	}
	req, _ := http.NewRequest(http.MethodPost, "http://"+ln.Addr().String()+"/v1/sync", nil)
	req.Header.Set("Authorization", "Bearer "+s.token)
	before := f.calls.Load()
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted || !strings.Contains(string(b), `"ok":true`) {
		t.Errorf("sync = %d %s", resp.StatusCode, b)
	}
	waitFor(t, func() bool { return f.calls.Load() > before })
	cancel()
	if err := <-done; err != nil {
		t.Errorf("serve returned %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "daemon.json")); !os.IsNotExist(err) {
		t.Error("daemon.json should be gone once the daemon stops")
	}
}

func waitFor(t *testing.T, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatal("timed out")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestHealthNamesTheProcess(t *testing.T) {
	s := newServer(t, &fakeSync{}, nil)
	ln, err := Listen(0)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = s.Serve(ctx, ln) }()
	port := ln.Addr().(*net.TCPAddr).Port
	waitFor(t, func() bool { _, ok := Healthy(context.Background(), port); return ok })
	if pid, _ := Healthy(context.Background(), port); pid != os.Getpid() {
		t.Errorf("health pid = %d, want %d", pid, os.Getpid())
	}
}

func TestStoppingKeepsAnotherDaemonsRecord(t *testing.T) {
	f := &fakeSync{}
	dir := t.TempDir()
	s, err := New(Options{Dir: dir, Version: "1.2.3", Sync: f.sync, Interval: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	ln, err := Listen(0)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- s.Serve(ctx, ln) }()
	waitFor(t, func() bool { _, err := ReadInfo(dir); return err == nil })
	newer := []byte(`{"port":1,"pid":424242,"version":"newer"}`)
	if err := os.WriteFile(filepath.Join(dir, "daemon.json"), newer, 0o600); err != nil {
		t.Fatal(err)
	}
	cancel()
	<-done
	if b, err := os.ReadFile(filepath.Join(dir, "daemon.json")); err != nil || string(b) != string(newer) {
		t.Errorf("a stopping daemon removed another daemon's record: %q %v", b, err)
	}
}
