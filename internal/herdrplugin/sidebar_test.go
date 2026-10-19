package herdrplugin

import (
	"strings"
	"testing"

	"github.com/BurntSushi/toml"
)

func TestSidebarBlockAddsValidRowsAndRemovesCleanly(t *testing.T) {
	user := "[keys]\nprefix = \"ctrl+b\"\n"
	if got := PlanSidebar(user); got != SidebarAdd {
		t.Fatalf("plan = %v", got)
	}
	added := AddSidebarBlock(user)
	var parsed struct {
		UI struct {
			Sidebar struct {
				Spaces struct {
					Rows [][]any `toml:"rows"`
				} `toml:"spaces"`
			} `toml:"sidebar"`
		} `toml:"ui"`
	}
	if _, err := toml.Decode(added, &parsed); err != nil {
		t.Fatalf("the block is not valid TOML: %v\n%s", err, added)
	}
	rows := parsed.UI.Sidebar.Spaces.Rows
	if len(rows) != 3 || len(rows[2]) != 5 || !strings.Contains(added, `token = "$tkt_alert"`) || !strings.Contains(added, `token = "$tkt_agent_idle"`) {
		t.Errorf("rows = %v\n%s", rows, added)
	}
	if PlanSidebar(added) != SidebarPresent {
		t.Error("a config with the block should report it present")
	}
	removed, ok := RemoveSidebarBlock(added)
	if !ok || strings.TrimSpace(removed) != strings.TrimSpace(user) {
		t.Errorf("remove = %v\n%q", ok, removed)
	}
}

func TestSidebarPlanLeavesTheUsersOwnRowsAlone(t *testing.T) {
	user := "[ui.sidebar.spaces]\nrows = [[\"workspace\"]]\n"
	if got := PlanSidebar(user); got != SidebarConflict {
		t.Errorf("plan = %v", got)
	}
	if got := PlanSidebar("[ui.sidebar]\nwidth = 30\n"); got != SidebarAdd {
		t.Errorf("other sidebar settings are no conflict: %v", got)
	}
}

func TestSidebarBlockCoexistsWithTheKeysBlock(t *testing.T) {
	cfg := AddBlock("", DefaultBindings)
	cfg = AddSidebarBlock(cfg)
	if _, err := toml.Decode(cfg, &map[string]any{}); err != nil {
		t.Fatalf("keys + sidebar blocks are not valid TOML: %v\n%s", err, cfg)
	}
	cfg, _ = RemoveBlock(cfg)
	if !strings.Contains(cfg, sidebarStart) {
		t.Error("removing the keys block removed the sidebar block")
	}
	cfg, _ = RemoveSidebarBlock(cfg)
	if strings.Contains(cfg, "tkt") {
		t.Errorf("leftovers:\n%s", cfg)
	}
}

func TestSidebarPlanTreatsInlineTablesAsTheUsers(t *testing.T) {
	for _, c := range []string{"[ui]\nsidebar = { width = 30 }\n", "ui = { accent = \"cyan\" }\n", "ui.sidebar = { width = 30 }\n"} {
		if got := PlanSidebar(c); got != SidebarConflict {
			t.Errorf("adding a table to an inline one breaks Herdr's strict TOML: %q → %v", c, got)
		}
	}
	if _, ok := RemoveSidebarBlock(sidebarStart + "\n[ui.sidebar.spaces]\nrows = []\n"); ok {
		t.Error("a block without its end marker must be left alone")
	}
}

func TestSidebarPlanUpdatesAnOlderManagedBlock(t *testing.T) {
	old := "[keys]\nprefix = \"ctrl+b\"\n\n" + sidebarStart + "\n[ui.sidebar.spaces]\nrows = [[\"workspace\"]]\n" + sidebarEnd + "\n"
	if got := PlanSidebar(old); got != SidebarOutdated {
		t.Fatalf("plan = %v", got)
	}
	updated := UpdateSidebarBlock(old)
	if PlanSidebar(updated) != SidebarPresent || strings.Count(updated, sidebarStart) != 1 || !strings.HasPrefix(updated, "[keys]") {
		t.Errorf("updated:\n%s", updated)
	}
}
