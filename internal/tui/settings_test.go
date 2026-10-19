package tui

import (
	"errors"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/pavp/tkt/internal/board"
	"github.com/pavp/tkt/internal/config"
)

func settingsBoard(t *testing.T) (tea.Model, *recorder) {
	t.Helper()
	r := &recorder{cfg: config.Config{
		RepoRoots: []string{"~/src"},
		Jira:      config.Jira{Site: "example.atlassian.net", Email: "dev@example.com"},
		Spaces:    []config.Space{{Key: "DEMO", Name: "Demo", Repos: []config.Repo{{Name: "example-org/web", Base: "main"}}}},
	}}
	snap := snapshot()
	snap.Repos = map[string][]string{"DEMO": {"example-org/api", "example-org/web"}}
	m := New(board.Snapshot{}, false, r, ViewColumns)
	next, _ := m.Update(tea.WindowSizeMsg{Width: 140, Height: 40})
	next, _ = next.Update(SnapshotMsg{Snap: snap})
	return press(next, ","), r
}

// send runs a key and feeds the resulting messages back, as the program loop would, following chained
// commands a few steps deep.
func send(m tea.Model, k tea.KeyMsg) tea.Model {
	m, cmd := m.Update(k)
	for i := 0; cmd != nil && i < 5; i++ {
		msg := cmd()
		if msg == nil {
			break
		}
		m, cmd = m.Update(msg)
	}
	return m
}

func typeText(m tea.Model, s string) tea.Model {
	return send(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)})
}

var (
	keyEnter = tea.KeyMsg{Type: tea.KeyEnter}
	keyTab   = tea.KeyMsg{Type: tea.KeyTab}
)

func TestSettingsListsConfiguredAndInferredRepositories(t *testing.T) {
	m, _ := settingsBoard(t)
	v := stripANSI(m.View())
	for _, want := range []string{"← Back to the board", "Settings", "Spaces & repositories", "Local clones", "DEMO · Demo", "REPOSITORY", "LOCAL PATH", "▸ example-org/web", "example-org/api", "inferred from your tickets' pull requests · enter to pin", "+ Add repository"} {
		if !strings.Contains(v, want) {
			t.Errorf("settings missing %q:\n%s", want, v)
		}
	}
}

func TestSettingsAddsARepositoryWithBaseAndPath(t *testing.T) {
	m, r := settingsBoard(t)
	m = press(m, "a")
	if !strings.Contains(stripANSI(m.View()), "Add a repository to DEMO") {
		t.Fatalf("no form:\n%s", stripANSI(m.View()))
	}
	m = typeText(m, "example-org/docs")
	m = send(m, keyTab)
	m = typeText(m, "develop")
	m = send(m, keyTab)
	m = typeText(m, "~/code/docs")
	m = send(m, keyEnter)
	if len(r.saved) != 1 {
		t.Fatalf("saved %d times", len(r.saved))
	}
	repos := r.saved[0].Spaces[0].Repos
	if len(repos) != 2 || repos[1] != (config.Repo{Name: "example-org/docs", Base: "develop", Path: "~/code/docs"}) {
		t.Errorf("repos = %+v", repos)
	}
	v := stripANSI(m.View())
	if !strings.Contains(v, "example-org/docs") || !strings.Contains(v, "✓ saved to your config.toml") {
		t.Errorf("view after save:\n%s", v)
	}
	if r.calls[len(r.calls)-1] != "refresh" {
		t.Errorf("the board should refresh after a save: %v", r.calls)
	}
}

func TestSettingsPinsAnInferredRepository(t *testing.T) {
	m, r := settingsBoard(t)
	m = press(m, "s", "enter")
	if !strings.Contains(stripANSI(m.View()), "Pin example-org/api to DEMO") {
		t.Fatalf("no pin form:\n%s", stripANSI(m.View()))
	}
	m = send(m, keyEnter)
	if len(r.saved) != 1 || len(r.saved[0].Spaces[0].Repos) != 2 || r.saved[0].Spaces[0].Repos[1].Name != "example-org/api" {
		t.Errorf("saved = %+v", r.saved)
	}
}

func TestSettingsRemovesOnlyAfterConfirming(t *testing.T) {
	m, r := settingsBoard(t)
	m = press(m, "d")
	if !strings.Contains(stripANSI(m.View()), "Remove example-org/web from DEMO? (y/n)") {
		t.Fatalf("no confirmation:\n%s", stripANSI(m.View()))
	}
	m = press(m, "n")
	if len(r.saved) != 0 {
		t.Fatal("removed without confirmation")
	}
	m = press(m, "d")
	m = send(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("y")})
	if len(r.saved) != 1 || len(r.saved[0].Spaces[0].Repos) != 0 {
		t.Errorf("saved = %+v", r.saved)
	}
	if v := stripANSI(m.View()); !strings.Contains(v, "example-org/web") || !strings.Contains(v, "inferred") {
		t.Errorf("a removed repo still inferred from Jira should show as inferred:\n%s", v)
	}
}

func TestSettingsEditsLocalCloneFolders(t *testing.T) {
	m, r := settingsBoard(t)
	m = send(m, keyTab)
	if v := stripANSI(m.View()); !strings.Contains(v, "Folders where tkt looks for your local clones") || !strings.Contains(v, "▸ ~/src") {
		t.Fatalf("roots section:\n%s", v)
	}
	m = press(m, "a")
	m = typeText(m, "~/work")
	m = send(m, keyEnter)
	if len(r.saved) != 1 || strings.Join(r.saved[0].RepoRoots, ",") != "~/src,~/work" {
		t.Errorf("saved = %+v", r.saved)
	}
	m = press(m, "enter")
	m = send(m, tea.KeyMsg{Type: tea.KeyCtrlU})
	m = typeText(m, "~/dev")
	send(m, keyEnter)
	if len(r.saved) != 2 || r.saved[1].RepoRoots[0] != "~/dev" {
		t.Errorf("edit = %+v", r.saved)
	}
}

func TestSettingsReportsAFailedSaveAndKeepsTheShownConfig(t *testing.T) {
	m, r := settingsBoard(t)
	r.saveErr = errors.New(`space DEMO: repo "bad" must look like owner/name`)
	m = press(m, "a")
	m = typeText(m, "bad")
	m = send(m, keyEnter)
	v := stripANSI(m.View())
	if !strings.Contains(v, `! not saved: space DEMO: repo "bad" must look like owner/name`) || strings.Contains(v, "▸ bad") {
		t.Errorf("failed save:\n%s", v)
	}
	m = press(m, "a")
	m = send(m, keyEnter)
	if !strings.Contains(stripANSI(m.View()), "Repository (owner/name) is required") {
		t.Errorf("an empty repository should be refused before saving:\n%s", stripANSI(m.View()))
	}
}

func TestSettingsClosesWithEscOrTheBackButton(t *testing.T) {
	m, _ := settingsBoard(t)
	if v := stripANSI(press(m, "esc").View()); strings.Contains(v, "Spaces & repositories") || !strings.Contains(v, "In Progress") {
		t.Errorf("esc should return to the board:\n%s", v)
	}
	y := lineIndex(m.View(), "← Back to the board")
	if v := stripANSI(click(m, 2, y).View()); strings.Contains(v, "Spaces & repositories") {
		t.Errorf("the back button should return to the board:\n%s", v)
	}
}

func TestSettingsShowsARemoveButtonOnConfiguredRows(t *testing.T) {
	m, r := settingsBoard(t)
	lines := strings.Split(stripANSI(m.View()), "\n")
	y := lineIndex(m.View(), "▸ example-org/web")
	if y < 0 || !strings.HasSuffix(strings.TrimRight(lines[y], " "), "✕") {
		t.Fatalf("no ✕ on the configured repository:\n%s", strings.Join(lines, "\n"))
	}
	if x := lipgloss.Width(lines[y][:strings.LastIndex(lines[y], "✕")]); x > 100 {
		t.Errorf("the ✕ should sit next to the row's columns, not at the pane's edge (column %d of 140)", x)
	}
	if inf := lineIndex(m.View(), "inferred from your tickets"); strings.HasSuffix(strings.TrimRight(lines[inf], " "), "✕") {
		t.Error("inferred repositories have nothing to remove")
	}
	x := lipgloss.Width(lines[y][:strings.LastIndex(lines[y], "✕")])
	m = click(m, x, y)
	if !strings.Contains(stripANSI(m.View()), "Remove example-org/web from DEMO? (y/n)") {
		t.Fatalf("clicking ✕ should ask to remove:\n%s", stripANSI(m.View()))
	}
	send(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("y")})
	if len(r.saved) != 1 || len(r.saved[0].Spaces[0].Repos) != 0 {
		t.Errorf("saved = %+v", r.saved)
	}
}

func TestSettingsFillsTheLocalPathFromFoundClones(t *testing.T) {
	m, r := settingsBoard(t)
	r.clones = map[string][]string{"example-org/web": {"~/src/web", "~/work/web"}}
	m = send(m, keyEnter)
	v := stripANSI(m.View())
	if !strings.Contains(v, "~/src/web") || !strings.Contains(v, "2 clones found · ctrl+n next") {
		t.Fatalf("the path should come prefilled from the clones found:\n%s", v)
	}
	m = send(m, tea.KeyMsg{Type: tea.KeyCtrlN})
	if !strings.Contains(stripANSI(m.View()), "~/work/web") {
		t.Errorf("ctrl+n should switch to the next clone:\n%s", stripANSI(m.View()))
	}
	send(m, keyEnter)
	if len(r.saved) != 1 || r.saved[0].Spaces[0].Repos[0].Path != "~/work/web" {
		t.Errorf("saved = %+v", r.saved)
	}
}

func TestSettingsFillsTheRepositoryFromThePickedFolder(t *testing.T) {
	m, r := settingsBoard(t)
	r.picked = "~/code/docs"
	r.origins = map[string]string{"~/code/docs": "example-org/docs"}
	m = press(m, "a")
	m = send(m, keyTab)
	m = send(m, keyTab)
	m = send(m, tea.KeyMsg{Type: tea.KeyCtrlO})
	v := stripANSI(m.View())
	if !strings.Contains(v, "~/code/docs") || !strings.Contains(v, "example-org/docs") {
		t.Fatalf("ctrl+o should fill the path and then the repository from its origin:\n%s", v)
	}
	if len(r.starts) != 1 || r.starts[0] != "~/src" {
		t.Errorf("the picker should start in the first clone folder: %v", r.starts)
	}
	send(m, keyEnter)
	if len(r.saved) != 1 || r.saved[0].Spaces[0].Repos[1] != (config.Repo{Name: "example-org/docs", Path: "~/code/docs"}) {
		t.Errorf("saved = %+v", r.saved)
	}
}

func TestSettingsPicksACloneFolder(t *testing.T) {
	m, r := settingsBoard(t)
	r.picked = "~/projects"
	m = send(m, keyTab)
	m = press(m, "a")
	m = send(m, tea.KeyMsg{Type: tea.KeyCtrlO})
	send(m, keyEnter)
	if len(r.saved) != 1 || strings.Join(r.saved[0].RepoRoots, ",") != "~/src,~/projects" {
		t.Errorf("saved = %+v", r.saved)
	}
}
