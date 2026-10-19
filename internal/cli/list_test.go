package cli

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/pavp/tkt/internal/config"
	"github.com/pavp/tkt/internal/jira"
	"github.com/pavp/tkt/internal/ticket"
)

type fakeGitHub struct {
	prs     []ticket.PullRequest
	reviews map[string][]ticket.PullRequest
	err     error
}

func (f *fakeGitHub) PullRequests(context.Context, []string) ([]ticket.PullRequest, error) {
	return f.prs, f.err
}
func (f *fakeGitHub) ReviewRequests(_ context.Context, owner string) ([]ticket.PullRequest, error) {
	return f.reviews[owner], f.err
}

var fixedNow = time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC)

const listConfig = baseConfig + `
[[spaces]]
key = "OPS"
`

func listHarness(t *testing.T) (*harness, *fakeGitHub) {
	t.Helper()
	h := newHarness(t, listConfig)
	h.env["XDG_DATA_HOME"] = filepath.Join(h.home, "data")
	h.creds["jira:example.atlassian.net:dev@example.com"] = "tok"
	h.app.Now = func() time.Time { return fixedNow }
	h.jira.issues = []jira.Issue{
		{Key: "DEMO-1", Type: "Story", Summary: "Filter accounts by date", Status: "In Progress", StatusCategory: "indeterminate"},
		{Key: "DEMO-2", Type: "Sub-task", Summary: "Picker", Status: "Done", StatusCategory: "done", Parent: "DEMO-1"},
		{Key: "DEMO-3", Type: "Story", Summary: "Export CSV", Status: "To Do", StatusCategory: "new"},
		{Key: "OPS-4", Type: "Bug", Summary: "Fix redirect", Status: "In Review", StatusCategory: "indeterminate"},
	}
	h.jira.sprints = map[string][]jira.Sprint{"DEMO": {{Space: "DEMO", Name: "DEMO S42", End: fixedNow.Add(4 * 24 * time.Hour)}}}
	gh := &fakeGitHub{
		prs: []ticket.PullRequest{
			{Repo: "example-org/web", Number: 812, Branch: "feature/DEMO-1-filter", State: ticket.PROpen, Checks: ticket.ChecksFail},
			{Repo: "example-org/web", Number: 45, Branch: "fix/OPS-4-redirect", State: ticket.PROpen, Checks: ticket.ChecksPass, Review: ticket.ReviewChangesRequested},
		},
		reviews: map[string][]ticket.PullRequest{"example-org": {{Repo: "example-org/web", Number: 820, Title: "Add pagination", Branch: "feature/DEMO-9-pages", State: ticket.PROpen}}},
	}
	h.app.NewGitHub = func(*config.Config) GitHubAPI { return gh }
	return h, gh
}

func TestListShowsNeedsYouFirst(t *testing.T) {
	h, _ := listHarness(t)
	if code := h.exec("list"); code != 0 {
		t.Fatalf("exit %d: %s", code, h.errOut)
	}
	out := h.out.String()
	for _, want := range []string{
		"DEMO S42 · 4d left",
		"NEEDS YOU",
		"!  DEMO-1  CI failing",
		"!  OPS-4   Changes requested",
		"!  #820    Review requested   example-org/web · Add pagination",
		"web#812 ✕",
		"↳ DEMO-2",
		"synced just now",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
	if i, j := strings.Index(out, "\nDEMO-1 "), strings.Index(out, "\nDEMO-3 "); i < 0 || j < 0 || i > j {
		t.Errorf("tickets not ordered by attention and column:\n%s", out)
	}
	if !strings.Contains(h.jira.jql, `project in ("DEMO", "OPS")`) {
		t.Errorf("jql = %s", h.jira.jql)
	}
}

func TestListFilters(t *testing.T) {
	h, _ := listHarness(t)
	h.exec("list", "--space", "OPS")
	if out := h.out.String(); strings.Contains(out, "DEMO-1") || !strings.Contains(out, "OPS-4") || strings.Contains(out, "#820") {
		t.Errorf("--space OPS:\n%s", out)
	}
	h.exec("list", "--needs-you")
	if out := h.out.String(); strings.Contains(out, "DEMO-3") || !strings.Contains(out, "DEMO-1") {
		t.Errorf("--needs-you:\n%s", out)
	}
	if code := h.exec("list", "--space", "NOPE"); code == 0 || !strings.Contains(h.errOut.String(), "space NOPE is not configured (configured: DEMO, OPS)") {
		t.Errorf("unknown space: %d %q", code, h.errOut)
	}
}

func TestListJSON(t *testing.T) {
	h, _ := listHarness(t)
	if code := h.exec("--json", "list"); code != 0 {
		t.Fatalf("exit %d", code)
	}
	var got struct {
		Snapshot struct {
			Tickets []ticket.Ticket `json:"tickets"`
		} `json:"snapshot"`
		NeedsYou []map[string]any `json:"needs_you"`
		Stale    bool             `json:"stale"`
	}
	if err := json.Unmarshal(h.out.Bytes(), &got); err != nil {
		t.Fatalf("%v\n%s", err, h.out)
	}
	if len(got.Snapshot.Tickets) != 3 || len(got.NeedsYou) != 3 || got.Stale {
		t.Errorf("json = %+v", got)
	}
}

func TestListFallsBackToCache(t *testing.T) {
	h, _ := listHarness(t)
	if code := h.exec("list"); code != 0 {
		t.Fatalf("warm-up exit %d: %s", code, h.errOut)
	}
	h.jira.searchErr = jira.ErrUnauthorized
	h.app.Now = func() time.Time { return fixedNow.Add(2 * time.Hour) }
	if code := h.exec("list"); code != 0 {
		t.Fatalf("exit %d: %s", code, h.errOut)
	}
	if !strings.Contains(h.out.String(), "DEMO-1") {
		t.Errorf("cached board not shown:\n%s", h.out)
	}
	if !strings.Contains(h.errOut.String(), "showing the cached board from") || !strings.Contains(h.errOut.String(), "run `tkt auth jira`") {
		t.Errorf("stale notice = %q", h.errOut)
	}
	if strings.Contains(h.out.String(), "synced") {
		t.Error("stale board claims to be synced")
	}
}

func TestListWithoutCacheReportsError(t *testing.T) {
	h, _ := listHarness(t)
	h.jira.searchErr = errors.New("dial tcp: no route")
	if code := h.exec("list"); code == 0 || !strings.Contains(h.errOut.String(), "no route") {
		t.Errorf("exit %d, %q", code, h.errOut)
	}
	if code := h.exec("list", "--cached"); code == 0 || !strings.Contains(h.errOut.String(), "no cached board yet") {
		t.Errorf("--cached without cache: exit %d, %q", code, h.errOut)
	}
}

func TestListCachedSkipsNetwork(t *testing.T) {
	h, gh := listHarness(t)
	h.exec("list")
	h.jira.searchErr = errors.New("must not be called")
	gh.err = errors.New("must not be called")
	if code := h.exec("list", "--cached"); code != 0 || h.errOut.Len() != 0 {
		t.Fatalf("exit %d, %q", code, h.errOut)
	}
}

func TestListWarnsWhenGitHubFails(t *testing.T) {
	h, gh := listHarness(t)
	gh.err = errors.New("rate limited")
	if code := h.exec("list"); code != 0 {
		t.Fatalf("exit %d", code)
	}
	if !strings.Contains(h.errOut.String(), "! pull requests unavailable: rate limited") || !strings.Contains(h.out.String(), "DEMO-1") {
		t.Errorf("stderr %q\nstdout %s", h.errOut, h.out)
	}
}

func TestListRequiresToken(t *testing.T) {
	h, _ := listHarness(t)
	delete(h.creds, "jira:example.atlassian.net:dev@example.com")
	if code := h.exec("list"); code == 0 || !strings.Contains(h.errOut.String(), "no Jira token: run `tkt auth jira`") {
		t.Errorf("exit %d, %q", code, h.errOut)
	}
}
