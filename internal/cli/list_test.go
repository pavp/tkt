package cli

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/pavp/tkt/internal/board"
	"github.com/pavp/tkt/internal/config"
	"github.com/pavp/tkt/internal/github"
	"github.com/pavp/tkt/internal/jira"
	"github.com/pavp/tkt/internal/ticket"
)

type fakeGitHub struct {
	prs         []ticket.PullRequest
	reviews     map[string][]ticket.PullRequest
	err         error
	failed      map[string][]github.FailedCheck
	changes     map[int][]github.ChangeRequest
	threads     map[int][]github.ReviewThread
	older       []ticket.PullRequest
	keysAsked   []string
	repos       map[string][]string
	ownersAsked []string
	// prCalls counts the full listings PullRequests made.
	prCalls int
}

func (f *fakeGitHub) Repos(_ context.Context, owner string) ([]string, error) {
	f.ownersAsked = append(f.ownersAsked, owner)
	return f.repos[owner], nil
}

func (f *fakeGitHub) PullRequestsForKeys(_ context.Context, _, keys []string) ([]ticket.PullRequest, error) {
	f.keysAsked = append(f.keysAsked, keys...)
	return f.older, nil
}

func (f *fakeGitHub) FailedChecks(_ context.Context, repo, sha string) ([]github.FailedCheck, error) {
	return f.failed[repo+"@"+sha], nil
}
func (f *fakeGitHub) ChangeRequests(_ context.Context, _ string, n int) ([]github.ChangeRequest, error) {
	return f.changes[n], nil
}
func (f *fakeGitHub) UnresolvedThreads(_ context.Context, _ string, n int) ([]github.ReviewThread, error) {
	return f.threads[n], nil
}

func (f *fakeGitHub) PullRequests(context.Context, []string) ([]ticket.PullRequest, error) {
	f.prCalls++
	return f.prs, f.err
}
func (f *fakeGitHub) ReviewRequests(_ context.Context, repos []string, _ bool) ([]ticket.PullRequest, error) {
	var out []ticket.PullRequest
	for _, r := range repos {
		owner, _, _ := strings.Cut(r, "/")
		out = append(out, f.reviews[owner]...)
		if f.reviews != nil {
			f.reviews[owner] = nil
		}
	}
	return out, f.err
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
		{Key: "DEMO-1", Type: "Story", Summary: "Filter accounts by date", Status: "In Progress", StatusCategory: "indeterminate",
			Sprints: []jira.Sprint{{Space: "DEMO", Name: "DEMO S42", State: "active", End: fixedNow.Add(4 * 24 * time.Hour)}}},
		{Key: "DEMO-2", Type: "Sub-task", Summary: "Picker", Status: "Done", StatusCategory: "done", Parent: "DEMO-1"},
		{Key: "DEMO-3", Type: "Story", Summary: "Export CSV", Status: "To Do", StatusCategory: "new"},
		{Key: "OPS-4", Type: "Bug", Summary: "Fix redirect", Status: "In Review", StatusCategory: "indeterminate"},
	}
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
		"REVIEW REQUESTED\n  #820  example-org/web · Add pagination",
		"web#812 ✕",
		"↳ DEMO-2",
		"synced just now",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
	if needs := out[:strings.Index(out, "REVIEW REQUESTED")]; strings.Contains(needs, "#820") {
		t.Errorf("review requests listed under NEEDS YOU:\n%s", out)
	}
	if i, j := strings.Index(out, "\nDEMO-1 "), strings.Index(out, "\nDEMO-3 "); i < 0 || j < 0 || i > j {
		t.Errorf("tickets not ordered by attention and column:\n%s", out)
	}
	if !strings.Contains(h.jira.jql, `project in ("DEMO", "OPS")`) {
		t.Errorf("jql = %s", h.jira.jql)
	}
}

func TestListHidesBotReviews(t *testing.T) {
	for _, args := range [][]string{{"list"}, {"--json", "list"}} {
		h, gh := listHarness(t)
		gh.reviews["example-org"] = append(gh.reviews["example-org"], ticket.PullRequest{Repo: "example-org/web", Number: 830, Title: "Bump vite", Author: "dependabot", AuthorBot: true, State: ticket.PROpen})
		if code := h.exec(args...); code != 0 {
			t.Fatalf("%v: exit %d: %s", args, code, h.errOut)
		}
		if out := h.out.String(); strings.Contains(out, "830") || !strings.Contains(out, "820") {
			t.Errorf("%v: bot review listed or person's review missing:\n%s", args, out)
		}
	}
}

func TestListFilters(t *testing.T) {
	h, _ := listHarness(t)
	h.exec("list", "--space", "OPS")
	if out := h.out.String(); strings.Contains(out, "DEMO-1") || !strings.Contains(out, "OPS-4") || strings.Contains(out, "#820") {
		t.Errorf("--space OPS:\n%s", out)
	}
	h.exec("list", "--needs-you")
	if out := h.out.String(); strings.Contains(out, "DEMO-3") || !strings.Contains(out, "DEMO-1") || strings.Contains(out, "#820") {
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
	if len(got.Snapshot.Tickets) != 3 || len(got.NeedsYou) != 2 || got.Stale {
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

func TestListFindsOlderPRsByKey(t *testing.T) {
	h, gh := listHarness(t)
	gh.older = []ticket.PullRequest{{Repo: "example-org/web", Number: 569, Title: "feat: DEMO-3 export users", Branch: "some-branch", State: ticket.PRMerged}}
	if code := h.exec("list"); code != 0 {
		t.Fatalf("exit %d: %s", code, h.errOut)
	}
	if !strings.Contains(strings.Join(gh.keysAsked, ","), "DEMO-3") || strings.Contains(strings.Join(gh.keysAsked, ","), "DEMO-1") {
		t.Errorf("searched keys = %v (only tickets without PRs)", gh.keysAsked)
	}
	if !strings.Contains(h.out.String(), "1 merged") {
		t.Errorf("older PR found by title not attached:\n%s", h.out)
	}
}

func TestListLeavesOutTeamTickets(t *testing.T) {
	h, _ := listHarness(t)
	h.app.renderList(board.Snapshot{Tickets: []ticket.Ticket{
		{Key: "DEMO-1", Summary: "Mine", Status: "In Progress", Category: ticket.InProgress},
		{Key: "DEMO-9", Summary: "Theirs", Status: "To Do", Category: ticket.ToDo, Team: true},
	}}, nil)
	if out := h.out.String(); !strings.Contains(out, "DEMO-1") || strings.Contains(out, "DEMO-9") {
		t.Errorf("tkt list is the user's tickets:\n%s", out)
	}
}
