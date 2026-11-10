package cli

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/pavp/tkt/internal/herdrport"
	"github.com/pavp/tkt/internal/ticket"
	"github.com/pavp/tkt/internal/tui"
)

func reviewedPR() ticket.PullRequest {
	return ticket.PullRequest{Repo: "example-org/web", Number: 820, Title: "Add pagination", Author: "alex", Branch: "feature/DEMO-9-pages",
		URL: "https://github.com/example-org/web/pull/820", State: ticket.PROpen}
}

func reviewSetupAgent() *handoff {
	pr := reviewedPR()
	return &handoff{Review: &pr, Repo: "example-org/web", Clone: "/src/web", Pane: "t9", Agent: "claude", Kind: "claude", Desktop: true}
}

func TestNewAgentReviewOnDesktopAsksASetupAgentForTheWorktree(t *testing.T) {
	h, d := desktopHarness(t)
	web := h.clone(t, "web")
	d.panes = []herdrport.Pane{{ID: "t1", WorkspaceID: "sp1", Cwd: web}}
	pr := reviewedPR()
	task := tui.AgentTask{Review: &pr, Kind: "claude"}
	c, err := h.app.LoadConfig()
	if err != nil {
		t.Fatal(err)
	}
	dir, note, label, err := h.app.taskPlace(context.Background(), c, task, "")
	if err != nil {
		t.Fatal(err)
	}
	if err := h.app.startTask(context.Background(), task, reviewPrompt(pr), dir, note, label); err != nil {
		t.Fatalf("startTask: %v", err)
	}
	if len(d.texts) != 1 {
		t.Fatalf("prompts = %q", d.texts)
	}
	p := d.texts[0]
	for _, want := range []string{"Prepare the review of pull request https://github.com/example-org/web/pull/820", "feature/DEMO-9-pages",
		"Create the worktree with git", "~/.herdr/worktrees/", "tkt herdr review-ready example-org/web#820", "Stop there"} {
		if !strings.Contains(p, want) {
			t.Errorf("setup prompt missing %q:\n%s", want, p)
		}
	}
	if strings.Contains(p, "/review-6-lens") {
		t.Errorf("the setup agent only makes the worktree:\n%s", p)
	}
	hos := h.app.loadState().Handoffs
	if len(hos) != 1 || hos[0].Review == nil || hos[0].Review.Number != 820 || hos[0].Pane != "t9" || hos[0].Agent != "claude" || hos[0].Clone != web || !hos[0].Desktop || hos[0].Key != "" {
		t.Errorf("handoffs = %+v", hos)
	}
}

func TestReviewReadyOnDesktopOpensTheReviewInANewSpaceAtTheWorktree(t *testing.T) {
	h, d, _ := desktopHandoffHarness(t, reviewSetupAgent())
	if code := h.exec("herdr", "review-ready", "example-org/web#820"); code != 0 {
		t.Fatalf("exit %d: %s", code, h.errOut)
	}
	want := []string{`space "review · web#820" /src/web-DEMO-7 claude`, "focus workspace sp20", "focus t20", "detach " + h.app.Env.DataDir()}
	if !reflect.DeepEqual(d.calls, want) {
		t.Errorf("calls = %q\nwant    %q", d.calls, want)
	}
	for _, s := range []string{"Review pull request https://github.com/example-org/web/pull/820", "/review-6-lens", "created for this review from the main checkout at /src/web"} {
		if len(d.texts) != 1 || !strings.Contains(d.texts[0], s) {
			t.Errorf("task missing %q: %q", s, d.texts)
		}
	}
	if links, _ := h.app.links().Load(); len(links) != 0 {
		t.Errorf("a review registers no work: %+v", links)
	}
	if hos := h.app.loadState().Handoffs; len(hos) != 0 {
		t.Errorf("handoff kept: %+v", hos)
	}
	if len(d.detached) != 1 {
		t.Fatalf("closer = %q", d.detached)
	}
	d.calls = nil
	if code := h.exec("herdr", "close-setup", d.detached[0][4]); code != 0 {
		t.Fatalf("close-setup exit %d: %s", code, h.errOut)
	}
	if want := []string{"close t9", "focus workspace sp20", "focus t20"}; !reflect.DeepEqual(d.calls, want) {
		t.Errorf("close-setup calls = %q, want %q", d.calls, want)
	}
	if !strings.Contains(h.out.String(), "close-setup example-org/web#820: closed pane t9") {
		t.Errorf("log:\n%s", h.out)
	}
}

func TestReviewReadyRefusesWhatItCannotHandOn(t *testing.T) {
	tests := []struct {
		name, arg, top string
		ho             *handoff
		wantErr        string
		keep           bool
	}{
		{"the main checkout", "example-org/web#820", "/src/web", reviewSetupAgent(), "is the main checkout", true},
		{"no review waiting", "example-org/web#821", "/src/web-DEMO-7", reviewSetupAgent(), "no review of example-org/web#821 waits for a worktree", true},
		{"not a pull request", "example-org/web", "/src/web-DEMO-7", reviewSetupAgent(), "OWNER/NAME#N", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h, d, _ := desktopHandoffHarness(t, tt.ho)
			h.inCheckout("feature/DEMO-9-pages", tt.top, "git@github.com:example-org/web.git")
			if code := h.exec("herdr", "review-ready", tt.arg); code == 0 || !strings.Contains(h.errOut.String(), tt.wantErr) {
				t.Errorf("exit %d, stderr %q, want %q", code, h.errOut, tt.wantErr)
			}
			if hos := h.app.loadState().Handoffs; (len(hos) == 1) != tt.keep {
				t.Errorf("handoffs = %+v", hos)
			}
			if len(d.texts) != 0 || len(d.detached) != 0 {
				t.Errorf("nothing opens: %q %q", d.texts, d.detached)
			}
		})
	}
}

func TestLinkOnDesktopLeavesAPendingReviewAlone(t *testing.T) {
	h, d, _ := desktopHandoffHarness(t, reviewSetupAgent())
	if code := h.exec("link"); code != 0 {
		t.Fatalf("exit %d: %s", code, h.errOut)
	}
	if len(d.texts) != 0 || len(h.app.loadState().Handoffs) != 1 {
		t.Errorf("tkt link hands on only Starts: texts %q, handoffs %+v", d.texts, h.app.loadState().Handoffs)
	}
}
