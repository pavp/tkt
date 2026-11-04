package cli

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/pavp/tkt/internal/board"
	"github.com/pavp/tkt/internal/daemon"
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
			got, ok := h.app.daemonSnapshot(context.Background())
			if !ok || !reflect.DeepEqual(got.Agents, tt.want) {
				t.Errorf("agents = %v (ok %v), want %v", got.Agents, ok, tt.want)
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
