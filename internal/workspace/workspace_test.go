package workspace

import (
	"context"
	"errors"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/pavp/tkt/internal/ticket"
)

type gitFake map[string]string

func (g gitFake) Run(_ context.Context, dir, name string, args []string, _ []byte) ([]byte, error) {
	key := dir + ": " + name + " " + strings.Join(args, " ")
	if v, ok := g[key]; ok {
		return []byte(v), nil
	}
	return nil, errors.New("not faked: " + key)
}

func (g gitFake) LookPath(string) (string, error) { return "", nil }

const porcelain = `worktree /src/web
HEAD abc
branch refs/heads/main

worktree /src/web-DEMO-1
HEAD def
branch refs/heads/feature/DEMO-1-filter

worktree /src/web-detached
HEAD 123
detached
`

func TestDiscover(t *testing.T) {
	g := gitFake{
		"/src/web: git worktree list --porcelain":                                             porcelain,
		"/src/web: git for-each-ref --format=%(refname:short) refs/heads refs/remotes/origin": "main\nfeature/DEMO-1-filter\nfix/DEMO-2\norigin/HEAD\norigin/feature/DEMO-3-remote\norigin/fix/DEMO-2\nchore/UTF-8\n",
	}
	got, err := Discover(context.Background(), g, []Clone{{Repo: "example-org/web", Dir: "/src/web"}}, []string{"DEMO"})
	if err != nil {
		t.Fatal(err)
	}
	want := []Work{
		{Key: "DEMO-1", Repo: "example-org/web", Branch: "feature/DEMO-1-filter", Path: "/src/web-DEMO-1", Source: SourceDiscovered},
		{Key: "DEMO-2", Repo: "example-org/web", Branch: "fix/DEMO-2", Source: SourceDiscovered},
		{Key: "DEMO-3", Repo: "example-org/web", Branch: "feature/DEMO-3-remote", Source: SourceDiscovered},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Discover =\n%+v\nwant\n%+v", got, want)
	}
}

func TestDiscoverReportsGitErrors(t *testing.T) {
	if _, err := Discover(context.Background(), gitFake{}, []Clone{{Repo: "example-org/web", Dir: "/x"}}, []string{"DEMO"}); err == nil || !strings.Contains(err.Error(), "example-org/web") {
		t.Errorf("err = %v", err)
	}
}

func TestLinksAddReplacesSameBranch(t *testing.T) {
	l := NewLinks(filepath.Join(t.TempDir(), "data"))
	if got, err := l.Load(); err != nil || got != nil {
		t.Fatalf("empty = %v, %v", got, err)
	}
	at := time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC)
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(l.Add(Work{Key: "DEMO-2", Repo: "example-org/web", Branch: "fix/DEMO-2", Path: "/a", Source: SourceAgent, LinkedAt: at}))
	must(l.Add(Work{Key: "DEMO-1", Repo: "example-org/web", Branch: "feature/DEMO-1", Path: "/b", Source: SourceAgent, LinkedAt: at}))
	must(l.Add(Work{Key: "DEMO-2", Repo: "example-org/web", Branch: "fix/DEMO-2", Path: "/moved", Source: SourceHook, LinkedAt: at}))
	got, err := l.Load()
	must(err)
	if len(got) != 2 || got[0].Key != "DEMO-1" || got[1].Path != "/moved" || got[1].Source != SourceHook {
		t.Errorf("links = %+v", got)
	}
}

func TestMerge(t *testing.T) {
	discovered := []Work{
		{Key: "DEMO-1", Repo: "example-org/web", Branch: "feature/DEMO-1", Path: "/wt1", Source: SourceDiscovered},
		{Key: "DEMO-3", Repo: "example-org/api", Branch: "feature/DEMO-3", Source: SourceDiscovered},
	}
	links := []Work{
		{Key: "DEMO-1", Repo: "example-org/web", Branch: "feature/DEMO-1", Source: SourceAgent},
		{Key: "DEMO-2", Repo: "example-org/web", Branch: "fix/DEMO-2", Path: "/wt2", Source: SourceHook},
	}
	got := Merge(links, discovered)
	if len(got) != 3 {
		t.Fatalf("Merge = %+v", got)
	}
	if got[0].Source != SourceAgent || got[0].Path != "/wt1" {
		t.Errorf("link did not win or lost the discovered path: %+v", got[0])
	}
	if got[1].Key != "DEMO-2" || got[2].Key != "DEMO-3" {
		t.Errorf("order = %+v", got)
	}
	if n := len(For(got, ticket.Key("DEMO-1"))); n != 1 {
		t.Errorf("For = %d", n)
	}
}

func TestMergeDropsLinksToAWorktreeNowOnAnotherBranch(t *testing.T) {
	discovered := []Work{
		{Key: "DEMO-1", Repo: "example-org/web", Branch: "feature/DEMO-1-homogenize", Path: "/wt/web-DEMO-1", Source: SourceDiscovered},
	}
	links := []Work{
		{Key: "DEMO-1", Repo: "example-org/web", Branch: "feature/DEMO-1-read-repository", Path: "/wt/web-DEMO-1/", Source: SourceAgent},
		{Key: "DEMO-2", Repo: "example-org/web", Branch: "fix/DEMO-2", Path: "/wt/web-DEMO-2", Source: SourceAgent},
	}
	got := Merge(links, discovered)
	if len(got) != 2 || got[0].Branch != "feature/DEMO-1-homogenize" || got[1].Key != "DEMO-2" {
		t.Errorf("Merge = %+v", got)
	}
}

func TestPrompts(t *testing.T) {
	p := StartPrompt("DEMO-1", "Filter accounts")
	for _, want := range []string{"Work on Jira ticket DEMO-1: Filter accounts", "must contain DEMO-1", "tkt context DEMO-1 --images .ticket-assets > .ticket.md", ".git/info/exclude", "tkt link DEMO-1"} {
		if !strings.Contains(p, want) {
			t.Errorf("start prompt missing %q", want)
		}
	}
	if strings.Contains(p, "herdr") {
		t.Error("plain start prompt mentions herdr")
	}
	if hp := HerdrStartPrompt("DEMO-1", "Filter accounts"); !strings.HasPrefix(hp, p) || !strings.Contains(hp, "herdr worktree create") || !strings.Contains(hp, "git worktree add") {
		t.Errorf("herdr start prompt = %q", hp)
	}
	bp := HerdrBranchPrompt("DEMO-1", "Filter accounts")
	for _, want := range []string{"Work on Jira ticket DEMO-1: Filter accounts", "do not create a worktree", "must contain DEMO-1", "tkt context DEMO-1 --images .ticket-assets > .ticket.md", ".git/info/exclude", "tkt link DEMO-1"} {
		if !strings.Contains(bp, want) {
			t.Errorf("branch prompt missing %q", want)
		}
	}
	if !strings.Contains(ContinuePrompt("DEMO-1", "Filter"), "Continue work on Jira ticket DEMO-1") {
		t.Error("continue prompt")
	}
}

func TestPruneForgetsLinksWhoseWorktreeAndBranchAreGone(t *testing.T) {
	clones := []Clone{{Repo: "example-org/web", Dir: "/src/web"}}
	discovered := []Work{
		{Key: "DEMO-2", Repo: "example-org/web", Branch: "fix/DEMO-2", Source: SourceDiscovered},
	}
	links := []Work{
		{Key: "DEMO-1", Repo: "example-org/web", Branch: "feature/DEMO-1", Path: "/wt/gone", Source: SourceAgent},
		{Key: "DEMO-2", Repo: "example-org/web", Branch: "fix/DEMO-2", Path: "/wt/removed", Source: SourceAgent},
		{Key: "DEMO-3", Repo: "example-org/web", Branch: "feature/DEMO-3", Path: "/wt/here", Source: SourceAgent},
		{Key: "DEMO-4", Repo: "example-org/api", Branch: "feature/DEMO-4", Path: "/wt/elsewhere", Source: SourceAgent},
	}
	exists := func(p string) bool { return p == "/wt/here" }
	got := Prune(links, discovered, clones, exists)
	want := []Work{
		{Key: "DEMO-2", Repo: "example-org/web", Branch: "fix/DEMO-2", Source: SourceAgent},
		links[2],
		links[3],
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Prune = %+v\nwant %+v", got, want)
	}
}

func TestLinksRemove(t *testing.T) {
	l := NewLinks(filepath.Join(t.TempDir(), "data"))
	at := time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC)
	for _, w := range []Work{
		{Key: "DEMO-1", Repo: "example-org/web", Branch: "feature/DEMO-1", Path: "/a", LinkedAt: at},
		{Key: "DEMO-2", Repo: "example-org/web", Branch: "fix/DEMO-2", Path: "/b", LinkedAt: at},
		{Key: "DEMO-2", Repo: "example-org/api", Branch: "fix/DEMO-2", Path: "/c", LinkedAt: at},
	} {
		if err := l.Add(w); err != nil {
			t.Fatal(err)
		}
	}
	removed, err := l.Remove(func(w Work) bool { return w.Key == "DEMO-2" })
	if err != nil || len(removed) != 2 {
		t.Fatalf("removed = %+v, %v", removed, err)
	}
	if got, _ := l.Load(); len(got) != 1 || got[0].Key != "DEMO-1" {
		t.Errorf("left = %+v", got)
	}
	if removed, err := l.Remove(func(Work) bool { return false }); err != nil || removed != nil {
		t.Errorf("nothing to remove = %+v, %v", removed, err)
	}
}
