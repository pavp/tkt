package cli

import (
	"context"
	"os"
	"testing"

	"github.com/pavp/tkt/internal/config"
	"github.com/pavp/tkt/internal/tui"
)

func TestBoardSaveConfigWritesAndSwapsTheConfig(t *testing.T) {
	h := newHarness(t, baseConfig)
	c, err := h.app.LoadConfig()
	if err != nil {
		t.Fatal(err)
	}
	b := &boardActions{a: h.app, c: c, ctx: context.Background()}
	edit := b.Config()
	edit.Spaces[0].Repos = append(edit.Spaces[0].Repos, config.Repo{Name: "example-org/api", Path: "~/code/api"})
	edit.RepoRoots = []string{"~/src", "~/code"}
	msg, ok := b.SaveConfig(edit)().(tui.SettingsSavedMsg)
	if !ok || msg.Err != nil {
		t.Fatalf("save = %+v", msg)
	}
	saved, err := config.Load(h.app.configPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(saved.Spaces[0].Repos) != 2 || saved.Spaces[0].Repos[1].Path != "~/code/api" || saved.Spaces[0].Repos[1].Base != "main" || len(saved.RepoRoots) != 2 {
		t.Errorf("saved = %+v", saved)
	}
	if got := b.Config(); len(got.Spaces[0].Repos) != 2 || msg.Config.Spaces[0].Repos[1].Base != "main" {
		t.Errorf("board keeps the old config: %+v", got)
	}
	if fi, _ := os.Stat(h.app.configPath); fi.Mode().Perm() != 0o600 {
		t.Errorf("mode = %o", fi.Mode().Perm())
	}
}

func TestBoardSaveConfigRejectsInvalidAndKeepsTheFile(t *testing.T) {
	h := newHarness(t, baseConfig)
	c, _ := h.app.LoadConfig()
	b := &boardActions{a: h.app, c: c, ctx: context.Background()}
	before, _ := os.ReadFile(h.app.configPath)
	edit := b.Config()
	edit.Spaces[0].Repos = append(edit.Spaces[0].Repos, config.Repo{Name: "not a repo"})
	msg := b.SaveConfig(edit)().(tui.SettingsSavedMsg)
	if msg.Err == nil {
		t.Fatal("an invalid repository was saved")
	}
	if after, _ := os.ReadFile(h.app.configPath); string(after) != string(before) {
		t.Errorf("config changed:\n%s", after)
	}
	if got := b.Config(); len(got.Spaces[0].Repos) != 1 {
		t.Errorf("board config changed: %+v", got)
	}
}
