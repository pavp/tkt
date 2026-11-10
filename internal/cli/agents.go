package cli

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/pavp/tkt/internal/config"
	"github.com/pavp/tkt/internal/herdrport"
	"github.com/pavp/tkt/internal/repos"
	"github.com/pavp/tkt/internal/ticket"
	"github.com/pavp/tkt/internal/tui"
	"github.com/pavp/tkt/internal/workspace"
)

func (b *boardActions) InHerdr() bool { return b.inHerdr() }

// OpenAgents lists Herdr's agents for a task: the free ones (idle or done) in a checkout of the task's
// repositories first, then the other free ones, then the busy ones, which still take it after their work.
func (b *boardActions) OpenAgents(task tui.AgentTask) tea.Cmd {
	return func() tea.Msg {
		agents, err := b.a.openAgents(b.ctx, b.cfg(), task)
		return tui.AgentsListedMsg{Task: task, Agents: agents, Err: err}
	}
}

func (a *App) openAgents(ctx context.Context, c *config.Config, task tui.AgentTask) ([]tui.OpenAgent, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	h := a.startHerdr(ctx)
	agents, err := h.Agents(ctx)
	if err != nil {
		return nil, err
	}
	wanted, res := taskRepos(task), a.Resolver(c)
	labels := map[string]string{}
	if wss, err := h.Workspaces(ctx); err == nil {
		for _, ws := range wss {
			labels[ws.ID] = ws.Label
		}
	}
	var out []tui.OpenAgent
	for _, ag := range agents {
		if ag.Agent == "" {
			continue
		}
		repo, _ := res.RepoAt(ctx, ag.Cwd)
		agent := tui.OpenAgent{
			Name: cmp.Or(ag.Name, ag.Agent), Pane: ag.PaneID, Status: ag.Status, Folder: ag.Cwd, Repo: repo, Workspace: labels[ag.WorkspaceID],
			SameRepo: repo != "" && slices.Contains(wanted, repo),
		}
		if repo != "" {
			if head, err := a.Run.Run(ctx, ag.Cwd, "git", []string{"rev-parse", "--abbrev-ref", "HEAD"}, nil); err == nil {
				agent.Branch = strings.TrimSpace(string(head))
				agent.Ticket, _ = ticket.KeyInBranch(agent.Branch, c.SpaceKeys())
			}
		}
		out = append(out, agent)
	}
	rank := func(ag tui.OpenAgent) int {
		free := ag.Status == "idle" || ag.Status == "done"
		switch {
		case free && ag.SameRepo:
			return 0
		case free:
			return 1
		}
		return 2
	}
	slices.SortStableFunc(out, func(x, y tui.OpenAgent) int { return rank(x) - rank(y) })
	return out, nil
}

// taskRepos are the repositories a task is about: the review request's, or those of the ticket's pull requests.
func taskRepos(task tui.AgentTask) []string {
	if task.Review != nil {
		return []string{task.Review.Repo}
	}
	var out []string
	if task.Known != nil {
		for _, pr := range task.Known.PRs {
			out = append(out, pr.Repo)
		}
	}
	return out
}

// SendTask prompts the agent in pane with the task, which waits in its queue when it is working, then focuses it
// and closes the board, as f does.
func (b *boardActions) SendTask(task tui.AgentTask, pane string) tea.Cmd {
	return func() tea.Msg {
		if err := b.a.sendTask(b.ctx, b.cfg(), task, pane); err != nil {
			return tui.StatusMsg(err.Error())
		}
		return b.handedOff("✓ sent " + task.Phrase() + " to the agent")
	}
}

func (a *App) sendTask(ctx context.Context, c *config.Config, task tui.AgentTask, pane string) error {
	prompt, err := a.taskPrompt(ctx, c, task)
	if err != nil {
		return err
	}
	h := a.startHerdr(ctx)
	if err := h.Prompt(ctx, pane, prompt); err != nil {
		return errors.New("could not send the " + task.String() + ": " + err.Error())
	}
	if task.Review == nil {
		markAgent(ctx, h, pane, task.Key)
	}
	bringForward(ctx, h, pane)
	return nil
}

func (b *boardActions) CopyTaskPrompt(task tui.AgentTask) tea.Cmd {
	return func() tea.Msg {
		prompt, err := b.a.taskPrompt(b.ctx, b.cfg(), task)
		if err != nil {
			return tui.StatusMsg(err.Error())
		}
		what := "feedback prompt"
		if task.Review != nil {
			what = "review prompt"
		}
		return b.Copy(prompt, what)()
	}
}

func (a *App) taskPrompt(ctx context.Context, c *config.Config, task tui.AgentTask) (string, error) {
	if task.Review != nil {
		return reviewPrompt(*task.Review), nil
	}
	t, err := a.taskTicket(ctx, c, task)
	if err != nil {
		return "", err
	}
	return a.feedbackPrompt(ctx, c, *t)
}

// taskTicket is the ticket whose feedback a task sends, the board's copy when it has one.
func (a *App) taskTicket(ctx context.Context, c *config.Config, task tui.AgentTask) (*ticket.Ticket, error) {
	if task.Known != nil {
		return task.Known, nil
	}
	d, err := a.loadDetail(ctx, c, task.Key, nil)
	if err != nil {
		return nil, err
	}
	return &d.Ticket, nil
}

// NewAgentTask hands a task to an agent where its work belongs, opening only what is missing; the board then
// closes on the agent's pane.
func (b *boardActions) NewAgentTask(task tui.AgentTask) tea.Cmd {
	return func() tea.Msg {
		prompt, err := b.a.taskPrompt(b.ctx, b.cfg(), task)
		if err != nil {
			return tui.StatusMsg(err.Error())
		}
		dir, note, label, err := b.a.taskPlace(b.ctx, b.cfg(), task, "")
		var missing noClone
		if errors.As(err, &missing) {
			if roots := b.cfg().RepoRoots; len(roots) > 0 {
				return tui.CloneNeededMsg{Task: task, Repo: missing.repo, Into: roots[0]}
			}
			return tui.StatusMsg(missing.repo + " is not cloned locally: set repo_roots in the config so tkt knows where to clone it")
		}
		if err != nil {
			return tui.StatusMsg(err.Error())
		}
		return b.startTask(task, prompt, dir, note, label)
	}
}

// CloneAndStart clones repo for a fix the user agreed to clone, then starts its agent in the new clone.
func (b *boardActions) CloneAndStart(task tui.AgentTask, repo string) tea.Cmd {
	return func() tea.Msg {
		prompt, err := b.a.taskPrompt(b.ctx, b.cfg(), task)
		if err != nil {
			return tui.StatusMsg(err.Error())
		}
		dir, note, label, err := b.a.cloneTaskPlace(b.ctx, b.cfg(), task, repo)
		if err != nil {
			return tui.StatusMsg(err.Error())
		}
		return b.startTask(task, prompt, dir, note, label)
	}
}

// cloneTaskPlace clones repo into the first repository root, then places the task in the new clone.
func (a *App) cloneTaskPlace(ctx context.Context, c *config.Config, task tui.AgentTask, repo string) (dir, note, label string, err error) {
	dest, err := a.Resolver(c).Clone(ctx, config.Repo{Name: repo})
	if err != nil {
		return "", "", "", err
	}
	return a.taskPlace(ctx, c, task, dest)
}

// noClone is a fix whose repository has no local clone yet.
type noClone struct{ repo string }

func (e noClone) Error() string { return e.repo + " is not cloned locally" }

// bringForward shows an agent's pane wherever it is: its workspace first, then the pane, which brings its own tab
// forward, as Start's "Go to the agent in Herdr" does.
func bringForward(ctx context.Context, h HerdrAPI, pane string) {
	if panes, err := h.Panes(ctx); err == nil {
		if i := slices.IndexFunc(panes, func(p herdrport.Pane) bool { return p.ID == pane }); i >= 0 {
			_ = h.FocusWorkspace(ctx, panes[i].WorkspaceID)
		}
	}
	_ = h.FocusPane(ctx, pane)
}

// startTask hands the task to an agent in dir, the note added to its prompt, and closes the board on its pane.
func (b *boardActions) startTask(task tui.AgentTask, prompt, dir, note, label string) tea.Msg {
	if err := b.a.startTask(b.ctx, task, prompt, dir, note, label); err != nil {
		return tui.StatusMsg(err.Error())
	}
	return b.handedOff("✓ an agent took " + task.Phrase())
}

func (a *App) startTask(ctx context.Context, task tui.AgentTask, prompt, dir, note, label string) error {
	h := a.startHerdr(ctx)
	if s, ok := h.(agentSpaces); ok && task.Review != nil && filepath.Clean(dir) != filepath.Clean(a.reviewsDir()) {
		return a.startReviewInWorktree(ctx, h, s, task, prompt, dir, label)
	}
	if note != "" {
		prompt += "\n\n" + note
	}
	pane, err := a.startTaskAgent(ctx, h, agentRequest{Kind: task.Kind, Dir: dir, Label: label, Prompt: prompt, Fresh: task.Review != nil})
	if err != nil {
		return fmt.Errorf("could not start an agent for the %s: %w", task.String(), err)
	}
	if task.Review == nil {
		markAgent(ctx, h, pane, task.Key)
	}
	bringForward(ctx, h, pane)
	return nil
}

// taskPlace is the folder a new agent works on a task in, and what it is told about it. A review runs in the
// repository's clone, the agent creating a worktree for the pull request's branch there. A fix runs in the ticket's worktree, else in a checkout
// already on its pull request's branch, else in the clone: checking the branch out there when the clone is free,
// creating a worktree for it otherwise, since tkt never creates worktrees itself. A review needs no clone: without
// one it runs in the data directory's reviews folder, the same every time; a fix without one returns noClone. clone,
// when set, is the clone just made for the fix.
func (a *App) taskPlace(ctx context.Context, c *config.Config, task tui.AgentTask, clone string) (dir, note, label string, err error) {
	if task.Review != nil {
		_, name, _ := strings.Cut(task.Review.Repo, "/")
		label = fmt.Sprintf("review · %s#%d", name, task.Review.Number)
		dir, err = a.cloneOf(ctx, c, task.Review.Repo)
		branch := "the pull request's branch"
		if task.Review.Branch != "" {
			branch = "its branch " + task.Review.Branch
		}
		note = "Do not change this checkout: create a worktree for " + branch + " following the project's rules, and review the pull request there."
		if errors.Is(err, repos.ErrNotFound) {
			dir = a.reviewsDir()
			note = "There is no local clone of " + task.Review.Repo + ": read the pull request with gh from its URL, and clone it into a temporary folder only if you need to run it."
			err = os.MkdirAll(dir, 0o700)
		}
		return dir, note, label, err
	}
	t, err := a.taskTicket(ctx, c, task)
	if err != nil {
		return "", "", "", err
	}
	label = string(t.Key)
	all, err := a.allWork(ctx, c)
	if err != nil {
		return "", "", "", err
	}
	for _, w := range workspace.For(all, t.Key) {
		if w.Path != "" {
			return w.Path, "", label, nil
		}
	}
	i := slices.IndexFunc(t.PRs, func(pr ticket.PullRequest) bool { return pr.State == ticket.PROpen })
	if i < 0 {
		return "", "", "", fmt.Errorf("%s has no open pull request to work on", t.Key)
	}
	pr := t.PRs[i]
	for _, w := range all {
		if w.Branch == pr.Branch && w.Path != "" {
			return w.Path, "", label, nil
		}
	}
	dir = clone
	if dir == "" {
		if dir, err = a.cloneOf(ctx, c, pr.Repo); errors.Is(err, repos.ErrNotFound) {
			return "", "", "", noClone{pr.Repo}
		} else if err != nil {
			return "", "", "", err
		}
	}
	note = fmt.Sprintf("Work in this checkout: check out its branch %s first.", pr.Branch)
	if !a.checkoutFree(ctx, dir) {
		note = fmt.Sprintf("This checkout has work in progress: create a worktree for its branch %s following the project's rules, work there, and register it with `tkt link`.", pr.Branch)
	}
	return dir, note, label, nil
}

// cloneOf is the local clone of a repository ("owner/name"): its configured path, else the first one found.
func (a *App) cloneOf(ctx context.Context, c *config.Config, repo string) (string, error) {
	r := config.Repo{Name: repo}
	if i := slices.IndexFunc(c.Repos(), func(x config.Repo) bool { return x.Name == repo }); i >= 0 {
		r = c.Repos()[i]
	}
	return a.Resolver(c).Resolve(ctx, r)
}

// reviewsDir is where a review without a local clone runs, the same folder every time.
func (a *App) reviewsDir() string { return filepath.Join(a.Env.DataDir(), "reviews") }

// agentRequest is the agent startTaskAgent prompts: of Kind (empty for the default), in Dir, its workspace labelled
// Label. Fresh always starts a new agent; Started, when set, hears of a new one before it gets Prompt.
type agentRequest struct {
	Kind, Dir, Label, Prompt string
	Fresh                    bool
	Started                  func(pane, name, kind string)
}

// startTaskAgent prompts a free agent already in dir, else starts one in a pane opened beside a pane of dir, else
// in a new workspace; panes without an agent are never taken over, since tkt cannot tell what runs in them. It
// returns the pane the task went to.
func (a *App) startTaskAgent(ctx context.Context, h HerdrAPI, req agentRequest) (string, error) {
	kind, dir, label, prompt, fresh := req.Kind, req.Dir, req.Label, req.Prompt, req.Fresh
	same := func(p string) bool { return p != "" && filepath.Clean(p) == filepath.Clean(dir) }
	agents, err := h.Agents(ctx)
	if err != nil {
		return "", err
	}
	for _, ag := range agents {
		if !fresh && ag.Agent != "" && same(ag.Cwd) && (ag.Status == "idle" || ag.Status == "done") {
			return ag.PaneID, h.Prompt(ctx, ag.PaneID, prompt)
		}
	}
	if _, ok := unattendedOf(ctx); kind == "" && !ok {
		kind, err = a.defaultAgent(ctx)
	} else {
		kind, err = a.pickAgent(ctx, kind)
	}
	if err != nil {
		return "", err
	}
	settle(ctx)
	pane, split := "", false
	if panes, err := h.Panes(ctx); err == nil {
		if i := slices.IndexFunc(panes, func(p herdrport.Pane) bool { return same(p.Cwd) }); i >= 0 {
			if s, err := h.SplitPane(ctx, panes[i].ID, dir); err == nil {
				pane, split = s.ID, true
			}
		}
	}
	if pane == "" {
		_, root, err := h.CreateWorkspace(ctx, dir, label, false)
		if err != nil {
			return "", err
		}
		pane = root.ID
	}
	name, agent, err := a.startAgent(ctx, h, pane, kind)
	if notReady(err) {
		err = h.WaitAgent(ctx, name, "idle", 4*time.Minute)
	}
	if err != nil {
		return "", err
	}
	if split {
		dropPlaceholder(ctx, h, pane, agent)
	}
	if req.Started != nil {
		req.Started(agent, name, kind)
	}
	return agent, a.promptWhenNamed(ctx, h, name, prompt)
}

// reviewPrompt asks an agent to review a pull request with its own tools and to keep its findings to itself: what
// reaches GitHub is up to the user.
func reviewPrompt(pr ticket.PullRequest) string {
	who := ""
	if pr.Author != "" {
		who = " by @" + pr.Author
	}
	title := ""
	if pr.Title != "" {
		title = fmt.Sprintf(" %q%s", pr.Title, who)
	} else if who != "" {
		title = who
	}
	return fmt.Sprintf("Review pull request %s (%s#%d%s).\n\n"+
		"Review it with the /review-6-lens skill; without it, with a review tool you have, or your own tools (for example `gh pr view` and `gh pr diff`).\n"+
		"Then report your findings here, most important first: what you would change and why, with the file and line.\n"+
		"Do not comment on, approve or request changes on the pull request in GitHub: the user decides what to post.",
		pr.URL, pr.Repo, pr.Number, title)
}
