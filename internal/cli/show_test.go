package cli

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/pavp/tkt/internal/board"
	"github.com/pavp/tkt/internal/config"
	"github.com/pavp/tkt/internal/jira"
	"github.com/pavp/tkt/internal/ticket"
)

const showConfig = `
[jira]
site = "example.atlassian.net"
email = "dev@example.com"

[[spaces]]
key = "DEMO"
acceptance_field = "customfield_10042"

[[spaces.repos]]
repo = "example-org/web"
`

func showHarness(t *testing.T) *harness {
	t.Helper()
	h, _ := listHarness(t)
	if err := os.WriteFile(h.app.configPath, []byte(showConfig), 0o600); err != nil {
		t.Fatal(err)
	}
	h.jira.details = map[string]jira.Issue{
		"DEMO-1": {
			Key: "DEMO-1", Type: "Story", Summary: "Filter accounts by date", Status: "In Progress", StatusCategory: "indeterminate", Assignee: "Ana López",
			Description: json.RawMessage(`{"type":"doc","content":[{"type":"paragraph","content":[{"type":"text","text":"Filter by "},{"type":"text","text":"creation date","marks":[{"type":"strong"}]}]}]}`),
			Fields: map[string]json.RawMessage{
				"customfield_10042": json.RawMessage(`{"type":"doc","content":[{"type":"bulletList","content":[{"type":"listItem","content":[{"type":"paragraph","content":[{"type":"text","text":"Range kept in the URL"}]}]}]}]}`),
			},
			Attachments: []jira.Attachment{
				{Filename: "mock up.png", MimeType: "image/png", Size: 2048, Content: "https://example.atlassian.net/att/1"},
				{Filename: "spec.pdf", MimeType: "application/pdf", Size: 3 << 20, Content: "https://example.atlassian.net/att/2"},
			},
		},
	}
	h.jira.comments = map[string][]jira.Comment{"DEMO-1": {
		{Author: "Product Owner", Created: time.Date(2026, 10, 17, 9, 0, 0, 0, time.UTC), Body: json.RawMessage(`{"type":"doc","content":[{"type":"paragraph","content":[{"type":"text","text":"Default to 30 days."}]}]}`)},
	}}
	h.jira.files = map[string]string{"https://example.atlassian.net/att/1": "PNGDATA"}
	return h
}

func TestShow(t *testing.T) {
	h := showHarness(t)
	if code := h.exec("show", "demo-1"); code != 0 {
		t.Fatalf("exit %d: %s", code, h.errOut)
	}
	out := h.out.String()
	for _, want := range []string{
		"DEMO-1 · Story · In Progress · Ana López",
		"https://example.atlassian.net/browse/DEMO-1",
		"! needs you: CI failing",
		"example-org/web#812  ✕ CI failing",
		"Filter by **creation date**",
		"Acceptance criteria\n  - Range kept in the URL",
		"Attachments (2)",
		"mock up.png  image/png  2 KB",
		"spec.pdf  application/pdf  3.0 MB",
		"Product Owner · ",
		"Default to 30 days.",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
}

func TestShowKeyFromBranch(t *testing.T) {
	h := showHarness(t)
	h.run.out["git rev-parse --abbrev-ref HEAD"] = "feature/DEMO-1-filter\n"
	if code := h.exec("show"); code != 0 || !strings.Contains(h.out.String(), "DEMO-1 · Story") {
		t.Fatalf("exit %d: %s %s", code, h.errOut, h.out)
	}
	h.run.out["git rev-parse --abbrev-ref HEAD"] = "fix-date-filter\n"
	if code := h.exec("show"); code == 0 || !strings.Contains(h.errOut.String(), `branch "fix-date-filter" carries no key of a configured space`) {
		t.Errorf("exit %d, %q", code, h.errOut)
	}
	if code := h.exec("show", "not-a-key"); code == 0 || !strings.Contains(h.errOut.String(), `"not-a-key" is not a ticket key`) {
		t.Errorf("exit %d, %q", code, h.errOut)
	}
}

func TestShowUnknownTicket(t *testing.T) {
	h := showHarness(t)
	if code := h.exec("show", "DEMO-404"); code == 0 || !strings.Contains(h.errOut.String(), "Issue does not exist") {
		t.Errorf("exit %d, %q", code, h.errOut)
	}
}

func TestShowWatchRedrawsUntilCancelled(t *testing.T) {
	h := showHarness(t)
	sleeps := 0
	h.app.Sleep = func(_ context.Context, d time.Duration) error {
		sleeps++
		if d != 30*time.Second {
			t.Errorf("interval = %s", d)
		}
		if sleeps == 2 {
			return context.Canceled
		}
		return nil
	}
	if code := h.exec("show", "DEMO-1", "--watch", "--interval", "30s"); code != 0 {
		t.Fatalf("exit %d: %s", code, h.errOut)
	}
	if n := strings.Count(h.out.String(), "\033[H\033[2J"); n != 3 {
		t.Errorf("redraws = %d, want 3", n)
	}
	if out := h.out.String(); !strings.HasPrefix(out, "\033[H\033[2JLoading DEMO-1…\n") {
		t.Errorf("the pane should say it is loading before the first load:\n%q", out)
	}
	if !strings.Contains(h.out.String(), "every 30s · Ctrl-C to stop") {
		t.Errorf("footer missing:\n%s", h.out)
	}
	if code := h.exec("show", "DEMO-1", "--watch", "--interval", "1s"); code == 0 || !strings.Contains(h.errOut.String(), "--interval must be at least 10s") {
		t.Errorf("exit %d, %q", code, h.errOut)
	}
}

func TestContextMarkdownWithImages(t *testing.T) {
	h := showHarness(t)
	dir := filepath.Join(h.home, "wt", ".ticket-assets")
	if code := h.exec("context", "DEMO-1", "--images", dir); code != 0 {
		t.Fatalf("exit %d: %s", code, h.errOut)
	}
	out := h.out.String()
	for _, want := range []string{
		"# DEMO-1 · Filter accounts by date\n",
		"- Status: In Progress",
		"## Description\n\nFilter by **creation date**",
		"## Acceptance criteria\n\n- Range kept in the URL",
		"## Pull requests\n\n- [example-org/web#812](",
		"- ![mock up.png](" + filepath.ToSlash(filepath.Join(dir, "mock_up.png")) + ")",
		"- [spec.pdf](https://example.atlassian.net/att/2) (3.0 MB)",
		"### Product Owner · 2026-10-17\n\nDefault to 30 days.",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
	got, err := os.ReadFile(filepath.Join(dir, "mock_up.png"))
	if err != nil || string(got) != "PNGDATA" {
		t.Errorf("image = %q, %v", got, err)
	}
	if _, err := os.Stat(filepath.Join(dir, "spec.pdf")); err == nil {
		t.Error("non-image attachment was downloaded")
	}
}

func TestContextWithoutImagesLinksEverything(t *testing.T) {
	h := showHarness(t)
	if code := h.exec("context", "DEMO-1"); code != 0 {
		t.Fatalf("exit %d: %s", code, h.errOut)
	}
	if !strings.Contains(h.out.String(), "- [mock up.png](https://example.atlassian.net/att/1)") {
		t.Errorf("image not linked:\n%s", h.out)
	}
}

func TestContextDownloadFailureIsAWarning(t *testing.T) {
	h := showHarness(t)
	h.jira.files = nil
	if code := h.exec("context", "DEMO-1", "--images", filepath.Join(h.home, "assets")); code != 0 {
		t.Fatalf("exit %d", code)
	}
	if !strings.Contains(h.errOut.String(), "! mock up.png not downloaded") || !strings.Contains(h.out.String(), "- [mock up.png](") {
		t.Errorf("stderr %q\n%s", h.errOut, h.out)
	}
}

func TestShowTakesPullRequestsFromARunningDaemon(t *testing.T) {
	h := showHarness(t)
	gh := h.app.NewGitHub(nil).(*fakeGitHub)
	h.app.NewGitHub = func(*config.Config) GitHubAPI { return gh }
	h.app.Executable = func() (string, error) { return "/opt/tkt/bin/tkt", nil }
	f := &fakeDaemons{board: &board.Snapshot{FetchedAt: time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC), Tickets: []ticket.Ticket{
		{Key: "DEMO-1", PRs: []ticket.PullRequest{{Repo: "example-org/web", Number: 900, State: ticket.PROpen, Checks: ticket.ChecksPass}}},
	}}}
	f.install(t, h)
	if code := h.exec("show", "DEMO-1"); code != 0 || gh.prCalls != 1 {
		t.Fatalf("without a daemon: exit %d, listings %d", code, gh.prCalls)
	}
	if _, err := h.app.ensureDaemon(context.Background()); err != nil {
		t.Fatal(err)
	}
	if code := h.exec("show", "DEMO-1"); code != 0 {
		t.Fatalf("exit %d: %s", code, h.errOut)
	}
	if out := h.out.String(); gh.prCalls != 1 || !strings.Contains(out, "example-org/web#900") || strings.Contains(out, "#812") {
		t.Errorf("show should take the daemon's pull requests (listings %d):\n%s", gh.prCalls, out)
	}
}

func TestShowTakesTheDetailFromARunningDaemon(t *testing.T) {
	h := showHarness(t)
	h.app.Executable = func() (string, error) { return "/opt/tkt/bin/tkt", nil }
	f := &fakeDaemons{
		board: &board.Snapshot{FetchedAt: time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC), Tickets: []ticket.Ticket{
			{Key: "DEMO-1", Summary: "Filter accounts by date", PRs: []ticket.PullRequest{{Repo: "example-org/web", Number: 900, State: ticket.PROpen}}},
		}},
		detail: func(_ context.Context, key ticket.Key, known *ticket.Ticket) (any, error) {
			return detail{Ticket: *known, Description: "Loaded by the daemon"}, nil
		},
	}
	f.install(t, h)
	if _, err := h.app.ensureDaemon(context.Background()); err != nil {
		t.Fatal(err)
	}
	h.jira.details = nil
	if code := h.exec("show", "DEMO-1"); code != 0 || !strings.Contains(h.out.String(), "Loaded by the daemon") {
		t.Fatalf("exit %d: %s\n%s", code, h.errOut, h.out)
	}

	h.out.Reset()
	h.app.Sleep = func(context.Context, time.Duration) error { return context.Canceled }
	if code := h.exec("show", "DEMO-1", "--watch"); code != 0 {
		t.Fatalf("exit %d: %s", code, h.errOut)
	}
	out := h.out.String()
	head, loading, loaded := strings.Index(out, "example-org/web#900"), strings.Index(out, "Loading DEMO-1…"), strings.Index(out, "Loaded by the daemon")
	if head < 0 || loading < head || loaded < loading || strings.Count(out[:loading], "Description") != 0 {
		t.Errorf("the board's copy should show before the detail loads:\n%s", out)
	}
}

func TestDaemonDetailUsesTheBoardsPullRequests(t *testing.T) {
	h := showHarness(t)
	known := &ticket.Ticket{Key: "DEMO-1", PRs: []ticket.PullRequest{{Repo: "example-org/web", Number: 900, State: ticket.PROpen}}}
	got, err := h.app.daemonDetail(context.Background(), "DEMO-1", known)
	if err != nil {
		t.Fatal(err)
	}
	d := got.(detail)
	if len(d.Ticket.PRs) != 1 || d.Ticket.PRs[0].Number != 900 || !strings.Contains(d.Description, "creation date") {
		t.Errorf("detail = %+v", d)
	}
}

func TestRunningChecksShowAnEllipsis(t *testing.T) {
	pr := ticket.PullRequest{Number: 311, State: ticket.PROpen, Checks: ticket.ChecksPending}
	if got := prMark(pr); got != "…" {
		t.Errorf("list mark = %q", got)
	}
	if got := prStateText(pr); got != "… CI running" {
		t.Errorf("show text = %q", got)
	}
}

func TestShowCountsApprovals(t *testing.T) {
	tests := []struct {
		pr   ticket.PullRequest
		want string
	}{
		{ticket.PullRequest{Review: ticket.ReviewApproved, Approvals: 2}, " · approved · 2 approvals"},
		{ticket.PullRequest{Review: ticket.ReviewRequired, Approvals: 1}, " · review required · 1 approval"},
		{ticket.PullRequest{Review: ticket.ReviewRequired}, " · review required"},
	}
	for _, tt := range tests {
		if got := reviewText(tt.pr); got != tt.want {
			t.Errorf("reviewText(%+v) = %q, want %q", tt.pr, got, tt.want)
		}
	}
}

func TestShowNeverStartsTheDaemon(t *testing.T) {
	h := showHarness(t)
	h.app.Executable = func() (string, error) { return "/opt/tkt/bin/tkt", nil }
	f := &fakeDaemons{}
	f.install(t, h)
	if code := h.exec("show", "DEMO-1"); code != 0 || f.started.Load() != 0 {
		t.Errorf("exit %d, daemons started %d", code, f.started.Load())
	}
}
