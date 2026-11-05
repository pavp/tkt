package cli

import (
	"context"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/pavp/tkt/internal/herdrport"
	"github.com/pavp/tkt/internal/workspace"
)

func TestStartInHerdrRunsTheAnswersGiven(t *testing.T) {
	tests := []struct {
		name  string
		opts  workspace.StartOptions
		setup func(t *testing.T) (*harness, func(t *testing.T))
	}{
		{
			name: "repositories and agent",
			opts: workspace.StartOptions{Repos: []string{"web"}, Agent: "opencode"},
			setup: func(t *testing.T) (*harness, func(t *testing.T)) {
				h, f, _ := herdrHarness(t)
				h.clone(t, "web")
				h.run.out["herdr agent start --help"] = herdrHelp("claude", "opencode")
				f.Results["workspace.create"] = `{"type":"workspace_created","workspace":{"workspace_id":"w9","label":"x"},"root_pane":{"pane_id":"w9:p1"},"tab":{}}`
				f.Results["pane.split"] = `{"type":"pane_info","pane":{"pane_id":"w9:p2","workspace_id":"w9"}}`
				return h, func(t *testing.T) {
					if start := f.Last("agent.start"); start["kind"] != "opencode" {
						t.Errorf("agent.start = %v", start)
					}
					if got := h.app.loadState().LastAgent; got != "opencode" {
						t.Errorf("last agent = %q", got)
					}
					if got := h.app.loadState().LastRepos["DEMO"]; !reflect.DeepEqual(got, []string{"example-org/web"}) {
						t.Errorf("last repos = %q", got)
					}
				}
			},
		},
		{
			name: "worktree",
			opts: workspace.StartOptions{Worktree: "/src/web-DEMO-7-b"},
			setup: func(t *testing.T) (*harness, func(t *testing.T)) {
				h, d := desktopHarness(t)
				h.clone(t, "web")
				h.run.out["git worktree list --porcelain"] = "worktree /src/web-DEMO-7-a\nHEAD a\nbranch refs/heads/feature/DEMO-7-a\n\n" +
					"worktree /src/web-DEMO-7-b\nHEAD b\nbranch refs/heads/feature/DEMO-7-b\n"
				d.wss = []herdrport.Workspace{{ID: "sp3", Label: "web-DEMO-7-a", Folder: "/src/web-DEMO-7-a"}, {ID: "sp4", Label: "web-DEMO-7-b", Folder: "/src/web-DEMO-7-b"}}
				d.panes = []herdrport.Pane{{ID: "t4", WorkspaceID: "sp4", Cwd: "/src/web-DEMO-7-b"}}
				return h, func(t *testing.T) {
					if !slices.Contains(d.calls, "start pi t4") || slices.ContainsFunc(d.calls, func(c string) bool { return strings.HasPrefix(c, "workspace ") }) {
						t.Errorf("calls = %q, want an agent beside the chosen worktree's shell t4, in its space", d.calls)
					}
				}
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h, check := tt.setup(t)
			if err := h.app.startInHerdr(context.Background(), "DEMO-7", tt.opts); err != nil {
				t.Fatalf("startInHerdr: %v\n%s", err, h.errOut)
			}
			check(t)
		})
	}
}

func TestAgentChoicesAreTheInstalledAgentsWithTheLastChosen(t *testing.T) {
	h := newHarness(t, baseConfig)
	h.run.out["herdr agent start --help"] = herdrHelp("pi", "claude", "codex")
	h.run.missing["codex"] = true
	st := h.app.loadState()
	st.LastAgent = "claude"
	h.app.saveState(st)
	kinds, last, err := h.app.agentChoices(context.Background())
	if want := []string{"pi", "claude"}; !reflect.DeepEqual(kinds, want) || last != "claude" || err != nil {
		t.Errorf("agentChoices = %q, %q, %v; want %q, claude, nil", kinds, last, err, want)
	}
	h.run.missing["herdr"] = true
	if kinds, _, err := h.app.agentChoices(context.Background()); err == nil || !strings.Contains(err.Error(), "not in PATH") || kinds != nil {
		t.Errorf("without Herdr: %q, %v", kinds, err)
	}
}
