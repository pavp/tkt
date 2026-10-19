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
	issues    []jira.Issue
	err       error
	jql       string
	sprints   map[string][]jira.Sprint
	sprintErr error
}

func (s *stubJira) Search(_ context.Context, jql string, _ []string) ([]jira.Issue, error) {
	s.jql = jql
	return s.issues, s.err
}
func (s *stubJira) ActiveSprints(_ context.Context, p string) ([]jira.Sprint, error) {
	return s.sprints[p], s.sprintErr
}
func (s *stubJira) BrowseURL(k string) string { return "https://example.atlassian.net/browse/" + k }

type stubGitHub struct {
	prs       []ticket.PullRequest
	prErr     error
	reviews   map[string][]ticket.PullRequest
	reviewErr error
	repos     []string
}

func (s *stubGitHub) PullRequests(_ context.Context, repos []string) ([]ticket.PullRequest, error) {
	s.repos = repos
	return s.prs, s.prErr
}
func (s *stubGitHub) ReviewRequests(_ context.Context, owner string) ([]ticket.PullRequest, error) {
	return s.reviews[owner], s.reviewErr
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
			{Key: "DEMO-1", Summary: "Filter", Type: "Story", Status: "In Progress", StatusCategory: "indeterminate"},
			{Key: "DEMO-2", Summary: "Sub", Type: "Sub-task", Status: "Code Review", StatusCategory: "indeterminate", Parent: "DEMO-1"},
			{Key: "OPS-3", Summary: "Ops", Type: "Task", Status: "To Do", StatusCategory: "new"},
		},
		sprints: map[string][]jira.Sprint{"DEMO": {{Space: "DEMO", Name: "DEMO S42"}}},
	}
	gh := &stubGitHub{
		prs: []ticket.PullRequest{{Repo: "example-org/web", Number: 9, Branch: "feature/DEMO-1-filter", State: ticket.PROpen, Checks: ticket.ChecksFail}},
		reviews: map[string][]ticket.PullRequest{"example-org": {
			{Repo: "example-org/web", Number: 20, Branch: "feature/DEMO-9", State: ticket.PROpen},
			{Repo: "example-org/web", Number: 20, Branch: "feature/DEMO-9", State: ticket.PROpen},
		}},
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
	if len(snap.Sprints) != 1 || !snap.FetchedAt.Equal(now) || len(snap.Warnings) != 0 {
		t.Errorf("snapshot = %+v", snap)
	}
}

func TestFetchDegradesWithoutGitHub(t *testing.T) {
	js := &stubJira{issues: []jira.Issue{{Key: "DEMO-1", StatusCategory: "new"}}, sprintErr: errors.New("agile disabled")}
	gh := &stubGitHub{prErr: errors.New("rate limited"), reviewErr: errors.New("rate limited")}
	snap, err := Fetch(context.Background(), cfg(), js, gh, now)
	if err != nil {
		t.Fatal(err)
	}
	if len(snap.Tickets) != 1 {
		t.Errorf("tickets = %+v", snap.Tickets)
	}
	joined := strings.Join(snap.Warnings, "\n")
	for _, want := range []string{"pull requests unavailable: rate limited", "review requests unavailable", "sprints of DEMO unavailable"} {
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
	if !reflect.DeepEqual(refs, []string{"DEMO-3:Changes requested", "DEMO-1:CI failing", "#20:Review requested"}) {
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
