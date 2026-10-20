package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func repoCompletionForm(t *testing.T) (tea.Model, *recorder) {
	t.Helper()
	m, r := settingsBoard(t)
	r.repos = map[string][]string{
		"example-org": {"example-org/web", "example-org/web-admin", "example-org/api", "example-org/Docs"},
		"other-org":   {"other-org/tools"},
	}
	r.clones = map[string][]string{"example-org/api": {"~/src/api"}}
	m = press(m, "a")
	return m, r
}

func repoValue(m tea.Model) string { return m.(Model).settings.form.values[fieldRepo] }

func TestTabCompletesRepositoriesFromGitHub(t *testing.T) {
	cases := []struct {
		name  string
		typed string
		tabs  int
		want  string
		hint  string
	}{
		{"an empty field offers the owners tkt knows", "", 1, "example-org/", ""},
		{"an owner prefix", "exa", 1, "example-org/", ""},
		{"a single repository", "example-org/a", 1, "example-org/api", ""},
		{"ignoring case, with the repository's spelling", "example-org/do", 1, "example-org/Docs", ""},
		{"common prefix of several", "example-org/w", 1, "example-org/web", "web-admin"},
		{"next tab cycles", "example-org/w", 2, "example-org/web", ""},
		{"and on", "example-org/w", 3, "example-org/web-admin", ""},
		{"no match says so", "example-org/zz", 1, "example-org/zz", "no repository of example-org starts with zz"},
		{"an unknown owner says why", "nobody/x", 1, "nobody/x", "cannot list nobody's repositories: no GitHub organization or user named nobody"},
		{"an unknown owner prefix", "zz", 1, "zz", "no known owner starts with zz · type owner/ to list its repositories"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			m, _ := repoCompletionForm(t)
			m = typeText(m, c.typed)
			for range c.tabs {
				m = send(m, keyTab)
			}
			if got := repoValue(m); got != c.want {
				t.Errorf("repository = %q, want %q", got, c.want)
			}
			if c.hint != "" && !strings.Contains(stripANSI(m.View()), c.hint) {
				t.Errorf("hint %q missing:\n%s", c.hint, stripANSI(m.View()))
			}
		})
	}
}

func TestCompletedRepositoryFindsItsClone(t *testing.T) {
	m, _ := repoCompletionForm(t)
	m = typeText(m, "example-org/a")
	m = send(m, keyTab)
	if f := m.(Model).settings.form; f.values[fieldPath] != "~/src/api" || f.focus != fieldRepo {
		t.Errorf("form = %q, focus %d", f.values, f.focus)
	}
}

func TestLateRepositoryListForAnEditedFieldIsIgnored(t *testing.T) {
	m, _ := repoCompletionForm(t)
	m = typeText(m, "example-org/w")
	m, _ = m.Update(ReposListedMsg{Owner: "other-org", Names: []string{"other-org/tools"}})
	if got := repoValue(m); got != "example-org/w" {
		t.Errorf("a list for another owner changed the field to %q", got)
	}
}
