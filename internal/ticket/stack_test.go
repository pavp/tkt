package ticket

import (
	"reflect"
	"testing"
)

func TestStacks(t *testing.T) {
	pr := func(repo string, n int, branch, base string) PullRequest {
		return PullRequest{Repo: repo, Number: n, Branch: branch, Base: base}
	}
	type entry struct{ n, depth, after int }
	tests := []struct {
		name string
		prs  []PullRequest
		want [][]entry
	}{
		{"no pull requests", nil, nil},
		{
			"pull requests on the default branch stand alone, in their order",
			[]PullRequest{pr("o/web", 2, "feature/DEMO-1-a", "main"), pr("o/web", 1, "feature/DEMO-1-b", "main")},
			[][]entry{{{2, 0, 0}}, {{1, 0, 0}}},
		},
		{
			"a chain listed out of order goes from its bottom to its top",
			[]PullRequest{pr("o/web", 3, "feat/x-3", "feat/x-2"), pr("o/web", 1, "feat/x-1", "main"), pr("o/web", 2, "feat/x-2", "feat/x-1")},
			[][]entry{{{1, 0, 0}, {2, 1, 1}, {3, 2, 2}}},
		},
		{
			"the same branch in another repository is not a parent",
			[]PullRequest{pr("o/web", 1, "feat/x-1", "main"), pr("o/api", 2, "feat/x-2", "feat/x-1")},
			[][]entry{{{1, 0, 0}}, {{2, 0, 0}}},
		},
		{
			"two pull requests on one parent follow it, each in its order",
			[]PullRequest{pr("o/web", 3, "feat/c", "feat/a"), pr("o/web", 1, "feat/a", "main"), pr("o/web", 2, "feat/b", "feat/a")},
			[][]entry{{{1, 0, 0}, {3, 1, 1}, {2, 1, 1}}},
		},
		{
			"a parent opened twice holds its stack once, on the open one",
			[]PullRequest{
				{Repo: "o/web", Number: 1, Branch: "feat/a", Base: "main", State: PRClosed},
				{Repo: "o/web", Number: 4, Branch: "feat/a", Base: "main", State: PROpen},
				pr("o/web", 5, "feat/b", "feat/a"),
			},
			[][]entry{{{1, 0, 0}}, {{4, 0, 0}, {5, 1, 4}}},
		},
		{
			"pull requests based on each other still show once each",
			[]PullRequest{pr("o/web", 1, "feat/a", "feat/b"), pr("o/web", 2, "feat/b", "feat/a")},
			[][]entry{{{1, 0, 0}, {2, 1, 1}}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var got [][]entry
			for _, s := range Stacks(tt.prs) {
				var es []entry
				for _, p := range s {
					es = append(es, entry{p.Number, p.Depth, p.After})
				}
				got = append(got, es)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("Stacks = %v, want %v", got, tt.want)
			}
		})
	}
}
