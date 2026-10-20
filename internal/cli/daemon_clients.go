package cli

import (
	"context"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/pavp/tkt/internal/board"
	"github.com/pavp/tkt/internal/daemon"
	"github.com/pavp/tkt/internal/tui"
)

// followRetry is the wait before the board looks for the daemon again after losing it.
const followRetry = 5 * time.Second

// daemonClient returns a client for the running daemon, starting one when none answers.
func (a *App) daemonClient(ctx context.Context) (*daemon.Client, error) {
	if a.StartDetached == nil || a.Executable == nil {
		return nil, errDaemonNotRunning
	}
	info, err := a.ensureDaemon(ctx)
	if err != nil {
		return nil, err
	}
	return daemon.Dial(a.Env.DataDir(), info)
}

// daemonSnapshot reads the board from the daemon; false sends the caller to Jira and GitHub itself, as
// when the daemon cannot start or has no board yet.
func (a *App) daemonSnapshot(ctx context.Context) (board.Snapshot, bool) {
	c, err := a.daemonClient(ctx)
	if err != nil {
		return board.Snapshot{}, false
	}
	snap, err := c.Tickets(ctx)
	return snap, err == nil
}

// followDaemon connects the board to the daemon and sends it the daemon's board whenever it changes,
// looking for the daemon again when it goes away, until ctx ends.
func (a *App) followDaemon(ctx context.Context, b *boardActions, send func(tea.Msg)) {
	for ctx.Err() == nil {
		c, err := a.daemonClient(ctx)
		if err == nil {
			b.setLive(c)
			_ = c.Events(ctx, func(e daemon.Event) {
				switch e.Type {
				case "snapshot", "agent.updated", "sync.finished":
					if snap, err := c.Tickets(ctx); err == nil {
						send(tui.SnapshotMsg{Snap: snap, Quiet: true})
					}
				}
			})
			b.setLive(nil)
		}
		select {
		case <-ctx.Done():
		case <-time.After(followRetry):
		}
	}
}
