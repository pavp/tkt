package board_test

import (
	"reflect"
	"testing"
	"time"

	"github.com/pavp/tkt/internal/board"
	"github.com/pavp/tkt/internal/ticket"
)

func TestThePreviewSaysWhatTheTUIsSays(t *testing.T) {
	story := ticket.Ticket{Key: "DEMO-1", Points: 3, Assignee: "Ada Lovelace", Category: ticket.InProgress,
		PRs: []ticket.PullRequest{{Repo: "example-org/api", Number: 12, State: ticket.PROpen, Checks: ticket.ChecksFail}}}
	if got, want := board.PreviewParts(story), []string{"3 points", "Ada Lovelace", "CI failing", "example-org/api#12"}; !reflect.DeepEqual(got, want) {
		t.Errorf("PreviewParts = %q, want %q", got, want)
	}
	tests := []struct {
		name     string
		t        ticket.Ticket
		hasAgent bool
		want     string
	}{
		{"CI failing with its agent", story, true, "f sends the failure to the agent"},
		{"CI failing without an agent", story, false, "f sends the failure to an agent"},
		{"to do without PRs", ticket.Ticket{Category: ticket.ToDo}, false, "enter asks for repos and opens an agent"},
		{"a team ticket", ticket.Ticket{Team: true}, false, "enter for details"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := board.ActionHint(tt.t, tt.hasAgent); got != tt.want {
				t.Errorf("ActionHint = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestTheSyncLabelSaysHowFreshTheBoardIs(t *testing.T) {
	now := time.Date(2026, 11, 4, 12, 30, 0, 0, time.Local)
	tests := []struct {
		name string
		s    board.SyncState
		want string
	}{
		{"syncing", board.SyncState{Loading: true, FetchedAt: now}, "refreshing…"},
		{"updating PRs", board.SyncState{Loading: true, Partial: true, FetchedAt: now}, "updating PRs…"},
		{"never", board.SyncState{}, "not synced yet"},
		{"failed", board.SyncState{Failed: true, FetchedAt: now.Add(-2 * time.Hour)}, "! cached · 10:30"},
		{"just now", board.SyncState{FetchedAt: now.Add(-20 * time.Second)}, "✓ synced just now"},
		{"minutes", board.SyncState{FetchedAt: now.Add(-3 * time.Minute)}, "✓ synced 3 min ago"},
		{"hours", board.SyncState{FetchedAt: now.Add(-2 * time.Hour)}, "✓ synced 10:30"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := board.SyncLabel(tt.s, now); got != tt.want {
				t.Errorf("SyncLabel = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestTheBacklogLabelSaysWhenItWasLoaded(t *testing.T) {
	now := time.Date(2026, 11, 4, 12, 30, 0, 0, time.Local)
	tests := []struct {
		name    string
		loading bool
		at      time.Time
		want    string
	}{
		{"loading", true, now, "refreshing backlog…"},
		{"never", false, time.Time{}, "not loaded yet"},
		{"just now", false, now.Add(-10 * time.Second), "✓ loaded just now"},
		{"minutes", false, now.Add(-4 * time.Minute), "✓ loaded 4 min ago"},
		{"hours", false, now.Add(-2 * time.Hour), "✓ loaded 10:30"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := board.BacklogLabel(tt.loading, tt.at, now); got != tt.want {
				t.Errorf("BacklogLabel = %q, want %q", got, tt.want)
			}
		})
	}
}
