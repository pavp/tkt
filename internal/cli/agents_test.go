package cli

import (
	"context"
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
