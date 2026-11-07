package textmatch_test

import (
	"slices"
	"testing"

	"github.com/pavp/tkt/internal/textmatch"
)

func TestFoldIgnoresCaseAndAccents(t *testing.T) {
	for in, want := range map[string]string{"En Revisión": "en revision", "ÑANDÚ": "nandu", "Çà ç'est": "ca c'est", "plain": "plain"} {
		if got := textmatch.Fold(in); got != want {
			t.Errorf("Fold(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestRankPutsNamesStartingWithTheTextFirst(t *testing.T) {
	tests := []struct {
		name, text string
		want       int
	}{
		{"sprint board", "SPR", 0},
		{"Team sprint", "spr", 1},
		{"En revisión", "revision", 1},
		{"Kanban", "spr", -1},
		{"anything", "  ", 0},
	}
	for _, tt := range tests {
		if got := textmatch.Rank(tt.name, tt.text); got != tt.want {
			t.Errorf("Rank(%q, %q) = %d, want %d", tt.name, tt.text, got, tt.want)
		}
	}
}

func TestFilterKeepsMatchesStartsFirstInTheirOrder(t *testing.T) {
	names := []string{"web-app", "api", "example-org/web", "Webhooks", "docs"}
	if got := textmatch.Filter(names, "web", func(s string) string { return s }); !slices.Equal(got, []string{"web-app", "Webhooks", "example-org/web"}) {
		t.Errorf("Filter = %v", got)
	}
	if got := textmatch.Filter(names, "", func(s string) string { return s }); !slices.Equal(got, names) {
		t.Errorf("an empty text should keep every name: %v", got)
	}
}
