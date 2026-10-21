package tui

import (
	"slices"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/pavp/tkt/internal/ticket"
)

// Transition is a status a ticket can move to from its current one. NeedsForm marks one whose Jira form
// has a required field, which the board leaves to Jira.
type Transition struct {
	ID        string
	To        string
	NeedsForm bool
}

// TransitionsMsg carries the statuses Key can move to.
type TransitionsMsg struct {
	Key         ticket.Key
	Transitions []Transition
	Err         error
}

// TransitionedMsg reports a move of Key to status To.
type TransitionedMsg struct {
	Key ticket.Key
	To  string
	Err error
}

func (m Model) transitions(msg TransitionsMsg) (tea.Model, tea.Cmd) {
	if msg.Err != nil {
		m.status = "could not load the statuses of " + string(msg.Key) + ": " + msg.Err.Error()
		return m, nil
	}
	if mn := m.moveMenu(msg.Key, msg.Transitions); len(mn.items) > 0 {
		m.status = ""
		m.menu = mn
	} else {
		m.status = "Jira offers no status change for " + string(msg.Key)
	}
	return m, nil
}

func (m Model) transitioned(msg TransitionedMsg) (tea.Model, tea.Cmd) {
	if msg.Err != nil {
		m.status = "could not move " + string(msg.Key) + " to " + msg.To + ": " + msg.Err.Error()
		return m, nil
	}
	m.status = string(msg.Key) + " moved to " + msg.To
	loading := m.beginLoading()
	return m, tea.Batch(m.actions.Refresh(), loading)
}

// moveMenu lists each destination once; one behind a required form opens the ticket in Jira instead, and
// any other asks before moving, since Jira is changed for everyone.
func (m Model) moveMenu(k ticket.Key, trs []Transition) *menu {
	a := m.actions
	var url string
	if i := slices.IndexFunc(m.snap.Tickets, func(t ticket.Ticket) bool { return t.Key == k }); i >= 0 {
		url = m.snap.Tickets[i].URL
	}
	var items []menuItem
	var seen []string
	for _, tr := range trs {
		if slices.Contains(seen, tr.To) {
			continue
		}
		seen = append(seen, tr.To)
		if tr.NeedsForm {
			if url != "" {
				items = append(items, menuItem{label: tr.To + " · in Jira ↗", link: url, run: func() tea.Cmd { return a.Open(url) }})
			}
			continue
		}
		items = append(items, menuItem{label: tr.To, sub: func() *menu {
			return &menu{title: "Move " + string(k) + " to " + tr.To + "?", items: []menuItem{
				{label: "Yes, move it", run: func() tea.Cmd { return a.Transition(k, tr.ID, tr.To) }},
				{label: "Cancel", run: func() tea.Cmd { return nil }},
			}}
		}})
	}
	return &menu{title: "Move " + string(k) + " to", items: items}
}
