package herdrplugin

import (
	"fmt"
	"sort"
	"strings"

	"github.com/BurntSushi/toml"
)

// Binding is a keybinding tkt offers to add to the user's Herdr config.
type Binding struct {
	Key, Action, Description string
}

// DefaultBindings are the shortcuts `tkt herdr install` offers.
var DefaultBindings = []Binding{
	{Key: "prefix+t", Action: ID + ".board", Description: "tkt board"},
	{Key: "prefix+i", Action: ID + ".ticket", Description: "tkt ticket pane"},
}

const (
	blockStart = "# >>> tkt keybindings (managed by `tkt herdr install`; `tkt herdr uninstall` removes them)"
	blockEnd   = "# <<< tkt keybindings"
)

type herdrKeys struct {
	Keys map[string]any `toml:"keys"`
}

// usedKeys collects every key a Herdr config binds: action bindings under [keys] and [[keys.command]]
// entries, mapped to what they do.
func usedKeys(config string) (map[string]string, error) {
	var c herdrKeys
	if _, err := toml.Decode(config, &c); err != nil {
		return nil, fmt.Errorf("parse Herdr config: %w", err)
	}
	used := map[string]string{}
	for name, v := range c.Keys {
		switch val := v.(type) {
		case string:
			used[val] = name
		case []map[string]any:
			for _, m := range val {
				key, _ := m["key"].(string)
				cmd, _ := m["command"].(string)
				if key != "" {
					used[key] = cmd
				}
			}
		case []any:
			for _, x := range val {
				if s, ok := x.(string); ok {
					used[s] = name
				}
			}
		}
	}
	return used, nil
}

// KeyPlan says which bindings to add, which are already in place, and which keys are taken by something else.
type KeyPlan struct {
	Add      []Binding
	Present  []Binding
	Conflict map[string]string
}

// PlanKeys compares DefaultBindings with the user's config and Herdr's defaults.
func PlanKeys(userConfig, defaultConfig string) (KeyPlan, error) {
	user, err := usedKeys(userConfig)
	if err != nil {
		return KeyPlan{}, err
	}
	defaults, err := usedKeys(defaultConfig)
	if err != nil {
		return KeyPlan{}, fmt.Errorf("herdr defaults: %w", err)
	}
	bound := map[string]bool{}
	for _, action := range user {
		bound[action] = true
	}
	plan := KeyPlan{Conflict: map[string]string{}}
	for _, b := range DefaultBindings {
		if bound[b.Action] {
			plan.Present = append(plan.Present, b)
			continue
		}
		if what, ok := user[b.Key]; ok {
			plan.Conflict[b.Key] = what
			continue
		}
		if what, ok := defaults[b.Key]; ok {
			plan.Conflict[b.Key] = what
			continue
		}
		plan.Add = append(plan.Add, b)
	}
	return plan, nil
}

// AddBlock appends the managed block for bindings to config.
func AddBlock(config string, bindings []Binding) string {
	var b strings.Builder
	b.WriteString(strings.TrimRight(config, "\n"))
	if config != "" {
		b.WriteString("\n\n")
	}
	b.WriteString(blockStart + "\n")
	for _, k := range bindings {
		fmt.Fprintf(&b, "[[keys.command]]\nkey = %s\ntype = \"plugin_action\"\ncommand = %s\ndescription = %s\n\n", tomlString(k.Key), tomlString(k.Action), tomlString(k.Description))
	}
	b.WriteString(blockEnd + "\n")
	return b.String()
}

// RemoveBlock drops the managed block; ok is false when config has none.
func RemoveBlock(config string) (string, bool) {
	return removeBetween(config, blockStart, blockEnd)
}

// Describe lists bindings as "prefix+t (tkt board), prefix+i (tkt ticket pane)".
func Describe(bs []Binding) string {
	parts := make([]string, len(bs))
	for i, b := range bs {
		parts[i] = fmt.Sprintf("%s (%s)", b.Key, b.Description)
	}
	sort.Strings(parts)
	return strings.Join(parts, ", ")
}
