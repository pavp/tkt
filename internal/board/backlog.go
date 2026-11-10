package board

import (
	"context"
	"errors"
	"fmt"
	"math"
	"slices"
	"sync"
	"time"

	"github.com/pavp/tkt/internal/config"
	"github.com/pavp/tkt/internal/jira"
	"github.com/pavp/tkt/internal/ticket"
)

type BacklogSource interface {
	Search(ctx context.Context, jql string, fields []string) ([]jira.Issue, error)
	FieldID(ctx context.Context, name string) (string, error)
	BrowseURL(key string) string
	Myself(ctx context.Context) (jira.User, error)
	OpenSprints(ctx context.Context, board int) ([]jira.Sprint, error)
	BacklogIssues(ctx context.Context, board int, fields []string) ([]jira.Issue, error)
	BoardFilter(ctx context.Context, board int) (int, error)
}

// Backlog is the configured team boards' open work: their active sprints, then their future sprints soonest
// first, and the tickets in no sprint, each ticket carrying its board's name.
type Backlog struct {
	FetchedAt  time.Time       `json:"fetched_at"`
	Sprints    []BacklogSprint `json:"sprints,omitempty"`
	Backlog    []ticket.Ticket `json:"backlog,omitempty"`
	Warnings   []string        `json:"warnings,omitempty"`
	LabelField string          `json:"label_field,omitempty"`
	// HasPoints says the site has a story points field, so an unestimated ticket counts as one.
	HasPoints bool `json:"has_points,omitempty"`
	// Me is the user's name as Jira gives it to assignees, to tell the user's tickets apart.
	Me string `json:"me,omitempty"`
	// NoTeamBoards says no space has a team board set, so the backlog is empty by configuration, not by Jira.
	NoTeamBoards bool `json:"no_team_boards,omitempty"`
}

type BacklogSprint struct {
	jira.Sprint
	BoardName string          `json:"board_name"`
	Tickets   []ticket.Ticket `json:"tickets,omitempty"`
}

// FetchBacklog asks every configured team board for its open sprints and backlog at once, then each board
// for its sprints' tickets through its own filter, as a sprint another board shares with it would otherwise
// bring every ticket of that board. A sprint is named after its own board when that one is configured, and a
// ticket after the first board in the configuration that shows it. A failing board becomes a warning; every
// board failing is an error.
func FetchBacklog(ctx context.Context, c *config.Config, js BacklogSource, now time.Time) (Backlog, error) {
	out := Backlog{FetchedAt: now}
	var boards []config.TeamBoard
	for _, s := range c.Spaces {
		boards = append(boards, s.TeamBoards...)
	}
	if len(boards) == 0 {
		out.NoTeamBoards = true
		return out, nil
	}
	fields, labelID, pointsID, warnings := issueFields(ctx, c, js)
	out.Warnings = append(out.Warnings, warnings...)
	if labelID != "" {
		out.LabelField = c.Jira.LabelField
	}
	out.HasPoints = pointsID != ""
	if me, err := js.Myself(ctx); err == nil {
		out.Me = me.DisplayName
	} else {
		out.Warnings = append(out.Warnings, "your Jira user is unknown: "+err.Error())
	}
	toTicket := func(is jira.Issue, board string) ticket.Ticket {
		t := ToTicket(c, is, js.BrowseURL(is.Key))
		if labelID != "" {
			t.Label = jira.FieldText(is.Fields[labelID])
		}
		if pointsID != "" {
			t.Points = jira.FieldNumber(is.Fields[pointsID])
		}
		t.Board = board
		return t
	}
	type boardResult struct {
		sprints []jira.Sprint
		issues  []jira.Issue
		filter  int
		warn    string
		err     error
	}
	results := make([]boardResult, len(boards))
	var wg sync.WaitGroup
	for i, b := range boards {
		wg.Go(func() {
			sprints, err := js.OpenSprints(ctx, b.ID)
			if err != nil {
				results[i].err = err
				return
			}
			issues, err := js.BacklogIssues(ctx, b.ID, fields)
			results[i] = boardResult{sprints: sprints, issues: issues, err: err}
			if err != nil || len(sprints) == 0 {
				return
			}
			if filter, ferr := js.BoardFilter(ctx, b.ID); ferr != nil {
				results[i].warn = fmt.Sprintf("the filter of team board %s could not be read, so its sprints show all their tickets: %v", b.Name, ferr)
			} else {
				results[i].filter = filter
			}
		})
	}
	wg.Wait()
	names := map[int]string{}
	for _, b := range boards {
		if _, dup := names[b.ID]; !dup {
			names[b.ID] = b.Name
		}
	}
	failed := 0
	seen := map[string]bool{}
	sprintAt := map[int]int{}
	for i, r := range results {
		if r.err != nil {
			failed++
			out.Warnings = append(out.Warnings, fmt.Sprintf("backlog of %s unavailable: %v", boards[i].Name, r.err))
			continue
		}
		if r.warn != "" {
			out.Warnings = append(out.Warnings, r.warn)
		}
		for _, sp := range r.sprints {
			if _, dup := sprintAt[sp.ID]; !dup {
				name, ok := names[sp.Board]
				if !ok {
					name = boards[i].Name
				}
				sprintAt[sp.ID] = len(out.Sprints)
				out.Sprints = append(out.Sprints, BacklogSprint{Sprint: sp, BoardName: name})
			}
		}
		for _, is := range r.issues {
			if !seen[is.Key] {
				seen[is.Key] = true
				out.Backlog = append(out.Backlog, toTicket(is, boards[i].Name))
			}
		}
	}
	if failed == len(boards) {
		return out, errors.New(out.Warnings[len(out.Warnings)-1])
	}
	type sprintSearch struct {
		ids    []int
		issues []jira.Issue
		err    error
	}
	searches := make([]sprintSearch, len(results))
	for i, r := range results {
		if r.err != nil || len(r.sprints) == 0 {
			continue
		}
		ids := make([]int, len(r.sprints))
		for n, sp := range r.sprints {
			ids[n] = sp.ID
		}
		slices.Sort(ids)
		searches[i].ids = ids
		jql := jira.SprintsJQL(ids)
		if r.filter > 0 {
			jql = jira.BoardSprintsJQL(r.filter, ids)
		}
		wg.Go(func() {
			searches[i].issues, searches[i].err = js.Search(ctx, jql, fields)
		})
	}
	wg.Wait()
	shown := map[int]map[string]bool{}
	for i, s := range searches {
		if s.err != nil {
			return out, s.err
		}
		for _, is := range s.issues {
			for _, sp := range is.Sprints {
				at, ok := sprintAt[sp.ID]
				if !ok || !slices.Contains(s.ids, sp.ID) {
					continue
				}
				if shown[sp.ID] == nil {
					shown[sp.ID] = map[string]bool{}
				}
				if !shown[sp.ID][is.Key] {
					shown[sp.ID][is.Key] = true
					out.Sprints[at].Tickets = append(out.Sprints[at].Tickets, toTicket(is, boards[i].Name))
				}
				break
			}
		}
	}
	slices.SortStableFunc(out.Sprints, func(a, b BacklogSprint) int {
		switch {
		case (a.State == "active") != (b.State == "active"):
			if a.State == "active" {
				return -1
			}
			return 1
		case a.Start.IsZero() != b.Start.IsZero():
			if a.Start.IsZero() {
				return 1
			}
			return -1
		}
		return a.Start.Compare(b.Start)
	})
	return out, nil
}

// PointsByStage sums the story points not started (To Do), in progress (In Progress and In Review) and done,
// and counts the tickets without an estimate.
func PointsByStage(ts []ticket.Ticket) (todo, doing, done float64, unestimated int) {
	for _, t := range ts {
		if t.Points == 0 {
			unestimated++
			continue
		}
		switch t.Category {
		case ticket.Done:
			done += t.Points
		case ticket.InProgress, ticket.InReview:
			doing += t.Points
		default:
			todo += t.Points
		}
	}
	return roundPoints(todo), roundPoints(doing), roundPoints(done), unestimated
}

// roundPoints drops the floating-point noise sums of fractional estimates leave, such as 19.400000000000027.
func roundPoints(p float64) float64 { return math.Round(p*100) / 100 }
