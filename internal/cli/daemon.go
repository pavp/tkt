package cli

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/pavp/tkt/internal/board"
	"github.com/pavp/tkt/internal/daemon"
	"github.com/pavp/tkt/internal/ticket"
)

// syncEvery is how often the daemon refreshes the board from Jira and GitHub.
const syncEvery = time.Minute

func newDaemonCmd(a *App) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "daemon",
		Short: "Background sync that keeps the board and the Herdr sidebar fresh",
		Long: "The daemon syncs Jira and GitHub once a minute in one place and serves the board on 127.0.0.1,\n" +
			"so the board, tkt list and the Herdr plugin read fresh data without each asking Jira and GitHub.\n" +
			"They start it in the background when it is not running; start, status and stop manage it by hand.",
	}
	cmd.AddCommand(&cobra.Command{
		Use:   "start",
		Short: "Start the daemon in the background unless one runs",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			info, err := a.ensureDaemon(cmd.Context())
			if err != nil {
				return err
			}
			a.printf("running · pid %d · 127.0.0.1:%d\n", info.PID, info.Port)
			return nil
		},
	}, &cobra.Command{
		Use:   "status",
		Short: "Say whether the daemon runs, and where",
		Args:  cobra.NoArgs,
		RunE:  func(cmd *cobra.Command, _ []string) error { return a.daemonStatus(cmd.Context()) },
	}, &cobra.Command{
		Use:   "stop",
		Short: "Stop the running daemon",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			pid, err := a.stopDaemon(cmd.Context())
			if err == nil {
				a.printf("✓ stopped the tkt daemon (pid %d)\n", pid)
			}
			return err
		},
	}, &cobra.Command{
		Use:   "run",
		Short: "Run the daemon in the foreground until interrupted",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
			defer stop()
			return a.runDaemon(ctx)
		},
	})
	return cmd
}

func (a *App) runDaemon(ctx context.Context) error {
	if info, ok := a.runningDaemon(ctx); ok {
		return fmt.Errorf("a tkt daemon is already running (pid %d)", info.PID)
	}
	var seed *board.Snapshot
	if snap, ok, err := a.cache().Load(); err == nil && ok {
		seed = &snap
	}
	s, err := daemon.New(daemon.Options{Dir: a.Env.DataDir(), Version: Version, Sync: a.daemonSync, Interval: syncEvery, Seed: seed, Detail: a.daemonDetail})
	if err != nil {
		return err
	}
	ln, err := daemon.Listen(0)
	if err != nil {
		return err
	}
	a.printf("tkt daemon listening on %s\n", ln.Addr())
	go a.watchAgents(ctx, s)
	return s.Serve(ctx, ln)
}

// daemonDetail serves GET /v1/tickets/{key} with the configuration read afresh, like each sync.
func (a *App) daemonDetail(ctx context.Context, key ticket.Key, known *ticket.Ticket) (any, error) {
	c, err := a.LoadConfig()
	if err != nil {
		return nil, err
	}
	return a.loadDetail(ctx, c, key, known)
}

// daemonSync reads the configuration afresh, so changes made in the board's settings apply without a
// restart, fails when the fetch does, so the daemon keeps its last good board, and rewrites the Herdr
// sidebar with the new board when Herdr runs.
func (a *App) daemonSync(ctx context.Context) (board.Snapshot, error) {
	c, err := a.LoadConfig()
	if err != nil {
		return board.Snapshot{}, err
	}
	snap, stale, err := a.loadSnapshot(ctx, c, false)
	if err != nil {
		return board.Snapshot{}, err
	}
	if stale != nil {
		return board.Snapshot{}, stale
	}
	if a.NewHerdr != nil {
		hctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		if _, agents, err := a.updateSidebar(hctx, c, snap, true); err == nil {
			snap.Agents = agents
		}
	}
	return snap, nil
}
