package cli

import (
	"context"
	"sync"
	"time"

	"github.com/pavp/tkt/internal/board"
)

// herdrRecheck is how often the daemon looks again for a Herdr that started or quit.
const herdrRecheck = 30 * time.Second

// daemonHerdr is one Herdr the daemon follows.
type daemonHerdr struct {
	flavor herdrFlavor
	api    HerdrAPI
}

// sidebar reports whether this Herdr shows the workspace sidebar tkt writes; Herdr desktop has none.
func (h daemonHerdr) sidebar() bool { return h.flavor == herdrClassic }

// herdrSet is every Herdr the daemon reaches. The daemon runs detached, without a terminal that would say which
// Herdr to use, so it follows all of them, and probes again only on recheck: Herdr desktop's probe spawns hc.
type herdrSet struct {
	probe func(context.Context) (classic, desktop bool)
	open  func(herdrFlavor) HerdrAPI

	mu      sync.Mutex
	probed  bool
	clients []daemonHerdr
}

func (a *App) daemonHerdrs() *herdrSet {
	a.herdrsOnce.Do(func() {
		a.herdrs = &herdrSet{probe: a.HerdrRunning, open: a.herdrOf}
		if a.HerdrRunning == nil {
			a.herdrs.probe = func(context.Context) (bool, bool) { return a.NewHerdr != nil, false }
			a.herdrs.open = func(herdrFlavor) HerdrAPI { return a.NewHerdr() }
		}
	})
	return a.herdrs
}

// current returns the Herdrs found by the last probe, probing the first time.
func (s *herdrSet) current(ctx context.Context) []daemonHerdr {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.probed {
		s.probeLocked(ctx)
	}
	return s.clients
}

// recheck probes again, keeping the client of a Herdr that still runs.
func (s *herdrSet) recheck(ctx context.Context) []daemonHerdr {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.probeLocked(ctx)
	return s.clients
}

func (s *herdrSet) probeLocked(ctx context.Context) {
	classic, desktop := s.probe(ctx)
	var next []daemonHerdr
	for _, f := range []struct {
		flavor herdrFlavor
		up     bool
	}{{herdrClassic, classic}, {herdrDesktop, desktop}} {
		if !f.up {
			continue
		}
		h := daemonHerdr{flavor: f.flavor}
		for _, old := range s.clients {
			if old.flavor == f.flavor {
				h.api = old.api
			}
		}
		if h.api == nil {
			h.api = s.open(f.flavor)
		}
		next = append(next, h)
	}
	s.probed, s.clients = true, next
}

// mostUrgentPerTicket joins agent states read from several Herdrs; a ticket with agents on two keeps the most urgent.
func mostUrgentPerTicket(parts ...map[string]string) map[string]string {
	merged := map[string]string{}
	for _, part := range parts {
		for key, state := range part {
			merged[key] = board.MostUrgentAgent([]string{merged[key], state})
		}
	}
	return merged
}

// agentMerge keeps each followed Herdr's agent states apart and gives the daemon their union. States are kept
// by ticket, so pane ids, which two Herdrs may share, never meet.
type agentMerge struct {
	d     agentBoard
	mu    sync.Mutex
	parts map[herdrFlavor]map[string]string
}

// set replaces one Herdr's agent states; nil forgets that Herdr.
func (m *agentMerge) set(flavor herdrFlavor, agents map[string]string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if agents == nil {
		delete(m.parts, flavor)
	} else {
		m.parts[flavor] = agents
	}
	if d, ok := m.d.(interface {
		SetAgentsBy(map[string]map[string]string)
	}); ok {
		by := map[string]map[string]string{}
		for f, p := range m.parts {
			by[f.wire()] = p
		}
		d.SetAgentsBy(by)
		return
	}
	parts := make([]map[string]string, 0, len(m.parts))
	for _, p := range m.parts {
		parts = append(parts, p)
	}
	m.d.SetAgents(mostUrgentPerTicket(parts...))
}

// herdrShare is the daemon board as one Herdr's watcher sees it.
type herdrShare struct {
	m      *agentMerge
	flavor herdrFlavor
}

func (s herdrShare) Board() (board.Snapshot, bool)      { return s.m.d.Board() }
func (s herdrShare) SetAgents(agents map[string]string) { s.m.set(s.flavor, agents) }

// watchAgents keeps the daemon's agent states and the Herdr sidebar current from every Herdr the daemon reaches,
// looking again every herdrRecheck for a Herdr that started or quit, until ctx ends.
func (a *App) watchAgents(ctx context.Context, d agentBoard) {
	t := time.NewTicker(herdrRecheck)
	defer t.Stop()
	a.followHerdrs(ctx, d, t.C)
}

// followHerdrs runs one watcher per Herdr found, starting and stopping watchers at each recheck, and returns only
// once every watcher has.
func (a *App) followHerdrs(ctx context.Context, d agentBoard, recheck <-chan time.Time) {
	set := a.daemonHerdrs()
	m := &agentMerge{d: d, parts: map[herdrFlavor]map[string]string{}}
	type watcher struct {
		stop context.CancelFunc
		done chan struct{}
	}
	watchers := map[herdrFlavor]watcher{}
	defer func() {
		for _, w := range watchers {
			w.stop()
		}
		for _, w := range watchers {
			<-w.done
		}
	}()
	clients := set.current(ctx)
	for {
		live := map[herdrFlavor]bool{}
		for _, h := range clients {
			live[h.flavor] = true
			if _, ok := watchers[h.flavor]; ok {
				continue
			}
			wctx, stop := context.WithCancel(ctx)
			w := watcher{stop, make(chan struct{})}
			watchers[h.flavor] = w
			go func() {
				defer close(w.done)
				a.watchHerdrAgents(wctx, h, herdrShare{m, h.flavor})
			}()
		}
		for flavor, w := range watchers {
			if live[flavor] {
				continue
			}
			w.stop()
			// The watcher must be gone before its states are forgotten, or a last write would bring them back.
			<-w.done
			delete(watchers, flavor)
			m.set(flavor, nil)
		}
		select {
		case <-ctx.Done():
			return
		case <-recheck:
		}
		clients = set.recheck(ctx)
	}
}
