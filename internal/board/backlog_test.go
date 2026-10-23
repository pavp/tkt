package board

import (
	"context"
	"errors"
	"os"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/pavp/tkt/internal/config"
	"github.com/pavp/tkt/internal/jira"
	"github.com/pavp/tkt/internal/ticket"
)

type stubBacklog struct {
	mu       sync.Mutex
	future   map[int][]jira.Sprint
	backlog  map[int][]jira.Issue
	sprinted []jira.Issue
	failing  map[int]error
	jqls     []string
}

func (s *stubBacklog) Search(_ context.Context, jql string, _ []string) ([]jira.Issue, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.jqls = append(s.jqls, jql)
	return s.sprinted, nil
}

func (s *stubBacklog) FieldID(_ context.Context, name string) (string, error) {
	if name == "Story Points" {
		return "customfield_10028", nil
	}
	return "", jira.ErrFieldNotFound
}

func (s *stubBacklog) Myself(context.Context) (jira.User, error) {
	return jira.User{AccountID: "acc-me", DisplayName: "Dev Example"}, nil
}

func (s *stubBacklog) BrowseURL(key string) string {
	return "https://example.atlassian.net/browse/" + key
}

func (s *stubBacklog) OpenSprints(_ context.Context, board int) ([]jira.Sprint, error) {
	if err := s.failing[board]; err != nil {
		return nil, err
	}
	return s.future[board], nil
}

func (s *stubBacklog) BacklogIssues(_ context.Context, board int, _ []string) ([]jira.Issue, error) {
	return s.backlog[board], nil
}

func backlogCfg() *config.Config {
	c := cfg()
	c.Spaces[0].TeamBoards = []config.TeamBoard{{ID: 418, Name: "User Access Center"}, {ID: 5205, Name: "Authorization Center"}}
	return c
}

func TestBacklogGroupsActiveSprintsThenFutureSoonestFirstThenTheBacklog(t *testing.T) {
	nov := func(d int) time.Time { return time.Date(2026, 11, d, 9, 0, 0, 0, time.UTC) }
	js := &stubBacklog{
		future: map[int][]jira.Sprint{
			418:  {{ID: 44, Name: "UAC S44", State: "future", Board: 418}, {ID: 43, Name: "UAC S43", State: "future", Board: 418, Start: nov(3)}},
			5205: {{ID: 12, Name: "AC S12", State: "future", Board: 5205, Start: nov(6)}, {ID: 11, Name: "AC S11", State: "active", Board: 5205, Start: nov(10)}},
		},
		sprinted: []jira.Issue{
			{Key: "DEMO-210", Summary: "Export users", Status: "To Do", StatusCategory: "new", Sprints: []jira.Sprint{{ID: 43, State: "future"}}},
			{Key: "DEMO-214", Summary: "Fix timeout", Status: "In Review", StatusCategory: "indeterminate", Sprints: []jira.Sprint{{ID: 40, State: "closed"}, {ID: 43, State: "future"}}},
			{Key: "DEMO-290", Summary: "Grant audit", Status: "En curso", StatusCategory: "indeterminate", Sprints: []jira.Sprint{{ID: 11, State: "active"}}},
			{Key: "DEMO-301", Summary: "Role matrix", Status: "To Do", StatusCategory: "new", Sprints: []jira.Sprint{{ID: 12, State: "future"}}},
		},
		backlog: map[int][]jira.Issue{
			418:  {{Key: "DEMO-180", Summary: "Clean old roles", Status: "To Do", StatusCategory: "new"}},
			5205: {{Key: "DEMO-180", Summary: "Clean old roles", Status: "To Do", StatusCategory: "new"}, {Key: "DEMO-305", Summary: "Rename roles", Status: "To Do", StatusCategory: "new"}},
		},
	}
	now := time.Date(2026, 10, 23, 12, 0, 0, 0, time.UTC)
	got, err := FetchBacklog(context.Background(), backlogCfg(), js, now)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, sp := range got.Sprints {
		names = append(names, sp.Name+"@"+sp.BoardName)
	}
	if !slices.Equal(names, []string{"AC S11@Authorization Center", "UAC S43@User Access Center", "AC S12@Authorization Center", "UAC S44@User Access Center"}) {
		t.Fatalf("sprints = %v", names)
	}
	keys := func(ts []ticket.Ticket) (out []string) {
		for _, t := range ts {
			out = append(out, string(t.Key)+"@"+t.Board)
		}
		return out
	}
	if k := keys(got.Sprints[0].Tickets); !slices.Equal(k, []string{"DEMO-290@Authorization Center"}) {
		t.Errorf("S11 = %v", k)
	}
	got.Sprints = got.Sprints[1:]
	if k := keys(got.Sprints[0].Tickets); !slices.Equal(k, []string{"DEMO-210@User Access Center", "DEMO-214@User Access Center"}) {
		t.Errorf("S43 = %v", k)
	}
	if got.Sprints[0].Tickets[1].Category != ticket.InReview || got.Sprints[0].Tickets[0].URL == "" {
		t.Errorf("tickets are built as the board's: %+v", got.Sprints[0].Tickets)
	}
	if len(got.Sprints[2].Tickets) != 0 {
		t.Errorf("an empty sprint still shows: %+v", got.Sprints[2])
	}
	if k := keys(got.Backlog); !slices.Equal(k, []string{"DEMO-180@User Access Center", "DEMO-305@Authorization Center"}) {
		t.Errorf("backlog = %v", k)
	}
	if !slices.Equal(js.jqls, []string{jira.SprintsJQL([]int{11, 12, 43, 44})}) {
		t.Errorf("one search covers every future sprint: %v", js.jqls)
	}
	if !got.HasPoints {
		t.Error("the space has a story points field")
	}
	if got.Me != "Dev Example" {
		t.Errorf("the backlog knows who the user is: %q", got.Me)
	}
	if !got.FetchedAt.Equal(now) {
		t.Errorf("fetched at %v", got.FetchedAt)
	}
}

func TestBacklogWithoutTeamBoardsAsksNothing(t *testing.T) {
	js := &stubBacklog{}
	got, err := FetchBacklog(context.Background(), cfg(), js, time.Now())
	if err != nil || len(got.Sprints) != 0 || len(got.Backlog) != 0 || len(js.jqls) != 0 {
		t.Errorf("got %+v, %v, searches %v", got, err, js.jqls)
	}
}

func TestBacklogOfAFailingBoardIsAWarning(t *testing.T) {
	js := &stubBacklog{
		failing: map[int]error{5205: errors.New("jira: 500")},
		backlog: map[int][]jira.Issue{418: {{Key: "DEMO-180", Summary: "Clean old roles", Status: "To Do", StatusCategory: "new"}}},
	}
	got, err := FetchBacklog(context.Background(), backlogCfg(), js, time.Now())
	if err != nil || len(got.Backlog) != 1 || !slices.ContainsFunc(got.Warnings, func(w string) bool { return strings.Contains(w, "Authorization Center") && strings.Contains(w, "500") }) {
		t.Errorf("got %+v, %v", got, err)
	}
	js.failing[418] = errors.New("jira: 401")
	if _, err := FetchBacklog(context.Background(), backlogCfg(), js, time.Now()); err == nil {
		t.Error("every board failing is an error")
	}
}

func TestPointsByStage(t *testing.T) {
	ts := []ticket.Ticket{
		{Category: ticket.ToDo, Points: 5}, {Category: ticket.ToDo}, {Category: ticket.InProgress, Points: 3},
		{Category: ticket.InReview, Points: 2}, {Category: ticket.Done, Points: 1}, {Category: ticket.Done},
	}
	todo, doing, done, unestimated := PointsByStage(ts)
	if todo != 5 || doing != 5 || done != 1 || unestimated != 2 {
		t.Errorf("got %v %v %v %v", todo, doing, done, unestimated)
	}
}

func TestBacklogCacheRoundTrips(t *testing.T) {
	c := NewBacklogCache(t.TempDir())
	if _, ok, err := c.Load(); ok || err != nil {
		t.Fatalf("empty cache: ok %v, err %v", ok, err)
	}
	want := Backlog{FetchedAt: time.Date(2026, 10, 23, 12, 0, 0, 0, time.UTC), Backlog: []ticket.Ticket{{Key: "DEMO-180", Board: "User Access Center"}}, HasPoints: true}
	if err := c.Save(want); err != nil {
		t.Fatal(err)
	}
	got, ok, err := c.Load()
	if !ok || err != nil || !got.FetchedAt.Equal(want.FetchedAt) || len(got.Backlog) != 1 || got.Backlog[0].Board != "User Access Center" || !got.HasPoints {
		t.Errorf("got %+v, %v, %v", got, ok, err)
	}
	if fi, err := os.Stat(c.Path); err != nil || fi.Mode().Perm() != 0o600 {
		t.Errorf("cache file: %v %v", fi, err)
	}
}
