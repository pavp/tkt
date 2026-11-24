package cli

import (
	"bytes"
	"strings"
	"testing"

	"github.com/pavp/tkt/internal/ticket"
)

func TestShowGroupsAStackFromItsBottom(t *testing.T) {
	d := detail{Ticket: ticket.Ticket{Key: "DEMO-1", Summary: "Export", PRs: []ticket.PullRequest{
		{Repo: "example-org/web", Number: 2, Branch: "feat/DEMO-1-export-2-csv", Base: "feat/DEMO-1-export-1-query", State: ticket.PROpen, URL: "u2"},
		{Repo: "example-org/api", Number: 9, Branch: "fix/DEMO-1-auth", Base: "main", State: ticket.PROpen, URL: "u9"},
		{Repo: "example-org/web", Number: 1, Branch: "feat/DEMO-1-export-1-query", Base: "main", State: ticket.PROpen, Checks: ticket.ChecksPass, URL: "u1"},
	}}}
	var b bytes.Buffer
	(&App{}).renderShow(&b, d)
	got := b.String()
	want := "Pull requests\n" +
		"  example-org/api#9  open  u9\n" +
		"  Stack of 2 · feat/DEMO-1-export · Open\n" +
		"    1  example-org/web#1  ✓ checks pass  u1\n" +
		"    2  example-org/web#2  open  u2\n"
	if !strings.Contains(got, want) {
		t.Errorf("show =\n%s\nwant the part\n%s", got, want)
	}
}
