package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/pavp/tkt/internal/daemon"
)

const (
	// daemonStartWait bounds how long a client waits for a daemon it started to answer.
	daemonStartWait = 5 * time.Second
)

var errDaemonNotRunning = errors.New("the tkt daemon is not running")

// runningDaemon reports the daemon daemon.json names when it answers; a record left by a crash does not.
func (a *App) runningDaemon(ctx context.Context) (daemon.Info, bool) {
	info, err := daemon.ReadInfo(a.Env.DataDir())
	if err != nil || info.Port == 0 {
		return daemon.Info{}, false
	}
	pid, ok := daemon.Healthy(ctx, info.Port)
	return info, ok && pid == info.PID
}

// ensureDaemon returns the running daemon, starting one in the background when none answers, and
// replacing one older than this tkt, which an upgrade leaves serving the old code. A lock file keeps
// concurrent starters, such as the Herdr hook and a board opening together, to one daemon.
func (a *App) ensureDaemon(ctx context.Context) (daemon.Info, error) {
	if info, ok := a.runningDaemon(ctx); ok && !olderThanUs(info.Version) {
		return info, nil
	}
	release, err := a.lockDaemonStart(ctx)
	if err != nil {
		return daemon.Info{}, err
	}
	defer release()
	if info, ok := a.runningDaemon(ctx); ok {
		if !olderThanUs(info.Version) {
			return info, nil
		}
		// A daemon that will not stop still serves a board, so keep it rather than fail.
		if _, err := a.stopDaemon(ctx); err != nil {
			return info, nil
		}
	}
	exe, err := a.Executable()
	if err != nil {
		return daemon.Info{}, err
	}
	logPath := filepath.Join(a.Env.DataDir(), "daemon.log")
	if _, err := a.StartDetached(exe, []string{"daemon", "run"}, logPath); err != nil {
		return daemon.Info{}, fmt.Errorf("start the tkt daemon: %w", err)
	}
	deadline := time.Now().Add(daemonStartWait)
	for time.Now().Before(deadline) {
		if info, ok := a.runningDaemon(ctx); ok {
			return info, nil
		}
		select {
		case <-ctx.Done():
			return daemon.Info{}, ctx.Err()
		case <-time.After(50 * time.Millisecond):
		}
	}
	return daemon.Info{}, fmt.Errorf("the tkt daemon did not start; see %s", logPath)
}

// olderThanUs reports whether a daemon of version should give way to this tkt: a release older than
// ours, or a development build. Only a release replaces, and never a newer daemon, so a board still
// running an older tkt cannot fight a newer one over the daemon.
func olderThanUs(version string) bool {
	ours, ok := parseVersion(Version)
	if !ok {
		return false
	}
	theirs, ok := parseVersion(version)
	return !ok || slices.Compare(theirs, ours) < 0
}

// parseVersion reads a release version such as 0.1.23 (an optional leading v); a dev build is not one.
func parseVersion(v string) ([]int, bool) {
	parts := strings.Split(strings.TrimPrefix(v, "v"), ".")
	out := make([]int, len(parts))
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil {
			return nil, false
		}
		out[i] = n
	}
	return out, len(out) > 0
}

// lockDaemonStart takes the start lock, waiting while another starter holds it. It is an OS lock on
// daemon.lock, which the OS drops if the holder dies, so a crashed starter never leaves it stuck.
func (a *App) lockDaemonStart(ctx context.Context) (func(), error) {
	dir := a.Env.DataDir()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(filepath.Join(dir, "daemon.lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	deadline := time.Now().Add(daemonStartWait + time.Second)
	for {
		err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			return func() {
				_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
				f.Close()
			}, nil
		}
		if !errors.Is(err, syscall.EWOULDBLOCK) {
			f.Close()
			return nil, err
		}
		if time.Now().After(deadline) {
			f.Close()
			return nil, errors.New("another tkt is still starting the daemon")
		}
		select {
		case <-ctx.Done():
			f.Close()
			return nil, ctx.Err()
		case <-time.After(50 * time.Millisecond):
		}
	}
}

// startDetached starts name in its own session, so it outlives the terminal and the pane that started it.
func startDetached(name string, args []string, logPath string) (int, error) {
	log, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return 0, err
	}
	defer log.Close()
	cmd := exec.Command(name, args...)
	cmd.Stdout, cmd.Stderr = log, log
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return 0, err
	}
	// Waiting in the background reaps the daemon when it exits before this process does.
	go func() { _ = cmd.Wait() }()
	return cmd.Process.Pid, nil
}

func (a *App) daemonStatus(ctx context.Context) error {
	info, ok := a.runningDaemon(ctx)
	if !ok {
		return errDaemonNotRunning
	}
	a.printf("running · pid %d · 127.0.0.1:%d · version %s\n", info.PID, info.Port, info.Version)
	return nil
}

// stopDaemon stops the running daemon and returns its pid; it prints nothing, since ensureDaemon also
// calls it in the middle of other commands' output.
func (a *App) stopDaemon(ctx context.Context) (int, error) {
	info, ok := a.runningDaemon(ctx)
	if !ok {
		return 0, errDaemonNotRunning
	}
	if err := a.Signal(info.PID, syscall.SIGTERM); err != nil {
		return 0, fmt.Errorf("stop the tkt daemon (pid %d): %w", info.PID, err)
	}
	deadline := time.Now().Add(daemonStartWait)
	for time.Now().Before(deadline) {
		if _, ok := daemon.Healthy(ctx, info.Port); !ok {
			return info.PID, nil
		}
		time.Sleep(50 * time.Millisecond)
	}
	return 0, fmt.Errorf("the tkt daemon (pid %d) did not stop", info.PID)
}
