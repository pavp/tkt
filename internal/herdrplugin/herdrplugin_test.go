package herdrplugin

import (
	"strings"
	"testing"

	"github.com/BurntSushi/toml"
)

func TestRenderIsValidTOML(t *testing.T) {
	out := Render(`/Users/dev/My Tools/tkt`, "0.1.0")
	var m struct {
		ID      string `toml:"id"`
		Version string `toml:"version"`
		Actions []struct {
			ID      string
			Command []string
		} `toml:"actions"`
		Panes []struct {
			ID, Placement string
			Command       []string
		} `toml:"panes"`
		Events []struct {
			On      string
			Command []string
		} `toml:"events"`
		LinkHandlers []struct{ ID, Pattern, Action string } `toml:"link_handlers"`
	}
	if _, err := toml.Decode(out, &m); err != nil {
		t.Fatalf("manifest is not valid TOML: %v\n%s", err, out)
	}
	if m.ID != "tkt" || m.Version != "0.1.0" {
		t.Errorf("id/version = %q %q", m.ID, m.Version)
	}
	if strings.Contains(out, "{{") {
		t.Error("unfilled placeholder")
	}
	for _, a := range m.Actions {
		if a.Command[0] != "/Users/dev/My Tools/tkt" {
			t.Errorf("action %s runs %q", a.ID, a.Command[0])
		}
	}
	panes := map[string]string{}
	for _, p := range m.Panes {
		panes[p.ID] = p.Placement + " " + strings.Join(p.Command[1:], " ")
	}
	if panes["board"] != "popup board" || panes["ticket"] != "split show --watch" || panes["setup"] != "popup init" {
		t.Errorf("panes = %v", panes)
	}
	if len(m.Events) != 1 || m.Events[0].On != "worktree.created" || strings.Join(m.Events[0].Command[1:], " ") != "herdr on-worktree" {
		t.Errorf("events = %+v", m.Events)
	}
	if len(m.LinkHandlers) != 2 || m.LinkHandlers[0].Action != "open" {
		t.Errorf("link handlers = %+v", m.LinkHandlers)
	}
}

func TestRenderQuotesUnusualPaths(t *testing.T) {
	out := Render("/tmp/we\"ird\\path\tx", "dev")
	var m struct {
		Actions []struct{ Command []string } `toml:"actions"`
	}
	if _, err := toml.Decode(out, &m); err != nil {
		t.Fatalf("invalid TOML for an unusual path: %v", err)
	}
	if got := m.Actions[0].Command[0]; got != "/tmp/we\"ird\\path\tx" {
		t.Errorf("path round-trip = %q", got)
	}
}
