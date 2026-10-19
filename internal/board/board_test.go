package board

import (
	"context"
	"errors"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/pavp/tkt/internal/config"
	"github.com/pavp/tkt/internal/jira"
	"github.com/pavp/tkt/internal/ticket"
)

type stubJira struct {
	issues []jira.Issue
	err    error
	jql    string
	// linked maps issue ids to their development-panel PRs; nil means the panel is unavailable.
	linked map[string][]jira.DevPullRequest
}

func (s *stubJira) Search(_ context.Context, jql string, _ []string) ([]jira.Issue, error) {
	s.jql = jql
	return s.issues, s.err
}
func (s *stubJira) BrowseURL(k string) string { return "https://example.atlassian.net/browse/" + k }
func (s *stubJira) DevPullRequests(_ context.Context, id string) ([]jira.DevPullRequest, error) {
	if s.linked == nil {
		return nil, errors.New("dev-status: 404")
	}
	return s.linked[id], nil
}

type stubGitHub struct {
	prs         []ticket.PullRequest
	prErr       error
	reviews     []ticket.PullRequest
	reviewErr   error
	repos       []string
	reviewRepos []string
	direct      bool
	reviewCalls int
}

func (s *stubGitHub) PullRequests(_ context.Context, repos []string) ([]ticket.PullRequest, error) {
	s.repos = repos
	return s.prs, s.prErr
}
func (s *stubGitHub) PullRequestsForKeys(context.Context, []string, []string) ([]ticket.PullRequest, error) {
	return nil, nil
}

func (s *stubGitHub) ReviewRequests(_ context.Context, repos []string, direct bool) ([]ticket.PullRequest, error) {
	s.reviewCalls++
	s.reviewRepos, s.direct = repos, direct
	return s.reviews, s.reviewErr
}

func cfg() *config.Config {
	return &config.Config{
		Jira: config.Jira{Site: "example.atlassian.net", Email: "dev@example.com"},
		Spaces: []config.Space{
			{Key: "DEMO", StatusOverrides: map[string]string{"Code Review": "review"}, Repos: []config.Repo{{Name: "example-org/web"}, {Name: "example-org/api"}}},
			{Key: "OPS", Repos: []config.Repo{{Name: "example-org/web"}}},
		},
	}
}

var now = time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC)

func TestFetchAssemblesTickets(t *testing.T) {
	js := &stubJira{
		issues: []jira.Issue{
			{Key: "DEMO-1", Summary: "Filter", Type: "Story", Status: "In Progress", StatusCategory: "indeterminate",
				Sprints: []jira.Sprint{{Space: "DEMO", Name: "DEMO S41", State: "closed"}, {Space: "DEMO", Name: "DEMO S42", State: "active"}}},
			{Key: "DEMO-2", Summary: "Sub", Type: "Sub-task", Status: "Code Review", StatusCategory: "indeterminate", Parent: "DEMO-1"},
			{Key: "OPS-3", Summary: "Ops", Type: "Task", Status: "To Do", StatusCategory: "new",
				Sprints: []jira.Sprint{{Space: "OPS", Name: "DEMO S42", State: "active"}}},
		},
	}
	gh := &stubGitHub{
		prs: []ticket.PullRequest{{Repo: "example-org/web", Number: 9, Branch: "feature/DEMO-1-filter", State: ticket.PROpen, Checks: ticket.ChecksFail}},
		reviews: []ticket.PullRequest{
			{Repo: "example-org/web", Number: 20, Branch: "feature/DEMO-9", State: ticket.PROpen},
			{Repo: "example-org/web", Number: 20, Branch: "feature/DEMO-9", State: ticket.PROpen},
		},
	}
	snap, err := Fetch(context.Background(), cfg(), js, gh, now)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(js.jql, `project in ("DEMO", "OPS")`) {
		t.Errorf("jql = %s", js.jql)
	}
	if !reflect.DeepEqual(gh.repos, []string{"example-org/api", "example-org/web"}) {
		t.Errorf("repos queried = %v", gh.repos)
	}
	if len(snap.Tickets) != 2 {
		t.Fatalf("top-level tickets = %+v", snap.Tickets)
	}
	demo := snap.Tickets[0]
	if demo.Category != ticket.InProgress || demo.URL != "https://example.atlassian.net/browse/DEMO-1" || len(demo.PRs) != 1 {
		t.Errorf("DEMO-1 = %+v", demo)
	}
	if len(demo.Subtasks) != 1 || demo.Subtasks[0].Category != ticket.InReview {
		t.Errorf("subtask override not applied: %+v", demo.Subtasks)
	}
	if len(snap.Reviews) != 1 {
		t.Errorf("reviews not deduplicated: %+v", snap.Reviews)
	}
	if len(snap.Sprints) != 1 || snap.Sprints[0].Name != "DEMO S42" || !snap.FetchedAt.Equal(now) || len(snap.Warnings) != 0 {
		t.Errorf("only the active sprint of my tickets, once: %+v", snap.Sprints)
	}
	if !gh.direct || !reflect.DeepEqual(gh.reviewRepos, []string{"example-org/api", "example-org/web"}) {
		t.Errorf("reviews queried for %v direct=%v", gh.reviewRepos, gh.direct)
	}
}

func TestFetchReviewModes(t *testing.T) {
	js := &stubJira{issues: []jira.Issue{{Key: "DEMO-1", StatusCategory: "new"}}}
	c := cfg()
	c.Reviews = config.ReviewsOff
	gh := &stubGitHub{reviews: []ticket.PullRequest{{Number: 1}}}
	snap, err := Fetch(context.Background(), c, js, gh, now)
	if err != nil || gh.reviewCalls != 0 || len(snap.Reviews) != 0 {
		t.Errorf("off: calls %d, reviews %d, %v", gh.reviewCalls, len(snap.Reviews), err)
	}
	c.Reviews = config.ReviewsTeam
	if _, err := Fetch(context.Background(), c, js, gh, now); err != nil || gh.direct {
		t.Errorf("team mode asked for direct requests only: %v", err)
	}
}

func TestFetchDegradesWithoutGitHub(t *testing.T) {
	js := &stubJira{issues: []jira.Issue{{Key: "DEMO-1", StatusCategory: "new"}}}
	gh := &stubGitHub{prErr: errors.New("rate limited"), reviewErr: errors.New("rate limited")}
	snap, err := Fetch(context.Background(), cfg(), js, gh, now)
	if err != nil {
		t.Fatal(err)
	}
	if len(snap.Tickets) != 1 {
		t.Errorf("tickets = %+v", snap.Tickets)
	}
	joined := strings.Join(snap.Warnings, "\n")
	for _, want := range []string{"pull requests unavailable: rate limited", "review requests unavailable"} {
		if !strings.Contains(joined, want) {
			t.Errorf("warnings missing %q: %v", want, snap.Warnings)
		}
	}
}

func TestFetchFailsWithoutJiraOrSpaces(t *testing.T) {
	if _, err := Fetch(context.Background(), cfg(), &stubJira{err: jira.ErrUnauthorized}, &stubGitHub{}, now); !errors.Is(err, jira.ErrUnauthorized) {
		t.Errorf("err = %v", err)
	}
	c := cfg()
	c.Spaces = nil
	if _, err := Fetch(context.Background(), c, &stubJira{}, &stubGitHub{}, now); err == nil || !strings.Contains(err.Error(), "no Jira spaces configured") {
		t.Errorf("err = %v", err)
	}
}

func TestNeedsYouAndSorted(t *testing.T) {
	snap := Snapshot{
		Tickets: []ticket.Ticket{
			{Key: "DEMO-5", Category: ticket.ToDo},
			{Key: "DEMO-4", Category: ticket.Done},
			{Key: "DEMO-3", Category: ticket.InReview, PRs: []ticket.PullRequest{{State: ticket.PROpen, Review: ticket.ReviewChangesRequested}}},
			{Key: "DEMO-2", Category: ticket.InProgress},
			{Key: "DEMO-1", Category: ticket.InProgress, PRs: []ticket.PullRequest{{State: ticket.PROpen, Checks: ticket.ChecksFail}}},
		},
		Reviews: []ticket.PullRequest{{Repo: "example-org/web", Number: 20, Title: "Pagination"}},
	}
	var refs []string
	for _, a := range snap.NeedsYou() {
		refs = append(refs, a.Ref+":"+string(a.Reason))
	}
	if !reflect.DeepEqual(refs, []string{"DEMO-3:Changes requested", "DEMO-1:CI failing"}) {
		t.Errorf("NeedsYou = %v", refs)
	}
	var keys []ticket.Key
	for _, tk := range Sorted(snap.Tickets) {
		keys = append(keys, tk.Key)
	}
	if !reflect.DeepEqual(keys, []ticket.Key{"DEMO-1", "DEMO-3", "DEMO-2", "DEMO-5", "DEMO-4"}) {
		t.Errorf("Sorted = %v", keys)
	}
	if got := snap.Column(ticket.InProgress); len(got) != 2 {
		t.Errorf("Column = %+v", got)
	}
}

func TestFilter(t *testing.T) {
	snap := Snapshot{
		Tickets: []ticket.Ticket{{Key: "DEMO-1"}, {Key: "OPS-2"}},
		Reviews: []ticket.PullRequest{{Number: 1, Branch: "feature/DEMO-9"}, {Number: 2, Branch: "chore/x"}},
		Sprints: []jira.Sprint{{Space: "DEMO"}, {Space: "OPS"}},
	}
	got := snap.Filter("DEMO")
	if len(got.Tickets) != 1 || len(got.Reviews) != 1 || len(got.Sprints) != 1 {
		t.Errorf("Filter = %+v", got)
	}
	if all := snap.Filter(""); len(all.Tickets) != 2 {
		t.Error("empty filter dropped tickets")
	}
}

func TestDaysLeft(t *testing.T) {
	for _, tt := range []struct {
		end  time.Time
		want int
	}{
		{now.Add(4*24*time.Hour - time.Hour), 4},
		{now.Add(24 * time.Hour), 1},
		{now.Add(time.Minute), 1},
		{now.Add(-time.Hour), 0},
		{time.Time{}, 0},
	} {
		if got := DaysLeft(tt.end, now); got != tt.want {
			t.Errorf("DaysLeft(%v) = %d, want %d", tt.end, got, tt.want)
		}
	}
}

func TestCacheRoundTrip(t *testing.T) {
	c := NewCache(filepath.Join(t.TempDir(), "data"))
	if _, ok, err := c.Load(); ok || err != nil {
		t.Fatalf("empty cache = %v, %v", ok, err)
	}
	in := Snapshot{FetchedAt: now, Tickets: []ticket.Ticket{{Key: "DEMO-1", Category: ticket.ToDo}}}
	if err := c.Save(in); err != nil {
		t.Fatal(err)
	}
	out, ok, err := c.Load()
	if err != nil || !ok || len(out.Tickets) != 1 || !out.FetchedAt.Equal(now) {
		t.Fatalf("Load = %+v, %v, %v", out, ok, err)
	}
}

func TestSidebarLabel(t *testing.T) {
	failing := ticket.Ticket{Key: "DEMO-1", Status: "In Progress", Category: ticket.InProgress, PRs: []ticket.PullRequest{
		{Number: 812, State: ticket.PROpen, Checks: ticket.ChecksFail},
		{Number: 790, State: ticket.PRMerged, Checks: ticket.ChecksPass},
	}}
	if got := SidebarLabel(failing, "working"); got != "DEMO-1 · ✕ CI · #812 · claude working" {
		t.Errorf("failing = %q", got)
	}
	changes := ticket.Ticket{Key: "DEMO-2", Category: ticket.InReview, PRs: []ticket.PullRequest{{Number: 45, State: ticket.PROpen, Checks: ticket.ChecksPass, Review: ticket.ReviewChangesRequested}}}
	if got := SidebarLabel(changes, ""); got != "DEMO-2 · changes requested · #45" {
		t.Errorf("changes = %q", got)
	}
	green := ticket.Ticket{Key: "DEMO-3", Category: ticket.InReview, PRs: []ticket.PullRequest{{Number: 9, State: ticket.PROpen, Checks: ticket.ChecksPass}}}
	if got := SidebarLabel(green, "idle"); got != "DEMO-3 · ✓ CI · #9 · claude idle" {
		t.Errorf("green = %q", got)
	}
	if got := SidebarLabel(ticket.Ticket{Key: "DEMO-4", Status: "To Do"}, ""); got != "DEMO-4 · To Do" {
		t.Errorf("todo = %q", got)
	}
	long := ticket.Ticket{Key: "DEMO-5", Status: strings.Repeat("x", 100)}
	if got := SidebarLabel(long, ""); len([]rune(got)) != 80 || !strings.HasSuffix(got, "…") {
		t.Errorf("long label = %d runes", len([]rune(got)))
	}
}

func TestFind(t *testing.T) {
	s := Snapshot{Tickets: []ticket.Ticket{{Key: "DEMO-1", Subtasks: []ticket.Ticket{{Key: "DEMO-2"}}}}}
	if _, ok := s.Find("DEMO-2"); !ok {
		t.Error("subtask not found")
	}
	if _, ok := s.Find("DEMO-9"); ok {
		t.Error("found a missing ticket")
	}
}

func TestSprintLabel(t *testing.T) {
	for _, tt := range []struct {
		sp   jira.Sprint
		want string
	}{
		{jira.Sprint{Name: "S42", End: now.Add(4 * 24 * time.Hour)}, "S42 · 4d left"},
		{jira.Sprint{Name: "S41", End: now.Add(-time.Hour)}, "S41 · overdue"},
		{jira.Sprint{Name: "Kanban"}, "Kanban"},
	} {
		if got := SprintLabel(tt.sp, now); got != tt.want {
			t.Errorf("SprintLabel = %q, want %q", got, tt.want)
		}
	}
}

type countingGitHub struct {
	stubGitHub
	keyCalls [][]string
}

func (c *countingGitHub) PullRequestsForKeys(_ context.Context, _ []string, keys []string) ([]ticket.PullRequest, error) {
	c.keyCalls = append(c.keyCalls, keys)
	return nil, nil
}

func TestFetchTakesLinkedPullRequestsFromJira(t *testing.T) {
	js := &stubJira{
		issues: []jira.Issue{
			{ID: "101", Key: "DEMO-1", Status: "In Progress", StatusCategory: "indeterminate"},
			{ID: "102", Key: "DEMO-2", Status: "Done", StatusCategory: "done"},
		},
		linked: map[string][]jira.DevPullRequest{
			"101": {{Repo: "example-org/web", Number: 9, Status: "OPEN"}},
			"102": {
				{Repo: "example-org/other", Number: 3, Title: "Old work", URL: "https://github.com/example-org/other/pull/3", Branch: "fix/x", Status: "MERGED"},
				{Repo: "example-org/web", Number: 4, Status: "DECLINED"},
			},
		},
	}
	gh := &countingGitHub{stubGitHub: stubGitHub{prs: []ticket.PullRequest{
		{Repo: "example-org/web", Number: 9, Branch: "no-key-here", State: ticket.PROpen, Checks: ticket.ChecksFail},
	}}}
	snap, err := Fetch(context.Background(), cfg(), js, gh, now)
	if err != nil {
		t.Fatal(err)
	}
	got := map[ticket.Key][]ticket.PullRequest{}
	for _, tk := range snap.Tickets {
		got[tk.Key] = tk.PRs
	}
	if prs := got["DEMO-1"]; len(prs) != 1 || prs[0].Checks != ticket.ChecksFail {
		t.Errorf("DEMO-1 should carry GitHub's copy with its checks: %+v", prs)
	}
	want := []ticket.PullRequest{
		{Repo: "example-org/other", Number: 3, Title: "Old work", URL: "https://github.com/example-org/other/pull/3", Branch: "fix/x", State: ticket.PRMerged},
		{Repo: "example-org/web", Number: 4, State: ticket.PRClosed},
	}
	if !reflect.DeepEqual(got["DEMO-2"], want) {
		t.Errorf("DEMO-2 PRs = %+v, want %+v", got["DEMO-2"], want)
	}
	if len(gh.keyCalls) != 0 || len(snap.Warnings) != 0 {
		t.Errorf("key search %v and warnings %v should not happen when Jira answered", gh.keyCalls, snap.Warnings)
	}
}

func TestFetchFallsBackToKeySearchWithoutJiraPanel(t *testing.T) {
	js := &stubJira{issues: []jira.Issue{
		{ID: "101", Key: "DEMO-1", Status: "In Progress", StatusCategory: "indeterminate"},
		{ID: "102", Key: "DEMO-2", Status: "Done", StatusCategory: "done"},
	}}
	gh := &countingGitHub{stubGitHub: stubGitHub{prs: []ticket.PullRequest{
		{Repo: "example-org/web", Number: 9, Branch: "feature/DEMO-1-a", State: ticket.PROpen},
	}}}
	snap, err := Fetch(context.Background(), cfg(), js, gh, now)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(gh.keyCalls, [][]string{{"DEMO-2"}}) || len(snap.Warnings) != 0 {
		t.Errorf("key search = %v, warnings = %v", gh.keyCalls, snap.Warnings)
	}
}

func TestFetchInfersRepositoriesFromJiraLinkedPRs(t *testing.T) {
	c := &config.Config{
		Jira: config.Jira{Site: "example.atlassian.net", Email: "dev@example.com"},
		Spaces: []config.Space{
			{Key: "DEMO"},
			{Key: "OPS", Repos: []config.Repo{{Name: "example-org/ops"}}},
		},
	}
	js := &stubJira{
		issues: []jira.Issue{
			{ID: "101", Key: "DEMO-1", Status: "In Progress", StatusCategory: "indeterminate"},
			{ID: "102", Key: "DEMO-2", Status: "Done", StatusCategory: "done"},
		},
		linked: map[string][]jira.DevPullRequest{
			"101": {{Repo: "example-org/web", Number: 9, Status: "OPEN"}},
			"102": {{Repo: "Example-Org/Web", Number: 3, Status: "MERGED"}, {Repo: "example-org/api", Number: 4, Status: "MERGED"}},
		},
	}
	gh := &stubGitHub{prs: []ticket.PullRequest{{Repo: "example-org/web", Number: 9, Branch: "x", State: ticket.PROpen, Checks: ticket.ChecksFail}}}
	snap, err := Fetch(context.Background(), c, js, gh, now)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"example-org/api", "example-org/ops", "example-org/web"}
	if !reflect.DeepEqual(gh.repos, want) || !reflect.DeepEqual(gh.reviewRepos, want) {
		t.Errorf("GitHub queried %v and reviews %v, want %v", gh.repos, gh.reviewRepos, want)
	}
	if got := snap.Repos; !reflect.DeepEqual(got, map[string][]string{"DEMO": {"example-org/api", "example-org/web"}, "OPS": {"example-org/ops"}}) {
		t.Errorf("snapshot repos = %v", got)
	}
	if prs := snap.Tickets[0].PRs; len(prs) != 1 || prs[0].Checks != ticket.ChecksFail {
		t.Errorf("DEMO-1 should get CI from the inferred repo: %+v", prs)
	}
}
