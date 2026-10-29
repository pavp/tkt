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
	"github.com/pavp/tkt/internal/herdr"
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
		ctx, cancel := context.WithTimeout(b.ctx, 10*time.Second)
		defer cancel()
		agents, err := b.a.NewHerdr().Agents(ctx)
		if err != nil {
			return tui.AgentsListedMsg{Task: task, Err: err}
		}
		wanted, res := taskRepos(task), b.a.Resolver(b.cfg())
		labels := map[string]string{}
		if wss, err := b.a.NewHerdr().Workspaces(ctx); err == nil {
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
				if head, err := b.a.Run.Run(ctx, ag.Cwd, "git", []string{"rev-parse", "--abbrev-ref", "HEAD"}, nil); err == nil {
					agent.Branch = strings.TrimSpace(string(head))
					agent.Ticket, _ = ticket.KeyInBranch(agent.Branch, b.cfg().SpaceKeys())
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
		return tui.AgentsListedMsg{Task: task, Agents: out}
	}
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
		prompt, err := b.taskPrompt(task)
		if err != nil {
			return tui.StatusMsg(err.Error())
		}
		h := b.a.NewHerdr()
		if err := h.Prompt(b.ctx, pane, prompt); err != nil {
			return tui.StatusMsg("could not send the " + task.String() + ": " + err.Error())
		}
		if task.Review == nil {
			markAgent(b.ctx, h, pane, task.Key)
		}
		bringForward(b.ctx, h, pane)
		return b.handedOff("✓ sent " + task.Phrase() + " to the agent")
	}
}

func (b *boardActions) CopyTaskPrompt(task tui.AgentTask) tea.Cmd {
	return func() tea.Msg {
		prompt, err := b.taskPrompt(task)
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

func (b *boardActions) taskPrompt(task tui.AgentTask) (string, error) {
	if task.Review != nil {
		return reviewPrompt(*task.Review), nil
	}
	t, err := b.taskTicket(task)
	if err != nil {
		return "", err
	}
	return b.a.feedbackPrompt(b.ctx, b.cfg(), *t)
}

// taskTicket is the ticket whose feedback a task sends, the board's copy when it has one.
func (b *boardActions) taskTicket(task tui.AgentTask) (*ticket.Ticket, error) {
	if task.Known != nil {
		return task.Known, nil
	}
	d, err := b.a.loadDetail(b.ctx, b.cfg(), task.Key, nil)
	if err != nil {
		return nil, err
	}
	return &d.Ticket, nil
}

// NewAgentTask hands a task to an agent where its work belongs, opening only what is missing; the board then
// closes on the agent's pane.
func (b *boardActions) NewAgentTask(task tui.AgentTask) tea.Cmd {
	return func() tea.Msg {
		prompt, err := b.taskPrompt(task)
		if err != nil {
			return tui.StatusMsg(err.Error())
		}
		dir, note, label, err := b.taskPlace(task, "")
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
		prompt, err := b.taskPrompt(task)
		if err != nil {
			return tui.StatusMsg(err.Error())
		}
		dest, err := b.a.Resolver(b.cfg()).Clone(b.ctx, config.Repo{Name: repo})
		if err != nil {
			return tui.StatusMsg(err.Error())
		}
		dir, note, label, err := b.taskPlace(task, dest)
		if err != nil {
			return tui.StatusMsg(err.Error())
		}
		return b.startTask(task, prompt, dir, note, label)
	}
}

// noClone is a fix whose repository has no local clone yet.
type noClone struct{ repo string }

func (e noClone) Error() string { return e.repo + " is not cloned locally" }

// bringForward shows an agent's pane wherever it is: its workspace first, then the pane, which brings its own tab
// forward, as Start's "Go to the agent in Herdr" does.
func bringForward(ctx context.Context, h HerdrAPI, pane string) {
	if panes, err := h.Panes(ctx); err == nil {
		if i := slices.IndexFunc(panes, func(p herdr.Pane) bool { return p.ID == pane }); i >= 0 {
			_ = h.FocusWorkspace(ctx, panes[i].WorkspaceID)
		}
	}
	_ = h.FocusPane(ctx, pane)
}

// startTask hands the task to an agent in dir, the note added to its prompt, and closes the board on its pane.
func (b *boardActions) startTask(task tui.AgentTask, prompt, dir, note, label string) tea.Msg {
	if note != "" {
		prompt += "\n\n" + note
	}
	h := b.a.NewHerdr()
	pane, err := b.a.startTaskAgent(b.ctx, h, task.Kind, dir, label, prompt)
	if err != nil {
		return tui.StatusMsg("could not start an agent for the " + task.String() + ": " + err.Error())
	}
	if task.Review == nil {
		markAgent(b.ctx, h, pane, task.Key)
	}
	bringForward(b.ctx, h, pane)
	return b.handedOff("✓ an agent took " + task.Phrase())
}

// taskPlace is the folder a new agent works on a task in, and what it is told about it. A review runs in the
// repository's clone and the agent picks its own tools. A fix runs in the ticket's worktree, else in a checkout
// already on its pull request's branch, else in the clone: checking the branch out there when the clone is free,
// creating a worktree for it otherwise, since tkt never creates worktrees itself. A review needs no clone: without
// one it runs in the data directory's reviews folder, the same every time; a fix without one returns noClone. clone,
// when set, is the clone just made for the fix.
func (b *boardActions) taskPlace(task tui.AgentTask, clone string) (dir, note, label string, err error) {
	if task.Review != nil {
		_, name, _ := strings.Cut(task.Review.Repo, "/")
		label = fmt.Sprintf("review · %s#%d", name, task.Review.Number)
		dir, err = b.cloneOf(task.Review.Repo)
		if errors.Is(err, repos.ErrNotFound) {
			dir = filepath.Join(b.a.Env.DataDir(), "reviews")
			note = "There is no local clone of " + task.Review.Repo + ": read the pull request with gh from its URL, and clone it into a temporary folder only if you need to run it."
			err = os.MkdirAll(dir, 0o700)
		}
		return dir, note, label, err
	}
	t, err := b.taskTicket(task)
	if err != nil {
		return "", "", "", err
	}
	label = string(t.Key)
	all, err := b.a.allWork(b.ctx, b.cfg())
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
		if dir, err = b.cloneOf(pr.Repo); errors.Is(err, repos.ErrNotFound) {
			return "", "", "", noClone{pr.Repo}
		} else if err != nil {
			return "", "", "", err
		}
	}
	note = fmt.Sprintf("Work in this checkout: check out its branch %s first.", pr.Branch)
	if !b.a.checkoutFree(b.ctx, dir) {
		note = fmt.Sprintf("This checkout has work in progress: create a worktree for its branch %s following the project's rules, work there, and register it with `tkt link`.", pr.Branch)
	}
	return dir, note, label, nil
}

// cloneOf is the local clone of a repository ("owner/name"): its configured path, else the first one found.
func (b *boardActions) cloneOf(repo string) (string, error) {
	c := b.cfg()
	r := config.Repo{Name: repo}
	if i := slices.IndexFunc(c.Repos(), func(x config.Repo) bool { return x.Name == repo }); i >= 0 {
		r = c.Repos()[i]
	}
	return b.a.Resolver(c).Resolve(b.ctx, r)
}

// startTaskAgent prompts a free agent already in dir, else starts one in a pane opened beside a pane of dir, else
// in a new workspace; panes without an agent are never taken over, since tkt cannot tell what runs in them. It
// returns the pane the task went to.
func (a *App) startTaskAgent(ctx context.Context, h HerdrAPI, kind, dir, label, prompt string) (string, error) {
	same := func(p string) bool { return p != "" && filepath.Clean(p) == filepath.Clean(dir) }
	agents, err := h.Agents(ctx)
	if err != nil {
		return "", err
	}
	for _, ag := range agents {
		if ag.Agent != "" && same(ag.Cwd) && (ag.Status == "idle" || ag.Status == "done") {
			return ag.PaneID, h.Prompt(ctx, ag.PaneID, prompt)
		}
	}
	if kind == "" {
		kind, err = a.defaultAgent(ctx)
	} else {
		kind, err = a.pickAgent(ctx, kind)
	}
	if err != nil {
		return "", err
	}
	pane := ""
	if panes, err := h.Panes(ctx); err == nil {
		if i := slices.IndexFunc(panes, func(p herdr.Pane) bool { return same(p.Cwd) }); i >= 0 {
			if split, err := h.SplitPane(ctx, panes[i].ID, dir); err == nil {
				pane = split.ID
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
	name, err := a.startAgent(ctx, h, pane, kind)
	if notReady(err) {
		err = h.WaitAgent(ctx, name, "idle", 4*time.Minute)
	}
	if err != nil {
		return "", err
	}
	return pane, a.promptWhenNamed(ctx, h, name, prompt)
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
		"Use your own tools to read it (for example `gh pr view` and `gh pr diff`); check it out or run it only if you need to.\n"+
		"Then report your findings here, most important first: what you would change and why, with the file and line.\n"+
		"Do not comment on, approve or request changes on the pull request in GitHub: the user decides what to post.",
		pr.URL, pr.Repo, pr.Number, title)
}
