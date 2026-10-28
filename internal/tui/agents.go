package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/pavp/tkt/internal/ticket"
)

// AgentTask is work the board hands to an agent: reviewing a review request, or resolving a ticket's feedback.
type AgentTask struct {
	Review *ticket.PullRequest
	Key    ticket.Key
	// Known is the board's copy of the ticket, whose pull requests need no fetching.
	Known *ticket.Ticket
	// Kind is the agent to open for it when a new one opens, empty for the last one chosen.
	Kind string
}

// AgentKindsMsg delivers the agents installed that Herdr can start, with the last one chosen, or why they are unknown.
type AgentKindsMsg struct {
	Kinds []string
	Last  string
	Err   error
}

func (t AgentTask) String() string {
	if t.Review != nil {
		return fmt.Sprintf("review of #%d", t.Review.Number)
	}
	return string(t.Key) + "'s feedback"
}

// Phrase names the task in a sentence, "the review of #820" or "DEMO-1's feedback".
func (t AgentTask) Phrase() string {
	if t.Review != nil {
		return "the " + t.String()
	}
	return t.String()
}

// OpenAgent is an agent running in Herdr, as the "an open agent" menus list it.
type OpenAgent struct {
	Name, Pane, Status, Folder string
	// Workspace is the label of the Herdr workspace the agent runs in.
	Workspace string
	// Repo ("owner/name") and Branch say what the agent's checkout holds, Ticket the key in that branch; all
	// empty outside a git checkout.
	Repo, Branch string
	Ticket       ticket.Key
	// SameRepo marks an agent working in a checkout of the task's repository.
	SameRepo bool
}

// CloneNeededMsg says a fix needs a clone of Repo, which the folder Into would hold, before an agent can take it.
type CloneNeededMsg struct {
	Task       AgentTask
	Repo, Into string
}

// cloneMenu asks before cloning, as the clone lands on the user's disk.
func (m Model) cloneMenu(msg CloneNeededMsg) *menu {
	return &menu{title: "Clone " + msg.Repo + "?", subtitle: "into " + msg.Into + " · then an agent takes " + msg.Task.Phrase(), items: []menuItem{
		{label: "Yes, clone it and start", run: func() tea.Cmd { return m.actions.CloneAndStart(msg.Task, msg.Repo) }},
		{label: "Cancel", run: func() tea.Cmd { return nil }},
	}}
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

// agentsMenu offers the open agents in the order they arrive, the free ones first, each named with its repository,
// ticket and status over its Herdr workspace, folder and branch; a working agent can still take the task, which waits in its queue until it
// finishes.
func (m Model) agentsMenu(task AgentTask, agents []OpenAgent) *menu {
	var items []menuItem
	for _, ag := range agents {
		parts := []string{ag.Name, filepath.Base(ag.Folder)}
		if _, name, ok := strings.Cut(ag.Repo, "/"); ok {
			parts[1] = name
		}
		if ag.Ticket != "" {
			parts = append(parts, string(ag.Ticket))
		}
		status := ag.Status
		if status == "working" {
			status = "working, it queues"
		}
		where := strings.TrimSpace(ag.Workspace + "  " + homeShort(ag.Folder))
		note := strings.TrimSpace(where + "\n" + ag.Branch)
		items = append(items, menuItem{label: strings.Join(append(parts, status), " · "), note: note, run: func() tea.Cmd { return m.actions.SendTask(task, ag.Pane) }})
	}
	return &menu{title: "Send " + task.Phrase() + " to", subtitle: "an agent open in Herdr", items: items}
}

// homeShort writes a folder under the user's home as "~/…".
func homeShort(dir string) string {
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		if rest, ok := strings.CutPrefix(dir, home); ok && (rest == "" || strings.HasPrefix(rest, "/")) {
			return "~" + rest
		}
	}
	return dir
}

// agentChoice asks which agent takes a task: a new one, placed where its work belongs, or one already open.
func (m Model) agentChoice(task AgentTask) *menu {
	newAgent := menuItem{label: "A new agent", run: func() tea.Cmd { return m.actions.NewAgentTask(task) }}
	if len(m.agentKinds) > 1 {
		newAgent = menuItem{label: "A new agent ▸", sub: func() *menu { return m.kindChoice(task) }}
	}
	return &menu{title: "Send " + task.Phrase() + " to", items: []menuItem{
		newAgent,
		{label: "An open agent ▸", run: func() tea.Cmd { return m.actions.OpenAgents(task) }},
	}}
}

// kindChoice asks which of the installed agents a new agent is, the last one chosen first.
func (m Model) kindChoice(task AgentTask) *menu {
	kinds := slices.Clone(m.agentKinds)
	if i := slices.Index(kinds, m.lastAgent); i > 0 {
		kinds = slices.Insert(slices.Delete(kinds, i, i+1), 0, m.lastAgent)
	}
	var items []menuItem
	for _, k := range kinds {
		note := ""
		if k == m.lastAgent {
			note = "last used"
		}
		items = append(items, menuItem{label: k, note: note, run: func() tea.Cmd {
			t := task
			t.Kind = k
			return m.actions.NewAgentTask(t)
		}})
	}
	return &menu{title: "Send " + task.Phrase() + " to", subtitle: "a new agent", items: items}
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
			{label: "Send its feedback to another agent ▸", sub: func() *menu { return m.agentChoice(task) }},
		}
	}
	return []menuItem{{label: anyAgentLabel(r), key: "f", sub: func() *menu { return m.agentChoice(task) }}}
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

// reviewItem asks an agent to review a pull request, anyone's; outside Herdr its prompt is copied instead.
func (m Model) reviewItem(pr ticket.PullRequest) menuItem {
	task := AgentTask{Review: &pr}
	if !m.actions.InHerdr() {
		return menuItem{label: "Copy the review prompt", run: func() tea.Cmd { return m.actions.CopyTaskPrompt(task) }}
	}
	return menuItem{label: "Review with ▸", sub: func() *menu { return m.agentChoice(task) }}
}
