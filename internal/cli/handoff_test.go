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
	f.Results["pane.list"] = `{"type":"pane_list","panes":[{"pane_id":"w1:p2","workspace_id":"w1"},{"pane_id":"w9:p1","workspace_id":"w9"}]}`
	return h, f
}

func setupAgent() handoff {
	return handoff{Key: "DEMO-7", Summary: "Export users", Repo: "example-org/web", Clone: "/src/web", Pane: "w1:p2", Agent: "claude", Kind: "claude", Workspace: "w1"}
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
	if f.Last("workspace.close")["workspace_id"] != "w1" || count(f, "pane.close") != 0 {
		t.Errorf("workspace.close = %v, pane.close %d", f.Last("workspace.close"), count(f, "pane.close"))
	}
	if st := h.app.loadState(); len(st.Handoffs) != 0 {
		t.Errorf("handoff kept: %+v", st.Handoffs)
	}
	if links, _ := h.app.links().Load(); len(links) != 1 || links[0].Key != "DEMO-7" {
		t.Errorf("links = %+v", links)
	}
}

func TestOnWorktreeClosesOnlyTheSetupPaneWhenItsWorkspaceIsNotTktsAlone(t *testing.T) {
	tests := []struct {
		name  string
		ws    string
		panes string
	}{
		{"split beside the user's pane", "", `[{"pane_id":"w1:p1","workspace_id":"w1"},{"pane_id":"w1:p2","workspace_id":"w1"},{"pane_id":"w9:p1","workspace_id":"w9"}]`},
		{"the user opened a pane in tkt's workspace", "w1", `[{"pane_id":"w1:p2","workspace_id":"w1"},{"pane_id":"w1:p3","workspace_id":"w1"},{"pane_id":"w9:p1","workspace_id":"w9"}]`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ho := setupAgent()
			ho.Workspace = tt.ws
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
