package cli

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/pavp/tkt/internal/ticket"
	"github.com/pavp/tkt/internal/workspace"
)

// headlessTimeout bounds the one-shot Claude that creates a Start's worktree: it only reads the repository's rules and
// runs git.
const headlessTimeout = 4 * time.Minute

// headlessWorktree is the answer the one-shot Claude must give, which tkt checks against git before trusting it.
type headlessWorktree struct {
	Branch   string `json:"branch"`
	Worktree string `json:"worktree"`
}

const headlessSchema = `{"type":"object","properties":{"branch":{"type":"string"},"worktree":{"type":"string"}},"required":["branch","worktree"]}`

// headlessArgs runs Claude once, without a session, user settings or MCP servers, so it follows only the
// repository's own rules and can do nothing but read them and run git.
func headlessArgs(key ticket.Key, summary, clone, home string) []string {
	folder := filepath.Join(home, ".herdr", "worktrees", filepath.Base(clone))
	prompt := fmt.Sprintf(`Create the worktree for Jira ticket %[1]s: %[2]s

Another agent does the work in it; your only job is to create it, without changing this checkout.
1. Following this repository's own conventions for branch names, base branch and tooling, choose the branch and its base. The branch name must contain %[1]s.
2. Bring the base up to date from origin, then create the worktree with git worktree add, on a new branch, where this repository's rules put worktrees; when they say nothing, use %[3]s/<branch with every / replaced by ->.
3. Answer with the branch and the worktree's absolute path.`, key, summary, folder)
	return []string{"-p", prompt, "--model", "haiku", "--output-format", "json", "--json-schema", headlessSchema,
		"--allowedTools", "Bash(git:*)", "Read", "Grep", "Glob",
		"--no-session-persistence", "--setting-sources", "project,local", "--strict-mcp-config"}
}

// startHeadless gives key to an agent of kind in a new space at the worktree a one-shot Claude creates in clone,
// registering it first. Nothing opens when the worktree is not one git has on a branch with key.
func (a *App) startHeadless(ctx context.Context, h HerdrAPI, s agentSpaces, plan startPlan, kind, clone, repo string) error {
	dir, err := a.headlessWorktree(ctx, plan.Key, plan.Summary, clone, repo)
	if err != nil {
		return fmt.Errorf("could not create the worktree of %s in %s: %w", plan.Key, clone, err)
	}
	ho := handoff{Key: plan.Key, Summary: plan.Summary, Repo: repo, Clone: clone, Kind: kind}
	if _, err := a.startSpaceHandoff(ctx, h, s, ho, dir); err != nil {
		return fmt.Errorf("could not start an agent for %s in %s: %w", plan.Key, dir, err)
	}
	return nil
}

// headlessWorktree runs the one-shot Claude in clone and returns the worktree it created, once registered.
func (a *App) headlessWorktree(ctx context.Context, key ticket.Key, summary, clone, repo string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, headlessTimeout)
	defer cancel()
	out, runErr := a.Run.Run(ctx, clone, a.claudeProgram(), headlessArgs(key, summary, clone, a.Env.Home), nil)
	var res struct {
		IsError    bool              `json:"is_error"`
		Result     string            `json:"result"`
		Structured *headlessWorktree `json:"structured_output"`
	}
	if err := json.Unmarshal(out, &res); err != nil || res.IsError || res.Structured == nil {
		return "", cmp.Or(runErr, fmt.Errorf("claude did not say which worktree it created: %s", cmp.Or(strings.TrimSpace(res.Result), strings.TrimSpace(string(out)))))
	}
	wt := *res.Structured
	co, err := a.inspectCheckout(ctx, wt.Worktree)
	switch {
	case err != nil:
		return "", fmt.Errorf("claude answered %s, which is not a git checkout: %w", wt.Worktree, err)
	case co.branch != wt.Branch:
		return "", fmt.Errorf("claude answered branch %s, but %s is on %s", wt.Branch, wt.Worktree, co.branch)
	case filepath.Clean(co.top) == filepath.Clean(clone):
		return "", fmt.Errorf("claude switched the main checkout to %s instead of creating a worktree", co.branch)
	case !strings.EqualFold(co.repo, repo):
		return "", fmt.Errorf("%s belongs to %s, not %s", co.top, cmp.Or(co.repo, "no GitHub repository"), repo)
	case !slices.Contains(ticket.BranchKeys(co.branch), key):
		return "", fmt.Errorf("branch %q does not contain %s", co.branch, key)
	}
	w := workspace.Work{Key: key, Repo: co.repo, Branch: co.branch, Path: co.top, Source: workspace.SourceAgent, LinkedAt: a.Now()}
	if err := a.links().Add(w); err != nil {
		return "", err
	}
	return co.top, nil
}

// claudeProgram is the program the claude kind runs, which may be configured with its own path.
func (a *App) claudeProgram() string {
	if run := a.agentRun("claude"); len(run) > 0 {
		return run[0]
	}
	return "claude"
}
