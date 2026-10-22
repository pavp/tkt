package tui

import (
	"cmp"
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

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

// movesFresh is how long the statuses asked when a menu opened stay good for Move to….
const movesFresh = time.Minute

type knownMoves struct {
	trs []Transition
	at  time.Time
}

// movesFetchedMsg carries the statuses asked ahead, when a ticket's menu opened.
type movesFetchedMsg TransitionsMsg

// prefetchMoves asks Jira where the item's ticket can move while the user reads its menu.
func (m Model) prefetchMoves(it item) tea.Cmd {
	if it.review() || it.ticket == nil || it.ticket.Team {
		return nil
	}
	k := it.ticket.Key
	if mv, ok := m.moves[k]; ok && m.now().Sub(mv.at) < movesFresh {
		return nil
	}
	ask := m.actions.Transitions(k)
	return func() tea.Msg {
		msg, _ := ask().(TransitionsMsg)
		return movesFetchedMsg(msg)
	}
}

func (m Model) movesFetched(msg movesFetchedMsg) (tea.Model, tea.Cmd) {
	if m.menu != nil && m.menu.loading == msg.Key {
		return m.transitions(TransitionsMsg(msg))
	}
	if msg.Err == nil && msg.Key != "" {
		m.moves[msg.Key] = knownMoves{msg.Transitions, m.now()}
	}
	return m, nil
}

// openMoves shows where k can move: at once when its statuses are known, otherwise a menu saying they
// are loading, which they replace when Jira answers.
func (m Model) openMoves(k ticket.Key, parent *menu) (tea.Model, tea.Cmd) {
	if mv, ok := m.moves[k]; ok && m.now().Sub(mv.at) < movesFresh {
		return m.showMoves(k, mv.trs, parent)
	}
	m.menu = &menu{title: "Move " + string(k) + " to", items: []menuItem{{label: dim.Render("loading the statuses Jira allows…")}}, back: parent, loading: k}
	return m, m.actions.Transitions(k)
}

func (m Model) transitions(msg TransitionsMsg) (tea.Model, tea.Cmd) {
	waiting := m.menu != nil && m.menu.loading == msg.Key
	var back *menu
	if waiting {
		back = m.menu.back
	}
	if msg.Err == nil {
		m.moves[msg.Key] = knownMoves{msg.Transitions, m.now()}
	}
	// Only the menu still waiting opens: one the user closed, or a second answer, stays closed.
	if !waiting {
		return m, nil
	}
	if msg.Err != nil {
		m.status = "could not load the statuses of " + string(msg.Key) + ": " + msg.Err.Error()
		m.menu = back
		return m, nil
	}
	return m.showMoves(msg.Key, msg.Transitions, back)
}

func (m Model) showMoves(k ticket.Key, trs []Transition, back *menu) (tea.Model, tea.Cmd) {
	if mn := m.moveMenu(k, trs); len(mn.items) > 0 {
		m.status = ""
		mn.back = back
		m.menu = mn
	} else {
		m.status = "Jira offers no status change for " + string(k)
		m.menu = back
	}
	return m, nil
}

// startFix sends a ticket's feedback to its agent from the board's copy of the ticket, saying so meanwhile.
func (m Model) startFix(k ticket.Key) (tea.Model, tea.Cmd) {
	var known *ticket.Ticket
	if t, ok := m.snap.Find(k); ok {
		known = &t
	}
	m.status = string(k) + " · sending its feedback to the agent…"
	return m, m.actions.Fix(k, known)
}

func (m Model) transitioned(msg TransitionedMsg) (tea.Model, tea.Cmd) {
	if msg.Err != nil {
		m.status = "could not move " + string(msg.Key) + " to " + msg.To + ": " + msg.Err.Error()
		return m, nil
	}
	m.status = string(msg.Key) + " moved to " + msg.To
	delete(m.moves, msg.Key)
	loading := m.beginLoading()
	return m, tea.Batch(m.actions.Refresh(), loading)
}

// User is someone a ticket can be assigned to.
type User struct {
	ID   string
	Name string
}

// UsersListedMsg carries the users assignable to Key that match Query.
type UsersListedMsg struct {
	Key   ticket.Key
	Query string
	Users []User
	Err   error
}

// formKinds are the kinds of field the board's form fills.
var formKinds = []string{KindOption, KindUser}

// moveForm is a transition's form being filled; chosen maps a field's ID to its option or user.
type moveForm struct {
	key     ticket.Key
	tr      Transition
	fields  []Field
	chosen  map[string]Option
	comment string
}

// formPrompt is the text box a form row opens: the comment, or a user field's search, whose matches list
// below it.
type formPrompt struct {
	form  moveForm
	field *Field
	text  string
	users []User
	sel   int
	// assign, when set, searches someone to give that ticket to instead of filling a form.
	assign ticket.Key
}

// key is the ticket the prompt belongs to.
func (p *formPrompt) key() ticket.Key { return cmp.Or(p.assign, p.form.key) }

// searching reports whether the prompt looks users up as the user types.
func (p *formPrompt) searching() bool { return p.field != nil || p.assign != "" }

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
		if field.Kind == KindUser {
			items = append(items, menuItem{label: name + ": " + value, prompt: &formPrompt{form: f, field: &field}})
			continue
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
	items = append(items, menuItem{label: "Comment: " + cmp.Or(f.comment, "—"), prompt: &formPrompt{form: f, text: f.comment}})
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
		return m.actions.Transition(f.key, f.tr.ID, f.tr.To, values, f.comment)
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
				{label: "Yes, move it", run: func() tea.Cmd { return a.Transition(k, tr.ID, tr.To, nil, "") }},
				{label: "Cancel", run: func() tea.Cmd { return nil }},
			}}
		}})
	}
	return &menu{title: "Move " + string(k) + " to", items: items}
}

func (m Model) promptKey(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	p := m.prompt
	switch k.Type {
	case tea.KeyCtrlC:
		return m, tea.Quit
	case tea.KeyEsc:
		if p.assign != "" {
			m.prompt, m.menu = nil, m.assignMenu(p.assign)
			return m, nil
		}
		m.prompt, m.menu = nil, m.formMenu(p.form)
	case tea.KeyEnter:
		if p.assign != "" {
			if len(p.users) == 0 {
				m.status = "type part of a name to find who to assign " + string(p.assign) + " to"
				return m, nil
			}
			u := p.users[p.sel]
			m.prompt, m.menu, m.status = nil, m.confirmAssign(p.assign, Assignee{ID: u.ID, Name: u.Name}), ""
			return m, nil
		}
		f := p.form
		switch {
		case p.field == nil:
			f.comment = strings.TrimSpace(p.text)
		case len(p.users) > 0:
			u := p.users[p.sel]
			f = f.with(p.field.ID, &Option{ID: u.ID, Value: u.Name})
		default:
			m.status = "type part of a name to find " + p.field.Name
			return m, nil
		}
		m.prompt, m.menu, m.status = nil, m.formMenu(f), ""
	case tea.KeyUp:
		p.sel = max(0, p.sel-1)
	case tea.KeyDown:
		p.sel = min(max(0, len(p.users)-1), p.sel+1)
	case tea.KeyBackspace:
		if r := []rune(p.text); len(r) > 0 {
			p.text = string(r[:len(r)-1])
			return m, m.searchUsers()
		}
	case tea.KeyRunes, tea.KeySpace:
		p.text += string(k.Runes)
		if k.Type == tea.KeySpace {
			p.text += " "
		}
		return m, m.searchUsers()
	}
	return m, nil
}

// searchUsers asks for the users matching a user field's text once it has two letters.
func (m Model) searchUsers() tea.Cmd {
	p := m.prompt
	if !p.searching() {
		return nil
	}
	if len([]rune(strings.TrimSpace(p.text))) < 2 {
		p.users, p.sel = nil, 0
		return nil
	}
	return m.actions.Users(p.key(), p.text)
}

func (m Model) usersListed(msg UsersListedMsg) (tea.Model, tea.Cmd) {
	p := m.prompt
	if p == nil || !p.searching() || p.key() != msg.Key || p.text != msg.Query {
		return m, nil
	}
	if msg.Err != nil {
		m.status = "could not search users: " + msg.Err.Error()
		return m, nil
	}
	p.users, p.sel = msg.Users, 0
	return m, nil
}

func (m Model) promptView() string {
	p := m.prompt
	width := 56
	if m.width > 0 {
		width = min(width, m.width-2)
	}
	inner := width - 6
	title, hint := "Comment", "enter keep · esc back"
	switch {
	case p.assign != "":
		title, hint = "Assign "+string(p.assign)+" to", "type to search · ↑↓ choose · enter pick · esc back"
	case p.field != nil:
		title, hint = p.field.Name, "type to search · ↑↓ choose · enter pick · esc back"
	}
	text := []rune(p.text)
	if len(text) > inner-3 {
		text = text[len(text)-(inner-3):]
	}
	lines := []string{bold.Render(ansi.Truncate(title, inner, "…")), "", "> " + string(text) + "█"}
	if p.searching() {
		lines = append(lines, "")
		if len(p.users) == 0 {
			lines = append(lines, dim.Render("no matches yet"))
		}
		const rows = 8
		start := max(0, p.sel-rows+1)
		end := min(len(p.users), start+rows)
		more := func(n int, arrow string) string {
			if n == 0 {
				return ""
			}
			return dim.Render(fmt.Sprintf("%s %d more", arrow, n))
		}
		// Both lines stay, blank when nothing is hidden, so the box keeps its height while scrolling.
		if len(p.users) > rows {
			lines = append(lines, more(start, "↑"))
		}
		for i := start; i < end; i++ {
			name := ansi.Truncate(p.users[i].Name, inner-2, "…")
			if i == p.sel {
				lines = append(lines, selected.Render("▸ "+name))
			} else {
				lines = append(lines, "  "+name)
			}
		}
		if len(p.users) > rows {
			lines = append(lines, more(len(p.users)-end, "↓"))
		}
	}
	lines = append(lines, "", dim.Render(ansi.Truncate(hint, inner, "…")))
	return menuBox.Width(width - 2).Render(strings.Join(lines, "\n"))
}

func (m Model) promptPlaced(height int) string {
	return lipgloss.Place(m.width, max(height, lipgloss.Height(m.promptView())), lipgloss.Center, lipgloss.Center, m.promptView())
}
