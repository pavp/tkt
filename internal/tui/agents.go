package tui

import (
	"fmt"
	"path/filepath"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/pavp/tkt/internal/ticket"
)

// AgentTask is work the board hands to an agent: reviewing a review request, or resolving a ticket's feedback.
type AgentTask struct {
	Review *ticket.PullRequest
	Key    ticket.Key
	// Known is the board's copy of the ticket, whose pull requests need no fetching.
	Known *ticket.Ticket
}

func (t AgentTask) String() string {
	if t.Review != nil {
		return fmt.Sprintf("review of #%d", t.Review.Number)
	}
	return string(t.Key) + "'s feedback"
}

// OpenAgent is an agent running in Herdr, as the "an open agent" menus list it.
type OpenAgent struct {
	Name, Pane, Status, Folder string
	// SameRepo marks an agent working in a checkout of the task's repository.
	SameRepo bool
}

// AgentsListedMsg delivers the open agents a task can go to, or why they could not be listed.
type AgentsListedMsg struct {
	Task   AgentTask
	Agents []OpenAgent
	Err    error
}

func (m Model) agentsListed(msg AgentsListedMsg) (tea.Model, tea.Cmd) {
	switch {
	case msg.Err != nil:
		m.status = "could not list the agents: " + msg.Err.Error()
	case len(msg.Agents) == 0:
		m.status = "no agent is open in Herdr"
	default:
		m.menu = m.agentsMenu(msg.Task, msg.Agents)
	}
	return m, nil
}

// agentsMenu offers the open agents in the order they arrive, the free ones first; a working agent can still
// take the task, which waits in its queue until it finishes.
func (m Model) agentsMenu(task AgentTask, agents []OpenAgent) *menu {
	var items []menuItem
	for _, ag := range agents {
		status := ag.Status
		if status == "working" {
			status = "working, it queues"
		}
		items = append(items, menuItem{label: ag.Name + " · " + filepath.Base(ag.Folder) + " · " + status, run: func() tea.Cmd { return m.actions.SendTask(task, ag.Pane) }})
	}
	return &menu{title: "Send the " + task.String() + " to", subtitle: "an agent open in Herdr", items: items}
}

// listAgents asks for the open agents a task can go to; they arrive as an AgentsListedMsg.
func (m *Model) listAgents(task AgentTask) tea.Cmd {
	m.status = "listing the agents open in Herdr…"
	return m.actions.OpenAgents(task)
}

// feedbackItems send a ticket's feedback: to its own agent when it has one, with another agent a step away;
// to an agent picked from those open when it has none; outside Herdr the prompt is copied instead.
func (m Model) feedbackItems(t ticket.Ticket, r ticket.Reason) []menuItem {
	task := AgentTask{Key: t.Key, Known: &t}
	switch {
	case !m.actions.InHerdr():
		return []menuItem{{label: "Copy the feedback prompt", key: "f", run: func() tea.Cmd { return m.actions.CopyTaskPrompt(task) }}}
	case m.snap.Agents[string(t.Key)] != "":
		return []menuItem{
			{label: fixLabel(r), key: "f", fix: t.Key},
			{label: "Send its feedback to another agent ▸", run: func() tea.Cmd { return m.actions.OpenAgents(task) }},
		}
	}
	return []menuItem{{label: anyAgentLabel(r), key: "f", run: func() tea.Cmd { return m.actions.OpenAgents(task) }}}
}

// anyAgentLabel words the fix for a reason when an agent is still to be picked.
func anyAgentLabel(r ticket.Reason) string {
	switch r {
	case ticket.ReasonChangesRequested:
		return "Send the review comments to an agent ▸"
	case ticket.ReasonUnresolved:
		return "Send the unresolved comments to an agent ▸"
	case ticket.ReasonConflicts:
		return "Ask an agent to resolve the conflicts ▸"
	}
	return "Send the CI failure to an agent ▸"
}

// reviewItem asks an agent to review a review request; outside Herdr its prompt is copied instead.
func (m Model) reviewItem(pr ticket.PullRequest) menuItem {
	task := AgentTask{Review: &pr}
	if !m.actions.InHerdr() {
		return menuItem{label: "Copy the review prompt", run: func() tea.Cmd { return m.actions.CopyTaskPrompt(task) }}
	}
	return menuItem{label: "Review with an open agent ▸", run: func() tea.Cmd { return m.actions.OpenAgents(task) }}
}
