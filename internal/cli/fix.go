package cli

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"path/filepath"
	"slices"
	"strings"

	"github.com/spf13/cobra"

	"github.com/pavp/tkt/internal/config"
	"github.com/pavp/tkt/internal/github"
	"github.com/pavp/tkt/internal/ticket"
	"github.com/pavp/tkt/internal/workspace"
)

func newFixCmd(a *App) *cobra.Command {
	var printOnly bool
	cmd := &cobra.Command{
		Use:   "fix [KEY]",
		Short: "Send a ticket's failing CI, review comments and merge conflicts to its agent",
		Long: "Collect the failing check logs, the requested-change reviews, the unresolved review comments and\n" +
			"the merge conflicts of a ticket's open pull requests, and send them to the ticket's Claude Code agent\n" +
			"in Herdr as a prompt. Outside Herdr, or with --print, the prompt is printed instead. Nothing is written\n" +
			"to Jira or GitHub.",
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := a.LoadConfig()
			if err != nil {
				return err
			}
			key, err := a.resolveKey(cmd.Context(), c, args)
			if err != nil {
				return err
			}
			msg, err := a.sendFeedback(cmd.Context(), c, key, printOnly)
			if err != nil {
				return err
			}
			a.printf("%s\n", msg)
			return nil
		},
	}
	cmd.Flags().BoolVar(&printOnly, "print", false, "print the prompt instead of sending it")
	return cmd
}

// sendFeedback builds the feedback prompt for key and delivers it, returning what happened.
func (a *App) sendFeedback(ctx context.Context, c *config.Config, key ticket.Key, printOnly bool) (string, error) {
	d, err := a.loadDetail(ctx, c, key, nil)
	if err != nil {
		return "", err
	}
	prompt, err := a.feedbackPrompt(ctx, c, d.Ticket)
	if err != nil {
		return "", err
	}
	if printOnly || a.Env.Getenv("HERDR_ENV") != "1" {
		return prompt, nil
	}
	pane, err := a.agentPaneFor(ctx, c, key)
	if err != nil {
		return "", err
	}
	if err := a.NewHerdr().Prompt(ctx, pane, prompt); err != nil {
		return "", err
	}
	return fmt.Sprintf("✓ Sent %s's feedback to its agent", key), nil
}

func (a *App) feedbackPrompt(ctx context.Context, c *config.Config, t ticket.Ticket) (string, error) {
	gh := a.NewGitHub(c)
	var b strings.Builder
	for _, pr := range t.PRs {
		if pr.State != ticket.PROpen {
			continue
		}
		if pr.Checks == ticket.ChecksFail && pr.HeadSHA != "" {
			checks, err := gh.FailedChecks(ctx, pr.Repo, pr.HeadSHA)
			if err != nil {
				return "", err
			}
			for _, fc := range checks {
				fmt.Fprintf(&b, "CI check %q failed on %s#%d (%s).\n", fc.Name, pr.Repo, pr.Number, fc.URL)
				if fc.Summary != "" {
					fmt.Fprintf(&b, "Summary: %s\n", fc.Summary)
				}
				if fc.LogTail != "" {
					fmt.Fprintf(&b, "End of the log:\n```\n%s\n```\n", fc.LogTail)
				}
				b.WriteString("\n")
			}
		}
		if pr.Review == ticket.ReviewChangesRequested {
			reviews, err := gh.ChangeRequests(ctx, pr.Repo, pr.Number)
			if err != nil {
				return "", err
			}
			for _, r := range reviews {
				writeReview(&b, pr, r)
			}
		}
		if pr.Unresolved > 0 {
			threads, err := gh.UnresolvedThreads(ctx, pr.Repo, pr.Number)
			if err != nil {
				return "", err
			}
			for _, th := range threads {
				writeThread(&b, pr, th)
			}
		}
		if pr.Merge == ticket.MergeConflicts {
			fmt.Fprintf(&b, "%s#%d has merge conflicts with its base branch: rebase it onto the base branch, resolve the conflicts, and push with --force-with-lease.\n\n", pr.Repo, pr.Number)
		}
	}
	if b.Len() == 0 {
		return "", fmt.Errorf("%s has no failing CI, requested changes, unresolved comments or conflicts on its open pull requests", t.Key)
	}
	return fmt.Sprintf("Feedback on Jira ticket %s (%s):\n\n%sFix these, run the relevant tests locally, and push to the same branch.", t.Key, t.Summary, b.String()), nil
}

func writeReview(b *strings.Builder, pr ticket.PullRequest, r github.ChangeRequest) {
	fmt.Fprintf(b, "@%s requested changes on %s#%d", r.Author, pr.Repo, pr.Number)
	if r.Body != "" {
		fmt.Fprintf(b, ": %s", r.Body)
	}
	b.WriteString("\n")
	for _, cm := range r.Comments {
		fmt.Fprintf(b, "- %s:%d: %s\n", cm.Path, cm.Line, strings.ReplaceAll(strings.TrimSpace(cm.Body), "\n", " "))
	}
	b.WriteString("\n")
}

func writeThread(b *strings.Builder, pr ticket.PullRequest, th github.ReviewThread) {
	fmt.Fprintf(b, "Unresolved comment on %s#%d at %s:%d:\n", pr.Repo, pr.Number, th.Path, th.Line)
	for _, cm := range th.Comments {
		fmt.Fprintf(b, "- @%s: %s\n", cm.Author, strings.ReplaceAll(cm.Body, "\n", " "))
	}
	b.WriteString("\n")
}

// agentPaneFor finds the Herdr pane of the agent working in one of the ticket's worktrees.
func (a *App) agentPaneFor(ctx context.Context, c *config.Config, key ticket.Key) (string, error) {
	all, err := a.allWork(ctx, c)
	if err != nil {
		return "", err
	}
	paths := map[string]bool{}
	for _, w := range workspace.For(all, key) {
		if w.Path != "" {
			paths[filepath.Clean(w.Path)] = true
		}
	}
	if len(paths) == 0 {
		return "", fmt.Errorf("%s has no worktree yet: start it first", key)
	}
	h := a.NewHerdr()
	wss, err := h.Workspaces(ctx)
	if err != nil {
		return "", err
	}
	ids := map[string]bool{}
	for _, ws := range wss {
		if ws.Worktree != nil && paths[filepath.Clean(ws.Worktree.CheckoutPath)] {
			ids[ws.ID] = true
		}
	}
	agents, err := h.Agents(ctx)
	if err != nil {
		return "", err
	}
	for _, ag := range agents {
		if ids[ag.WorkspaceID] && ag.Agent != "" {
			return ag.PaneID, nil
		}
	}
	// Herdr knows the worktree only of workspaces it created on one, so an agent opened otherwise is found
	// by its folder, as Start finds it.
	for _, p := range slices.Sorted(maps.Keys(paths)) {
		if ag, ok := agentAt(ctx, h, p); ok {
			return ag.PaneID, nil
		}
	}
	return "", errors.New("no agent is running in " + string(key) + "'s workspace: open it with Enter on the board, or `tkt herdr start " + string(key) + "`")
}
