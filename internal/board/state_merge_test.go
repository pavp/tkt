package board_test

import (
	"reflect"
	"testing"

	"github.com/pavp/tkt/internal/board"
)

func TestMergeSavedKeepsWhatChangedElsewhere(t *testing.T) {
	on, off := true, false
	tests := []struct {
		name               string
		base, mine, theirs board.SavedState
		want               board.SavedState
	}{
		{"a field changed only elsewhere stays", board.SavedState{}, board.SavedState{Team: true}, board.SavedState{HiddenBoards: []string{"Core"}},
			board.SavedState{Team: true, HiddenBoards: []string{"Core"}}},
		{"the own change wins on the same field", board.SavedState{View: board.ViewColumns}, board.SavedState{View: board.ViewList}, board.SavedState{View: board.ViewColumns, BacklogMerged: true},
			board.SavedState{View: board.ViewList, BacklogMerged: true}},
		{"a set keeps both sides' additions and removals", board.SavedState{HiddenRepos: []string{"example-org/a", "example-org/b"}},
			board.SavedState{HiddenRepos: []string{"example-org/b", "example-org/c"}}, board.SavedState{HiddenRepos: []string{"example-org/a", "example-org/b", "example-org/d"}},
			board.SavedState{HiddenRepos: []string{"example-org/b", "example-org/c", "example-org/d"}}},
		{"an unchanged team PRs toggle takes theirs", board.SavedState{TeamPRs: &off}, board.SavedState{TeamPRs: &off, Collapsed: []string{"mine:example-org/web"}}, board.SavedState{TeamPRs: &on},
			board.SavedState{TeamPRs: &on, Collapsed: []string{"mine:example-org/web"}}},
		{"nothing changed elsewhere is the own state", board.SavedState{}, board.SavedState{Team: true, HiddenBoards: []string{"Web"}}, board.SavedState{},
			board.SavedState{Team: true, HiddenBoards: []string{"Web"}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := board.MergeSaved(tt.base, tt.mine, tt.theirs); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("MergeSaved = %+v, want %+v", got, tt.want)
			}
		})
	}
}
