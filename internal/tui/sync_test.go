package tui

import (
	"slices"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestTheBoardTakesFiltersSavedElsewhere(t *testing.T) {
	m := New(snapshot(), true, &recorder{}, ViewColumns)
	next, _ := m.Update(tea.WindowSizeMsg{Width: 140, Height: 40})
	next, _ = next.Update(BoardStateMsg{State: BoardState{HiddenBoards: []string{"Core"}, Team: true, BacklogMerged: true}})
	got := next.(Model).state()
	if !got.Team || !got.BacklogMerged || !slices.Equal(got.HiddenBoards, []string{"Core"}) {
		t.Errorf("state = %+v, want the filters saved elsewhere", got)
	}
}

func TestSettingsTakeAConfigSavedElsewhere(t *testing.T) {
	tests := []struct {
		name     string
		conflict bool
		want     string
	}{
		{"changed while open", false, "changed elsewhere"},
		{"a save refused because it changed", true, "not saved"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m, r := settingsBoard(t)
			c := r.cfg.Clone()
			c.RepoRoots = []string{"~/elsewhere"}
			next, _ := m.Update(SettingsChangedMsg{Config: c, Conflict: tt.conflict})
			s := next.(Model).settings
			if s == nil || !slices.Equal(s.cfg.RepoRoots, []string{"~/elsewhere"}) || s.saving {
				t.Fatalf("settings = %+v, want the config saved elsewhere", s)
			}
			if !strings.Contains(s.err+s.msg, tt.want) {
				t.Errorf("message %q / %q, want %q", s.err, s.msg, tt.want)
			}
		})
	}
}
