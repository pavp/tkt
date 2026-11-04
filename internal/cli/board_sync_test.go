package cli

import (
	"context"
	"slices"
	"sync"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/pavp/tkt/internal/config"
	"github.com/pavp/tkt/internal/tui"
)

func TestABoardSaveKeepsWhatChangedElsewhere(t *testing.T) {
	h := newHarness(t, baseConfig)
	b := &boardActions{a: h.app, base: tui.BoardState{}}
	if err := h.app.saveBoardState(tui.BoardState{HiddenBoards: []string{"Core"}}); err != nil {
		t.Fatal(err)
	}
	msg := b.SaveState(tui.BoardState{Team: true})()
	got := h.app.loadBoardState()
	if !got.Team || !slices.Equal(got.HiddenBoards, []string{"Core"}) {
		t.Errorf("saved %+v, want the own Team and the board hidden elsewhere", got)
	}
	if m, ok := msg.(tui.BoardStateMsg); !ok || !m.State.Team || !slices.Equal(m.State.HiddenBoards, []string{"Core"}) {
		t.Errorf("SaveState = %#v, want the merged filters for the board to show", msg)
	}
	if again := b.SaveState(got)(); again != nil {
		t.Errorf("a save with nothing changed elsewhere = %#v, want nothing to show", again)
	}
}

func TestSettingsSaveRefusesAConfigChangedElsewhere(t *testing.T) {
	h := newHarness(t, baseConfig)
	c, _ := h.app.LoadConfig()
	b := &boardActions{a: h.app, c: c, ctx: context.Background()}
	base := b.Config()
	other := base.Clone()
	other.RepoRoots = []string{"~/elsewhere"}
	if _, err := h.app.saveConfig(other); err != nil {
		t.Fatal(err)
	}
	edit := base.Clone()
	edit.Jira.LabelField = "Product"
	msg, ok := b.SaveConfig(base, edit)().(tui.SettingsChangedMsg)
	if !ok || !msg.Conflict || !slices.Equal(msg.Config.RepoRoots, []string{"~/elsewhere"}) {
		t.Fatalf("SaveConfig = %#v, want a refusal with the config as saved elsewhere", msg)
	}
	saved, err := config.Load(h.app.configPath)
	if err != nil || saved.Jira.LabelField == "Product" || !slices.Equal(saved.RepoRoots, []string{"~/elsewhere"}) {
		t.Errorf("config.toml = %+v (%v), want it as saved elsewhere", saved, err)
	}
}

func TestSettingsSaveTwiceInARow(t *testing.T) {
	h := newHarness(t, baseConfig)
	c, _ := h.app.LoadConfig()
	b := &boardActions{a: h.app, c: c, ctx: context.Background()}
	base := b.Config()
	edit := base.Clone()
	edit.Jira.LabelField = "Product"
	first, ok := b.SaveConfig(base, edit)().(tui.SettingsSavedMsg)
	if !ok || first.Err != nil {
		t.Fatalf("first save = %#v", first)
	}
	next := first.Config.Clone()
	next.Jira.PointsField = "Story Points"
	if second, ok := b.SaveConfig(first.Config, next)().(tui.SettingsSavedMsg); !ok || second.Err != nil {
		t.Errorf("a second save from what the first saved = %#v, want it saved", second)
	}
}

func TestTheBoardFollowsFiltersAndSettingsSavedElsewhere(t *testing.T) {
	h, f := startHarness(t)
	f.board, f.watch = daemonBoard(), true
	c, err := h.app.LoadConfig()
	if err != nil {
		t.Fatal(err)
	}
	b := &boardActions{a: h.app, c: c, ctx: context.Background()}
	_ = b.Config()
	var mu sync.Mutex
	var got []tea.Msg
	send := func(m tea.Msg) {
		mu.Lock()
		defer mu.Unlock()
		got = append(got, m)
	}
	seen := func(match func(tea.Msg) bool) bool {
		mu.Lock()
		defer mu.Unlock()
		return slices.ContainsFunc(got, match)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go h.app.followDaemon(ctx, b, send)
	eventually(t, "the daemon's first board", func() bool {
		return seen(func(m tea.Msg) bool { _, ok := m.(tui.SnapshotMsg); return ok })
	})
	if err := h.app.saveBoardState(tui.BoardState{HiddenBoards: []string{"Core"}}); err != nil {
		t.Fatal(err)
	}
	eventually(t, "the filters saved elsewhere", func() bool {
		return seen(func(m tea.Msg) bool {
			s, ok := m.(tui.BoardStateMsg)
			return ok && slices.Equal(s.State.HiddenBoards, []string{"Core"})
		})
	})
	other := b.Config()
	other.RepoRoots = []string{"~/elsewhere"}
	if _, err := h.app.saveConfig(other); err != nil {
		t.Fatal(err)
	}
	eventually(t, "the settings saved elsewhere", func() bool {
		return seen(func(m tea.Msg) bool {
			s, ok := m.(tui.SettingsChangedMsg)
			return ok && !s.Conflict && slices.Equal(s.Config.RepoRoots, []string{"~/elsewhere"})
		})
	})
	if got := b.cfg(); got.RepoRoots[0] != h.home+"/elsewhere" {
		t.Errorf("the board kept using %v, want the config saved elsewhere", got.RepoRoots)
	}
}

func TestSettingsSaveAConfigWithNoSpaceYet(t *testing.T) {
	h := newHarness(t, "repo_roots = [\"~/src\"]\n\n[jira]\nsite = \"example.atlassian.net\"\nemail = \"dev@example.com\"\n")
	c, err := h.app.LoadConfig()
	if err != nil {
		t.Fatal(err)
	}
	b := &boardActions{a: h.app, c: c, ctx: context.Background()}
	base := b.Config()
	edit := base.Clone()
	edit.RepoRoots = append(edit.RepoRoots, "/opt")
	if msg, ok := b.SaveConfig(base.Clone(), edit)().(tui.SettingsSavedMsg); !ok || msg.Err != nil {
		t.Errorf("SaveConfig = %#v, want it saved: nothing changed elsewhere (the TUI passes a copy)", msg)
	}
	if _, changed := b.configChanged(); changed {
		t.Error("the board's own save reads as a change made elsewhere")
	}
}
