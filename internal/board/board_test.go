package board

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/pavp/tkt/internal/config"
	"github.com/pavp/tkt/internal/jira"
	"github.com/pavp/tkt/internal/ticket"
)

type stubJira struct {
	mu     sync.Mutex
	issues []jira.Issue
	err    error
	jql    string
	// team answers the team search, teamErr fails it; jqls records every search and panels every
	// development panel asked for.
	team    []jira.Issue
	teamErr error
	jqls    []string
	panels  []string
	// sprints answers ActiveSprints per board.
	sprints map[int][]int
	// teamGate, when set, holds the team search until the GitHub listing starts; teamStalled records a
	// search that gave up waiting, as one that held the listing back would.
	teamGate    chan struct{}
	teamStalled bool
	// linked maps issue ids to their development-panel PRs; nil means the panel is unavailable.
	linked map[string][]jira.DevPullRequest
	// fieldIDs answers FieldID; fields records what Search asked for.
	fieldIDs   map[string]string
	fields     []string
	fieldCalls int
}

func (s *stubJira) Search(_ context.Context, jql string, fields []string) ([]jira.Issue, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.jqls = append(s.jqls, jql)
	if strings.Contains(jql, "assignee != currentUser()") {
		gate := s.teamGate
		s.mu.Unlock()
		if gate != nil {
			select {
			case <-gate:
			case <-time.After(2 * time.Second):
				s.mu.Lock()
				s.teamStalled = true
				s.mu.Unlock()
			}
		}
		s.mu.Lock()
		return s.team, s.teamErr
	}
	s.jql, s.fields = jql, fields
	return s.issues, s.err
}

func (s *stubJira) ActiveSprints(_ context.Context, board int) ([]int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.sprints[board], nil
}

func (s *stubJira) FieldID(_ context.Context, name string) (string, error) {
	s.fieldCalls++
	if id, ok := s.fieldIDs[name]; ok {
		return id, nil
	}
	return "", jira.ErrFieldNotFound
}
func (s *stubJira) BrowseURL(k string) string { return "https://example.atlassian.net/browse/" + k }
func (s *stubJira) DevPullRequests(_ context.Context, id string) ([]jira.DevPullRequest, error) {
	s.mu.Lock()
	s.panels = append(s.panels, id)
	s.mu.Unlock()
	if s.linked == nil {
		return nil, errors.New("dev-status: 404")
	}
	return s.linked[id], nil
}

type stubGitHub struct {
	// listing, when set, is closed as the PR listing starts.
	listing chan struct{}
	// hold, when set, keeps the PR listing waiting until it closes; held records a listing that gave up.
	hold        chan struct{}
	held        bool
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
	if s.listing != nil {
		close(s.listing)
	}
	if s.hold != nil {
		select {
		case <-s.hold:
		case <-time.After(2 * time.Second):
			s.held = true
		}
	}
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

// teamCfg is cfg with a team board on DEMO.
func teamCfg() *config.Config {
	c := cfg()
	c.Spaces[0].TeamBoards = []config.TeamBoard{{ID: 5205, Name: "Authorization Center"}}
	return c
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

func TestFetchKeepsBotReviewsMarkedAndGroupsThemByRepo(t *testing.T) {
	js := &stubJira{issues: []jira.Issue{{Key: "DEMO-1", StatusCategory: "new"}}}
	reviews := []ticket.PullRequest{
		{Repo: "example-org/web", Number: 9},
		{Repo: "example-org/api", Number: 4, Author: "dependabot", AuthorBot: true},
		{Repo: "example-org/api", Number: 7},
		{Repo: "example-org/web", Number: 2},
	}
	snap, err := Fetch(context.Background(), cfg(), js, &stubGitHub{reviews: reviews}, now)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := reviewIDs(snap.Reviews), []string{"example-org/api#4", "example-org/api#7", "example-org/web#2", "example-org/web#9"}; !reflect.DeepEqual(got, want) {
		t.Errorf("reviews = %v, want %v", got, want)
	}
	if got, want := reviewIDs(snap.WithoutBots().Reviews), []string{"example-org/api#7", "example-org/web#2", "example-org/web#9"}; !reflect.DeepEqual(got, want) {
		t.Errorf("without bots = %v, want %v", got, want)
	}
	if len(snap.Reviews) != 4 {
		t.Error("WithoutBots changed the snapshot it was called on")
	}
}

func reviewIDs(prs []ticket.PullRequest) []string {
	var out []string
	for _, p := range prs {
		out = append(out, fmt.Sprintf("%s#%d", p.Repo, p.Number))
	}
	return out
}

func TestToTicketPutsBlockedTicketsInToDo(t *testing.T) {
	c := cfg()
	c.Spaces[1].StatusOverrides = map[string]string{"Blocked": "in_progress"}
	tests := []struct {
		key, status string
		want        ticket.Category
	}{
		{"DEMO-1", "Blocked", ticket.ToDo},
		{"DEMO-2", "Bloqueado", ticket.ToDo},
		{"DEMO-3", "In Progress", ticket.InProgress},
		{"OPS-1", "Blocked", ticket.InProgress},
	}
	for _, tt := range tests {
		got := ToTicket(c, jira.Issue{Key: tt.key, Status: tt.status, StatusCategory: "indeterminate"}, "")
		if got.Category != tt.want {
			t.Errorf("%s %q in %q, want %q", tt.key, tt.status, got.Category, tt.want)
		}
	}
}

func TestToTicketMarksStatusesAsConfigured(t *testing.T) {
	c := cfg()
	c.Spaces[0].StatusOverrides = map[string]string{"Pending test": "review"}
	c.Spaces[0].StatusMarks = map[string]string{"On hold": "blocked"}
	tests := []struct {
		status string
		want   ticket.Mark
	}{
		{"Pending test", ticket.MarkStatus},
		{"On hold", ticket.MarkBlocked},
		{"Blocked", ticket.MarkBlocked},
		{"In Progress", ticket.MarkNone},
	}
	for _, tt := range tests {
		got := ToTicket(c, jira.Issue{Key: "DEMO-1", Status: tt.status, StatusCategory: "indeterminate"}, "")
		if got.Mark != tt.want || got.Blocked() != (tt.want == ticket.MarkBlocked) {
			t.Errorf("%q marked %q (blocked %v), want %q", tt.status, got.Mark, got.Blocked(), tt.want)
		}
	}
}

func TestFetchLabelsTicketsWithTheConfiguredField(t *testing.T) {
	issues := []jira.Issue{
		{Key: "DEMO-1", StatusCategory: "new", Fields: map[string]json.RawMessage{"customfield_10136": json.RawMessage(`{"value":"User Access Center"}`)}},
		{Key: "DEMO-2", StatusCategory: "new", Fields: map[string]json.RawMessage{"customfield_10136": json.RawMessage(`null`)}},
	}
	tests := []struct {
		name, field string
		wantLabel   string
		wantWarning string
		wantField   bool
	}{
		{"configured", "Product", "User Access Center", "", true},
		{"missing in Jira", "Team", "", `Jira has no field "Team": check label_field in the config`, false},
		{"not configured", "", "", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			js := &stubJira{issues: issues, fieldIDs: map[string]string{"Product": "customfield_10136"}}
			c := cfg()
			c.Jira.LabelField = tt.field
			snap, err := Fetch(context.Background(), c, js, &stubGitHub{}, now)
			if err != nil {
				t.Fatal(err)
			}
			if snap.Tickets[0].Label != tt.wantLabel || snap.Tickets[1].Label != "" {
				t.Errorf("labels = %q, %q", snap.Tickets[0].Label, snap.Tickets[1].Label)
			}
			if got := slices.Contains(js.fields, "customfield_10136"); got != tt.wantField {
				t.Errorf("fields asked = %v", js.fields)
			}
			if tt.wantWarning != "" && !slices.Contains(snap.Warnings, tt.wantWarning) {
				t.Errorf("warnings = %v", snap.Warnings)
			}
			if tt.field == "" && js.fieldCalls != 0 {
				t.Errorf("looked up a field nobody configured")
			}
		})
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

func TestTicketLine(t *testing.T) {
	failing := ticket.Ticket{Key: "DEMO-1", Status: "In Progress", Category: ticket.InProgress, PRs: []ticket.PullRequest{
		{Number: 812, State: ticket.PROpen, Checks: ticket.ChecksFail},
		{Number: 790, State: ticket.PRMerged, Checks: ticket.ChecksPass},
	}}
	tests := []struct {
		name  string
		t     ticket.Ticket
		want  string
		alert bool
	}{
		{"failing CI", failing, "✕ CI · #812", true},
		{"changes requested", ticket.Ticket{Key: "DEMO-2", Category: ticket.InReview, PRs: []ticket.PullRequest{{Number: 45, State: ticket.PROpen, Checks: ticket.ChecksPass, Review: ticket.ReviewChangesRequested}}}, "changes requested · #45", true},
		{"green", ticket.Ticket{Key: "DEMO-3", Category: ticket.InReview, PRs: []ticket.PullRequest{{Number: 9, State: ticket.PROpen, Checks: ticket.ChecksPass}}}, "✓ CI · #9", false},
		{"no PRs", ticket.Ticket{Key: "DEMO-4", Status: "To Do"}, "To Do", false},
	}
	for _, tt := range tests {
		got, alert := TicketLine(tt.t)
		if got != tt.want || alert != tt.alert {
			t.Errorf("%s: got %q, %v; want %q, %v", tt.name, got, alert, tt.want, tt.alert)
		}
	}
	long, _ := TicketLine(ticket.Ticket{Key: "DEMO-5", Status: strings.Repeat("x", 100)})
	if len([]rune(long)) != 80 || !strings.HasSuffix(long, "…") {
		t.Errorf("long line = %d runes", len([]rune(long)))
	}
}

func TestAgentLine(t *testing.T) {
	tests := []struct {
		status, want string
		kind         AgentKind
	}{
		{"working", "● claude working", AgentWorking},
		{"blocked", "! claude needs you", AgentNeedsYou},
		{"idle", "○ claude idle", AgentIdle},
		{"done", "○ claude idle", AgentIdle},
		{"unknown", "no agent", AgentNone},
		{"", "no agent", AgentNone},
	}
	for _, tt := range tests {
		got, kind := AgentLine(tt.status)
		if got != tt.want || kind != tt.kind {
			t.Errorf("%q: got %q, %v; want %q, %v", tt.status, got, kind, tt.want, tt.kind)
		}
	}
}

func TestMostUrgentAgent(t *testing.T) {
	tests := []struct {
		in   []string
		want string
	}{
		{[]string{"idle", "working"}, "working"},
		{[]string{"working", "blocked", "idle"}, "blocked"},
		{[]string{"done", "idle"}, "idle"},
		{[]string{"unknown"}, ""},
		{nil, ""},
	}
	for _, tt := range tests {
		got, _ := AgentLine(MostUrgentAgent(tt.in))
		if want, _ := AgentLine(tt.want); got != want {
			t.Errorf("%v: got %q, want %q", tt.in, got, tt.want)
		}
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

func TestFetchWarnsAboutSpacesWithoutRepositories(t *testing.T) {
	c := &config.Config{Spaces: []config.Space{{Key: "DEMO"}, {Key: "OPS", Repos: []config.Repo{{Name: "example-org/ops"}}}}}
	js := &stubJira{issues: []jira.Issue{{ID: "1", Key: "DEMO-1", Status: "To Do", StatusCategory: "new"}}, linked: map[string][]jira.DevPullRequest{}}
	snap, err := Fetch(context.Background(), c, js, &stubGitHub{}, now)
	if err != nil {
		t.Fatal(err)
	}
	want := "DEMO has no repositories yet: none of your tickets has a pull request, so CI and reviews are off; add one in the config"
	if !reflect.DeepEqual(snap.Warnings, []string{want}) {
		t.Errorf("warnings = %q", snap.Warnings)
	}
}

func TestFetchSkipsIgnoredRepositories(t *testing.T) {
	c := &config.Config{
		Jira: config.Jira{Site: "example.atlassian.net", Email: "dev@example.com"},
		Spaces: []config.Space{
			{Key: "DEMO", Ignored: []string{"Example-Org/API", "example-org/ops"}, Repos: []config.Repo{{Name: "example-org/ops"}}},
		},
	}
	js := &stubJira{
		issues: []jira.Issue{{ID: "101", Key: "DEMO-1", Status: "In Progress", StatusCategory: "indeterminate"}},
		linked: map[string][]jira.DevPullRequest{
			"101": {{Repo: "example-org/web", Number: 9, Status: "OPEN"}, {Repo: "example-org/api", Number: 4, Status: "MERGED"}},
		},
	}
	gh := &stubGitHub{}
	snap, err := Fetch(context.Background(), c, js, gh, now)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"example-org/ops", "example-org/web"}
	if !reflect.DeepEqual(gh.repos, want) || !reflect.DeepEqual(snap.Repos["DEMO"], want) {
		t.Errorf("GitHub queried %v, snapshot repos %v, want %v (configured repos stay even when ignored)", gh.repos, snap.Repos, want)
	}
	for _, pr := range snap.Tickets[0].PRs {
		if strings.EqualFold(pr.Repo, "example-org/api") {
			t.Errorf("a pull request from an ignored repository is still shown: %+v", pr)
		}
	}
}

func TestFetchExplainsASpaceWhoseRepositoriesAreAllIgnored(t *testing.T) {
	c := &config.Config{
		Jira:   config.Jira{Site: "example.atlassian.net", Email: "dev@example.com"},
		Spaces: []config.Space{{Key: "DEMO", Ignored: []string{"example-org/api"}}},
	}
	js := &stubJira{
		issues: []jira.Issue{{ID: "101", Key: "DEMO-1", Status: "In Progress", StatusCategory: "indeterminate"}},
		linked: map[string][]jira.DevPullRequest{"101": {{Repo: "example-org/api", Number: 4, Status: "OPEN"}}},
	}
	snap, err := Fetch(context.Background(), c, js, &stubGitHub{}, now)
	if err != nil {
		t.Fatal(err)
	}
	want := "DEMO has no repositories yet: you ignore every repository of its pull requests, so CI and reviews are off; add one in the config"
	if !slices.Contains(snap.Warnings, want) {
		t.Errorf("warnings = %q", snap.Warnings)
	}
}

// parallelJira answers DevPullRequests only once enough calls overlap, recording how many ran at once.
type parallelJira struct {
	stubJira
	want     int32
	inFlight atomic.Int32
	most     atomic.Int32
}

func (p *parallelJira) DevPullRequests(ctx context.Context, id string) ([]jira.DevPullRequest, error) {
	n := p.inFlight.Add(1)
	defer p.inFlight.Add(-1)
	for {
		if m := p.most.Load(); n <= m || p.most.CompareAndSwap(m, n) {
			break
		}
	}
	deadline := time.Now().Add(300 * time.Millisecond)
	for p.inFlight.Load() < p.want && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	return p.stubJira.DevPullRequests(ctx, id)
}

func TestFetchAsksJiraForPullRequestsInParallel(t *testing.T) {
	js := &parallelJira{want: 8}
	js.linked = map[string][]jira.DevPullRequest{}
	for i := range 12 {
		id := fmt.Sprint(100 + i)
		js.issues = append(js.issues, jira.Issue{ID: id, Key: fmt.Sprintf("DEMO-%d", i+1), Status: "To Do", StatusCategory: "new"})
		js.linked[id] = []jira.DevPullRequest{{Repo: fmt.Sprintf("example-org/repo%02d", i), Number: i + 1, Status: "OPEN"}}
	}
	c := &config.Config{Jira: config.Jira{Site: "example.atlassian.net", Email: "dev@example.com"}, Spaces: []config.Space{{Key: "DEMO"}}}
	snap, err := Fetch(context.Background(), c, js, &stubGitHub{}, now)
	if err != nil {
		t.Fatal(err)
	}
	if got := js.most.Load(); got != 8 {
		t.Errorf("%d development-panel calls ran at once, want 8", got)
	}
	if len(snap.Repos["DEMO"]) != 12 {
		t.Errorf("repos = %v", snap.Repos["DEMO"])
	}
	for _, tk := range snap.Tickets {
		if len(tk.PRs) != 1 {
			t.Errorf("%s pull requests = %+v", tk.Key, tk.PRs)
		}
	}
}

// overlappingGitHub reports whether review requests were asked while pull requests were still loading.
type overlappingGitHub struct {
	stubGitHub
	reviewsAsked chan struct{}
	overlapped   bool
}

func (o *overlappingGitHub) PullRequests(ctx context.Context, repos []string) ([]ticket.PullRequest, error) {
	select {
	case <-o.reviewsAsked:
		o.overlapped = true
	case <-time.After(time.Second):
	}
	return o.stubGitHub.PullRequests(ctx, repos)
}

func (o *overlappingGitHub) ReviewRequests(ctx context.Context, repos []string, direct bool) ([]ticket.PullRequest, error) {
	close(o.reviewsAsked)
	return o.stubGitHub.ReviewRequests(ctx, repos, direct)
}

func TestFetchAsksForReviewsWhilePullRequestsLoad(t *testing.T) {
	gh := &overlappingGitHub{reviewsAsked: make(chan struct{})}
	gh.reviews = []ticket.PullRequest{{Repo: "example-org/web", Number: 820}}
	snap, err := Fetch(context.Background(), cfg(), &stubJira{issues: []jira.Issue{{ID: "1", Key: "DEMO-1", Status: "To Do", StatusCategory: "new"}}}, gh, now)
	if err != nil {
		t.Fatal(err)
	}
	if !gh.overlapped {
		t.Error("review requests waited for the pull requests")
	}
	if len(snap.Reviews) != 1 {
		t.Errorf("reviews = %+v", snap.Reviews)
	}
}

// countingJira counts development-panel calls per issue id.
type countingJira struct {
	stubJira
	mu    sync.Mutex
	asked map[string]int
}

func (c *countingJira) DevPullRequests(ctx context.Context, id string) ([]jira.DevPullRequest, error) {
	c.mu.Lock()
	if c.asked == nil {
		c.asked = map[string]int{}
	}
	c.asked[id]++
	c.mu.Unlock()
	return c.stubJira.DevPullRequests(ctx, id)
}

func TestFetchSinceReusesDevPanelsOfUnchangedTickets(t *testing.T) {
	updated := time.Date(2026, 10, 20, 9, 0, 0, 0, time.UTC)
	issues := []jira.Issue{
		{ID: "101", Key: "DEMO-1", Status: "To Do", StatusCategory: "new", Updated: updated},
		{ID: "102", Key: "DEMO-2", Status: "To Do", StatusCategory: "new", Updated: updated.Add(time.Hour)},
		{ID: "103", Key: "DEMO-3", Status: "To Do", StatusCategory: "new", Updated: updated},
	}
	cached := []jira.DevPullRequest{{Repo: "example-org/web", Number: 9, Status: "OPEN"}}
	prev := Snapshot{Panels: map[string]DevPanel{
		"DEMO-1": {Updated: updated, Asked: now.Add(-time.Minute), PRs: cached},
		"DEMO-2": {Updated: updated, Asked: now.Add(-time.Minute)},
		"DEMO-3": {Updated: updated, Asked: now.Add(-11 * time.Minute)},
	}}
	js := &countingJira{stubJira: stubJira{issues: issues, linked: map[string][]jira.DevPullRequest{
		"102": {{Repo: "example-org/api", Number: 4, Status: "OPEN"}},
	}}}
	snap, err := FetchSince(context.Background(), cfg(), js, &stubGitHub{}, now, prev)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]int{"102": 1, "103": 1}
	if !reflect.DeepEqual(js.asked, want) {
		t.Errorf("Jira was asked %v, want %v (unchanged and fresh tickets reuse their panel)", js.asked, want)
	}
	if prs := snap.Tickets[0].PRs; len(prs) != 1 || prs[0].Number != 9 {
		t.Errorf("DEMO-1 should keep its cached pull request: %+v", prs)
	}
	if p := snap.Panels["DEMO-2"]; !p.Updated.Equal(updated.Add(time.Hour)) || !p.Asked.Equal(now) || len(p.PRs) != 1 {
		t.Errorf("DEMO-2's fresh panel = %+v", p)
	}
	if p := snap.Panels["DEMO-1"]; !p.Asked.Equal(now.Add(-time.Minute)) {
		t.Errorf("a reused panel keeps when it was asked: %+v", p)
	}
}

func TestFetchSinceDoesNotKeepFailedPanels(t *testing.T) {
	js := &stubJira{issues: []jira.Issue{{ID: "101", Key: "DEMO-1", Status: "To Do", StatusCategory: "new"}}}
	snap, err := FetchSince(context.Background(), cfg(), js, &stubGitHub{}, now, Snapshot{})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := snap.Panels["DEMO-1"]; ok {
		t.Error("a failed development panel must be asked again next time")
	}
}

func TestFetchSinceKeepsIgnoredPullRequestsInTheCachedPanel(t *testing.T) {
	updated := time.Date(2026, 10, 20, 9, 0, 0, 0, time.UTC)
	c := cfg()
	c.Spaces[0].Ignored = []string{"example-org/old"}
	panel := []jira.DevPullRequest{{Repo: "example-org/old", Number: 4, Status: "OPEN"}, {Repo: "example-org/web", Number: 9, Status: "OPEN"}}
	prev := Snapshot{Panels: map[string]DevPanel{"DEMO-1": {Updated: updated, Asked: now, PRs: panel}}}
	js := &stubJira{issues: []jira.Issue{{ID: "101", Key: "DEMO-1", Status: "To Do", StatusCategory: "new", Updated: updated}}}
	snap, err := FetchSince(context.Background(), c, js, &stubGitHub{}, now, prev)
	if err != nil {
		t.Fatal(err)
	}
	if got := snap.Panels["DEMO-1"].PRs; len(got) != 2 || got[0].Number != 4 || got[1].Number != 9 {
		t.Errorf("the cached panel was changed by the ignore filter: %+v", got)
	}
	if prev.Panels["DEMO-1"].PRs[0].Number != 4 {
		t.Errorf("the previous snapshot was changed: %+v", prev.Panels["DEMO-1"].PRs)
	}
}

func TestToTicketKeepsTheAssignee(t *testing.T) {
	c := &config.Config{}
	if got := ToTicket(c, jira.Issue{Key: "DEMO-1", Assignee: "Ana López"}, ""); got.Assignee != "Ana López" {
		t.Errorf("assignee = %q", got.Assignee)
	}
}

func TestTeamTicketsComeOnlyWhenAsked(t *testing.T) {
	now := time.Date(2026, 10, 21, 12, 0, 0, 0, time.UTC)
	js := &stubJira{
		issues: []jira.Issue{{ID: "1", Key: "DEMO-1", Summary: "Mine", Status: "In Progress", StatusCategory: "indeterminate", Assignee: "Dev Example",
			Sprints: []jira.Sprint{{ID: 42, Space: "DEMO", Name: "DEMO S42", State: "active"}, {ID: 41, Space: "DEMO", Name: "DEMO S41", State: "closed"}}}},
		team: []jira.Issue{
			{ID: "7", Key: "DEMO-7", Summary: "Theirs", Status: "In Progress", StatusCategory: "indeterminate", Assignee: "Bea Ruiz",
				Sprints: []jira.Sprint{{ID: 11807, Space: "DEMO", Name: "DEMO S42", State: "active"}, {ID: 99, Space: "DEMO", Name: "Another team's sprint", State: "active"}}},
			{ID: "8", Key: "DEMO-8", Summary: "Nobody's", Status: "To Do", StatusCategory: "new"},
		},
		linked: map[string][]jira.DevPullRequest{},
	}
	gh := &stubGitHub{prs: []ticket.PullRequest{{Repo: "example-org/web", Number: 70, Branch: "feature/DEMO-7-theirs", State: ticket.PROpen, Checks: ticket.ChecksFail}}}
	js.sprints = map[int][]int{5205: {11807}}
	snap, err := FetchWith(context.Background(), teamCfg(), js, gh, now, Snapshot{}, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if len(js.jqls) != 1 || len(snap.Tickets) != 1 {
		t.Fatalf("off: searches %v, tickets %+v", js.jqls, snap.Tickets)
	}
	js.jqls, js.panels = nil, nil
	snap, err = FetchWith(context.Background(), teamCfg(), js, gh, now, Snapshot{}, Options{Team: true})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(js.jqls, jira.TeamSprintJQL(cfg().SpaceKeys(), []int{11807})) {
		t.Errorf("on: the team search should cover the team boards' active sprints: %v", js.jqls)
	}
	if len(snap.Sprints) != 1 || snap.Sprints[0].ID != 42 {
		t.Errorf("only the user's sprints head the board: %+v", snap.Sprints)
	}
	byKey := map[ticket.Key]ticket.Ticket{}
	for _, tk := range snap.Tickets {
		byKey[tk.Key] = tk
	}
	theirs, nobody, mine := byKey["DEMO-7"], byKey["DEMO-8"], byKey["DEMO-1"]
	if !theirs.Team || !nobody.Team || mine.Team || theirs.Assignee != "Bea Ruiz" || theirs.Board != "Authorization Center" {
		t.Errorf("team marks: %+v", snap.Tickets)
	}
	if len(theirs.PRs) != 1 || theirs.PRs[0].Number != 70 || len(theirs.Attention()) != 0 {
		t.Errorf("a team ticket takes its PRs from the listing and never needs the user: %+v", theirs)
	}
	if slices.Contains(js.panels, "7") || slices.Contains(js.panels, "8") || !slices.Contains(js.panels, "1") {
		t.Errorf("only the user's tickets ask for their development panel: %v", js.panels)
	}
	boardless := &stubJira{issues: js.issues, team: js.team, linked: js.linked}
	if _, err := FetchWith(context.Background(), cfg(), boardless, gh, now, Snapshot{}, Options{Team: true}); err != nil || len(boardless.jqls) != 1 {
		t.Errorf("without team boards there is no team to look for: %v %v", err, boardless.jqls)
	}
	js.teamErr = errors.New("jira: 500")
	snap, err = FetchWith(context.Background(), teamCfg(), js, gh, now, Snapshot{}, Options{Team: true})
	if err != nil || !slices.ContainsFunc(snap.Warnings, func(w string) bool { return strings.Contains(w, "team tickets unavailable") }) || len(snap.Tickets) != 1 {
		t.Errorf("a failed team search is a warning: %v %v %+v", err, snap.Warnings, snap.Tickets)
	}
}

func TestTheTeamSearchNeverHoldsTheGitHubListingBack(t *testing.T) {
	gate := make(chan struct{})
	js := &stubJira{
		issues:   []jira.Issue{{ID: "1", Key: "DEMO-1", Summary: "Mine", Status: "In Progress", StatusCategory: "indeterminate", Sprints: []jira.Sprint{{ID: 42, Space: "DEMO", Name: "DEMO S42", State: "active"}}}},
		team:     []jira.Issue{{ID: "7", Key: "DEMO-7", Summary: "Theirs", Status: "To Do", StatusCategory: "new"}},
		linked:   map[string][]jira.DevPullRequest{},
		teamGate: gate,
		sprints:  map[int][]int{5205: {11807}},
	}
	gh := &stubGitHub{listing: gate}
	if _, err := FetchWith(context.Background(), teamCfg(), js, gh, time.Now(), Snapshot{}, Options{Team: true}); err != nil {
		t.Fatal(err)
	}
	if js.teamStalled {
		t.Error("the sync waited for the team search before listing pull requests; they should run together")
	}
}

func TestFetchPaintsJiraBeforeGitHub(t *testing.T) {
	now := time.Date(2026, 10, 21, 12, 0, 0, 0, time.UTC)
	js := &stubJira{
		issues: []jira.Issue{
			{ID: "1", Key: "DEMO-1", Summary: "Mine", Status: "In Review", StatusCategory: "indeterminate", Sprints: []jira.Sprint{{ID: 42, Space: "DEMO", Name: "DEMO S42", State: "active"}}},
			{ID: "2", Key: "DEMO-2", Summary: "New one", Status: "To Do", StatusCategory: "new"},
		},
		team:   []jira.Issue{{ID: "7", Key: "DEMO-7", Summary: "Theirs", Status: "To Do", StatusCategory: "new"}},
		linked: map[string][]jira.DevPullRequest{"2": {{Repo: "example-org/web", Number: 31, Title: "Start", URL: "https://github.com/example-org/web/pull/31", Branch: "feature/DEMO-2-start", Status: "OPEN"}}},
	}
	prev := Snapshot{FetchedAt: now.Add(-time.Minute), Tickets: []ticket.Ticket{{Key: "DEMO-1", Status: "In Progress",
		PRs: []ticket.PullRequest{{Repo: "example-org/web", Number: 30, State: ticket.PROpen, Checks: ticket.ChecksFail}}}},
		Reviews: []ticket.PullRequest{{Repo: "example-org/web", Number: 99, Title: "Theirs"}}}
	hold := make(chan struct{})
	gh := &stubGitHub{hold: hold, prs: []ticket.PullRequest{{Repo: "example-org/web", Number: 30, Branch: "feature/DEMO-1-x", State: ticket.PROpen, Checks: ticket.ChecksPass}}}
	var partial Snapshot
	paints := 0
	js.sprints = map[int][]int{5205: {11807}}
	snap, err := FetchWith(context.Background(), teamCfg(), js, gh, now, prev, Options{Team: true, Partial: func(s Snapshot) {
		paints++
		partial = s
		close(hold)
	}})
	if err != nil {
		t.Fatal(err)
	}
	if paints != 1 || gh.held {
		t.Fatalf("the partial board should come while GitHub answers: paints %d, listing gave up %v", paints, gh.held)
	}
	byKey := map[ticket.Key]ticket.Ticket{}
	for _, tk := range partial.Tickets {
		byKey[tk.Key] = tk
	}
	mine, fresh, theirs := byKey["DEMO-1"], byKey["DEMO-2"], byKey["DEMO-7"]
	if !partial.Partial || mine.Status != "In Review" || len(mine.PRs) != 1 || mine.PRs[0].Checks != ticket.ChecksFail {
		t.Errorf("Jira's news with the previous board's pull requests: %+v", mine)
	}
	if len(fresh.PRs) != 1 || fresh.PRs[0].Number != 31 || !theirs.Team || len(partial.Reviews) != 1 {
		t.Errorf("a new ticket takes its PRs from its development panel, the team shows, reviews stay: %+v %+v %+v", fresh, theirs, partial.Reviews)
	}
	if snap.Partial || snap.Tickets[0].Key != "DEMO-1" || snap.Tickets[0].PRs[0].Checks != ticket.ChecksPass {
		t.Errorf("the full board follows: %+v", snap.Tickets)
	}
}
