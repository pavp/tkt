package cli

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"slices"

	"github.com/pavp/tkt/internal/config"
	"github.com/pavp/tkt/internal/daemon"
	"github.com/pavp/tkt/internal/herdrport"
	"github.com/pavp/tkt/internal/tui"
	"github.com/pavp/tkt/internal/workspace"
)

func agentTask(task daemon.Task, kind string) tui.AgentTask {
	return tui.AgentTask{Key: task.Key, Known: task.Known, Review: task.Review, Kind: kind}
}

// daemonTaskAgents serves GET /v1/tasks/agents: the open agents the board's "An open agent" menu offers for a task,
// with the kinds its "A new agent" menu offers.
func (a *App) daemonTaskAgents(ctx context.Context, task daemon.Task, herdr string) (daemon.TaskAgents, error) {
	ctx, err := a.unattendedIn(ctx, herdr, nil)
	if err != nil {
		return daemon.TaskAgents{}, err
	}
	c, err := a.LoadConfig()
	if err != nil {
		return daemon.TaskAgents{}, err
	}
	t := agentTask(task, "")
	agents, err := a.openAgents(ctx, c, t)
	if err != nil {
		return daemon.TaskAgents{}, err
	}
	out := daemon.TaskAgents{Phrase: t.Phrase()}
	for _, ag := range agents {
		out.Agents = append(out.Agents, daemon.OpenAgent{Name: ag.Name, Pane: ag.Pane, Status: ag.Status, Folder: ag.Folder, Workspace: ag.Workspace,
			Repo: ag.Repo, Branch: ag.Branch, Ticket: ag.Ticket, SameRepo: ag.SameRepo})
	}
	kinds, last, err := a.agentChoices(ctx)
	out.Kinds, out.LastAgent = kinds, last
	if err != nil {
		out.KindsError = err.Error()
	}
	return out, nil
}

// daemonTask serves POST /v1/tasks with the board's actions that hand a ticket's feedback or a review to an agent:
// its own agent (f), an open agent, or a new one where the work belongs, cloning first only when asked to.
func (a *App) daemonTask(ctx context.Context, task daemon.Task, req daemon.TaskRequest, settled func()) error {
	ctx, err := a.unattendedIn(ctx, req.Herdr, settled)
	if err != nil {
		return err
	}
	c, err := a.LoadConfig()
	if err != nil {
		return err
	}
	t := agentTask(task, req.Agent)
	switch req.To {
	case "own":
		_, pane, err := a.deliverFeedback(ctx, c, task.Key, task.Known, false, nil)
		if err != nil {
			return err
		}
		bringForward(ctx, a.startHerdr(ctx), pane)
		return nil
	case "open":
		if err := a.openAgentAt(ctx, req.Pane); err != nil {
			return err
		}
		return a.sendTask(ctx, c, t, req.Pane)
	}
	err = a.newAgentTask(ctx, c, t, req.Clone)
	if need := (workspace.NeedsAnswer{}); errors.As(err, &need) {
		return daemon.NeedsAnswerError{Need: need.Need}
	}
	return err
}

// openAgentAt refuses a pane where no agent runs, since the prompt would be typed into whatever runs there.
func (a *App) openAgentAt(ctx context.Context, pane string) error {
	agents, err := a.startHerdr(ctx).Agents(ctx)
	if err != nil {
		return err
	}
	if !slices.ContainsFunc(agents, func(ag herdrport.Agent) bool { return ag.PaneID == pane && ag.Agent != "" }) {
		return fmt.Errorf("%q is not an agent open in Herdr", pane)
	}
	return nil
}

// newAgentTask is the board's "A new agent", with its clone question answered by clone, asking every question before
// anything clones or opens.
func (a *App) newAgentTask(ctx context.Context, c *config.Config, t tui.AgentTask, clone *bool) error {
	if t.Kind != "" {
		if _, err := a.chooseAgent(ctx, t.Kind); err != nil {
			return err
		}
	}
	prompt, err := a.taskPrompt(ctx, c, t)
	if err != nil {
		return err
	}
	dir, note, label, err := a.taskPlace(ctx, c, t, "")
	var missing noClone
	if errors.As(err, &missing) {
		switch {
		case len(c.RepoRoots) == 0:
			return errors.New(missing.repo + " is not cloned locally: set repo_roots in the config so tkt knows where to clone it")
		case !cloneable.MatchString(missing.repo):
			return fmt.Errorf("%q is not a repository tkt clones", missing.repo)
		case clone == nil:
			return daemon.NeedsAnswerError{Need: workspace.NeedClone, Repo: missing.repo, Into: c.RepoRoots[0]}
		case !*clone:
			return missing
		}
		if t.Kind == "" {
			if _, err := a.chooseAgent(ctx, ""); err != nil {
				return err
			}
		}
		settle(ctx)
		dir, note, label, err = a.cloneTaskPlace(ctx, c, t, missing.repo)
	}
	if err != nil {
		return err
	}
	return a.startTask(ctx, t, prompt, dir, note, label)
}

// cloneable is a GitHub "owner/name", whose name the clone's folder takes inside the first repository root.
var cloneable = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9-]*/[A-Za-z0-9_.-]*[A-Za-z0-9_-][A-Za-z0-9_.-]*$`)
