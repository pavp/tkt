package board_test

import (
	"testing"

	"github.com/pavp/tkt/internal/board"
)

func TestFormValuesTreatsAnEmptyChoiceAsNone(t *testing.T) {
	fields := []board.Field{
		{ID: "owner", Name: "Owner", Kind: board.KindUser, Required: true},
		{ID: "note", Name: "Reviewer", Kind: board.KindUser},
	}
	tests := []struct {
		name    string
		chosen  map[string]string
		missing string
		values  int
	}{
		{"a required field sent empty is missing", map[string]string{"owner": ""}, "owner", 0},
		{"an optional field sent empty is left out", map[string]string{"owner": "acc-ada", "note": ""}, "", 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			values, missing := board.FormValues(fields, tt.chosen)
			got := ""
			if missing != nil {
				got = missing.ID
			}
			if got != tt.missing || len(values) != tt.values {
				t.Errorf("FormValues = %v, missing %q; want %d values, missing %q", values, got, tt.values, tt.missing)
			}
		})
	}
}
