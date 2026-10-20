package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// folderForm opens the "Add a folder with local clones" form, whose only field is a path.
func folderForm(t *testing.T, dirs map[string][]string) (tea.Model, *recorder) {
	t.Helper()
	m, r := settingsBoard(t)
	r.dirs = dirs
	m = press(m, "l", "a")
	if !strings.Contains(stripANSI(m.View()), "Add a folder with local clones") {
		t.Fatalf("no folder form:\n%s", stripANSI(m.View()))
	}
	return m, r
}

func pathValue(m tea.Model) string {
	f := m.(Model).settings.form
	return f.values[f.pathField()]
}

func TestTabCompletesTheTypedFolder(t *testing.T) {
	dirs := map[string][]string{
		"~/":         {".cache", "dev", "Documents", "downloads"},
		"~/dev/":     {"personal", "work"},
		"~/dev/per/": nil,
	}
	cases := []struct {
		name  string
		typed string
		tabs  int
		want  string
		hint  string
	}{
		{"single match gets its slash", "~/dev/p", 1, "~/dev/personal/", ""},
		{"common prefix of several matches, case-insensitive", "~/d", 1, "~/d", "dev · Documents · downloads"},
		{"next tab cycles through the matches", "~/d", 2, "~/dev/", ""},
		{"cycling wraps around", "~/d", 5, "~/dev/", ""},
		{"common prefix longer than the typed text", "~/do", 1, "~/do", "Documents · downloads"},
		{"hidden folders only when asked for", "~/.", 1, "~/.cache/", ""},
		{"a bare ~ becomes the home folder", "~", 1, "~/", ""},
		{"no match says so", "~/zz", 1, "~/zz", "no folder starts with zz"},
		{"an empty listing says so", "~/dev/per/x", 1, "~/dev/per/x", "no folder starts with x"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			m, _ := folderForm(t, dirs)
			m = typeText(m, c.typed)
			for range c.tabs {
				m = send(m, keyTab)
			}
			if got := pathValue(m); got != c.want {
				t.Errorf("path = %q, want %q", got, c.want)
			}
			if c.hint != "" && !strings.Contains(stripANSI(m.View()), c.hint) {
				t.Errorf("hint %q missing:\n%s", c.hint, stripANSI(m.View()))
			}
		})
	}
}

func TestTabCompletesCaseInsensitivelyWithTheFolderSpelling(t *testing.T) {
	m, _ := folderForm(t, map[string][]string{"~/": {"Projects"}})
	m = typeText(m, "~/proj")
	m = send(m, keyTab)
	if got := pathValue(m); got != "~/Projects/" {
		t.Errorf("path = %q", got)
	}
}

func TestEmptyPathTabOffersKnownFolders(t *testing.T) {
	m, r := folderForm(t, nil)
	m = send(m, keyTab)
	if got := pathValue(m); got != "~/src" {
		t.Errorf("path = %q, want the configured clone folder", got)
	}
	if len(r.listed) != 0 {
		t.Errorf("known folders need no listing, listed %v", r.listed)
	}
}

func TestEmptyRepoPathTabCyclesFoundClonesThenFolders(t *testing.T) {
	m, r := settingsBoard(t)
	r.clones = map[string][]string{"example-org/docs": {"~/src/docs", "~/work/docs"}}
	m = press(m, "a")
	m = typeText(m, "example-org/docs")
	m = send(m, keyDown)
	f := m.(Model).settings.form
	f.values[fieldPath] = ""
	var got []string
	for range 4 {
		m = send(m, keyTab)
		got = append(got, pathValue(m))
	}
	want := []string{"~/src/docs", "~/work/docs", "~/src", "~/src/docs"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("tabs gave %v, want %v", got, want)
	}
}

func TestLateListingForAnEditedPathIsIgnored(t *testing.T) {
	m, _ := folderForm(t, nil)
	m = typeText(m, "~/dev/p")
	m, _ = m.Update(DirsListedMsg{Path: "~/", Names: []string{"other"}})
	if got := pathValue(m); got != "~/dev/p" {
		t.Errorf("a listing for another folder changed the path to %q", got)
	}
}

func TestTypingAfterCyclingStartsAFreshCompletion(t *testing.T) {
	m, r := folderForm(t, map[string][]string{"~/": {"dev", "docs"}, "~/dev/": {"personal"}})
	m = typeText(m, "~/d")
	m = send(m, keyTab)
	m = send(m, keyTab)
	m = typeText(m, "p")
	m = send(m, keyTab)
	if got := pathValue(m); got != "~/dev/personal/" {
		t.Errorf("path = %q, listed %v", got, r.listed)
	}
}

func TestArrowsStillMoveBetweenFieldsInARepoForm(t *testing.T) {
	m, _ := settingsBoard(t)
	m = press(m, "a")
	m = send(m, keyDown)
	if !strings.Contains(stripANSI(m.View()), "tab complete") {
		t.Errorf("footer should explain tab completion in the path field:\n%s", stripANSI(m.View()))
	}
	m = send(m, tea.KeyMsg{Type: tea.KeyDown})
	if f := m.(Model).settings.form; f.focus != fieldRepo {
		t.Errorf("down from the path field should go back to the repository, focus = %d", f.focus)
	}
	if !strings.Contains(stripANSI(m.View()), "tab complete") {
		t.Errorf("tab completes the repository field too:\n%s", stripANSI(m.View()))
	}
}
