package cli

import (
	"context"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/pavp/tkt/internal/board"
	"github.com/pavp/tkt/internal/daemon"
)

// syncEvery is how often the daemon refreshes the board from Jira and GitHub.
const syncEvery = time.Minute

func newDaemonCmd(a *App) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "daemon",
		Short: "Background sync that keeps the board and the Herdr sidebar fresh",
		Long: "The daemon syncs Jira and GitHub once a minute in one place and serves the board on 127.0.0.1,\n" +
			"so the board, tkt list and the Herdr plugin read fresh data without each asking Jira and GitHub.",
	}
	cmd.AddCommand(&cobra.Command{
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
	var seed *board.Snapshot
	if snap, ok, err := a.cache().Load(); err == nil && ok {
		seed = &snap
	}
	s, err := daemon.New(daemon.Options{Dir: a.Env.DataDir(), Version: Version, Sync: a.daemonSync, Interval: syncEvery, Seed: seed})
	if err != nil {
		return err
	}
	ln, err := daemon.Listen(0)
	if err != nil {
		return err
	}
	a.printf("tkt daemon listening on %s\n", ln.Addr())
	return s.Serve(ctx, ln)
}

// daemonSync reads the configuration afresh, so changes made in the board's settings apply without a
// restart, and fails when the fetch does, so the daemon keeps its last good board.
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
	return snap, nil
}
