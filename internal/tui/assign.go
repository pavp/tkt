package tui

import (
	"cmp"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/pavp/tkt/internal/ticket"
)

// Assignee is who a ticket is given to: the user (Me), someone by account ID, or nobody when both are empty.
type Assignee struct {
	ID   string
	Name string
	Me   bool
}

func (a Assignee) words() string {
	switch {
	case a.Me:
		return "you"
	case a.ID == "":
		return "nobody"
	}
	return a.Name
}

// AssignedMsg reports giving Key to Who.
type AssignedMsg struct {
	Key ticket.Key
	Who Assignee
	Err error
}

// assignItem opens who the ticket can be given to; teammates' tickets too, so one can be taken.
func (m Model) assignItem(k ticket.Key) menuItem {
	return menuItem{label: "Assign to… ▸", sub: func() *menu { return m.assignMenu(k) }}
}

func (m Model) assignMenu(k ticket.Key) *menu {
	var now string
	if t, ok := m.snap.Find(k); ok {
		now = t.Assignee
	}
	items := []menuItem{{label: "Me", sub: func() *menu { return m.confirmAssign(k, Assignee{Me: true}) }}}
	if now != "" {
		items = append(items, menuItem{label: "Unassign", sub: func() *menu { return m.confirmAssign(k, Assignee{}) }})
	}
	items = append(items, menuItem{label: "Search someone…", prompt: &formPrompt{assign: k}})
	return &menu{title: "Assign " + string(k) + " · now " + cmp.Or(now, "unassigned"), items: items}
}

// confirmAssign asks before changing the assignee, since Jira is changed for everyone.
func (m Model) confirmAssign(k ticket.Key, who Assignee) *menu {
	question, yes := "Assign "+string(k)+" to "+who.words()+"?", "Yes, assign it"
	if who.ID == "" && !who.Me {
		question, yes = "Leave "+string(k)+" unassigned?", "Yes, unassign it"
	}
	return &menu{title: question, items: []menuItem{
		{label: yes, run: func() tea.Cmd { return m.actions.Assign(k, who) }},
		{label: "Cancel", run: func() tea.Cmd { return nil }},
	}}
}

func (m Model) assigned(msg AssignedMsg) (tea.Model, tea.Cmd) {
	if msg.Err != nil {
		m.status = "could not assign " + string(msg.Key) + " to " + msg.Who.words() + ": " + msg.Err.Error()
		return m, nil
	}
	m.status = string(msg.Key) + " assigned to " + msg.Who.words()
	if msg.Who.ID == "" && !msg.Who.Me {
		m.status = string(msg.Key) + " is now unassigned"
	}
	loading := m.beginLoading()
	return m, tea.Batch(m.actions.Refresh(), loading)
}
