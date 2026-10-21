package cli

import (
	"context"
	"encoding/json"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/pavp/tkt/internal/board"
	"github.com/pavp/tkt/internal/config"
	"github.com/pavp/tkt/internal/daemon"
	"github.com/pavp/tkt/internal/ticket"
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

// daemonTicket returns the running daemon's copy of key, for its pull requests; it never starts a daemon,
// and is nil without one.
func (a *App) daemonTicket(ctx context.Context, key ticket.Key) *ticket.Ticket {
	info, ok := a.runningDaemon(ctx)
	if !ok {
		return nil
	}
	c, err := daemon.Dial(a.Env.DataDir(), info)
	if err != nil {
		return nil
	}
	snap, err := c.Tickets(ctx)
	if err != nil {
		return nil
	}
	if t, ok := snap.Find(key); ok {
		return &t
	}
	return nil
}

// ticketDetail asks a running daemon for the detail, since it holds the Jira token already and reading
// the token can take seconds (powershell.exe on WSL); without a daemon that answers, it loads it here.
func (a *App) ticketDetail(ctx context.Context, c *config.Config, key ticket.Key) (detail, error) {
	if info, ok := a.runningDaemon(ctx); ok {
		if dc, err := daemon.Dial(a.Env.DataDir(), info); err == nil {
			var d detail
			if err := dc.Ticket(ctx, string(key), &d); err == nil {
				return d, nil
			}
		}
	}
	return a.loadDetail(ctx, c, key, a.daemonTicket(ctx, key))
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
				case "sync.started":
					send(tui.SyncingMsg{})
				case "sync.failed":
					var failed struct {
						Message string `json:"message"`
					}
					_ = json.Unmarshal(e.Data, &failed)
					send(tui.SyncFailedMsg{Message: failed.Message})
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
