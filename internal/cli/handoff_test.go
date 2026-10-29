package cli

import (
	"strings"
	"testing"
	"time"

	"github.com/pavp/tkt/internal/herdr/herdrtest"
)

const worktreeEvent = `{"event":"worktree.created","data":{"type":"worktree_created","workspace":{"workspace_id":"w9","label":"feature-demo-7-export"},"worktree":{"checkout_path":"/src/web-DEMO-7","branch":"feature/DEMO-7-export"}}}`

func handoffHarness(t *testing.T, ho handoff) (*harness, *herdrtest.FakeServer) {
	t.Helper()
	h, f, _ := herdrHarness(t)
	now := time.Date(2026, 10, 28, 12, 0, 0, 0, time.UTC)
	h.app.Now = func() time.Time { return now }
	if ho.At.IsZero() {
		ho.At = now.Add(-5 * time.Minute)
	}
	st := h.app.loadState()
	st.Handoffs = []handoff{ho}
	h.app.saveState(st)
	h.inCheckout("feature/DEMO-7-export", "/src/web-DEMO-7", "git@github.com:example-org/web.git")
	h.env["HERDR_PLUGIN_EVENT_JSON"] = worktreeEvent
	f.Results["agent.list"] = `{"type":"agent_list","agents":[{"pane_id":"w1:p2","workspace_id":"w1","name":"claude","agent":"claude","agent_status":"idle"}]}`
	f.Results["pane.list"] = `{"type":"pane_list","panes":[{"pane_id":"w1:p1","workspace_id":"w1"},{"pane_id":"w1:p2","workspace_id":"w1"},{"pane_id":"w9:p1","workspace_id":"w9"}]}`
	return h, f
}

func setupAgent() handoff {
	return handoff{Key: "DEMO-7", Summary: "Export users", Repo: "example-org/web", Clone: "/src/web", Pane: "w1:p2", Agent: "claude", Kind: "claude", Workspace: "w1", Host: "w1:p1"}
}

func TestOnWorktreeHandsTheTicketToAnAgentBornInTheWorktree(t *testing.T) {
	h, f := handoffHarness(t, setupAgent())
	if code := h.exec("herdr", "on-worktree"); code != 0 {
		t.Fatalf("exit %d: %s", code, h.errOut)
	}
	if start := f.Last("agent.start"); start["pane_id"] != "w9:p1" || start["kind"] != "claude" {
		t.Errorf("agent.start = %v", start)
	}
	prompt, _ := f.Last("agent.prompt")["text"].(string)
	for _, want := range []string{"Work on Jira ticket DEMO-7: Export users", "tkt context DEMO-7 --images .ticket-assets > .ticket.md", "copy from /src/web the files git ignores", "tkt link DEMO-7"} {
		if !strings.Contains(prompt, want) {
			t.Errorf("prompt missing %q:\n%s", want, prompt)
		}
	}
	if f.Last("pane.focus")["pane_id"] != "w9:p1" {
		t.Errorf("pane.focus = %v", f.Last("pane.focus"))
	}
	if f.Last("pane.close")["pane_id"] != "w1:p2" || f.Last("workspace.close")["workspace_id"] != "w1" {
		t.Errorf("pane.close = %v, workspace.close = %v", f.Last("pane.close"), f.Last("workspace.close"))
	}
	if st := h.app.loadState(); len(st.Handoffs) != 0 {
		t.Errorf("handoff kept: %+v", st.Handoffs)
	}
	if links, _ := h.app.links().Load(); len(links) != 1 || links[0].Key != "DEMO-7" {
		t.Errorf("links = %+v", links)
	}
}

func TestOnWorktreeClosesOnlyTheSetupPaneWhenItsWorkspaceIsNotTkts(t *testing.T) {
	tests := []struct {
		name  string
		ws    string
		host  string
		panes string
	}{
		{"split beside the user's pane", "", "", `[{"pane_id":"w1:p1","workspace_id":"w1"},{"pane_id":"w1:p2","workspace_id":"w1"},{"pane_id":"w9:p1","workspace_id":"w9"}]`},
		{"the user opened a pane in tkt's workspace", "w1", "w1:p1", `[{"pane_id":"w1:p1","workspace_id":"w1"},{"pane_id":"w1:p2","workspace_id":"w1"},{"pane_id":"w1:p3","workspace_id":"w1"},{"pane_id":"w9:p1","workspace_id":"w9"}]`},
		{"the user replaced tkt's first pane", "w1", "w1:p1", `[{"pane_id":"w1:p3","workspace_id":"w1"},{"pane_id":"w1:p2","workspace_id":"w1"},{"pane_id":"w9:p1","workspace_id":"w9"}]`},
		{"a handoff saved without its first pane", "w1", "", `[{"pane_id":"w1:p1","workspace_id":"w1"},{"pane_id":"w1:p2","workspace_id":"w1"},{"pane_id":"w9:p1","workspace_id":"w9"}]`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ho := setupAgent()
			ho.Workspace, ho.Host = tt.ws, tt.host
			h, f := handoffHarness(t, ho)
			f.Results["pane.list"] = `{"type":"pane_list","panes":` + tt.panes + `}`
			if code := h.exec("herdr", "on-worktree"); code != 0 {
				t.Fatalf("exit %d: %s", code, h.errOut)
			}
			if f.Last("pane.close")["pane_id"] != "w1:p2" || count(f, "workspace.close") != 0 {
				t.Errorf("pane.close = %v, workspace.close %d", f.Last("pane.close"), count(f, "workspace.close"))
			}
		})
	}
}

func TestOnWorktreeLeavesTheSetupAgentWhenTheHandoffFails(t *testing.T) {
	h, f := handoffHarness(t, setupAgent())
	f.Results["agent.list"] = `{"type":"agent_list","agents":[{"pane_id":"w1:p2","workspace_id":"w1","name":"claude"},{"pane_id":"w9:p1","workspace_id":"w9","name":"opencode"}]}`
	if code := h.exec("herdr", "on-worktree"); code != 0 {
		t.Fatalf("exit %d: %s", code, h.errOut)
	}
	if count(f, "agent.start") != 0 || count(f, "workspace.close") != 0 || count(f, "pane.close") != 0 {
		t.Errorf("acted on a failed handoff: %v", f.Requests)
	}
	body, _ := f.Last("notification.show")["body"].(string)
	if !strings.Contains(body, "DEMO-7") || !strings.Contains(body, "w1:p2") {
		t.Errorf("notification = %v", f.Last("notification.show"))
	}
	if !strings.Contains(h.errOut.String(), "could not hand DEMO-7 to an agent in its worktree") {
		t.Errorf("stderr = %q", h.errOut)
	}
	if st := h.app.loadState(); len(st.Handoffs) != 0 {
		t.Errorf("handoff kept: %+v", st.Handoffs)
	}
}

func TestOnWorktreeOnlyLinksWithoutAMatchingHandoff(t *testing.T) {
	tests := []struct {
		name string
		ho   func(*handoff)
		kept int
	}{
		{"another repository", func(ho *handoff) { ho.Repo = "example-org/api" }, 1},
		{"another ticket", func(ho *handoff) { ho.Key = "DEMO-8" }, 1},
		{"older than an hour", func(ho *handoff) { ho.At = time.Date(2026, 10, 28, 10, 0, 0, 0, time.UTC) }, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ho := setupAgent()
			tt.ho(&ho)
			h, f := handoffHarness(t, ho)
			if code := h.exec("herdr", "on-worktree"); code != 0 {
				t.Fatalf("exit %d: %s", code, h.errOut)
			}
			if count(f, "agent.start") != 0 || count(f, "workspace.close") != 0 || count(f, "pane.close") != 0 {
				t.Errorf("handed off: %v", f.Requests)
			}
			if links, _ := h.app.links().Load(); len(links) != 1 {
				t.Errorf("links = %+v", links)
			}
			if st := h.app.loadState(); len(st.Handoffs) != tt.kept {
				t.Errorf("handoffs = %+v", st.Handoffs)
			}
		})
	}
}

func TestStartThenWorktreeLeavesOneAgentInTheWorktree(t *testing.T) {
	h, f, _ := herdrHarness(t)
	web := h.clone(t, "web")
	f.Results["workspace.create"] = `{"type":"workspace_created","workspace":{"workspace_id":"w1","label":"x"},"root_pane":{"pane_id":"w1:p1"}}`
	f.Results["pane.split"] = `{"type":"pane_info","pane":{"pane_id":"w1:p2","workspace_id":"w1"}}`
	if code := h.exec("herdr", "start", "DEMO-7", "--repo", "web"); code != 0 {
		t.Fatalf("exit %d: %s", code, h.errOut)
	}
	f.Results["agent.list"] = `{"type":"agent_list","agents":[{"pane_id":"w1:p2","workspace_id":"w1","name":"claude"}]}`
	f.Results["pane.list"] = `{"type":"pane_list","panes":[{"pane_id":"w1:p1","workspace_id":"w1"},{"pane_id":"w9:p1","workspace_id":"w9"}]}`
	h.inCheckout("feature/DEMO-7-export", "/src/web-DEMO-7", "git@github.com:example-org/web.git")
	h.env["HERDR_PLUGIN_EVENT_JSON"] = worktreeEvent
	if code := h.exec("herdr", "on-worktree"); code != 0 {
		t.Fatalf("exit %d: %s", code, h.errOut)
	}
	if start := f.Last("agent.start"); start["pane_id"] != "w9:p1" {
		t.Errorf("agent.start = %v", start)
	}
	if text, _ := f.Last("agent.prompt")["text"].(string); !strings.Contains(text, "copy from "+web) {
		t.Errorf("prompt = %q", text)
	}
	if f.Last("pane.close")["pane_id"] != "w1:p2" || f.Last("workspace.close")["workspace_id"] != "w1" {
		t.Errorf("pane.close = %v, workspace.close = %v", f.Last("pane.close"), f.Last("workspace.close"))
	}
}

func TestOnWorktreeKeepsTheSetupAgentWhenTheNewAgentGetsNoPrompt(t *testing.T) {
	h, f := handoffHarness(t, setupAgent())
	f.Errors["agent.prompt"] = "agent_not_ready: agent claude is not an active named agent"
	if code := h.exec("herdr", "on-worktree"); code != 0 {
		t.Fatalf("exit %d: %s", code, h.errOut)
	}
	if count(f, "agent.start") != 1 || count(f, "workspace.close") != 0 || count(f, "pane.close") != 0 {
		t.Errorf("closed the setup agent: %v", f.Requests)
	}
	if body, _ := f.Last("notification.show")["body"].(string); !strings.Contains(body, "w9:p1 did not get its prompt") || !strings.Contains(body, "w1:p2") {
		t.Errorf("notification = %v", f.Last("notification.show"))
	}
}

func TestOnWorktreeTellsTheUserWhenTktsWorkspaceStays(t *testing.T) {
	h, f := handoffHarness(t, setupAgent())
	f.Errors["workspace.close"] = "workspace_busy"
	if code := h.exec("herdr", "on-worktree"); code != 0 {
		t.Fatalf("exit %d: %s", code, h.errOut)
	}
	body, _ := f.Last("notification.show")["body"].(string)
	if f.Last("pane.close")["pane_id"] != "w1:p2" || !strings.Contains(body, "its workspace w1 stays") {
		t.Errorf("pane.close = %v, notification = %q", f.Last("pane.close"), body)
	}
}

func TestOnWorktreeTellsTheUserWhenTheSetupAgentCannotBeClosed(t *testing.T) {
	h, f := handoffHarness(t, setupAgent())
	f.Errors["pane.close"] = "pane_busy"
	if code := h.exec("herdr", "on-worktree"); code != 0 {
		t.Fatalf("exit %d: %s", code, h.errOut)
	}
	if body, _ := f.Last("notification.show")["body"].(string); !strings.Contains(body, "could not close the setup agent of DEMO-7 in pane w1:p2") {
		t.Errorf("notification = %v", f.Last("notification.show"))
	}
}

func TestEventWorkspaceIsTheWorkspaceTheEventOpened(t *testing.T) {
	raw := `{"event":"worktree.created","data":{"worktree":{"source":{"workspace":{"workspace_id":"w1"}},"checkout_path":"/x"},"workspace":{"workspace_id":"w9"}}}`
	for range 50 {
		if got := eventWorkspace(raw); got != "w9" {
			t.Fatalf("eventWorkspace = %q", got)
		}
	}
}

func TestOnWorktreeWaitsForTheNewAgentsQuestionBeforeItsPrompt(t *testing.T) {
	h, f := handoffHarness(t, setupAgent())
	f.FailOnce("agent.prompt", "agent_blocked: agent claude is blocked and requires interactive input")
	if code := h.exec("herdr", "on-worktree"); code != 0 {
		t.Fatalf("exit %d: %s", code, h.errOut)
	}
	if wait := f.Last("agent.wait"); wait["target"] != "claude" {
		t.Errorf("agent.wait = %v", wait)
	}
	if count(f, "agent.prompt") != 2 || count(f, "notification.show") != 0 {
		t.Errorf("agent.prompt %d, notifications %d", count(f, "agent.prompt"), count(f, "notification.show"))
	}
	if f.Last("workspace.close")["workspace_id"] != "w1" {
		t.Errorf("setup agent not closed: %v", f.Requests)
	}
}

func TestOnWorktreeKeepsTheSetupAgentWhenTheNewAgentsQuestionIsNeverAnswered(t *testing.T) {
	h, f := handoffHarness(t, setupAgent())
	f.Errors["agent.prompt"] = "agent_blocked: agent claude is blocked and requires interactive input"
	f.Errors["agent.wait"] = "timeout"
	if code := h.exec("herdr", "on-worktree"); code != 0 {
		t.Fatalf("exit %d: %s", code, h.errOut)
	}
	if count(f, "pane.close") != 0 || count(f, "workspace.close") != 0 {
		t.Errorf("closed the setup agent: %v", f.Requests)
	}
	if body, _ := f.Last("notification.show")["body"].(string); !strings.Contains(body, "w1:p2") {
		t.Errorf("notification = %v", f.Last("notification.show"))
	}
	if st := h.app.loadState(); len(st.Handoffs) != 0 {
		t.Errorf("handoff kept: %+v", st.Handoffs)
	}
}
