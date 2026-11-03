package cli

import (
	"errors"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/pavp/tkt/internal/herdrport"
)

type detachedCall struct {
	name string
	args []string
}

// desktopHandoffHarness is a setup agent (claude in t9) that created the worktree of DEMO-7 and runs `tkt link`
// there, in a Herdr desktop terminal.
func desktopHandoffHarness(t *testing.T, ho *handoff) (*harness, *desktopHerdr, *[]detachedCall) {
	t.Helper()
	h, d := desktopHarness(t)
	h.env["HC_TERMINAL_ID"] = "t9"
	now := time.Date(2026, 10, 28, 12, 0, 0, 0, time.UTC)
	h.app.Now = func() time.Time { return now }
	h.app.Executable = func() (string, error) { return "/opt/tkt/bin/tkt", nil }
	var detached []detachedCall
	h.app.StartDetached = func(name string, args []string, _ string) (int, error) {
		detached = append(detached, detachedCall{name, args})
		return 42, nil
	}
	if ho != nil {
		ho.At = now.Add(-5 * time.Minute)
		st := h.app.loadState()
		st.Handoffs = []handoff{*ho}
		h.app.saveState(st)
	}
	d.agents = []herdrport.Agent{{PaneID: "t9", WorkspaceID: "sp1", Agent: "claude", Name: "claude", Status: "idle"}}
	h.inCheckout("feature/DEMO-7-export", "/src/web-DEMO-7", "git@github.com:example-org/web.git")
	return h, d, &detached
}

func desktopSetupAgent() *handoff {
	return &handoff{Key: "DEMO-7", Summary: "Export users", Repo: "example-org/web", Clone: "/src/web", Pane: "t9", Agent: "claude", Kind: "claude", Desktop: true}
}

func TestHerdrStartOnDesktopAsksTheSetupAgentForGitAndTktLink(t *testing.T) {
	h, d := desktopHarness(t)
	h.clone(t, "web")
	if code := h.exec("herdr", "start", "DEMO-7", "--repo", "web", "--agent", "claude"); code != 0 {
		t.Fatalf("exit %d: %s", code, h.errOut)
	}
	if len(d.texts) != 1 {
		t.Fatalf("prompts = %q", d.texts)
	}
	p := d.texts[0]
	for _, want := range []string{"Prepare Jira ticket DEMO-7: Export users", "must contain DEMO-7", "Create the worktree with git", "~/.herdr/worktrees/", "tkt link DEMO-7", "Stop there"} {
		if !strings.Contains(p, want) {
			t.Errorf("setup prompt missing %q:\n%s", want, p)
		}
	}
	if strings.Contains(p, "herdr worktree create") {
		t.Errorf("setup prompt asks for a command Herdr desktop lacks:\n%s", p)
	}
}

func TestLinkOnDesktopHandsThePendingTicketToAnAgentInANewSpace(t *testing.T) {
	h, d, detached := desktopHandoffHarness(t, desktopSetupAgent())
	if code := h.exec("link"); code != 0 {
		t.Fatalf("exit %d: %s", code, h.errOut)
	}
	logPath := filepath.Join(h.app.Env.DataDir(), "handoff.log")
	want := []string{`space "DEMO-7 · web-DEMO-7" /src/web-DEMO-7 claude`, "mark t20 DEMO-7", "focus workspace sp20", "focus t20", "detach " + h.app.Env.DataDir()}
	if !reflect.DeepEqual(d.calls, want) {
		t.Errorf("calls = %q\nwant    %q", d.calls, want)
	}
	for _, s := range []string{"Work on Jira ticket DEMO-7: Export users", "copy from /src/web the files git ignores", "tkt link DEMO-7"} {
		if len(d.texts) != 1 || !strings.Contains(d.texts[0], s) {
			t.Errorf("task missing %q: %q", s, d.texts)
		}
	}
	if links, _ := h.app.links().Load(); len(links) != 1 || links[0].Path != "/src/web-DEMO-7" {
		t.Errorf("links = %+v", links)
	}
	if hos := h.app.loadState().Handoffs; len(hos) != 0 {
		t.Errorf("handoff kept: %+v", hos)
	}
	if len(*detached) != 0 {
		t.Errorf("started outside herdr-core: %+v", *detached)
	}
	// herdr-core runs the closer, so it outlives the setup agent's terminal, which ends every process it started.
	if len(d.detached) != 1 || len(d.detached[0]) != 6 || d.detached[0][0] != "sh" || d.detached[0][3] != "/opt/tkt/bin/tkt" || d.detached[0][5] != logPath {
		t.Fatalf("closer = %q", d.detached)
	}
	job := d.detached[0][4]

	d.calls = nil
	if code := h.exec("herdr", "close-setup", job); code != 0 {
		t.Fatalf("close-setup exit %d: %s", code, h.errOut)
	}
	if want := []string{"close t9", "focus workspace sp20", "focus t20"}; !reflect.DeepEqual(d.calls, want) {
		t.Errorf("close-setup calls = %q, want %q", d.calls, want)
	}
	for _, l := range []string{"close-setup DEMO-7: closing the setup agent t9", "close-setup DEMO-7: closed pane t9", "close-setup DEMO-7: focused t20 in sp20"} {
		if !strings.Contains(h.out.String(), l) {
			t.Errorf("log missing %q:\n%s", l, h.out)
		}
	}
}

func TestCloseSetupOnDesktopClosesTheSpaceTktOpenedForIt(t *testing.T) {
	ho := desktopSetupAgent()
	ho.Workspace, ho.Host = "sp1", "t1"
	h, d, _ := desktopHandoffHarness(t, ho)
	d.panes = []herdrport.Pane{{ID: "t1", WorkspaceID: "sp1"}, {ID: "t9", WorkspaceID: "sp1"}, {ID: "t20", WorkspaceID: "sp20"}}
	if code := h.exec("link"); code != 0 || len(d.detached) != 1 {
		t.Fatalf("exit %d, detached %+v: %s", code, d.detached, h.errOut)
	}
	d.calls = nil
	if code := h.exec("herdr", "close-setup", d.detached[0][4]); code != 0 {
		t.Fatalf("close-setup exit %d: %s", code, h.errOut)
	}
	if want := []string{"close t9", "close workspace sp1", "focus workspace sp20", "focus t20"}; !reflect.DeepEqual(d.calls, want) {
		t.Errorf("calls = %q, want %q", d.calls, want)
	}
	if !strings.Contains(h.out.String(), "close-setup DEMO-7: closed space sp1") {
		t.Errorf("log = %q", h.out)
	}
}

func TestLinkOnDesktopWithoutAPendingHandoffOnlyLinks(t *testing.T) {
	h, d, detached := desktopHandoffHarness(t, nil)
	if code := h.exec("link"); code != 0 {
		t.Fatalf("exit %d: %s", code, h.errOut)
	}
	if len(d.calls) != 0 || len(*detached) != 0 || len(d.detached) != 0 {
		t.Errorf("calls = %q, detached = %+v", d.calls, *detached)
	}
	if !strings.Contains(h.out.String(), "✓ DEMO-7 ← feature/DEMO-7-export (example-org/web)") {
		t.Errorf("output = %q", h.out)
	}
}

func TestLinkOnDesktopKeepsTheSetupAgentWhenTheHandoffFails(t *testing.T) {
	tests := []struct {
		name    string
		space   error
		spawn   error
		message string
	}{
		{"the space does not open", errors.New("herdr-app space.new: boom"), nil,
			"could not hand DEMO-7 to an agent in its worktree (herdr-app space.new: boom); its agent stays in pane t9"},
		{"the closer does not start", nil, errors.New("no fork"),
			"could not close the setup agent of DEMO-7 in pane t9: no fork"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h, d, _ := desktopHandoffHarness(t, desktopSetupAgent())
			d.spaceErr, d.runErr = tt.space, tt.spawn
			if code := h.exec("link"); code != 0 {
				t.Fatalf("exit %d: %s", code, h.errOut)
			}
			if !strings.Contains(h.errOut.String(), tt.message) {
				t.Errorf("stderr = %q, want %q", h.errOut, tt.message)
			}
			for _, c := range d.calls {
				if strings.HasPrefix(c, "close") {
					t.Errorf("closed %q", c)
				}
			}
			if links, _ := h.app.links().Load(); len(links) != 1 {
				t.Errorf("links = %+v", links)
			}
			if hos := h.app.loadState().Handoffs; len(hos) != 0 {
				t.Errorf("handoff kept: %+v", hos)
			}
		})
	}
}

func TestLinkInHerdrLeavesTheHandoffToTheWorktreeHook(t *testing.T) {
	ho := setupAgent()
	h, f := handoffHarness(t, ho)
	h.env["HERDR_ENV"] = "1"
	if code := h.exec("link"); code != 0 {
		t.Fatalf("exit %d: %s", code, h.errOut)
	}
	if n := count(f, "agent.start") + count(f, "pane.close"); n != 0 {
		t.Errorf("link completed the handoff: %d calls", n)
	}
	if hos := h.app.loadState().Handoffs; len(hos) != 1 {
		t.Errorf("handoffs = %+v", hos)
	}
}

func TestLinkOnDesktopAtTheMainCheckoutKeepsTheHandoff(t *testing.T) {
	h, d, _ := desktopHandoffHarness(t, desktopSetupAgent())
	h.inCheckout("feature/DEMO-7-export", "/src/web", "git@github.com:example-org/web.git")
	if code := h.exec("link"); code != 0 {
		t.Fatalf("exit %d: %s", code, h.errOut)
	}
	if len(d.calls) != 0 || len(d.detached) != 0 {
		t.Errorf("handed off from the main checkout: %q", d.calls)
	}
	if !strings.Contains(h.errOut.String(), "the main checkout stays free: create a separate worktree for DEMO-7 with git and run `tkt link DEMO-7` there") {
		t.Errorf("stderr = %q", h.errOut)
	}
	if hos := h.app.loadState().Handoffs; len(hos) != 1 {
		t.Errorf("handoffs = %+v", hos)
	}
	if links, _ := h.app.links().Load(); len(links) != 1 {
		t.Errorf("links = %+v", links)
	}
}

func TestEachHerdrCompletesOnlyTheHandoffsItsStartSaved(t *testing.T) {
	t.Run("desktop link leaves a Herdr 0.9.5 handoff", func(t *testing.T) {
		ho := desktopSetupAgent()
		ho.Desktop, ho.Pane = false, "w1:p2"
		h, d, _ := desktopHandoffHarness(t, ho)
		if code := h.exec("link"); code != 0 {
			t.Fatalf("exit %d: %s", code, h.errOut)
		}
		if len(d.calls) != 0 || len(h.app.loadState().Handoffs) != 1 {
			t.Errorf("calls = %q, handoffs = %+v", d.calls, h.app.loadState().Handoffs)
		}
	})
	t.Run("the Herdr 0.9.5 hook leaves a desktop handoff", func(t *testing.T) {
		ho := setupAgent()
		ho.Desktop = true
		h, f := handoffHarness(t, ho)
		if code := h.exec("herdr", "on-worktree"); code != 0 {
			t.Fatalf("exit %d: %s", code, h.errOut)
		}
		if n := count(f, "agent.start") + count(f, "pane.close"); n != 0 || len(h.app.loadState().Handoffs) != 1 {
			t.Errorf("%d calls, handoffs = %+v", n, h.app.loadState().Handoffs)
		}
	})
}
