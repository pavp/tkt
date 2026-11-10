package cli

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/pavp/tkt/internal/ticket"
	"github.com/pavp/tkt/internal/tui"
)

// startReviewSetup gives a review to a setup agent beside the clone, on Herdr desktop: it creates the pull request's
// worktree and runs `tkt herdr review-ready` there, which opens the reviewing agent in a space of its own, since an
// agent cannot move to another folder or space.
func (a *App) startReviewSetup(ctx context.Context, h HerdrAPI, task tui.AgentTask, clone, label string) error {
	pr := *task.Review
	saved := func(pane, name, kind string) {
		st := a.loadState()
		st.Handoffs = append(slices.DeleteFunc(st.Handoffs, func(x handoff) bool { return sameReview(x, pr.Repo, pr.Number) }),
			handoff{Review: &pr, Repo: pr.Repo, Clone: clone, Pane: pane, Agent: name, Kind: kind, At: a.Now(), Desktop: true})
		a.saveState(st)
	}
	pane, err := a.startTaskAgent(ctx, h, agentRequest{Kind: task.Kind, Dir: clone, Label: label, Prompt: reviewSetupPrompt(pr), Fresh: true, Started: saved})
	if err != nil {
		return fmt.Errorf("could not start an agent for the %s: %w", task.String(), err)
	}
	bringForward(ctx, h, pane)
	return nil
}

func sameReview(ho handoff, repo string, number int) bool {
	return ho.Desktop && ho.Review != nil && strings.EqualFold(ho.Review.Repo, repo) && ho.Review.Number == number
}

func reviewSetupPrompt(pr ticket.PullRequest) string {
	fetch := fmt.Sprintf("Fetch its branch %s from origin.", pr.Branch)
	if pr.Branch == "" {
		fetch = fmt.Sprintf("Fetch its head from origin (pull/%d/head) into a local branch.", pr.Number)
	}
	return fmt.Sprintf(`Prepare the review of pull request %[1]s (%[2]s#%[3]d).
Another agent does the review; your only job is to create its worktree, leaving this checkout as it is.
1. %[4]s
2. Create the worktree with git on that branch where this repository's rules put worktrees; when they say nothing, use ~/.herdr/worktrees/<repository folder name>/<branch with every / replaced by ->. If a worktree of that branch already exists, use it.
3. From inside the worktree, run: tkt herdr review-ready %[2]s#%[3]d
4. Stop there. tkt starts the reviewing agent in a new space at the worktree and closes this one.`, pr.URL, pr.Repo, pr.Number, fetch)
}

var reviewArg = regexp.MustCompile(`^([^/#\s]+/[^/#\s]+)#([1-9][0-9]*)$`)

func newReviewReadyCmd(a *App) *cobra.Command {
	return &cobra.Command{
		Use:   "review-ready OWNER/NAME#N",
		Short: "Hand a pending review on to an agent in a new space at this worktree (Herdr desktop)",
		Long: "Run by the setup agent of the board's \"Review with ▸ A new agent\" from inside the worktree it created:\n" +
			"the review's agent opens in a new space there and the setup agent closes. It registers no work.",
		Hidden: true,
		Args:   cobra.ExactArgs(1),
		RunE:   func(cmd *cobra.Command, args []string) error { return a.reviewReady(cmd.Context(), args[0]) },
	}
}

func (a *App) reviewReady(ctx context.Context, arg string) error {
	m := reviewArg.FindStringSubmatch(arg)
	if m == nil {
		return fmt.Errorf("%q is not a pull request: give it as OWNER/NAME#N", arg)
	}
	repo := m[1]
	number, _ := strconv.Atoi(m[2])
	co, err := a.inspectCheckout(ctx, "")
	if err != nil {
		return err
	}
	match := func(ho handoff) bool { return sameReview(ho, repo, number) }
	for _, ho := range a.loadState().Handoffs {
		if match(ho) && filepath.Clean(ho.Clone) == filepath.Clean(co.top) {
			return fmt.Errorf("%s is the main checkout, and the main checkout stays free: create a separate worktree for %s with git and run this command there", co.top, arg)
		}
	}
	h := a.NewHerdr()
	s, ok := h.(agentSpaces)
	if !ok {
		return errors.New("only Herdr desktop hands a review on to a new space")
	}
	ho, ok := a.takeHandoffWhere(match)
	if !ok {
		return fmt.Errorf("no review of %s waits for a worktree: start one from the board's Review with ▸ A new agent", arg)
	}
	_, name, _ := strings.Cut(ho.Review.Repo, "/")
	task := reviewPrompt(*ho.Review) + "\n\nThis worktree was created for this review from the main checkout at " + ho.Clone + ": review the pull request here."
	_, pane, err := s.OpenAgentSpace(ctx, co.top, fmt.Sprintf("review · %s#%d", name, number), ho.Kind, task)
	if !a.handedOff(ctx, h, ho, err) {
		return err
	}
	a.noteWindow(focusAgent(ctx, h, pane))
	a.closeLater(ctx, h, s, ho, pane, co.top)
	return nil
}
