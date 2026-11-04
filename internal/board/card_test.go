package board_test

import (
	"reflect"
	"testing"

	"github.com/pavp/tkt/internal/board"
	"github.com/pavp/tkt/internal/ticket"
)

func TestACardSaysWhatTheTUIsCardSays(t *testing.T) {
	tests := []struct {
		name string
		t    ticket.Ticket
		want board.Card
	}{
		{"an estimated story", ticket.Ticket{Key: "DEMO-1", Type: "Story", Points: 3, Assignee: "Ada Lovelace", Category: ticket.InProgress},
			board.Card{Points: "3pt", Who: "Ada L.", TypeKind: "story"}},
		{"an unassigned bug", ticket.Ticket{Key: "DEMO-2", Type: "Bug", Points: 0.5, Category: ticket.ToDo},
			board.Card{Points: "0.5pt", Who: "unassigned", TypeKind: "bug"}},
		{"a parent with subtasks", ticket.Ticket{Key: "DEMO-3", Type: "Historia", Assignee: "Grace", Category: ticket.InProgress,
			Subtasks: []ticket.Ticket{{Key: "DEMO-4", Category: ticket.Done}, {Key: "DEMO-5", Category: ticket.ToDo}, {Key: "DEMO-6", Category: ticket.InProgress}}},
			board.Card{Who: "Grace", Progress: "1/3 subtasks · 1 to do", TypeKind: "story"}},
		{"a subtask before a task", ticket.Ticket{Key: "DEMO-7", Type: "Subtarea", Category: ticket.ToDo}, board.Card{Who: "unassigned", TypeKind: "subtask"}},
		{"an unknown type", ticket.Ticket{Key: "DEMO-8", Type: "Spike", Category: ticket.ToDo}, board.Card{Who: "unassigned", TypeKind: "other"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := board.CardOf(tt.t, false)
			got.Details, got.Hint = nil, ""
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("CardOf = %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestPointSumsHaveNoFloatingPointNoise(t *testing.T) {
	var ts []ticket.Ticket
	for _, p := range []float64{0.1, 0.2} {
		ts = append(ts, ticket.Ticket{Points: p, Category: ticket.InProgress})
	}
	if _, doing, _, _ := board.PointsByStage(ts); doing != 0.3 {
		t.Errorf("PointsByStage doing = %v, want 0.3", doing)
	}
	var rows []board.Row
	for i := range ts {
		rows = append(rows, board.Row{Ticket: &ts[i]})
	}
	if got := board.Points(rows); got != 0.3 {
		t.Errorf("Points = %v, want 0.3", got)
	}
}

func TestACardSummarizesItsFinishedPullRequests(t *testing.T) {
	pr := func(n int, state ticket.PRState) ticket.PullRequest {
		return ticket.PullRequest{Number: n, State: state}
	}
	tests := []struct {
		name string
		prs  []ticket.PullRequest
		want string
	}{
		{"one merged", []ticket.PullRequest{pr(12, ticket.PRMerged)}, "#12 merged"},
		{"one closed beside an open one", []ticket.PullRequest{pr(12, ticket.PROpen), pr(9, ticket.PRClosed)}, "#9 closed"},
		{"several", []ticket.PullRequest{pr(1, ticket.PRMerged), pr(2, ticket.PRMerged), pr(3, ticket.PRClosed)}, ticket.FinishedSummary(2, 1)},
		{"none finished", []ticket.PullRequest{pr(12, ticket.PROpen)}, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := board.CardOf(ticket.Ticket{Key: "DEMO-1", PRs: tt.prs}, false).Finished; got != tt.want {
				t.Errorf("Finished = %q, want %q", got, tt.want)
			}
		})
	}
}
