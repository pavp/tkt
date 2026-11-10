package cli

import (
	"cmp"
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/pavp/tkt/internal/ticket"
	"github.com/pavp/tkt/internal/tui"
)

// startReviewInWorktree gives a review a worktree of its own on Herdr desktop and starts its agent in a new space
// there. A review decides nothing about the branch, which already exists, so tkt creates the worktree itself, where
// Herdr desktop puts them, instead of a setup agent: an agent cannot move to another folder or space.
func (a *App) startReviewInWorktree(ctx context.Context, h HerdrAPI, s agentSpaces, task tui.AgentTask, prompt, clone, label string) error {
	pr := *task.Review
	dir, existed, err := a.reviewWorktree(ctx, clone, pr)
	if err != nil {
		return fmt.Errorf("could not create the worktree of %s#%d: %w", pr.Repo, pr.Number, err)
	}
	if existed {
		prompt += "\n\nThis worktree already existed: bring it up to date with the pull request's head before you review."
	}
	_, pane, err := s.OpenAgentSpace(ctx, dir, label, task.Kind, prompt)
	if err != nil {
		return fmt.Errorf("could not start an agent for the %s: %w", task.String(), err)
	}
	a.noteWindow(focusAgent(ctx, h, pane))
	return nil
}

// reviewWorktree is the worktree of pr's branch: the one git already has, else a new one at Herdr desktop's
// ~/.herdr/worktrees/<clone folder>/<branch with / as -> on the pull request's head. A branch already local is never
// moved: the new worktree then holds the head detached.
func (a *App) reviewWorktree(ctx context.Context, clone string, pr ticket.PullRequest) (string, bool, error) {
	git := func(args ...string) (string, error) {
		out, err := a.Run.Run(ctx, clone, "git", args, nil)
		if err != nil {
			return "", fmt.Errorf("git %s: %w: %s", args[0], err, strings.TrimSpace(string(out)))
		}
		return string(out), nil
	}
	branch := cmp.Or(pr.Branch, fmt.Sprintf("pr-%d", pr.Number))
	list, err := git("worktree", "list", "--porcelain")
	if err != nil {
		return "", false, err
	}
	if dir := worktreeOfBranch(list, branch); dir != "" && filepath.Clean(dir) != filepath.Clean(clone) {
		return dir, true, nil
	}
	if _, err := git("fetch", "origin", fmt.Sprintf("pull/%d/head", pr.Number)); err != nil {
		return "", false, err
	}
	dir := filepath.Join(a.Env.Home, ".herdr", "worktrees", filepath.Base(clone), strings.ReplaceAll(branch, "/", "-"))
	add := []string{"worktree", "add", "-b", branch, dir, "FETCH_HEAD"}
	if _, err := a.Run.Run(ctx, clone, "git", []string{"rev-parse", "--verify", "--quiet", "refs/heads/" + branch}, nil); err == nil {
		add = []string{"worktree", "add", "--detach", dir, "FETCH_HEAD"}
	}
	if _, err := git(add...); err != nil {
		return "", false, err
	}
	return dir, false, nil
}

// worktreeOfBranch is the folder of the worktree that has branch checked out in `git worktree list --porcelain`.
func worktreeOfBranch(list, branch string) string {
	for _, block := range strings.Split(list, "\n\n") {
		dir := ""
		for _, line := range strings.Split(block, "\n") {
			if p, ok := strings.CutPrefix(line, "worktree "); ok {
				dir = p
			}
			if line == "branch refs/heads/"+branch && dir != "" {
				return dir
			}
		}
	}
	return ""
}
