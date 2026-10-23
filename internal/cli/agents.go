package cli

import (
	"cmp"
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/pavp/tkt/internal/repos"
	"github.com/pavp/tkt/internal/ticket"
	"github.com/pavp/tkt/internal/tui"
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
		wanted := taskRepos(task)
		var out []tui.OpenAgent
		for _, ag := range agents {
			if ag.Agent == "" {
				continue
			}
			out = append(out, tui.OpenAgent{
				Name: cmp.Or(ag.Name, ag.Agent), Pane: ag.PaneID, Status: ag.Status, Folder: ag.Cwd,
				SameRepo: slices.Contains(wanted, b.a.originRepo(ctx, ag.Cwd)),
			})
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

// originRepo names the GitHub repository ("owner/name") a folder's origin points to, or "".
func (a *App) originRepo(ctx context.Context, dir string) string {
	if dir == "" {
		return ""
	}
	out, err := a.Run.Run(ctx, dir, "git", []string{"remote", "get-url", "origin"}, nil)
	if err != nil {
		return ""
	}
	owner, name, ok := repos.ParseRemote(strings.TrimSpace(string(out)))
	if !ok {
		return ""
	}
	return owner + "/" + name
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
		_ = h.FocusPane(b.ctx, pane)
		return tea.QuitMsg{}
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
	t := task.Known
	if t == nil {
		d, err := b.a.loadDetail(b.ctx, b.cfg(), task.Key, nil)
		if err != nil {
			return "", err
		}
		t = &d.Ticket
	}
	return b.a.feedbackPrompt(b.ctx, b.cfg(), *t)
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
