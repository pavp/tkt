package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pavp/tkt/internal/jira"
	"github.com/pavp/tkt/internal/workspace"
)

const workConfig = `
repo_roots = ["~/src"]

[jira]
site = "example.atlassian.net"
email = "dev@example.com"

[[spaces]]
key = "DEMO"

[[spaces.repos]]
repo = "example-org/web"

[[spaces.repos]]
repo = "example-org/api"
`

type launchCall struct {
	dir, name string
	args      []string
}

func workHarness(t *testing.T) (*harness, *[]launchCall) {
	t.Helper()
	h, _ := listHarness(t)
	if err := os.WriteFile(h.app.configPath, []byte(workConfig), 0o600); err != nil {
		t.Fatal(err)
	}
	h.jira.details = map[string]jira.Issue{
		"DEMO-7": {Key: "DEMO-7", Summary: "Export users", Status: "To Do", StatusCategory: "new"},
		"DEMO-1": {Key: "DEMO-1", Summary: "Filter accounts", Status: "In Progress", StatusCategory: "indeterminate"},
	}
	h.run.out["git worktree list --porcelain"] = ""
	h.run.out["git for-each-ref --format=%(refname:short) refs/heads refs/remotes/origin"] = "main\n"
	var calls []launchCall
	h.app.Launch = func(dir, name string, args []string) error {
		calls = append(calls, launchCall{dir, name, args})
		return nil
	}
	return h, &calls
}

func (h *harness) clone(t *testing.T, name string) string {
	t.Helper()
	dir := filepath.Join(h.home, "src", name)
	if err := os.MkdirAll(filepath.Join(dir, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	h.run.out["remote "+dir] = "git@github.com:example-org/" + name + ".git"
	return dir
}

func (h *harness) inCheckout(branch, top, remote string) {
	h.run.out["git rev-parse --abbrev-ref HEAD"] = branch + "\n"
	h.run.out["git rev-parse --show-toplevel"] = top + "\n"
	h.run.out["git remote get-url origin"] = remote + "\n"
}

func TestLinkRegistersWorktree(t *testing.T) {
	h, _ := workHarness(t)
	h.inCheckout("feature/DEMO-7-export", "/src/web-DEMO-7", "git@github.com:example-org/web.git")
	if code := h.exec("link"); code != 0 {
		t.Fatalf("exit %d: %s", code, h.errOut)
	}
	if !strings.Contains(h.out.String(), "✓ DEMO-7 ← feature/DEMO-7-export (example-org/web)") {
		t.Errorf("output = %q", h.out)
	}
	links, err := h.app.links().Load()
	if err != nil || len(links) != 1 {
		t.Fatalf("links = %+v, %v", links, err)
	}
	if l := links[0]; l.Key != "DEMO-7" || l.Path != "/src/web-DEMO-7" || l.Source != workspace.SourceAgent || l.LinkedAt.IsZero() {
		t.Errorf("link = %+v", l)
	}
	if code := h.exec("link", "--source", "hook", "demo-7"); code != 0 {
		t.Fatalf("exit %d: %s", code, h.errOut)
	}
	links, _ = h.app.links().Load()
	if len(links) != 1 || links[0].Source != workspace.SourceHook {
		t.Errorf("relink did not replace: %+v", links)
	}
}

func TestLinkRejectsBranchWithoutKey(t *testing.T) {
	h, _ := workHarness(t)
	h.inCheckout("fix-date-filter", "/src/web-x", "git@github.com:example-org/web.git")
	if code := h.exec("link"); code == 0 || !strings.Contains(h.errOut.String(), `branch "fix-date-filter" has no ticket key; rename it to include one, e.g. feature/DEMO-123-short-name`) {
		t.Errorf("exit %d, %q", code, h.errOut)
	}
	if code := h.exec("link", "DEMO-7"); code == 0 || !strings.Contains(h.errOut.String(), `branch "fix-date-filter" does not contain DEMO-7`) {
		t.Errorf("exit %d, %q", code, h.errOut)
	}
	h.inCheckout("feature/DEMO-1-other", "/src/web-x", "git@github.com:example-org/web.git")
	if code := h.exec("link", "DEMO-7"); code == 0 || !strings.Contains(h.errOut.String(), "does not contain DEMO-7") {
		t.Errorf("mismatched key: exit %d, %q", code, h.errOut)
	}
	if links, _ := h.app.links().Load(); len(links) != 0 {
		t.Errorf("rejected links were saved: %+v", links)
	}
}

func TestLinkWarnsForUnconfiguredRepo(t *testing.T) {
	h, _ := workHarness(t)
	h.inCheckout("feature/DEMO-7-x", "/src/tool", "https://github.com/example-org/tool")
	if code := h.exec("link"); code != 0 {
		t.Fatalf("exit %d: %s", code, h.errOut)
	}
	if !strings.Contains(h.errOut.String(), "example-org/tool is not in your config") {
		t.Errorf("stderr = %q", h.errOut)
	}
}

func TestLinkOutsideGit(t *testing.T) {
	h, _ := workHarness(t)
	if code := h.exec("link"); code == 0 || !strings.Contains(h.errOut.String(), "not inside a git checkout") {
		t.Errorf("exit %d, %q", code, h.errOut)
	}
	h.inCheckout("HEAD", "/x", "git@github.com:example-org/web.git")
	if code := h.exec("link"); code == 0 || !strings.Contains(h.errOut.String(), "HEAD is detached") {
		t.Errorf("exit %d, %q", code, h.errOut)
	}
}

func TestStartPicksReposAndLaunchesAgent(t *testing.T) {
	h, calls := workHarness(t)
	web := h.clone(t, "web")
	api := h.clone(t, "api")
	h.app.Stdin = strings.NewReader("2,1\n")
	if code := h.exec("start", "DEMO-7"); code != 0 {
		t.Fatalf("exit %d: %s", code, h.errOut)
	}
	if len(*calls) != 1 {
		t.Fatalf("launches = %+v", *calls)
	}
	c := (*calls)[0]
	if c.dir != api || c.name != "claude" || !strings.Contains(c.args[0], "Work on Jira ticket DEMO-7: Export users") {
		t.Errorf("launch = %+v", c)
	}
	out := h.out.String()
	if !strings.Contains(out, "→ Suggested: move DEMO-7 to In Progress in Jira") || !strings.Contains(out, "Also start it in "+web) {
		t.Errorf("output:\n%s", out)
	}
	if !strings.Contains(h.errOut.String(), "1) example-org/web") {
		t.Errorf("menu not shown: %q", h.errOut)
	}
	if got := h.app.loadState().LastRepos["DEMO"]; strings.Join(got, ",") != "example-org/api,example-org/web" {
		t.Errorf("last used = %v", got)
	}

	*calls = nil
	h.app.in = nil
	h.app.Stdin = strings.NewReader("\n")
	h.exec("start", "DEMO-7")
	if !strings.Contains(h.errOut.String(), "(last used)") || len(*calls) != 1 {
		t.Errorf("last used not preselected: %q %+v", h.errOut, *calls)
	}
}

func TestStartRepoFlagSkipsPrompt(t *testing.T) {
	h, calls := workHarness(t)
	web := h.clone(t, "web")
	if code := h.exec("start", "DEMO-7", "--repo", "web"); code != 0 {
		t.Fatalf("exit %d: %s", code, h.errOut)
	}
	if len(*calls) != 1 || (*calls)[0].dir != web {
		t.Errorf("launches = %+v", *calls)
	}
	if code := h.exec("start", "DEMO-7", "--repo", "nope"); code == 0 || !strings.Contains(h.errOut.String(), "nope is not a repository of space DEMO") {
		t.Errorf("exit %d, %q", code, h.errOut)
	}
}

func TestStartOpensExistingWorktree(t *testing.T) {
	h, calls := workHarness(t)
	h.clone(t, "web")
	h.run.out["git worktree list --porcelain"] = "worktree /src/web-DEMO-1\nHEAD a\nbranch refs/heads/feature/DEMO-1-filter\n"
	if code := h.exec("start", "DEMO-1"); code != 0 {
		t.Fatalf("exit %d: %s", code, h.errOut)
	}
	if len(*calls) != 1 || (*calls)[0].dir != "/src/web-DEMO-1" || !strings.Contains((*calls)[0].args[0], "Continue work on Jira ticket DEMO-1") {
		t.Errorf("launches = %+v", *calls)
	}
	if strings.Contains(h.out.String(), "Suggested") {
		t.Error("suggested a transition for a ticket already in progress")
	}
}

func TestStartOffersClone(t *testing.T) {
	h, calls := workHarness(t)
	h.app.Stdin = strings.NewReader("1\nn\n")
	if code := h.exec("start", "DEMO-7"); code == 0 || !strings.Contains(h.errOut.String(), "skipping example-org/web: not cloned locally (pass --clone to clone it)") {
		t.Errorf("declined clone: exit %d, %q", code, h.errOut)
	}
	dest := filepath.Join(h.home, "src", "web")
	h.run.out["gh repo clone example-org/web "+dest] = ""
	h.app.in = nil
	h.app.Stdin = strings.NewReader("1\n")
	if code := h.exec("start", "DEMO-7", "--clone"); code != 0 {
		t.Fatalf("exit %d: %s", code, h.errOut)
	}
	if len(*calls) != 1 || (*calls)[0].dir != dest {
		t.Errorf("launches = %+v", *calls)
	}
}

func TestStartUnknownSpace(t *testing.T) {
	h, _ := workHarness(t)
	if code := h.exec("start", "OPS-1"); code == 0 || !strings.Contains(h.errOut.String(), "space OPS is not configured") {
		t.Errorf("exit %d, %q", code, h.errOut)
	}
}

func TestInteractiveCommandsRejectJSON(t *testing.T) {
	h, calls := workHarness(t)
	for _, args := range [][]string{{"--json", "start", "DEMO-7"}, {"--json", "init"}} {
		if code := h.exec(args...); code == 0 || !strings.Contains(h.errOut.String(), "--json is not supported by `tkt "+args[1]+"`: it is interactive") {
			t.Errorf("%v: exit %d, %q", args, code, h.errOut)
		}
	}
	if len(*calls) != 0 {
		t.Error("launched an agent despite --json")
	}
}

func TestLinkAcceptsLowercaseKeysInBranches(t *testing.T) {
	h, _ := workHarness(t)
	h.inCheckout("test/demo-7-coverage", "/src/web-demo-7", "git@github.com:example-org/web.git")
	if code := h.exec("link"); code != 0 || !strings.Contains(h.out.String(), "✓ DEMO-7 ← test/demo-7-coverage") {
		t.Fatalf("exit %d: %s %s", code, h.out, h.errOut)
	}
	if code := h.exec("link", "DEMO-7"); code != 0 {
		t.Errorf("explicit key against a lowercase branch: exit %d, %s", code, h.errOut)
	}
}
