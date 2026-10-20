package tui

import (
	"fmt"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

var keyUp = tea.KeyMsg{Type: tea.KeyUp}

func dropdown(m tea.Model) []string { return m.(Model).settings.form.cands }

func openRepoDropdown(t *testing.T) (tea.Model, *recorder) {
	t.Helper()
	m, r := repoCompletionForm(t)
	m = typeText(m, "example-org/")
	m = send(m, keyTab)
	if len(dropdown(m)) != 4 {
		t.Fatalf("dropdown = %v", dropdown(m))
	}
	return m, r
}

func TestDropdownListsTheMatchesUnderTheField(t *testing.T) {
	m, _ := openRepoDropdown(t)
	v := stripANSI(m.View())
	rows := map[string]bool{}
	for _, l := range strings.Split(v, "\n") {
		rows[strings.TrimSpace(strings.Trim(strings.TrimSpace(l), "│"))] = true
	}
	for _, want := range []string{"web", "web-admin", "api", "Docs", "4 matches · ↑↓ choose · enter pick · esc close"} {
		if !rows[want] {
			t.Errorf("dropdown missing the row %q:\n%s", want, v)
		}
	}
}

func TestDropdownKeys(t *testing.T) {
	cases := []struct {
		name   string
		keys   []tea.KeyMsg
		value  string
		open   bool
		inForm bool
	}{
		{"down highlights the first match", []tea.KeyMsg{keyDown}, "example-org/web", true, true},
		{"down again moves on", []tea.KeyMsg{keyDown, keyDown}, "example-org/web-admin", true, true},
		{"up wraps to the last", []tea.KeyMsg{keyUp}, "example-org/Docs", true, true},
		{"enter picks the highlighted match", []tea.KeyMsg{keyDown, keyDown, keyEnter}, "example-org/web-admin", false, true},
		{"enter with nothing highlighted picks the first", []tea.KeyMsg{keyEnter}, "example-org/web", false, true},
		{"esc closes only the dropdown", []tea.KeyMsg{keyEsc}, "example-org/", false, true},
		{"typing filters the list", []tea.KeyMsg{runes("w")}, "example-org/w", true, true},
		{"typing past every match closes it", []tea.KeyMsg{runes("zz")}, "example-org/zz", false, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			m, r := openRepoDropdown(t)
			for _, k := range c.keys {
				m = send(m, k)
			}
			f := m.(Model).settings.form
			if f == nil {
				t.Fatal("the form closed")
			}
			if f.values[fieldRepo] != c.value || (len(f.cands) > 0) != c.open {
				t.Errorf("value %q open %v, want %q open %v", f.values[fieldRepo], len(f.cands) > 0, c.value, c.open)
			}
			if len(r.saved) != 0 {
				t.Error("the dropdown must not save the form")
			}
		})
	}
}

func TestDropdownFilterKeepsOnlyMatches(t *testing.T) {
	m, r := openRepoDropdown(t)
	before := len(r.listed)
	m = send(m, runes("w"))
	if got := strings.Join(dropdown(m), ","); got != "example-org/web,example-org/web-admin" {
		t.Errorf("filtered = %s", got)
	}
	if len(r.listed) != before {
		t.Errorf("filtering asked GitHub again: %v", r.listed[before:])
	}
}

func TestDropdownPickFindsTheClone(t *testing.T) {
	m, _ := openRepoDropdown(t)
	m = send(m, keyDown)
	m = send(m, keyDown)
	m = send(m, keyDown)
	m = send(m, keyEnter)
	if f := m.(Model).settings.form; f.values[fieldRepo] != "example-org/api" || f.values[fieldPath] != "~/src/api" {
		t.Errorf("form = %q", f.values)
	}
}

func TestDropdownScrollsAndClicks(t *testing.T) {
	m, r := settingsBoard(t)
	var many []string
	for i := range 12 {
		many = append(many, fmt.Sprintf("example-org/repo%02d", i))
	}
	r.repos = map[string][]string{"example-org": many}
	m = press(m, "a")
	m = typeText(m, "example-org/r")
	m = send(m, keyTab)
	v := stripANSI(m.View())
	if !strings.Contains(v, "repo07") || strings.Contains(v, "repo08") || !strings.Contains(v, "↓ 4 more") {
		t.Fatalf("first page should show 8 rows and a marker:\n%s", v)
	}
	for range 10 {
		m = send(m, keyDown)
	}
	if v := stripANSI(m.View()); !strings.Contains(v, "repo09") || !strings.Contains(v, "↑") {
		t.Errorf("the highlighted match should stay visible:\n%s", v)
	}
	lines := strings.Split(stripANSI(m.View()), "\n")
	y := lineIndex(m.View(), "repo05")
	m = click(m, strings.Index(lines[y], "repo05"), y)
	if got := repoValue(m); got != "example-org/repo05" || len(dropdown(m)) != 0 {
		t.Errorf("click picked %q, open %v", got, dropdown(m))
	}
}

func TestPathDropdown(t *testing.T) {
	m, _ := folderForm(t, map[string][]string{"~/": {"dev", "Documents", "downloads"}})
	m = typeText(m, "~/d")
	m = send(m, keyTab)
	m = send(m, keyDown)
	m = send(m, keyDown)
	m = send(m, keyEnter)
	if got := pathValue(m); got != "~/Documents/" || len(dropdown(m)) != 0 {
		t.Errorf("path = %q, open %v", got, dropdown(m))
	}
}
