package herdrplugin

import (
	"strings"
	"testing"

	"github.com/BurntSushi/toml"
)

const defaults = `
[keys]
prefix = "ctrl+b"
help = "prefix+?"
new_tab = "prefix+c"
focus_left = ["prefix+h", "prefix+left"]
`

func TestPlanKeys(t *testing.T) {
	tests := []struct {
		name, user   string
		add, present []string
		conflict     map[string]string
	}{
		{"fresh config", "", []string{"prefix+t", "prefix+alt+t", "prefix+i"}, nil, nil},
		{"already bound by hand", "[[keys.command]]\nkey = \"prefix+t\"\ntype = \"plugin_action\"\ncommand = \"tkt.board\"\n", []string{"prefix+alt+t", "prefix+i"}, []string{"prefix+t"}, nil},
		{"key taken by the user", "[keys]\nzoom = \"prefix+t\"\n", []string{"prefix+alt+t", "prefix+i"}, nil, map[string]string{"prefix+t": "zoom"}},
		{"key taken by another plugin", "[[keys.command]]\nkey = \"prefix+i\"\ntype = \"plugin_action\"\ncommand = \"other.thing\"\n", []string{"prefix+t", "prefix+alt+t"}, nil, map[string]string{"prefix+i": "other.thing"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			plan, err := PlanKeys(tt.user, defaults)
			if err != nil {
				t.Fatal(err)
			}
			if got := keys(plan.Add); strings.Join(got, ",") != strings.Join(tt.add, ",") {
				t.Errorf("add = %v, want %v", got, tt.add)
			}
			if got := keys(plan.Present); strings.Join(got, ",") != strings.Join(tt.present, ",") {
				t.Errorf("present = %v, want %v", got, tt.present)
			}
			for k, v := range tt.conflict {
				if plan.Conflict[k] != v {
					t.Errorf("conflict[%s] = %q, want %q", k, plan.Conflict[k], v)
				}
			}
		})
	}
}

func TestPlanKeysRespectsHerdrDefaults(t *testing.T) {
	plan, err := PlanKeys("", "[keys]\nsomething = \"prefix+t\"\n")
	if err != nil {
		t.Fatal(err)
	}
	if plan.Conflict["prefix+t"] != "something" || len(plan.Add) != 2 {
		t.Errorf("plan = %+v", plan)
	}
}

func keys(bs []Binding) []string {
	var out []string
	for _, b := range bs {
		out = append(out, b.Key)
	}
	return out
}

func TestAddAndRemoveBlock(t *testing.T) {
	user := "onboarding = false\n\n[theme]\nname = \"catppuccin\"\n"
	added := AddBlock(user, DefaultBindings)
	var parsed struct {
		Onboarding bool `toml:"onboarding"`
		Keys       struct {
			Command []struct{ Key, Type, Command string } `toml:"command"`
		} `toml:"keys"`
	}
	if _, err := toml.Decode(added, &parsed); err != nil {
		t.Fatalf("result is not valid TOML: %v\n%s", err, added)
	}
	if len(parsed.Keys.Command) != 3 || parsed.Keys.Command[0].Command != "tkt.board" || parsed.Keys.Command[1].Command != "tkt.board-pane" || parsed.Keys.Command[1].Type != "plugin_action" {
		t.Errorf("bindings = %+v", parsed.Keys.Command)
	}
	removed, ok := RemoveBlock(added)
	if !ok || removed != user {
		t.Errorf("RemoveBlock = %v\n%q\nwant\n%q", ok, removed, user)
	}
	if _, ok := RemoveBlock(user); ok {
		t.Error("removed a block that was not there")
	}
	if got := AddBlock("", DefaultBindings[:1]); !strings.HasPrefix(got, blockStart) {
		t.Errorf("empty config block = %q", got)
	}
}
