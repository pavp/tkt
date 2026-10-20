package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func runes(s string) tea.KeyMsg { return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)} }

func browseFolders(t *testing.T, names []string) tea.Model {
	t.Helper()
	m, r := folderForm(t, map[string][]string{"~/src/": names, "~/src/web-app/": {}, "~/src/.config/": {}})
	r.noNative = true
	return send(m, keyCtrlO)
}

func TestBrowserFilterNarrowsTheFolders(t *testing.T) {
	cases := []struct {
		name  string
		keys  []tea.KeyMsg
		shown []string
		gone  []string
		path  string
	}{
		{"typing after / filters ignoring case", []tea.KeyMsg{runes("/"), runes("WEB")}, []string{"/WEB", "web-app/", "old-web/"}, []string{"api/"}, ""},
		{"enter chooses the highlighted match", []tea.KeyMsg{runes("/"), runes("web-"), keyEnter}, nil, nil, "~/src/web-app"},
		{"right opens the highlighted match", []tea.KeyMsg{runes("/"), runes("web-"), keyRight}, []string{"~/src/web-app/"}, nil, ""},
		{"backspace edits the filter", []tea.KeyMsg{runes("/"), runes("webx"), {Type: tea.KeyBackspace}}, []string{"web-app/"}, []string{"no folder matches"}, ""},
		{"esc clears the filter and keeps browsing", []tea.KeyMsg{runes("/"), runes("web"), keyEsc}, []string{"api/", "web-app/"}, []string{"/web"}, ""},
		{"no match says so", []tea.KeyMsg{runes("/"), runes("zz")}, []string{"no folder matches zz"}, []string{"api/"}, ""},
		{"after esc, space chooses the folder shown", []tea.KeyMsg{runes("/"), runes("web"), keyEsc, keySpace}, nil, nil, "~/src"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			m := browseFolders(t, []string{".config", "api", "old-web", "web-app"})
			for _, k := range c.keys {
				m = send(m, k)
			}
			if c.path != "" {
				if got := pathValue(m); got != c.path {
					t.Errorf("path = %q, want %q", got, c.path)
				}
				return
			}
			v := stripANSI(m.View())
			for _, s := range c.shown {
				if !strings.Contains(v, s) {
					t.Errorf("missing %q:\n%s", s, v)
				}
			}
			for _, s := range c.gone {
				if strings.Contains(v, s) {
					t.Errorf("unexpected %q:\n%s", s, v)
				}
			}
		})
	}
}

func TestBrowserDotTogglesHiddenFolders(t *testing.T) {
	m := browseFolders(t, []string{".config", "api"})
	if strings.Contains(stripANSI(m.View()), ".config") {
		t.Fatal("hidden folders should start hidden")
	}
	m = send(m, runes("."))
	if v := stripANSI(m.View()); !strings.Contains(v, ".config/") || !strings.Contains(v, ". hide hidden") {
		t.Errorf("dot should show hidden folders:\n%s", v)
	}
	m = send(m, keyRight)
	m = send(m, keyEnter)
	if got := pathValue(m); got != "~/src/.config" {
		t.Errorf("path = %q", got)
	}
}

func TestBrowserShowsBrokenLinksWithoutEnteringThem(t *testing.T) {
	m, r := folderForm(t, map[string][]string{"~/src/": {"api"}})
	r.noNative = true
	r.broken = map[string][]string{"~/src/": {"gone"}}
	m = send(m, keyCtrlO)
	if v := stripANSI(m.View()); !strings.Contains(v, "gone (broken link)") {
		t.Fatalf("broken link missing:\n%s", v)
	}
	m = send(m, keyDown)
	m = send(m, keyRight)
	if b := browser(m); b == nil || b.dir != "~/src/" {
		t.Errorf("a broken link must not be entered, browser = %+v", b)
	}
}
