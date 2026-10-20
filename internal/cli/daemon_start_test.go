package cli

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/pavp/tkt/internal/board"
	"github.com/pavp/tkt/internal/daemon"
)

// fakeDaemons starts an in-process daemon whenever the CLI asks for a background one, and counts them.
type fakeDaemons struct {
	started atomic.Int32
	mu      sync.Mutex
	stops   []context.CancelFunc
	// served closes when each fake daemon's Serve returns, so a stop waits for its cleanup.
	served  []chan struct{}
	servers []*daemon.Server
	// board seeds every fake daemon; nil leaves them without a board.
	board *board.Snapshot
	// version is what the next fake daemon reports; empty means this CLI's Version.
	version string
}

func (f *fakeDaemons) server() *daemon.Server {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.servers[len(f.servers)-1]
}

func (f *fakeDaemons) install(t *testing.T, h *harness) {
	t.Helper()
	h.app.StartDetached = func(name string, args []string, logPath string) (int, error) {
		if !strings.HasSuffix(name, "tkt") || strings.Join(args, " ") != "daemon run" || filepath.Base(logPath) != "daemon.log" {
			t.Errorf("started %s %v (log %s)", name, args, logPath)
		}
		f.started.Add(1)
		f.mu.Lock()
		seed := f.board
		version := cmp.Or(f.version, Version)
		f.mu.Unlock()
		sync := func(context.Context) (board.Snapshot, error) {
			return board.Snapshot{}, errors.New("fake daemons do not sync")
		}
		s, err := daemon.New(daemon.Options{Dir: h.app.Env.DataDir(), Version: version, Sync: sync, Interval: time.Hour, Seed: seed})
		if err != nil {
			return 0, err
		}
		ln, err := daemon.Listen(0)
		if err != nil {
			return 0, err
		}
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan struct{})
		f.mu.Lock()
		f.stops = append(f.stops, cancel)
		f.servers = append(f.servers, s)
		f.served = append(f.served, done)
		f.mu.Unlock()
		go func() { _ = s.Serve(ctx, ln); close(done) }()
		return os.Getpid(), nil
	}
	t.Cleanup(func() {
		f.mu.Lock()
		defer f.mu.Unlock()
		for _, stop := range f.stops {
			stop()
		}
	})
}

// stopAll stops every fake daemon and waits for each to finish its shutdown: they share this process's
// pid, so an unfinished one could remove the record of the daemon started after it.
func (f *fakeDaemons) stopAll() {
	f.mu.Lock()
	stops, served := slices.Clone(f.stops), slices.Clone(f.served)
	f.mu.Unlock()
	for _, stop := range stops {
		stop()
	}
	for _, done := range served {
		<-done
	}
}

func startHarness(t *testing.T) (*harness, *fakeDaemons) {
	t.Helper()
	h := newHarness(t, baseConfig)
	h.app.Executable = func() (string, error) { return "/opt/tkt/bin/tkt", nil }
	h.app.Now = func() time.Time { return time.Date(2026, 10, 20, 12, 5, 0, 0, time.UTC) }
	f := &fakeDaemons{}
	f.install(t, h)
	return h, f
}

func TestEnsureDaemonStartsOneWhenNoneAnswers(t *testing.T) {
	h, f := startHarness(t)
	info, err := h.app.ensureDaemon(context.Background())
	if err != nil || info.Port == 0 {
		t.Fatalf("ensure = %+v, %v", info, err)
	}
	again, err := h.app.ensureDaemon(context.Background())
	if err != nil || again.Port != info.Port {
		t.Errorf("second ensure = %+v, %v", again, err)
	}
	if n := f.started.Load(); n != 1 {
		t.Errorf("started %d daemons, want 1", n)
	}
}

func TestEnsureDaemonReplacesAnOlderDaemon(t *testing.T) {
	tests := []struct {
		name, cli, running string
		replaced           bool
	}{
		{"older release", "0.1.23", "0.1.16", true},
		{"older patch with more digits", "0.1.10", "0.1.9", true},
		{"a dev daemon", "0.1.23", "dev", true},
		{"same version", "0.1.23", "0.1.23", false},
		{"newer daemon", "0.1.23", "0.1.24", false},
		{"a dev CLI", "dev", "0.1.16", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h, f := startHarness(t)
			was := Version
			t.Cleanup(func() { Version = was })
			f.version = tt.running
			old, err := h.app.ensureDaemon(context.Background())
			if err != nil || old.Version != tt.running {
				t.Fatalf("first daemon = %+v, %v", old, err)
			}
			Version, f.version = tt.cli, ""
			var signalled []int
			h.app.Signal = func(pid int, sig syscall.Signal) error {
				if sig == syscall.SIGTERM {
					signalled = append(signalled, pid)
					f.stopAll()
				}
				return nil
			}
			got, err := h.app.ensureDaemon(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if replaced := len(signalled) == 1 && f.started.Load() == 2 && got.Version == tt.cli; replaced != tt.replaced {
				t.Errorf("replaced = %v (signalled %v, started %d, now %q)", replaced, signalled, f.started.Load(), got.Version)
			}
			if out := h.out.String() + h.errOut.String(); out != "" {
				t.Errorf("replacing the daemon printed %q into the command's output", out)
			}
			if !tt.replaced && (got.Port != old.Port || len(signalled) != 0) {
				t.Errorf("kept daemon changed: %+v, signalled %v", got, signalled)
			}
		})
	}
}

func TestEnsureDaemonStartsOnlyOneWhenAskedAtOnce(t *testing.T) {
	h, f := startHarness(t)
	var wg sync.WaitGroup
	errs := make(chan error, 5)
	for range 5 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := h.app.ensureDaemon(context.Background())
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Errorf("ensure: %v", err)
		}
	}
	if n := f.started.Load(); n != 1 {
		t.Errorf("started %d daemons, want 1", n)
	}
}

func TestEnsureDaemonIgnoresAStaleRecord(t *testing.T) {
	h, f := startHarness(t)
	dir := h.app.Env.DataDir()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "daemon.json"), []byte(`{"port":1,"pid":999999,"version":"old"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "daemon.lock"), []byte("999999"), 0o600); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-time.Minute)
	_ = os.Chtimes(filepath.Join(dir, "daemon.lock"), old, old)
	if _, err := h.app.ensureDaemon(context.Background()); err != nil {
		t.Fatal(err)
	}
	if n := f.started.Load(); n != 1 {
		t.Errorf("started %d daemons, want 1 despite the stale files", n)
	}
}

func TestDaemonStatusAndStop(t *testing.T) {
	h, f := startHarness(t)
	if code := h.exec("daemon", "status"); code == 0 || !strings.Contains(h.errOut.String()+h.out.String(), "not running") {
		t.Errorf("status before start: exit %d %q %q", code, h.out, h.errOut)
	}
	info, err := h.app.ensureDaemon(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if code := h.exec("daemon", "status"); code != 0 || !strings.Contains(h.out.String(), "running") || !strings.Contains(h.out.String(), "127.0.0.1:") {
		t.Errorf("status: exit %d %q", code, h.out)
	}
	var signalled []int
	h.app.Signal = func(pid int, sig syscall.Signal) error {
		if sig == syscall.SIGTERM {
			signalled = append(signalled, pid)
			f.stopAll()
		}
		return nil
	}
	if code := h.exec("daemon", "stop"); code != 0 || len(signalled) != 1 || signalled[0] != info.PID {
		t.Errorf("stop: exit %d, signalled %v, want [%d]", code, signalled, info.PID)
	}
	if want := fmt.Sprintf("✓ stopped the tkt daemon (pid %d)", info.PID); !strings.Contains(h.out.String(), want) {
		t.Errorf("stop printed %q, want %q", h.out, want)
	}
}

func TestDaemonRunRefusesASecondDaemon(t *testing.T) {
	h, _ := startHarness(t)
	if _, err := h.app.ensureDaemon(context.Background()); err != nil {
		t.Fatal(err)
	}
	if code := h.exec("daemon", "run"); code == 0 || !strings.Contains(h.errOut.String(), "already running") {
		t.Errorf("second run: exit %d %q", code, h.errOut)
	}
}

func TestStartLockHasOneHolderDespiteALeftoverFile(t *testing.T) {
	h := newHarness(t, baseConfig)
	dir := h.app.Env.DataDir()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "daemon.lock")
	for round := range 20 {
		if err := os.WriteFile(path, []byte("stale"), 0o600); err != nil {
			t.Fatal(err)
		}
		old := time.Now().Add(-time.Minute)
		_ = os.Chtimes(path, old, old)
		var holders, most atomic.Int32
		var wg sync.WaitGroup
		for range 4 {
			wg.Add(1)
			go func() {
				defer wg.Done()
				release, err := h.app.lockDaemonStart(context.Background())
				if err != nil {
					t.Error(err)
					return
				}
				n := holders.Add(1)
				for m := most.Load(); n > m && !most.CompareAndSwap(m, n); m = most.Load() {
				}
				time.Sleep(2 * time.Millisecond)
				holders.Add(-1)
				release()
			}()
		}
		wg.Wait()
		if most.Load() > 1 {
			t.Fatalf("round %d: %d starters held the lock at once", round, most.Load())
		}
	}
}

func TestADaemonAnsweringForAnotherPidIsNotOurs(t *testing.T) {
	h, f := startHarness(t)
	info, err := h.app.ensureDaemon(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	stale := info
	stale.PID = info.PID + 1
	b, _ := json.Marshal(stale)
	if err := os.WriteFile(filepath.Join(h.app.Env.DataDir(), "daemon.json"), b, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, ok := h.app.runningDaemon(context.Background()); ok {
		t.Error("a daemon whose pid differs from daemon.json's was taken as ours")
	}
	var signalled []int
	h.app.Signal = func(pid int, _ syscall.Signal) error { signalled = append(signalled, pid); return nil }
	h.exec("daemon", "stop")
	if len(signalled) != 0 {
		t.Errorf("stop signalled %v, an unverified pid", signalled)
	}
	f.stopAll()
}
