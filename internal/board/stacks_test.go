package board

import (
	"strconv"
	"strings"
	"testing"

	"github.com/pavp/tkt/internal/ticket"
)

func TestPRStacks(t *testing.T) {
	open := func(n int, branch, base string) ticket.PullRequest {
		return ticket.PullRequest{Repo: "example-org/web", Number: n, Branch: branch, Base: base, State: ticket.PROpen, Merge: ticket.MergeReady}
	}
	failing := open(3, "refactor/module-imports-3-c", "refactor/module-imports-2-b")
	failing.Checks = ticket.ChecksFail
	merged := open(1, "refactor/module-imports-1-a", "main")
	merged.State = ticket.PRMerged
	tests := []struct {
		name       string
		prs        []ticket.PullRequest
		wantName   string
		wantStatus string
		rows       []string
	}{
		{
			"a ready stack is named by its branches' common part and ready",
			[]ticket.PullRequest{open(2, "refactor/module-imports-2-b", "refactor/module-imports-1-a"), open(1, "refactor/module-imports-1-a", "main")},
			"refactor/module-imports", "Ready to merge",
			[]string{"#1 Ready to merge after 0", "#2 Ready to merge after 1"},
		},
		{
			"the stack takes its worst pull request's status, and a merged one says so",
			[]ticket.PullRequest{merged, open(2, "refactor/module-imports-2-b", "refactor/module-imports-1-a"), failing},
			"refactor/module-imports", "CI failing",
			[]string{"#1 Merged after 0", "#2 Ready to merge after 1", "#3 CI failing after 2"},
		},
		{
			"branches sharing only their kind are named by the bottom's branch",
			[]ticket.PullRequest{open(1, "feat/login", "main"), open(2, "feat/signup", "feat/login")},
			"feat/login", "Ready to merge",
			[]string{"#1 Ready to merge after 0", "#2 Ready to merge after 1"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := PRStacks(tt.prs)
			if len(got) != 1 {
				t.Fatalf("stacks = %+v", got)
			}
			s := got[0]
			if s.Name != tt.wantName || s.Status != tt.wantStatus {
				t.Errorf("name %q status %q, want %q %q", s.Name, s.Status, tt.wantName, tt.wantStatus)
			}
			var rows []string
			for _, r := range s.PRs {
				rows = append(rows, "#"+strconv.Itoa(r.Number)+" "+r.Status+" after "+strconv.Itoa(r.After))
			}
			if len(rows) != len(tt.rows) {
				t.Fatalf("rows = %q, want %q", rows, tt.rows)
			}
			for i := range rows {
				if rows[i] != tt.rows[i] {
					t.Errorf("row %d = %q, want %q", i, rows[i], tt.rows[i])
				}
			}
		})
	}
	if alone := PRStacks([]ticket.PullRequest{open(7, "fix/x", "main")}); len(alone) != 1 || len(alone[0].PRs) != 1 || alone[0].Status != "Ready to merge" {
		t.Errorf("a pull request alone = %+v", alone)
	}
	if PRStacks(nil) != nil {
		t.Error("no pull requests, no stacks")
	}
}

func TestPRStacksWithKnownPullRequests(t *testing.T) {
	pr := func(n int, branch, base string) ticket.PullRequest {
		return ticket.PullRequest{Repo: "example-org/web", Number: n, Branch: branch, Base: base, State: ticket.PROpen}
	}
	own := []ticket.PullRequest{pr(2, "feat/DEMO-2-b", "feat/DEMO-1-a")}
	known := []KnownPR{
		{PullRequest: pr(1, "feat/DEMO-1-a", "main"), Key: "DEMO-1"},
		{PullRequest: pr(2, "feat/DEMO-2-b", "feat/DEMO-1-a"), Key: "DEMO-2"},
		{PullRequest: pr(3, "feat/DEMO-3-c", "feat/DEMO-2-b"), Key: "DEMO-3"},
		{PullRequest: pr(9, "fix/other", "main")},
	}
	got := PRStacksWith("DEMO-2", own, known)
	if len(got) != 1 {
		t.Fatalf("stacks = %+v", got)
	}
	var rows []string
	for _, r := range got[0].PRs {
		rows = append(rows, "#"+strconv.Itoa(r.Number)+" "+string(r.Ticket))
	}
	if want := []string{"#1 DEMO-1", "#2 DEMO-2", "#3 DEMO-3"}; strings.Join(rows, ",") != strings.Join(want, ",") {
		t.Errorf("rows = %q, want %q", rows, want)
	}
	if alone := PRStacksWith("DEMO-2", own, nil); len(alone) != 1 || len(alone[0].PRs) != 1 || alone[0].PRs[0].Ticket != "DEMO-2" {
		t.Errorf("without known pull requests = %+v", alone)
	}
}

func TestKnownPRsNameTheirTicket(t *testing.T) {
	snap := Snapshot{
		Tickets: []ticket.Ticket{{Key: "DEMO-1", PRs: []ticket.PullRequest{{Repo: "o/web", Number: 1, Branch: "feat/DEMO-1-a"}}}},
		Reviews: []ticket.PullRequest{{Repo: "o/web", Number: 5, Branch: "feat/DEMO-5-x"}},
		Others:  []ticket.PullRequest{{Repo: "o/web", Number: 6, Branch: "chore/bump"}},
	}
	var got []string
	for _, k := range snap.KnownPRs() {
		got = append(got, strconv.Itoa(k.Number)+":"+string(k.Key))
	}
	if want := "1:DEMO-1,5:DEMO-5,6:"; strings.Join(got, ",") != want {
		t.Errorf("known = %q, want %s", got, want)
	}
}
