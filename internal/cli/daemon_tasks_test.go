package cli

import (
	"context"
	"errors"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/pavp/tkt/internal/daemon"
	"github.com/pavp/tkt/internal/herdr/herdrtest"
	"github.com/pavp/tkt/internal/ticket"
)

// daemonTasks is the agents harness as the detached daemon sees it: no Herdr terminal in its environment, both
// Herdrs running, Herdr 0.9.5 faked by f and Herdr desktop refusing every call.
func daemonTasks(t *testing.T) (*harness, *herdrtest.FakeServer) {
	t.Helper()
	_, h, f := agentsHarness(t)
	delete(h.env, "HERDR_ENV")
	classic := h.app.NewHerdr()
	h.app.NewHerdr = func() HerdrAPI { return failingHerdr{errors.New("the daemon must use the Herdr the caller named")} }
	h.app.HerdrRunning = func(context.Context) (bool, bool) { return true, true }
	h.app.herdrOf = func(fl herdrFlavor) HerdrAPI {
		if fl == herdrClassic {
			return classic
		}
		return failingHerdr{errors.New("Herdr desktop must not be used")}
	}
	h.run.out["herdr agent start --help"] = herdrHelp("claude", "opencode")
	return h, f
}

func herdrWrites(f *herdrtest.FakeServer) int {
	n := 0
	for _, m := range []string{"agent.prompt", "agent.start", "pane.split", "workspace.create", "pane.focus", "workspace.focus", "pane.report_metadata"} {
		n += count(f, m)
	}
	return n
}

func TestADaemonTaskListsTheAgentsOfTheHerdrNamed(t *testing.T) {
	h, _ := daemonTasks(t)
	pr := ticket.PullRequest{Repo: "example-org/web", Number: 820}
	got, err := h.app.daemonTaskAgents(context.Background(), daemon.Task{Review: &pr}, "classic")
	if err != nil {
		t.Fatal(err)
	}
	var order []string
	for _, ag := range got.Agents {
		order = append(order, ag.Name+" "+ag.Status)
	}
	if strings.Join(order, ", ") != "claude idle, claude-2 done, claude-3 working" || got.Phrase != "the review of #820" {
		t.Errorf("agents %v, phrase %q", order, got.Phrase)
	}
	if a := got.Agents[0]; a.Pane != "w1:p1" || a.Repo != "example-org/web" || a.Ticket != "DEMO-2" || a.Workspace != "web" || !a.SameRepo {
		t.Errorf("first agent %+v", a)
	}
	if !slices.Equal(got.Kinds, []string{"claude", "opencode"}) || got.KindsError != "" {
		t.Errorf("kinds %v %q", got.Kinds, got.KindsError)
	}
}

func TestADaemonTaskGoesOnlyToAnOpenAgent(t *testing.T) {
	pr := ticket.PullRequest{Repo: "example-org/web", Number: 820, URL: "https://github.com/example-org/web/pull/820"}
	for _, pane := range []string{"w4:p1", "w9:p9", "w1:p1; rm -rf ~"} {
		t.Run(pane, func(t *testing.T) {
			h, f := daemonTasks(t)
			err := h.app.daemonTask(context.Background(), daemon.Task{Review: &pr}, daemon.TaskRequest{Herdr: "classic", To: "open", Pane: pane}, func() {})
			if err == nil || !strings.Contains(err.Error(), "is not an agent open in Herdr") || herdrWrites(f) != 0 {
				t.Errorf("daemonTask = %v, %d Herdr writes: a pane without an agent never gets the prompt", err, herdrWrites(f))
			}
		})
	}
	h, f := daemonTasks(t)
	if err := h.app.daemonTask(context.Background(), daemon.Task{Review: &pr}, daemon.TaskRequest{Herdr: "classic", To: "open", Pane: "w2:p1"}, func() {}); err != nil {
		t.Fatal(err)
	}
	if got := f.Last("agent.prompt"); got["target"] != "w2:p1" || !strings.Contains(got["text"].(string), "Review pull request https://github.com/example-org/web/pull/820") {
		t.Errorf("prompt %v", got)
	}
	if f.Last("pane.focus")["pane_id"] != "w2:p1" {
		t.Errorf("the agent is brought forward: %v", f.Last("pane.focus"))
	}
}

func TestADaemonTaskOpensOnlyAnAgentKind(t *testing.T) {
	pr := ticket.PullRequest{Repo: "example-org/cli", Number: 7, URL: "https://github.com/example-org/cli/pull/7"}
	for _, agent := range []string{"/bin/sh", "sh", "osascript", "--help", "$(id)", "pi"} {
		t.Run(agent, func(t *testing.T) {
			h, f := daemonTasks(t)
			f.Results["agent.list"] = `{"type":"agent_list","agents":[]}`
			settled := false
			err := h.app.daemonTask(context.Background(), daemon.Task{Review: &pr}, daemon.TaskRequest{Herdr: "classic", To: "new", Agent: agent}, func() { settled = true })
			if err == nil || !strings.Contains(err.Error(), "is not an agent kind") || settled || herdrWrites(f) != 0 {
				t.Errorf("daemonTask = %v, settled %v, %d Herdr writes", err, settled, herdrWrites(f))
			}
		})
	}
}

func TestADaemonTaskNamesTheAgentKindItNeeds(t *testing.T) {
	h, f := daemonTasks(t)
	f.Results["agent.list"] = `{"type":"agent_list","agents":[]}`
	f.Results["pane.list"] = `{"type":"pane_list","panes":[]}`
	pr := ticket.PullRequest{Repo: "example-org/cli", Number: 7, URL: "https://github.com/example-org/cli/pull/7"}
	settled := false
	err := h.app.daemonTask(context.Background(), daemon.Task{Review: &pr}, daemon.TaskRequest{Herdr: "classic", To: "new"}, func() { settled = true })
	if need := (daemon.NeedsAnswerError{}); !errors.As(err, &need) || need.Need != "agent" || settled || herdrWrites(f) != 0 {
		t.Errorf("daemonTask = %v, settled %v, %d Herdr writes, want it to need the agent first", err, settled, herdrWrites(f))
	}
}

func TestADaemonTaskOpensANewAgentForTheReview(t *testing.T) {
	h, f := daemonTasks(t)
	f.Results["agent.list"] = `{"type":"agent_list","agents":[]}`
	f.Results["pane.list"] = `{"type":"pane_list","panes":[]}`
	f.Results["workspace.create"] = `{"type":"workspace_created","workspace":{"workspace_id":"w9","label":"x"},"root_pane":{"pane_id":"w9:p1"}}`
	pr := ticket.PullRequest{Repo: "example-org/cli", Number: 7, URL: "https://github.com/example-org/cli/pull/7"}
	settled := false
	err := h.app.daemonTask(context.Background(), daemon.Task{Review: &pr}, daemon.TaskRequest{Herdr: "classic", To: "new", Agent: "opencode"}, func() { settled = true })
	if err != nil || !settled {
		t.Fatalf("daemonTask = %v, settled %v", err, settled)
	}
	if f.Last("agent.start")["kind"] != "opencode" || f.Last("workspace.create")["cwd"] != filepath.Join(h.app.Env.DataDir(), "reviews") {
		t.Errorf("start %v in %v", f.Last("agent.start"), f.Last("workspace.create"))
	}
	if text, _ := f.Last("agent.prompt")["text"].(string); !strings.Contains(text, "Review pull request https://github.com/example-org/cli/pull/7") {
		t.Errorf("prompt %q", text)
	}
}

func TestADaemonTaskAsksBeforeCloning(t *testing.T) {
	no, yes := false, true
	h, f := daemonTasks(t)
	h.clone(t, "web")
	f.Results["agent.list"] = `{"type":"agent_list","agents":[]}`
	f.Results["pane.list"] = `{"type":"pane_list","panes":[]}`
	f.Results["workspace.create"] = `{"type":"workspace_created","workspace":{"workspace_id":"w9","label":"x"},"root_pane":{"pane_id":"w9:p1"}}`
	c, err := h.app.LoadConfig()
	if err != nil {
		t.Fatal(err)
	}
	root := c.RepoRoots[0]
	dest := filepath.Join(root, "api")
	fix := daemon.Task{Key: "DEMO-1"}
	settled := false
	err = h.app.daemonTask(context.Background(), fix, daemon.TaskRequest{Herdr: "classic", To: "new", Agent: "claude"}, func() { settled = true })
	if need := (daemon.NeedsAnswerError{}); !errors.As(err, &need) || need.Need != "clone" || need.Repo != "example-org/api" || need.Into != root {
		t.Fatalf("daemonTask = %v, want it to ask to clone example-org/api into %s", err, root)
	}
	if settled || herdrWrites(f) != 0 || slices.ContainsFunc(h.run.calls, func(c string) bool { return strings.HasPrefix(c, "gh repo clone") }) {
		t.Errorf("settled %v, %d Herdr writes, calls %v: nothing runs before the answer", settled, herdrWrites(f), h.run.calls)
	}
	err = h.app.daemonTask(context.Background(), fix, daemon.TaskRequest{Herdr: "classic", To: "new", Agent: "claude", Clone: &no}, func() { settled = true })
	if err == nil || !strings.Contains(err.Error(), "example-org/api is not cloned locally") || settled {
		t.Errorf("without cloning = %v, settled %v", err, settled)
	}
	h.run.out["gh repo clone example-org/api "+dest] = ""
	h.run.out["git status --porcelain"] = ""
	h.run.out["git rev-parse --abbrev-ref HEAD"] = "main"
	h.run.out["git symbolic-ref --short refs/remotes/origin/HEAD"] = "origin/main"
	if err := h.app.daemonTask(context.Background(), fix, daemon.TaskRequest{Herdr: "classic", To: "new", Agent: "claude", Clone: &yes}, func() { settled = true }); err != nil || !settled {
		t.Fatalf("cloning = %v, settled %v", err, settled)
	}
	if !slices.Contains(h.run.calls, "gh repo clone example-org/api "+dest) || f.Last("workspace.create")["cwd"] != dest {
		t.Errorf("calls %v, create %v", h.run.calls, f.Last("workspace.create"))
	}
	if f.Last("pane.report_metadata") == nil {
		t.Error("the fix's agent is marked with its ticket")
	}
}

func TestADaemonTaskSendsTheFeedbackToTheTicketsOwnAgent(t *testing.T) {
	h, f := daemonTasks(t)
	h.clone(t, "web")
	h.run.out["git worktree list --porcelain"] = "worktree /src/web-DEMO-1\nHEAD a\nbranch refs/heads/feature/DEMO-1-filter\n"
	f.Results["workspace.list"] = `{"type":"workspace_list","workspaces":[{"workspace_id":"w1","worktree":{"checkout_path":"/src/web-DEMO-1"}},{"workspace_id":"w2"}]}`
	f.Results["agent.list"] = `{"type":"agent_list","agents":[{"pane_id":"w2:p1","workspace_id":"w2","agent":"claude"},{"pane_id":"w1:p2","workspace_id":"w1","agent":"claude"}]}`
	if err := h.app.daemonTask(context.Background(), daemon.Task{Key: "DEMO-1"}, daemon.TaskRequest{Herdr: "classic", To: "own"}, func() {}); err != nil {
		t.Fatal(err)
	}
	if got := f.Last("agent.prompt"); got["target"] != "w1:p2" || !strings.Contains(got["text"].(string), `CI check "unit-tests" failed`) {
		t.Errorf("prompt = %v", got)
	}
	if f.Last("pane.focus")["pane_id"] != "w1:p2" {
		t.Errorf("the agent is brought forward: %v", f.Last("pane.focus"))
	}
}
