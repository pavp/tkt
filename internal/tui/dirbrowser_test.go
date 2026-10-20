package tui

import (
	"fmt"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

var (
	keyCtrlO = tea.KeyMsg{Type: tea.KeyCtrlO}
	keyLeft  = tea.KeyMsg{Type: tea.KeyLeft}
	keyRight = tea.KeyMsg{Type: tea.KeyRight}
	keyDown  = tea.KeyMsg{Type: tea.KeyDown}
	keyEsc   = tea.KeyMsg{Type: tea.KeyEsc}
)

var browserDirs = map[string][]string{
	"~/":              {"dev", "src"},
	"~/src/":          {"api", "web"},
	"~/src/api/":      {},
	"~/src/web/":      {"docs"},
	"/home/":          {"dev"},
	"/":               {"home", "tmp"},
	"~/dev/":          {"personal"},
	"~/src/web/docs/": {},
}

func browsing(t *testing.T) (tea.Model, *recorder) {
	t.Helper()
	m, r := folderForm(t, browserDirs)
	r.noNative = true
	return m, r
}

func browser(m tea.Model) *dirBrowser { return m.(Model).settings.form.browser }

func TestCtrlOBrowsesFoldersInTheTerminalWithoutANativePicker(t *testing.T) {
	m, r := browsing(t)
	m = send(m, keyCtrlO)
	if len(r.starts) != 0 {
		t.Fatalf("no native picker should open, got %v", r.starts)
	}
	v := stripANSI(m.View())
	for _, want := range []string{"Choose a folder", "~/src/", "▸ api", "web", "→ open · ← up · enter choose this folder · / filter · . show hidden · esc cancel"} {
		if !strings.Contains(v, want) {
			t.Errorf("browser missing %q:\n%s", want, v)
		}
	}
}

func TestBrowserNavigatesAndChoosesTheCurrentFolder(t *testing.T) {
	cases := []struct {
		name string
		keys []tea.KeyMsg
		want string
	}{
		{"choose where it opens", nil, "~/src"},
		{"enter a folder", []tea.KeyMsg{keyDown, keyRight}, "~/src/web"},
		{"an empty folder can be chosen", []tea.KeyMsg{keyRight}, "~/src/api"},
		{"go up", []tea.KeyMsg{keyLeft}, "~"},
		{"go above home", []tea.KeyMsg{keyLeft, keyLeft}, "/home"},
		{"up to the root", []tea.KeyMsg{keyLeft, keyLeft, keyLeft}, "/"},
		{"the root has no parent", []tea.KeyMsg{keyLeft, keyLeft, keyLeft, keyLeft}, "/"},
		{"vim keys", []tea.KeyMsg{{Type: tea.KeyRunes, Runes: []rune("j")}, {Type: tea.KeyRunes, Runes: []rune("l")}, {Type: tea.KeyRunes, Runes: []rune("h")}}, "~/src"},
		{"the cursor stops at the last folder", []tea.KeyMsg{keyDown, keyDown, keyDown, keyRight}, "~/src/web"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			m, _ := browsing(t)
			m = send(m, keyCtrlO)
			for _, k := range c.keys {
				m = send(m, k)
			}
			m = send(m, keyEnter)
			if browser(m) != nil {
				t.Fatal("enter should close the browser")
			}
			if got := pathValue(m); got != c.want {
				t.Errorf("path = %q, want %q", got, c.want)
			}
		})
	}
}

func TestBrowserOpensAtTheTypedPath(t *testing.T) {
	m, r := browsing(t)
	m = typeText(m, "~/src/web")
	m = send(m, keyCtrlO)
	if last := r.listed[len(r.listed)-1]; last != "~/src/web/" {
		t.Errorf("browser opened at %q", last)
	}
	if !strings.Contains(stripANSI(m.View()), "docs") {
		t.Errorf("browser should list ~/src/web:\n%s", stripANSI(m.View()))
	}
}

func TestBrowserEscKeepsThePath(t *testing.T) {
	m, _ := browsing(t)
	m = typeText(m, "~/dev")
	m = send(m, keyCtrlO)
	m = send(m, keyLeft)
	m = send(m, keyEsc)
	if browser(m) != nil || pathValue(m) != "~/dev" {
		t.Errorf("esc should close the browser and keep the path, got %q", pathValue(m))
	}
	if m.(Model).settings.form == nil {
		t.Error("esc in the browser must not close the form")
	}
}

func TestBrowserShowsUnreadableFoldersAndLetsYouGoUp(t *testing.T) {
	m, _ := browsing(t)
	m = typeText(m, "~/src/gone")
	m = send(m, keyCtrlO)
	if !strings.Contains(stripANSI(m.View()), "no such folder") {
		t.Errorf("listing error missing:\n%s", stripANSI(m.View()))
	}
	m = send(m, keyLeft)
	m = send(m, keyEnter)
	if got := pathValue(m); got != "~/src" {
		t.Errorf("path = %q", got)
	}
}

func TestBrowserIgnoresListingsItNoLongerWaitsFor(t *testing.T) {
	m, _ := browsing(t)
	m = send(m, keyCtrlO)
	m, _ = m.Update(DirsListedMsg{Path: "~/dev/", Dir: "~/dev/", Abs: "/home/dev/dev/", Names: []string{"personal"}})
	if v := stripANSI(m.View()); strings.Contains(v, "personal") {
		t.Errorf("a stale listing replaced the browser:\n%s", v)
	}
}

func TestBrowserChoiceNamesTheRepository(t *testing.T) {
	m, r := settingsBoard(t)
	r.noNative = true
	r.dirs = browserDirs
	r.origins = map[string]string{"~/src/web": "example-org/web"}
	m = press(m, "a")
	m = send(m, keyTab)
	m = send(m, keyCtrlO)
	m = send(m, keyDown)
	m = send(m, keyRight)
	m = send(m, keyEnter)
	f := m.(Model).settings.form
	if f.values[fieldPath] != "~/src/web" || f.values[fieldRepo] != "example-org/web" {
		t.Errorf("form = %q", f.values)
	}
}

func TestCtrlOUsesTheNativePickerWhenThereIsOne(t *testing.T) {
	m, r := folderForm(t, browserDirs)
	m = send(m, keyCtrlO)
	if len(r.starts) != 1 || browser(m) != nil {
		t.Errorf("native picker starts = %v, browser = %v", r.starts, browser(m))
	}
}

func TestBrowserScrollsLongFolders(t *testing.T) {
	var many []string
	for i := range 20 {
		many = append(many, fmt.Sprintf("repo%02d", i))
	}
	m, _ := folderForm(t, map[string][]string{"~/src/": many})
	m.(Model).actions.(*recorder).noNative = true
	m = send(m, keyCtrlO)
	v := stripANSI(m.View())
	if !strings.Contains(v, "↓ more") || strings.Contains(v, "↑ more") || strings.Contains(v, "repo12") {
		t.Errorf("first page should end before repo12 with a ↓ marker:\n%s", v)
	}
	for range 15 {
		m = send(m, keyDown)
	}
	v = stripANSI(m.View())
	if !strings.Contains(v, "▸ repo15/") || !strings.Contains(v, "↑ more") || strings.Contains(v, "repo03") {
		t.Errorf("the selected folder should stay visible after scrolling:\n%s", v)
	}
	m = send(m, keyRight)
	m = send(m, keyEnter)
	if got := pathValue(m); got != "~/src/repo15" {
		t.Errorf("path = %q", got)
	}
}
