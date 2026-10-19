package cli

import (
	"context"
	"os"
	"strings"
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
	if len(saved.Spaces[0].Repos) != 2 || saved.Spaces[0].Repos[1].Path != "~/code/api" || saved.Spaces[0].Repos[1].Base != "" || len(saved.RepoRoots) != 2 {
		t.Errorf("saved = %+v", saved)
	}
	if got := b.Config(); len(got.Spaces[0].Repos) != 2 || len(msg.Config.Spaces[0].Repos) != 2 {
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

func TestBoardSaveConfigKeepsTildePaths(t *testing.T) {
	h := newHarness(t, baseConfig+"path = \"~/src/web\"\n")
	c, err := h.app.LoadConfig()
	if err != nil {
		t.Fatal(err)
	}
	b := &boardActions{a: h.app, c: c, ctx: context.Background()}
	edit := b.Config()
	if edit.RepoRoots[0] != "~/src" || edit.Spaces[0].Repos[0].Path != "~/src/web" {
		t.Fatalf("the editor should see the paths as written: %+v", edit)
	}
	edit.RepoRoots = []string{"~/code"}
	if msg := b.SaveConfig(edit)().(tui.SettingsSavedMsg); msg.Err != nil || msg.Config.RepoRoots[0] != "~/code" {
		t.Fatalf("save = %+v", msg)
	}
	data, _ := os.ReadFile(h.app.configPath)
	if strings.Contains(string(data), h.home) || !strings.Contains(string(data), "~/code") {
		t.Errorf("save expanded ~ paths:\n%s", data)
	}
	if got := b.cfg(); got.RepoRoots[0] != h.home+"/code" || got.Spaces[0].Repos[0].Path != h.home+"/src/web" {
		t.Errorf("in-use config not expanded: %+v", got)
	}
}

func TestBoardFindsClonesAndReadsOrigins(t *testing.T) {
	h, _ := workHarness(t)
	h.clone(t, "web")
	c, err := h.app.LoadConfig()
	if err != nil {
		t.Fatal(err)
	}
	b := &boardActions{a: h.app, c: c, ctx: context.Background()}
	found := b.FindClones("example-org/web")().(tui.ClonesFoundMsg)
	if found.Repo != "example-org/web" || len(found.Paths) != 1 || found.Paths[0] != "~/src/web" {
		t.Errorf("clones = %+v", found)
	}
	origin := b.RepoAt("~/src/web")().(tui.RepoFoundMsg)
	if origin.Repo != "example-org/web" || origin.Path != "~/src/web" {
		t.Errorf("origin = %+v", origin)
	}
	h.app.Env.GOOS = "linux"
	h.run.missing["zenity"], h.run.missing["kdialog"] = true, true
	if picked := b.PickFolder("~/src")().(tui.FolderPickedMsg); picked.Err == nil || !strings.Contains(picked.Err.Error(), "no folder picker") {
		t.Errorf("without a picker = %+v", picked)
	}
}
