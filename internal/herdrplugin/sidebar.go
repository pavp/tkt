package herdrplugin

import (
	"strings"

	"github.com/BurntSushi/toml"
)

const (
	sidebarStart = "# >>> tkt sidebar (managed by `tkt herdr install`; `tkt herdr uninstall` removes it)"
	sidebarEnd   = "# <<< tkt sidebar"
)

// SidebarRows are Herdr's default space rows plus tkt's ticket line and agent line. Herdr styles a
// token, not a value, so each line has one token per color and tkt reports only the one that applies.
const SidebarRows = `[ui.sidebar.spaces]
rows = [
  ["state_icon", "workspace"],
  ["branch", "git_status"],
  [{ token = "$tkt", fg = "#8DBBFF" }, { token = "$tkt_alert", fg = "#F5B970" }],
  [{ token = "$tkt_agent", fg = "#8DBBFF" }, { token = "$tkt_agent_alert", fg = "#F5B970" }, { token = "$tkt_agent_idle", dim = true }],
]
`

type SidebarPlan int

const (
	SidebarAdd SidebarPlan = iota
	SidebarPresent
	// SidebarConflict means the user configures space rows already; tkt never rewrites them.
	SidebarConflict
)

func PlanSidebar(config string) SidebarPlan {
	if strings.Contains(config, sidebarStart) {
		return SidebarPresent
	}
	var c struct {
		UI struct {
			Sidebar struct {
				Spaces map[string]any `toml:"spaces"`
			} `toml:"sidebar"`
		} `toml:"ui"`
	}
	if _, err := toml.Decode(config, &c); err == nil && c.UI.Sidebar.Spaces != nil {
		return SidebarConflict
	}
	return SidebarAdd
}

// AddSidebarBlock appends the managed sidebar rows to config.
func AddSidebarBlock(config string) string {
	var b strings.Builder
	b.WriteString(strings.TrimRight(config, "\n"))
	if config != "" {
		b.WriteString("\n\n")
	}
	b.WriteString(sidebarStart + "\n" + SidebarRows + sidebarEnd + "\n")
	return b.String()
}

// RemoveSidebarBlock drops the managed sidebar rows; ok is false when config has none.
func RemoveSidebarBlock(config string) (string, bool) {
	return removeBetween(config, sidebarStart, sidebarEnd)
}

func removeBetween(config, start, end string) (string, bool) {
	i := strings.Index(config, start)
	if i < 0 {
		return config, false
	}
	j := strings.Index(config[i:], end)
	if j < 0 {
		return config, false
	}
	j += i + len(end)
	if j < len(config) && config[j] == '\n' {
		j++
	}
	return strings.TrimRight(config[:i], "\n") + "\n" + config[j:], true
}
