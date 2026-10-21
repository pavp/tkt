package tui

import (
	"maps"
	"slices"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/pavp/tkt/internal/ticket"
)

// Transition is a status a ticket can move to from its current one. NeedsForm marks one whose Jira form
// asks for what the board cannot fill, which it leaves to Jira; Fields are those it can.
type Transition struct {
	ID        string
	To        string
	NeedsForm bool
	Fields    []Field
}

// The kinds of field the board fills: one of a list's options, or a user.
const (
	KindOption = "option"
	KindUser   = "user"
)

type Field struct {
	ID       string
	Name     string
	Kind     string
	Required bool
	Options  []Option
}

type Option struct {
	ID    string
	Value string
}

// Value fills Field with an option's or a user's ID.
type Value struct {
	Field string
	Kind  string
	ID    string
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

// formKinds are the kinds of field the board's form fills.
var formKinds = []string{KindOption}

// moveForm is a transition's form being filled; chosen maps a field's ID to its option.
type moveForm struct {
	key    ticket.Key
	tr     Transition
	fields []Field
	chosen map[string]Option
}

// formMsg shows form, with status in the footer.
type formMsg struct {
	form   moveForm
	status string
}

// leftToJira reports whether the board must leave tr to Jira's own form, and which fields it fills.
func leftToJira(tr Transition) (bool, []Field) {
	var fields []Field
	for _, f := range tr.Fields {
		switch {
		case slices.Contains(formKinds, f.Kind):
			fields = append(fields, f)
		case f.Required:
			return true, nil
		}
	}
	return tr.NeedsForm, fields
}

func (f moveForm) with(field string, o *Option) moveForm {
	f.chosen = maps.Clone(f.chosen)
	if o == nil {
		delete(f.chosen, field)
	} else {
		f.chosen[field] = *o
	}
	return f
}

// formMenu shows the fields with their values; moving needs every required one, and is itself the
// confirmation, since the values to be written are on screen.
func (m Model) formMenu(f moveForm) *menu {
	reopen := func(f moveForm, status string) tea.Cmd {
		return func() tea.Msg { return formMsg{form: f, status: status} }
	}
	var items []menuItem
	for _, field := range f.fields {
		name := field.Name
		if field.Required {
			name += " *"
		}
		value := "—"
		if o, ok := f.chosen[field.ID]; ok {
			value = o.Value
		}
		items = append(items, menuItem{label: name + ": " + value, sub: func() *menu {
			var opts []menuItem
			if !field.Required {
				opts = append(opts, menuItem{label: "— none", run: func() tea.Cmd { return reopen(f.with(field.ID, nil), "") }})
			}
			for _, o := range field.Options {
				opts = append(opts, menuItem{label: o.Value, run: func() tea.Cmd { return reopen(f.with(field.ID, &o), "") }})
			}
			return &menu{title: field.Name, items: opts}
		}})
	}
	move := func() tea.Cmd {
		var values []Value
		for _, field := range f.fields {
			o, ok := f.chosen[field.ID]
			if !ok {
				if field.Required {
					return reopen(f, "choose "+field.Name+" first")
				}
				continue
			}
			values = append(values, Value{Field: field.ID, Kind: field.Kind, ID: o.ID})
		}
		return m.actions.Transition(f.key, f.tr.ID, f.tr.To, values)
	}
	items = append(items,
		menuItem{label: "Move " + string(f.key) + " to " + f.tr.To + " now", run: move},
		menuItem{label: "Cancel", run: func() tea.Cmd { return nil }},
	)
	return &menu{title: "Move " + string(f.key) + " to " + f.tr.To, items: items}
}

// moveMenu lists each destination once. One behind a form the board cannot fill opens the ticket in Jira;
// one with a form the board fills opens it; any other asks before moving, since Jira is changed for everyone.
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
		jira, fields := leftToJira(tr)
		if jira {
			if url != "" {
				items = append(items, menuItem{label: tr.To + " · in Jira ↗", link: url, run: func() tea.Cmd { return a.Open(url) }})
			}
			continue
		}
		if len(fields) > 0 {
			form := moveForm{key: k, tr: tr, fields: fields, chosen: map[string]Option{}}
			items = append(items, menuItem{label: tr.To, sub: func() *menu { return m.formMenu(form) }})
			continue
		}
		items = append(items, menuItem{label: tr.To, sub: func() *menu {
			return &menu{title: "Move " + string(k) + " to " + tr.To + "?", items: []menuItem{
				{label: "Yes, move it", run: func() tea.Cmd { return a.Transition(k, tr.ID, tr.To, nil) }},
				{label: "Cancel", run: func() tea.Cmd { return nil }},
			}}
		}})
	}
	return &menu{title: "Move " + string(k) + " to", items: items}
}
