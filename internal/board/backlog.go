package board

import (
	"context"
	"errors"
	"fmt"
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
}

type BacklogSprint struct {
	jira.Sprint
	BoardName string          `json:"board_name"`
	Tickets   []ticket.Ticket `json:"tickets,omitempty"`
}

// FetchBacklog asks every configured team board for its open sprints and backlog at once, then one search
// for those sprints' tickets. A failing board becomes a warning; every board failing is an error.
func FetchBacklog(ctx context.Context, c *config.Config, js BacklogSource, now time.Time) (Backlog, error) {
	out := Backlog{FetchedAt: now}
	var boards []config.TeamBoard
	for _, s := range c.Spaces {
		boards = append(boards, s.TeamBoards...)
	}
	if len(boards) == 0 {
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
			results[i] = boardResult{sprints, issues, err}
		})
	}
	wg.Wait()
	failed := 0
	seen := map[string]bool{}
	sprintAt := map[int]int{}
	for i, r := range results {
		if r.err != nil {
			failed++
			out.Warnings = append(out.Warnings, fmt.Sprintf("backlog of %s unavailable: %v", boards[i].Name, r.err))
			continue
		}
		for _, sp := range r.sprints {
			if _, dup := sprintAt[sp.ID]; !dup {
				sprintAt[sp.ID] = len(out.Sprints)
				out.Sprints = append(out.Sprints, BacklogSprint{Sprint: sp, BoardName: boards[i].Name})
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
	if len(sprintAt) > 0 {
		ids := make([]int, 0, len(sprintAt))
		for id := range sprintAt {
			ids = append(ids, id)
		}
		slices.Sort(ids)
		issues, err := js.Search(ctx, jira.SprintsJQL(ids), fields)
		if err != nil {
			return out, err
		}
		for _, is := range issues {
			for _, sp := range is.Sprints {
				if at, ok := sprintAt[sp.ID]; ok {
					group := &out.Sprints[at]
					group.Tickets = append(group.Tickets, toTicket(is, group.BoardName))
					break
				}
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
	return todo, doing, done, unestimated
}
