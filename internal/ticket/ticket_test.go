package ticket

import (
	"reflect"
	"testing"
)

func TestKeysIn(t *testing.T) {
	tests := []struct {
		in   string
		want []Key
	}{
		{"feature/DEMO-123-add-filter", []Key{"DEMO-123"}},
		{"fix/DEMO-1", []Key{"DEMO-1"}},
		{"DEMO-12_OPS-7", []Key{"DEMO-12", "OPS-7"}},
		{"DEMO-12 and DEMO-12 again", []Key{"DEMO-12"}},
		{"fix-123-lowercase", nil},
		{"release-2026", nil},
		{"XDEMO-1", []Key{"XDEMO-1"}},
		{"DEMO-12345678x", []Key{"DEMO-12345678"}},
		{"A1DEMO-5", []Key{"A1DEMO-5"}},
		{"", nil},
	}
	for _, tt := range tests {
		if got := KeysIn(tt.in); !reflect.DeepEqual(got, tt.want) {
			t.Errorf("KeysIn(%q) = %v, want %v", tt.in, got, tt.want)
		}
	}
}

func TestKeyInBranchFiltersBySpace(t *testing.T) {
	spaces := []string{"DEMO", "OPS"}
	tests := []struct {
		branch string
		want   Key
		ok     bool
	}{
		{"feature/DEMO-170450-filter", "DEMO-170450", true},
		{"chore/UTF-8-cleanup", "", false},
		{"feature/UTF-8-OPS-9", "OPS-9", true},
		{"fix-date-filter", "", false},
		{"test/demo-170449-per-folder-coverage", "DEMO-170449", true},
		{"fix-123-lowercase", "", false},
	}
	for _, tt := range tests {
		got, ok := KeyInBranch(tt.branch, spaces)
		if got != tt.want || ok != tt.ok {
			t.Errorf("KeyInBranch(%q) = %q, %v; want %q, %v", tt.branch, got, ok, tt.want, tt.ok)
		}
	}
}

func TestCategoryFor(t *testing.T) {
	overrides := map[string]Category{"Code Review": InReview}
	tests := []struct {
		status, category string
		want             Category
	}{
		{"To Do", "new", ToDo},
		{"In Progress", "indeterminate", InProgress},
		{"Code Review", "indeterminate", InReview},
		{"Done", "done", Done},
		{"Weird", "", ToDo},
	}
	for _, tt := range tests {
		if got := CategoryFor(tt.status, tt.category, overrides); got != tt.want {
			t.Errorf("CategoryFor(%q, %q) = %q, want %q", tt.status, tt.category, got, tt.want)
		}
	}
}

func TestBlocked(t *testing.T) {
	tests := []struct {
		status string
		want   bool
	}{
		{"Blocked", true},
		{"BLOCKED BY CLIENT", true},
		{"Bloqueado", true},
		{"In Progress", false},
		{"", false},
	}
	for _, tt := range tests {
		if got := (Ticket{Status: tt.status}).Blocked(); got != tt.want {
			t.Errorf("Blocked() for %q = %v, want %v", tt.status, got, tt.want)
		}
	}
}

func TestWorstChecks(t *testing.T) {
	if got := WorstChecks(ChecksPass, ChecksFail, ChecksPending); got != ChecksFail {
		t.Errorf("got %q, want fail", got)
	}
	if got := WorstChecks(ChecksPass, ChecksPending); got != ChecksPending {
		t.Errorf("got %q, want pending", got)
	}
	if got := WorstChecks(); got != ChecksNone {
		t.Errorf("got %q, want none", got)
	}
}

func TestAttention(t *testing.T) {
	tests := []struct {
		name string
		t    Ticket
		want []Reason
	}{
		{"failing CI", Ticket{Category: InProgress, PRs: []PullRequest{{State: PROpen, Checks: ChecksFail}}}, []Reason{ReasonCIFailing}},
		{"changes requested", Ticket{Category: InReview, PRs: []PullRequest{{State: PROpen, Checks: ChecksPass, Review: ReviewChangesRequested}}}, []Reason{ReasonChangesRequested}},
		{"both across repos", Ticket{Category: InReview, PRs: []PullRequest{{State: PROpen, Checks: ChecksFail}, {State: PROpen, Review: ReviewChangesRequested}}}, []Reason{ReasonCIFailing, ReasonChangesRequested}},
		{"unresolved comments", Ticket{Category: InReview, PRs: []PullRequest{{State: PROpen, Checks: ChecksPass, Unresolved: 2}}}, []Reason{ReasonUnresolved}},
		{"conflicts", Ticket{Category: InReview, PRs: []PullRequest{{State: PROpen, Merge: MergeConflicts}}}, []Reason{ReasonConflicts}},
		{"every reason in order", Ticket{Category: InReview, PRs: []PullRequest{{State: PROpen, Merge: MergeConflicts, Unresolved: 1}, {State: PROpen, Checks: ChecksFail, Review: ReviewChangesRequested}}}, []Reason{ReasonCIFailing, ReasonChangesRequested, ReasonUnresolved, ReasonConflicts}},
		{"merged PR ignored", Ticket{Category: InReview, PRs: []PullRequest{{State: PRMerged, Checks: ChecksFail, Unresolved: 3}}}, nil},
		{"done ticket ignored", Ticket{Category: Done, PRs: []PullRequest{{State: PROpen, Checks: ChecksFail}}}, nil},
		{"no PRs", Ticket{Category: ToDo}, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.t.Attention(); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("Attention() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestAttachPRs(t *testing.T) {
	tickets := []Ticket{{Key: "DEMO-1"}, {Key: "DEMO-2"}, {Key: "OPS-3"}}
	prs := []PullRequest{
		{Repo: "example-org/web", Number: 9, Branch: "feature/DEMO-1-a"},
		{Repo: "example-org/api", Number: 4, Branch: "feature/DEMO-1-a"},
		{Repo: "example-org/web", Number: 10, Branch: "fix/DEMO-2-OPS-3"},
		{Repo: "example-org/web", Number: 11, Branch: "chore/no-key"},
		{Repo: "example-org/api", Number: 12, Branch: "test/demo-2-lowercase"},
	}
	got := AttachPRs(tickets, prs)
	want := map[Key][]int{"DEMO-1": {4, 9}, "DEMO-2": {12, 10}, "OPS-3": {10}}
	for _, tk := range got {
		var nums []int
		for _, pr := range tk.PRs {
			nums = append(nums, pr.Number)
		}
		if !reflect.DeepEqual(nums, want[tk.Key]) {
			t.Errorf("%s PRs = %v, want %v", tk.Key, nums, want[tk.Key])
		}
	}
	if len(tickets[0].PRs) != 0 {
		t.Error("AttachPRs mutated its input")
	}
}

func TestGroupSubtasks(t *testing.T) {
	in := []Ticket{
		{Key: "DEMO-1"},
		{Key: "DEMO-2", Parent: "DEMO-1"},
		{Key: "DEMO-3", Parent: "DEMO-99"},
	}
	got := GroupSubtasks(in)
	if len(got) != 2 || got[0].Key != "DEMO-1" || got[1].Key != "DEMO-3" {
		t.Fatalf("top level = %+v", got)
	}
	if len(got[0].Subtasks) != 1 || got[0].Subtasks[0].Key != "DEMO-2" {
		t.Errorf("DEMO-1 subtasks = %+v", got[0].Subtasks)
	}
}

func TestSplitPRsAndSummary(t *testing.T) {
	open, merged, closed := SplitPRs([]PullRequest{{Number: 1, State: PROpen}, {Number: 2, State: PRMerged}, {Number: 3, State: PRMerged}, {Number: 4, State: PRClosed}})
	if len(open) != 1 || merged != 2 || closed != 1 {
		t.Errorf("split = %v %d %d", open, merged, closed)
	}
	if got := FinishedSummary(merged, closed); got != "2 merged · 1 closed" {
		t.Errorf("summary = %q", got)
	}
	if got := FinishedSummary(0, 0); got != "" {
		t.Errorf("empty summary = %q", got)
	}
}

func TestAttachPRsMatchesTitlesOnce(t *testing.T) {
	got := AttachPRs([]Ticket{{Key: "DEMO-1"}}, []PullRequest{
		{Repo: "example-org/web", Number: 5, Branch: "x", Title: "feat: DEMO-1 thing"},
		{Repo: "example-org/web", Number: 6, Branch: "feature/demo-1-y", Title: "DEMO-1: same ticket in both"},
		{Repo: "example-org/web", Number: 6, Branch: "feature/demo-1-y", Title: "DEMO-1: same ticket in both"},
	})
	if len(got[0].PRs) != 2 {
		t.Errorf("PRs = %+v", got[0].PRs)
	}
}
