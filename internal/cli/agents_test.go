package cli

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/pavp/tkt/internal/herdr/herdrtest"
	"github.com/pavp/tkt/internal/ticket"
	"github.com/pavp/tkt/internal/tui"
)

func agentsHarness(t *testing.T) (*boardActions, *harness, *herdrtest.FakeServer) {
	t.Helper()
	h, _, f := fixHarness(t)
	h.env["HERDR_ENV"] = "1"
	h.app.Env.GOOS = "darwin"
	f.Results["agent.list"] = `{"type":"agent_list","agents":[
 {"pane_id":"w3:p1","workspace_id":"w3","agent":"claude","name":"claude-3","agent_status":"working","cwd":"/src/web-DEMO-2"},
 {"pane_id":"w2:p1","workspace_id":"w2","agent":"claude","name":"claude-2","agent_status":"done","cwd":"/src/api"},
 {"pane_id":"w4:p1","workspace_id":"w4","agent":"","agent_status":"unknown","cwd":"/src/web"},
 {"pane_id":"w1:p1","workspace_id":"w1","agent":"claude","name":"claude","agent_status":"idle","cwd":"/src/web"}]}`
	h.run.out["remote /src/web"] = "git@github.com:example-org/web.git"
	h.run.out["remote /src/web-DEMO-2"] = "git@github.com:example-org/web.git"
	h.run.out["remote /src/api"] = "git@github.com:example-org/api.git"
	h.run.out["pbcopy"] = ""
	h.run.out["git rev-parse --abbrev-ref HEAD"] = "feature/DEMO-2-pager"
	f.Results["workspace.list"] = `{"type":"workspace_list","workspaces":[{"workspace_id":"w1","label":"web"},{"workspace_id":"w3","label":"DEMO-2 · web"}]}`
	c, err := h.app.LoadConfig()
	if err != nil {
		t.Fatal(err)
	}
	return &boardActions{a: h.app, c: c, ctx: context.Background()}, h, f
}

func TestOpenAgentsPutTheFreeOnesOfTheRepositoryFirst(t *testing.T) {
	b, _, _ := agentsHarness(t)
	review := tui.AgentTask{Review: &ticket.PullRequest{Repo: "example-org/web", Number: 820}}
	got, ok := b.OpenAgents(review)().(tui.AgentsListedMsg)
	if !ok || got.Err != nil {
		t.Fatalf("msg %+v", got)
	}
	var order []string
	for _, ag := range got.Agents {
		order = append(order, ag.Name+" "+ag.Status)
	}
	if strings.Join(order, ", ") != "claude idle, claude-2 done, claude-3 working" {
		t.Errorf("free in the repository, free elsewhere, then working; panes without an agent left out: %v", order)
	}
	if a := got.Agents[0]; a.Repo != "example-org/web" || a.Branch != "feature/DEMO-2-pager" || a.Ticket != "DEMO-2" || a.Workspace != "web" {
		t.Errorf("each agent says what it works on: %+v", a)
	}
	if !got.Agents[0].SameRepo || got.Agents[1].SameRepo || got.Agents[0].Pane != "w1:p1" || got.Agents[0].Folder != "/src/web" {
		t.Errorf("agents %+v", got.Agents)
	}
}

func TestSendTaskPromptsTheAgentWithTheReview(t *testing.T) {
	b, _, f := agentsHarness(t)
	pr := ticket.PullRequest{Repo: "example-org/web", Number: 820, Title: "Add pagination", Author: "alex", URL: "https://github.com/example-org/web/pull/820"}
	if _, ok := b.SendTask(tui.AgentTask{Review: &pr}, "w3:p1")().(tea.QuitMsg); !ok {
		t.Fatal("sending focuses the agent and closes the board")
	}
	got := f.Last("agent.prompt")
	text, _ := got["text"].(string)
	for _, want := range []string{"Review pull request https://github.com/example-org/web/pull/820", "example-org/web#820", `"Add pagination" by @alex`, "report", "Do not comment on, approve or request changes"} {
		if !strings.Contains(text, want) {
			t.Errorf("prompt lacks %q:\n%s", want, text)
		}
	}
	if got["target"] != "w3:p1" || f.Last("pane.focus")["pane_id"] != "w3:p1" {
		t.Errorf("prompt %v, focus %v", got, f.Last("pane.focus"))
	}
}

func TestSendTaskPromptsTheAgentWithTheFeedback(t *testing.T) {
	b, _, f := agentsHarness(t)
	if _, ok := b.SendTask(tui.AgentTask{Key: "DEMO-1"}, "w2:p1")().(tea.QuitMsg); !ok {
		t.Fatal("sending focuses the agent and closes the board")
	}
	if text, _ := f.Last("agent.prompt")["text"].(string); !strings.Contains(text, `CI check "unit-tests" failed`) || f.Last("agent.prompt")["target"] != "w2:p1" {
		t.Errorf("prompt %v", f.Last("agent.prompt"))
	}
}

func TestCopyTaskPrompt(t *testing.T) {
	b, h, _ := agentsHarness(t)
	pr := ticket.PullRequest{Repo: "example-org/web", Number: 820, URL: "https://github.com/example-org/web/pull/820"}
	if got := b.CopyTaskPrompt(tui.AgentTask{Review: &pr})(); got != tui.StatusMsg("✓ copied the review prompt") {
		t.Errorf("status %v", got)
	}
	if in := h.run.stdin["pbcopy"]; !strings.Contains(in, "Review pull request https://github.com/example-org/web/pull/820") {
		t.Errorf("copied %q", in)
	}
	if got := b.CopyTaskPrompt(tui.AgentTask{Key: "DEMO-1"})(); got != tui.StatusMsg("✓ copied the feedback prompt") {
		t.Errorf("status %v", got)
	}
}

func TestNewAgentPlacesTheReview(t *testing.T) {
	tests := []struct {
		name, agents, panes string
		wantPrompt          string
		wantSplit, wantNew  bool
	}{
		{name: "a free agent in the clone takes it", agents: `[{"pane_id":"w5:p1","workspace_id":"w5","agent":"claude","name":"claude-5","agent_status":"done","cwd":"WEB"}]`, panes: `[]`, wantPrompt: "w5:p1"},
		{name: "a busy clone gets a pane beside", agents: `[{"pane_id":"w5:p1","workspace_id":"w5","agent":"claude","agent_status":"working","cwd":"WEB"}]`, panes: `[{"pane_id":"w5:p1","workspace_id":"w5","cwd":"WEB"}]`, wantSplit: true, wantPrompt: "claude"},
		{name: "a shell in the clone is left alone", agents: `[]`, panes: `[{"pane_id":"w6:p1","workspace_id":"w6","cwd":"WEB"}]`, wantSplit: true, wantPrompt: "claude"},
		{name: "nothing open there opens a workspace", agents: `[]`, panes: `[]`, wantNew: true, wantPrompt: "claude"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b, h, f := agentsHarness(t)
			web := h.clone(t, "web")
			f.Results["agent.list"] = `{"type":"agent_list","agents":` + strings.ReplaceAll(tt.agents, "WEB", web) + `}`
			f.Results["pane.list"] = `{"type":"pane_list","panes":` + strings.ReplaceAll(tt.panes, "WEB", web) + `}`
			f.Results["pane.split"] = `{"type":"pane_info","pane":{"pane_id":"w5:p9","workspace_id":"w5","cwd":"` + web + `"}}`
			f.Results["workspace.create"] = `{"type":"workspace_created","workspace":{"workspace_id":"w9","label":"x"},"root_pane":{"pane_id":"w9:p1"}}`
			pr := ticket.PullRequest{Repo: "example-org/web", Number: 820, URL: "https://github.com/example-org/web/pull/820"}
			if msg := b.NewAgentTask(tui.AgentTask{Review: &pr})(); msg != (tea.QuitMsg{}) {
				t.Fatalf("msg %v", msg)
			}
			if got := f.Last("agent.prompt"); got["target"] != tt.wantPrompt || !strings.Contains(got["text"].(string), "Review pull request") {
				t.Errorf("prompt %v", got)
			}
			if (count(f, "pane.split") > 0) != tt.wantSplit || (count(f, "workspace.create") > 0) != tt.wantNew {
				t.Errorf("split %d, created %d", count(f, "pane.split"), count(f, "workspace.create"))
			}
			if tt.wantSplit && (f.Last("pane.split")["cwd"] != web || f.Last("agent.start")["pane_id"] != "w5:p9") {
				t.Errorf("split %v, start %v", f.Last("pane.split"), f.Last("agent.start"))
			}
			if tt.wantNew && f.Last("workspace.create")["cwd"] != web {
				t.Errorf("create %v", f.Last("workspace.create"))
			}
		})
	}
}

func TestNewAgentWorksOnTheFixWhereItCan(t *testing.T) {
	for _, dirty := range []bool{false, true} {
		b, h, f := agentsHarness(t)
		h.clone(t, "web")
		api := h.clone(t, "api")
		f.Results["agent.list"] = `{"type":"agent_list","agents":[]}`
		f.Results["pane.list"] = `{"type":"pane_list","panes":[]}`
		f.Results["workspace.create"] = `{"type":"workspace_created","workspace":{"workspace_id":"w9","label":"x"},"root_pane":{"pane_id":"w9:p1"}}`
		h.run.out["git status --porcelain"] = ""
		if dirty {
			h.run.out["git status --porcelain"] = " M web/form.go"
		}
		h.run.out["git rev-parse --abbrev-ref HEAD"] = "main"
		h.run.out["git symbolic-ref --short refs/remotes/origin/HEAD"] = "origin/main"
		if msg := b.NewAgentTask(tui.AgentTask{Key: "DEMO-1"})(); msg != (tea.QuitMsg{}) {
			t.Fatalf("dirty %v: msg %v", dirty, msg)
		}
		text, _ := f.Last("agent.prompt")["text"].(string)
		want := "check out its branch feature/DEMO-1-filter"
		if dirty {
			want = "create a worktree for its branch feature/DEMO-1-filter"
		}
		if !strings.Contains(text, `CI check "unit-tests" failed`) || !strings.Contains(text, want) || f.Last("workspace.create")["cwd"] != api {
			t.Errorf("dirty %v: prompt %q in %v", dirty, text, f.Last("workspace.create"))
		}
	}
}

func TestNewAgentReviewsWithoutAClone(t *testing.T) {
	b, h, f := agentsHarness(t)
	f.Results["agent.list"] = `{"type":"agent_list","agents":[]}`
	f.Results["pane.list"] = `{"type":"pane_list","panes":[]}`
	f.Results["workspace.create"] = `{"type":"workspace_created","workspace":{"workspace_id":"w9","label":"x"},"root_pane":{"pane_id":"w9:p1"}}`
	pr := ticket.PullRequest{Repo: "example-org/cli", Number: 7, URL: "https://github.com/example-org/cli/pull/7"}
	if msg := b.NewAgentTask(tui.AgentTask{Review: &pr})(); msg != (tea.QuitMsg{}) {
		t.Fatalf("msg %v", msg)
	}
	want := filepath.Join(h.app.Env.DataDir(), "reviews")
	if got := f.Last("workspace.create")["cwd"]; got != want {
		t.Errorf("a review without a clone runs in %v, want %s", got, want)
	}
	if fi, err := os.Stat(want); err != nil || !fi.IsDir() || fi.Mode().Perm() != 0o700 {
		t.Errorf("reviews folder: %v %v", fi, err)
	}
	if text, _ := f.Last("agent.prompt")["text"].(string); !strings.Contains(text, "no local clone of example-org/cli") {
		t.Errorf("prompt %q", text)
	}
}

func TestNewAgentAsksToCloneForAFix(t *testing.T) {
	b, h, f := agentsHarness(t)
	h.clone(t, "web")
	f.Results["agent.list"] = `{"type":"agent_list","agents":[]}`
	f.Results["pane.list"] = `{"type":"pane_list","panes":[]}`
	f.Results["workspace.create"] = `{"type":"workspace_created","workspace":{"workspace_id":"w9","label":"x"},"root_pane":{"pane_id":"w9:p1"}}`
	got, ok := b.NewAgentTask(tui.AgentTask{Key: "DEMO-1"})().(tui.CloneNeededMsg)
	root := b.cfg().RepoRoots[0]
	if !ok || got.Repo != "example-org/api" || got.Into != root {
		t.Fatalf("msg %+v", got)
	}
	if count(f, "workspace.create") != 0 {
		t.Error("nothing starts before the clone")
	}
	dest := filepath.Join(root, "api")
	h.run.out["gh repo clone example-org/api "+dest] = ""
	h.run.out["git status --porcelain"] = ""
	h.run.out["git rev-parse --abbrev-ref HEAD"] = "main"
	h.run.out["git symbolic-ref --short refs/remotes/origin/HEAD"] = "origin/main"
	if msg := b.CloneAndStart(got.Task, got.Repo)(); msg != (tea.QuitMsg{}) {
		t.Fatalf("msg %v", msg)
	}
	if !slices.Contains(h.run.calls, "gh repo clone example-org/api "+dest) || f.Last("workspace.create")["cwd"] != dest {
		t.Errorf("calls %v, create %v", h.run.calls, f.Last("workspace.create"))
	}
}
