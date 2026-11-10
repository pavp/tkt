package cli

import (
	"cmp"
	"context"
	"path/filepath"
	"reflect"
	"slices"
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

// startDesktopReview runs the board's "Review with ▸ A new agent" for pr on Herdr desktop, with a pane open at the clone.
func startDesktopReview(t *testing.T, h *harness, pr ticket.PullRequest) error {
	t.Helper()
	task := tui.AgentTask{Review: &pr, Kind: "claude"}
	c, err := h.app.LoadConfig()
	if err != nil {
		t.Fatal(err)
	}
	dir, note, label, err := h.app.taskPlace(context.Background(), c, task, "")
	if err != nil {
		t.Fatal(err)
	}
	return h.app.startTask(context.Background(), task, reviewPrompt(pr), dir, note, label)
}

func TestNewAgentReviewOnDesktopCreatesTheWorktreeAndOpensItsSpace(t *testing.T) {
	tests := []struct {
		name, branch, listed, wantAdd string
		localBranch                   bool
	}{
		{"a new local branch at the pull request's head", "feature/DEMO-9-pages", "", "git worktree add -b feature/DEMO-9-pages %s FETCH_HEAD", false},
		{"the head detached when the branch is already local", "feature/DEMO-9-pages", "", "git worktree add --detach %s FETCH_HEAD", true},
		{"pr-N when the branch is unknown", "", "", "git worktree add -b pr-820 %s FETCH_HEAD", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h, d := desktopHarness(t)
			web := h.clone(t, "web")
			d.panes = []herdrport.Pane{{ID: "t1", WorkspaceID: "sp1", Cwd: web}}
			pr := reviewedPR()
			pr.Branch = tt.branch
			local := cmp.Or(tt.branch, "pr-820")
			wt := filepath.Join(h.home, ".herdr", "worktrees", "web", strings.ReplaceAll(local, "/", "-"))
			h.run.out["git worktree list --porcelain"] = "worktree " + web + "\nHEAD 1111\nbranch refs/heads/main\n\n"
			h.run.out["git fetch origin pull/820/head"] = ""
			if tt.localBranch {
				h.run.out["git rev-parse --verify --quiet refs/heads/"+local] = "2222\n"
			}
			add := strings.Replace(tt.wantAdd, "%s", wt, 1)
			h.run.out[add] = ""
			if err := startDesktopReview(t, h, pr); err != nil {
				t.Fatalf("startTask: %v", err)
			}
			if !slices.Contains(h.run.calls, add) {
				t.Errorf("git calls = %q, want %q", h.run.calls, add)
			}
			want := []string{`space "review · web#820" ` + wt + ` claude`, "focus workspace sp20", "focus t20"}
			if !reflect.DeepEqual(d.calls, want) {
				t.Errorf("calls = %q\nwant    %q (one agent, in a space at the worktree)", d.calls, want)
			}
			if len(d.texts) != 1 || !strings.Contains(d.texts[0], "/review-6-lens") || !strings.Contains(d.texts[0], "https://github.com/example-org/web/pull/820") || strings.Contains(d.texts[0], "create a worktree") {
				t.Errorf("task = %q", d.texts)
			}
			if hos := h.app.loadState().Handoffs; len(hos) != 0 {
				t.Errorf("no setup agent, so no handoff: %+v", hos)
			}
			if links, _ := h.app.links().Load(); len(links) != 0 {
				t.Errorf("a review registers no work: %+v", links)
			}
		})
	}
}

func TestNewAgentReviewOnDesktopReusesTheBranchsWorktree(t *testing.T) {
	h, d := desktopHarness(t)
	web := h.clone(t, "web")
	h.run.out["git worktree list --porcelain"] = "worktree " + web + "\nbranch refs/heads/main\n\nworktree /src/web-pages\nHEAD 3333\nbranch refs/heads/feature/DEMO-9-pages\n\n"
	if err := startDesktopReview(t, h, reviewedPR()); err != nil {
		t.Fatalf("startTask: %v", err)
	}
	for _, c := range h.run.calls {
		if strings.HasPrefix(c, "git fetch") || strings.HasPrefix(c, "git worktree add") {
			t.Errorf("the worktree exists: %q", h.run.calls)
		}
	}
	if len(d.calls) == 0 || d.calls[0] != `space "review · web#820" /src/web-pages claude` {
		t.Errorf("calls = %q", d.calls)
	}
	if len(d.texts) != 1 || !strings.Contains(d.texts[0], "already existed") {
		t.Errorf("task = %q, want it told to bring the worktree up to date", d.texts)
	}
}

func TestNewAgentReviewOnDesktopOpensNothingWhenGitFails(t *testing.T) {
	h, d := desktopHarness(t)
	web := h.clone(t, "web")
	h.run.out["git worktree list --porcelain"] = "worktree " + web + "\n\n"
	err := startDesktopReview(t, h, reviewedPR())
	if err == nil || !strings.Contains(err.Error(), "could not create the worktree of example-org/web#820") {
		t.Fatalf("err = %v", err)
	}
	if len(d.calls) != 0 {
		t.Errorf("calls = %q, want nothing opened", d.calls)
	}
}
