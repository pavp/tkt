package daemon

import (
	"net/http"
	"testing"
	"time"

	"github.com/pavp/tkt/internal/board"
)

func TestEachHerdrsAgentsStayApart(t *testing.T) {
	s, err := New(Options{Dir: t.TempDir(), Version: "1.2.3", Sync: (&fakeSync{}).sync, Interval: time.Hour, Seed: &board.Snapshot{}})
	if err != nil {
		t.Fatal(err)
	}
	s.SetAgentsBy(map[string]map[string]string{"desktop": {"DEMO-1": "working"}, "classic": {"DEMO-1": "blocked", "DEMO-2": "idle"}})
	snap, _ := s.Board()
	if snap.AgentsBy["desktop"]["DEMO-1"] != "working" || snap.AgentsBy["classic"]["DEMO-2"] != "idle" || snap.Agents["DEMO-1"] != "blocked" {
		t.Errorf("snapshot agents = %v by Herdr %v, want each Herdr's apart and their most urgent union", snap.Agents, snap.AgentsBy)
	}
	tests := []struct {
		query string
		code  int
		want  map[string]any
	}{
		{"?herdr=desktop", http.StatusOK, map[string]any{"DEMO-1": "working"}},
		{"?herdr=classic", http.StatusOK, map[string]any{"DEMO-1": "blocked", "DEMO-2": "idle"}},
		{"", http.StatusOK, map[string]any{"DEMO-1": "blocked", "DEMO-2": "idle"}},
		{"?herdr=tmux", http.StatusBadRequest, nil},
	}
	for _, tt := range tests {
		code, body := get(t, s.Handler(), http.MethodGet, "/v1/board"+tt.query, s.token)
		if code != tt.code {
			t.Errorf("GET /v1/board%s = %d %v", tt.query, code, body)
			continue
		}
		if tt.want != nil {
			got, _ := body["agents"].(map[string]any)
			if len(got) != len(tt.want) {
				t.Errorf("GET /v1/board%s agents = %v, want %v", tt.query, got, tt.want)
			}
			for k, v := range tt.want {
				if got[k] != v {
					t.Errorf("GET /v1/board%s agents = %v, want %v", tt.query, got, tt.want)
				}
			}
		}
	}
}
