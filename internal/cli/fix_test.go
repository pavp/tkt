package cli

import (
	"context"
	"strings"
	"testing"

	"github.com/pavp/tkt/internal/config"
	"github.com/pavp/tkt/internal/github"
	"github.com/pavp/tkt/internal/herdr"
	"github.com/pavp/tkt/internal/herdr/herdrtest"
	"github.com/pavp/tkt/internal/jira"
	"github.com/pavp/tkt/internal/ticket"
)

func fixHarness(t *testing.T) (*harness, *fakeGitHub, *herdrtest.FakeServer) {
	t.Helper()
	h, _, _ := herdrHarness(t)
	h.jira.details["DEMO-1"] = jira.Issue{Key: "DEMO-1", Summary: "Filter accounts", Status: "In Progress", StatusCategory: "indeterminate"}
	gh := &fakeGitHub{
		prs: []ticket.PullRequest{
			{Repo: "example-org/web", Number: 812, Branch: "feature/DEMO-1-filter", State: ticket.PROpen, Checks: ticket.ChecksFail, HeadSHA: "abc"},
			{Repo: "example-org/api", Number: 31, Branch: "feature/DEMO-1-filter", State: ticket.PROpen, Checks: ticket.ChecksPass, Review: ticket.ReviewChangesRequested},
		},
		failed:  map[string][]github.FailedCheck{"example-org/web@abc": {{Name: "unit-tests", URL: "https://github.com/example-org/web/runs/11", LogTail: "--- FAIL: TestRange"}}},
		changes: map[int][]github.ChangeRequest{31: {{Author: "alex", Body: "Cover the empty range.", Comments: []github.ReviewComment{{Path: "api/range.go", Line: 12, Body: "Missing\ntest"}}}}},
	}
	h.app.NewGitHub = func(*config.Config) GitHubAPI { return gh }
	f := herdrtest.Start(t)
	h.app.NewHerdr = func() HerdrAPI { return herdr.New(f.Path) }
	return h, gh, f
}

func TestFixPrintsPromptOutsideHerdr(t *testing.T) {
	h, _, f := fixHarness(t)
	if code := h.exec("fix", "DEMO-1"); code != 0 {
		t.Fatalf("exit %d: %s", code, h.errOut)
	}
	out := h.out.String()
	for _, want := range []string{
		"Feedback on Jira ticket DEMO-1 (Filter accounts):",
		`CI check "unit-tests" failed on example-org/web#812 (https://github.com/example-org/web/runs/11).`,
		"```\n--- FAIL: TestRange\n```",
		"@alex requested changes on example-org/api#31: Cover the empty range.",
		"- api/range.go:12: Missing test",
		"push to the same branch",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
	if strings.Index(out, "@alex") > strings.Index(out, "unit-tests") {
		t.Errorf("feedback should follow the pull requests' order:\n%s", out)
	}
	if count(f, "agent.prompt") != 0 {
		t.Error("prompted an agent outside Herdr")
	}
}

func TestFixSendsToTheTicketAgentInHerdr(t *testing.T) {
	h, _, f := fixHarness(t)
	h.env["HERDR_ENV"] = "1"
	h.clone(t, "web")
	h.run.out["git worktree list --porcelain"] = "worktree /src/web-DEMO-1\nHEAD a\nbranch refs/heads/feature/DEMO-1-filter\n"
	f.Results["workspace.list"] = `{"type":"workspace_list","workspaces":[{"workspace_id":"w1","worktree":{"checkout_path":"/src/web-DEMO-1"}},{"workspace_id":"w2"}]}`
	f.Results["agent.list"] = `{"type":"agent_list","agents":[{"pane_id":"w2:p1","workspace_id":"w2","agent":"claude"},{"pane_id":"w1:p2","workspace_id":"w1","agent":"claude"}]}`
	if code := h.exec("fix", "DEMO-1"); code != 0 {
		t.Fatalf("exit %d: %s", code, h.errOut)
	}
	got := f.Last("agent.prompt")
	if got["target"] != "w1:p2" || !strings.Contains(got["text"].(string), `CI check "unit-tests" failed`) {
		t.Errorf("prompt = %v", got)
	}
	if !strings.Contains(h.out.String(), "✓ Sent DEMO-1's feedback to its agent") {
		t.Errorf("output = %q", h.out)
	}
}

func TestFixErrors(t *testing.T) {
	h, gh, f := fixHarness(t)
	h.env["HERDR_ENV"] = "1"
	if code := h.exec("fix", "DEMO-1"); code == 0 || !strings.Contains(h.errOut.String(), "DEMO-1 has no worktree yet") {
		t.Errorf("no worktree: exit %d, %q", code, h.errOut)
	}
	h.clone(t, "web")
	h.run.out["git worktree list --porcelain"] = "worktree /src/web-DEMO-1\nHEAD a\nbranch refs/heads/feature/DEMO-1-filter\n"
	f.Results["workspace.list"] = `{"type":"workspace_list","workspaces":[{"workspace_id":"w1","worktree":{"checkout_path":"/src/web-DEMO-1"}}]}`
	if code := h.exec("fix", "DEMO-1"); code == 0 || !strings.Contains(h.errOut.String(), "no agent is running in DEMO-1's workspace") {
		t.Errorf("no agent: exit %d, %q", code, h.errOut)
	}
	gh.prs = []ticket.PullRequest{{Repo: "example-org/web", Number: 9, Branch: "feature/DEMO-1", State: ticket.PROpen, Checks: ticket.ChecksPass}}
	if code := h.exec("fix", "DEMO-1", "--print"); code == 0 || !strings.Contains(h.errOut.String(), "DEMO-1 has no failing CI, requested changes, unresolved comments or conflicts") {
		t.Errorf("nothing to fix: exit %d, %q", code, h.errOut)
	}
}

func TestFixSendsUnresolvedCommentsAndConflicts(t *testing.T) {
	h, gh, _ := fixHarness(t)
	gh.prs = []ticket.PullRequest{
		{Repo: "example-org/web", Number: 40, Branch: "feature/DEMO-1-form", State: ticket.PROpen, Checks: ticket.ChecksPass, Unresolved: 1},
		{Repo: "example-org/web", Number: 41, Branch: "feature/DEMO-1-names", State: ticket.PROpen, Checks: ticket.ChecksPass, Merge: ticket.MergeConflicts},
	}
	gh.threads = map[int][]github.ReviewThread{40: {{Path: "web/form.go", Line: 7, Comments: []github.ThreadComment{{Author: "sam", Body: "Rename\nthis"}, {Author: "alex", Body: "Why?"}}}}}
	if code := h.exec("fix", "DEMO-1"); code != 0 {
		t.Fatalf("exit %d: %s", code, h.errOut)
	}
	out := h.out.String()
	for _, want := range []string{
		"Unresolved comment on example-org/web#40 at web/form.go:7:",
		"- @sam: Rename this",
		"- @alex: Why?",
		"example-org/web#41 has merge conflicts with its base branch: rebase it onto the base branch, resolve the conflicts",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
}

func TestFixFindsTheAgentInAWorkspaceWithoutAWorktree(t *testing.T) {
	h, _, f := fixHarness(t)
	h.env["HERDR_ENV"] = "1"
	h.clone(t, "web")
	h.run.out["git worktree list --porcelain"] = "worktree /src/web-DEMO-1\nHEAD a\nbranch refs/heads/feature/DEMO-1-filter\n"
	f.Results["workspace.list"] = `{"type":"workspace_list","workspaces":[{"workspace_id":"w3"}]}`
	f.Results["agent.list"] = `{"type":"agent_list","agents":[{"pane_id":"w3:p1","workspace_id":"w3","agent":"claude","cwd":"/src/web-DEMO-1/app"}]}`
	if code := h.exec("fix", "DEMO-1"); code != 0 {
		t.Fatalf("exit %d: %s", code, h.errOut)
	}
	if got := f.Last("agent.prompt"); got["target"] != "w3:p1" {
		t.Errorf("prompt = %v", got)
	}
	f.Results["agent.list"] = `{"type":"agent_list","agents":[{"pane_id":"w3:p1","workspace_id":"w3","agent":"claude"}]}`
	f.Results["pane.list"] = `{"type":"pane_list","panes":[{"pane_id":"w3:p1","workspace_id":"w3","cwd":"/src/web-DEMO-1"}]}`
	if code := h.exec("fix", "DEMO-1"); code != 0 {
		t.Fatalf("an agent placed by its pane's folder: exit %d: %s", code, h.errOut)
	}
}

func TestDetailOfAKnownTicketWithoutPullRequestsAsksJirasPanelNotGitHub(t *testing.T) {
	h, gh, _ := fixHarness(t)
	is := h.jira.details["DEMO-1"]
	is.ID = "10001"
	h.jira.details["DEMO-1"] = is
	h.jira.devPRs = map[string][]jira.DevPullRequest{"10001": {{Repo: "example-org/web", Number: 31, Title: "Pager", URL: "https://github.com/example-org/web/pull/31", Status: "OPEN"}}}
	c, err := h.app.LoadConfig()
	if err != nil {
		t.Fatal(err)
	}
	d, err := h.app.loadDetail(context.Background(), c, "DEMO-1", &ticket.Ticket{Key: "DEMO-1"})
	if err != nil {
		t.Fatal(err)
	}
	if gh.prCalls != 0 {
		t.Errorf("a known ticket never lists every pull request: %d listings", gh.prCalls)
	}
	if len(d.Ticket.PRs) != 1 || d.Ticket.PRs[0].Number != 31 || d.Ticket.PRs[0].State != ticket.PROpen {
		t.Errorf("Jira's development panel names its pull requests: %+v", d.Ticket.PRs)
	}
}

func TestDetailAsksTheDevelopmentPanelWithTheKnownTicketsID(t *testing.T) {
	h, gh, _ := fixHarness(t)
	h.jira.devPRs = map[string][]jira.DevPullRequest{"10002": {{Repo: "example-org/web", Number: 32, Status: "MERGED"}}}
	c, err := h.app.LoadConfig()
	if err != nil {
		t.Fatal(err)
	}
	d, err := h.app.loadDetail(context.Background(), c, "DEMO-1", &ticket.Ticket{Key: "DEMO-1", ID: "10002"})
	if err != nil {
		t.Fatal(err)
	}
	if gh.prCalls != 0 || len(d.Ticket.PRs) != 1 || d.Ticket.PRs[0].Number != 32 || d.Ticket.PRs[0].State != ticket.PRMerged {
		t.Errorf("the known ID asks the panel beside the issue: listings %d, PRs %+v", gh.prCalls, d.Ticket.PRs)
	}
}
