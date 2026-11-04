package cli

import (
	"context"
	"reflect"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/pavp/tkt/internal/board"
	"github.com/pavp/tkt/internal/daemon"
	"github.com/pavp/tkt/internal/tui"
)

func TestTheBoardShowsOnlyItsOwnHerdrsAgents(t *testing.T) {
	tests := []struct {
		name string
		env  map[string]string
		want map[string]string
	}{
		{"in a Herdr desktop terminal", map[string]string{"HC_TERMINAL_ID": "t5"}, map[string]string{"DEMO-1": "working"}},
		{"in a Herdr 0.9.5 terminal", map[string]string{"HERDR_ENV": "1"}, map[string]string{"DEMO-9": "blocked"}},
		{"outside both", map[string]string{}, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h, f := startHarness(t)
			snap := daemonBoard()
			snap.AgentsBy = map[string]map[string]string{"desktop": {"DEMO-1": "working"}, "classic": {"DEMO-9": "blocked"}}
			f.board = snap
			for k, v := range tt.env {
				h.env[k] = v
			}
			c, err := h.app.LoadConfig()
			if err != nil {
				t.Fatal(err)
			}
			got := make(chan tui.SnapshotMsg, 8)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			go h.app.followDaemon(ctx, &boardActions{a: h.app, c: c, ctx: ctx}, func(m tea.Msg) {
				if s, ok := m.(tui.SnapshotMsg); ok {
					got <- s
				}
			})
			select {
			case m := <-got:
				if !reflect.DeepEqual(m.Snap.Agents, tt.want) {
					t.Errorf("the board's agents = %v, want %v", m.Snap.Agents, tt.want)
				}
			case <-time.After(3 * time.Second):
				t.Fatal("no board from the daemon")
			}
			if cached := h.app.boardAgentsHere(board.Snapshot{Agents: map[string]string{"DEMO-1": "working", "DEMO-9": "blocked"}, AgentsBy: snap.AgentsBy}); !reflect.DeepEqual(cached.Agents, tt.want) {
				t.Errorf("the cached board's agents = %v, want %v", cached.Agents, tt.want)
			}
		})
	}
}

func TestTheDaemonKeepsEachHerdrsAgentsApart(t *testing.T) {
	s, err := daemon.New(daemon.Options{Dir: t.TempDir(), Version: "1.2.3", Interval: time.Hour, Seed: &board.Snapshot{},
		Sync: func(context.Context, func(board.Snapshot)) (board.Snapshot, error) { return board.Snapshot{}, nil }})
	if err != nil {
		t.Fatal(err)
	}
	m := &agentMerge{d: s, parts: map[herdrFlavor]map[string]string{}}
	herdrShare{m, herdrDesktop}.SetAgents(map[string]string{"DEMO-1": "working"})
	herdrShare{m, herdrClassic}.SetAgents(map[string]string{"DEMO-2": "idle"})
	snap, _ := s.Board()
	want := map[string]map[string]string{"desktop": {"DEMO-1": "working"}, "classic": {"DEMO-2": "idle"}}
	if !reflect.DeepEqual(snap.AgentsBy, want) || len(snap.Agents) != 2 {
		t.Errorf("agents by Herdr = %v, union %v; want %v", snap.AgentsBy, snap.Agents, want)
	}
}

func TestListStillShowsEveryAgent(t *testing.T) {
	h, f := startHarness(t)
	snap := daemonBoard()
	snap.Agents = map[string]string{"DEMO-9": "working"}
	snap.AgentsBy = map[string]map[string]string{"classic": {"DEMO-9": "working"}}
	f.board = snap
	got, ok := h.app.daemonSnapshot(context.Background())
	if !ok || got.Agents["DEMO-9"] != "working" {
		t.Errorf("tkt list's snapshot agents = %v, want every agent as before", got.Agents)
	}
}

func TestAPluginPaneOfHerdrIsHerdrsTerminal(t *testing.T) {
	h, _ := startHarness(t)
	h.env["HERDR_PLUGIN_ID"] = "tkt"
	if got := h.app.terminalHerdr(); got != "classic" {
		t.Errorf("terminalHerdr = %q, want classic", got)
	}
}
