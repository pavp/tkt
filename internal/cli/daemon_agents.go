package cli

import (
	"context"
	"slices"
	"time"

	"github.com/pavp/tkt/internal/board"
	"github.com/pavp/tkt/internal/herdr"
)

// agentBoard is the part of the daemon the agent watcher feeds.
type agentBoard interface {
	Board() (board.Snapshot, bool)
	SetAgents(agents map[string]string)
}

// paneEvents are the Herdr events that can add or remove an agent; status changes are followed per pane.
var paneEvents = []string{"pane.created", "pane.closed", "pane.exited", "pane.agent_detected", "workspace.closed"}

const (
	// agentSettle lets a burst of Herdr events pass before the agents are read again.
	agentSettle = 300 * time.Millisecond
	// watchRetry is the first wait before following Herdr again after it dropped or was not running.
	watchRetry    = 2 * time.Second
	watchRetryMax = 30 * time.Second
)

// watchAgents keeps the daemon's agent states and the Herdr sidebar current as Herdr reports changes,
// following Herdr again whenever it drops the subscription or starts later, until ctx ends.
func (a *App) watchAgents(ctx context.Context, d agentBoard) {
	wait := watchRetry
	for ctx.Err() == nil {
		began := time.Now()
		if err := a.watchAgentsOnce(ctx, d); err == nil {
			wait = watchRetry
			continue
		}
		wait = nextWatchWait(wait, time.Since(began))
		if a.Sleep(ctx, wait) != nil {
			return
		}
	}
}

// nextWatchWait doubles the wait after a failure, up to watchRetryMax, but starts over after a
// subscription that lived a while, since that failure is new rather than repeated.
func nextWatchWait(wait, lived time.Duration) time.Duration {
	if lived >= time.Minute {
		return watchRetry
	}
	return min(wait*2, watchRetryMax)
}

// watchAgentsOnce follows Herdr until it fails, or returns nil once the panes running agents change, so
// the caller subscribes again for the new panes.
func (a *App) watchAgentsOnce(ctx context.Context, d agentBoard) error {
	panes, err := a.syncAgents(ctx, d)
	if err != nil {
		return err
	}
	subs := make([]herdr.Subscription, 0, len(paneEvents)+len(panes))
	for _, e := range paneEvents {
		subs = append(subs, herdr.Subscription{Type: e})
	}
	for _, p := range panes {
		subs = append(subs, herdr.Subscription{Type: "pane.agent_status_changed", PaneID: p})
	}
	wctx, cancel := context.WithCancel(ctx)
	defer cancel()
	changed := make(chan struct{}, 1)
	done := make(chan error, 1)
	go func() {
		done <- a.NewHerdr().Watch(wctx, subs, func(herdr.Event) {
			select {
			case changed <- struct{}{}:
			default:
			}
		})
	}()
	for {
		select {
		case err := <-done:
			return err
		case <-changed:
			if err := a.Sleep(ctx, agentSettle); err != nil {
				return err
			}
			now, err := a.syncAgents(ctx, d)
			if err != nil {
				return err
			}
			if !slices.Equal(now, panes) {
				return nil
			}
		}
	}
}

// syncAgents reads the agent state of each ticket on the daemon's board, rewrites the Herdr sidebar,
// and returns the panes running agents, sorted.
func (a *App) syncAgents(ctx context.Context, d agentBoard) ([]string, error) {
	c, err := a.LoadConfig()
	if err != nil {
		return nil, err
	}
	// Without a board every ticket workspace would read "not in your sprint", so the sidebar waits for
	// the first sync.
	snap, have := d.Board()
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	_, agents, err := a.updateSidebar(ctx, c, snap, have)
	if err != nil {
		return nil, err
	}
	d.SetAgents(agents)
	list, err := a.NewHerdr().Agents(ctx)
	if err != nil {
		return nil, err
	}
	var panes []string
	for _, ag := range list {
		if ag.Agent != "" {
			panes = append(panes, ag.PaneID)
		}
	}
	slices.Sort(panes)
	return panes, nil
}
