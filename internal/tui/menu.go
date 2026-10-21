package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/pavp/tkt/internal/board"
	"github.com/pavp/tkt/internal/ticket"
)

type menuItem struct {
	label string
	key   string
	run   func() tea.Cmd
	// detail, when set, opens that ticket's detail view instead of running an action.
	detail ticket.Key
	// link is the URL c copies while the item is highlighted.
	link string
}

// menu lists what can be done with the selected ticket or review request.
type menu struct {
	title string
	items []menuItem
	sel   int
}

var menuBox = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(lipgloss.Color("111")).Padding(0, 2)

// buildMenu offers only the actions that apply: PR links when there are PRs, feedback when CI fails
// or changes were requested.
func (m Model) buildMenu(it item) *menu {
	a := m.actions
	if it.review() {
		pr := *it.pr
		return &menu{title: fmt.Sprintf("Review request #%d", pr.Number), items: []menuItem{
			{label: "Open the pull request in GitHub ↗", key: "p", link: pr.URL, run: func() tea.Cmd { return a.Open(pr.URL) }},
		}}
	}
	if it.mine() {
		return m.prMenu(it)
	}
	t := *it.ticket
	items := []menuItem{
		{label: m.startLabel(t.Key), run: func() tea.Cmd { return a.StartChoices(t.Key) }},
		{label: "Show details", key: "i", detail: t.Key},
		{label: "Open the ticket pane next to its agent", run: func() tea.Cmd { return a.TicketPane(t.Key) }},
	}
	if t.URL != "" {
		items = append(items, menuItem{label: "Open in Jira ↗", key: "o", link: t.URL, run: func() tea.Cmd { return a.Open(t.URL) }})
	}
	for i, pr := range t.PRs {
		pr := pr
		key := ""
		if i == 0 {
			key = "p"
		}
		items = append(items, menuItem{label: "Open " + prRef(pr) + " in GitHub ↗", key: key, link: pr.URL, run: func() tea.Cmd { return a.Open(pr.URL) }})
	}
	for _, r := range t.Attention() {
		label := "Send the CI failure to the agent"
		if r == ticket.ReasonChangesRequested {
			label = "Send the review comments to the agent"
		}
		items = append(items, menuItem{label: label, key: "f", run: func() tea.Cmd { return a.Fix(t.Key) }})
	}
	items = append(items,
		menuItem{label: "Copy the key " + string(t.Key), run: func() tea.Cmd { return a.Copy(string(t.Key), "key") }},
		menuItem{label: "Copy the ticket context (.ticket.md)", run: func() tea.Cmd { return a.CopyContext(t.Key) }},
	)
	return &menu{title: "Actions for " + string(t.Key), items: items}
}

// prMenu acts on one of the user's pull requests first, then on its ticket, leaving out the ticket's other
// pull requests, which have rows of their own in the PRs filter.
func (m Model) prMenu(it item) *menu {
	a := m.actions
	pr, t := *it.pr, *it.ticket
	items := []menuItem{{label: fmt.Sprintf("Open #%d in GitHub ↗", pr.Number), key: "p", link: pr.URL, run: func() tea.Cmd { return a.Open(pr.URL) }}}
	switch {
	case pr.Checks == ticket.ChecksFail:
		items = append(items, menuItem{label: "Send the CI failure to the agent", key: "f", run: func() tea.Cmd { return a.Fix(t.Key) }})
	case pr.Review == ticket.ReviewChangesRequested:
		items = append(items, menuItem{label: "Send the review comments to the agent", key: "f", run: func() tea.Cmd { return a.Fix(t.Key) }})
	}
	items = append(items,
		menuItem{label: m.startLabel(t.Key), run: func() tea.Cmd { return a.StartChoices(t.Key) }},
		menuItem{label: "Open the ticket pane next to its agent", run: func() tea.Cmd { return a.TicketPane(t.Key) }},
		menuItem{label: "Show details of " + string(t.Key), key: "i", detail: t.Key},
	)
	if t.URL != "" {
		items = append(items, menuItem{label: "Open " + string(t.Key) + " in Jira ↗", key: "o", link: t.URL, run: func() tea.Cmd { return a.Open(t.URL) }})
	}
	return &menu{title: fmt.Sprintf("Pull request #%d · %s", pr.Number, t.Key), items: items}
}

// startLabel says where Start leads: it focuses the ticket's agent when one already runs.
func (m Model) startLabel(k ticket.Key) string {
	if _, kind := board.AgentLine(m.snap.Agents[string(k)]); kind != board.AgentNone {
		return "Go to the agent in Herdr"
	}
	return "Start · open the agent"
}

func (m Model) menuKey(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	mn := m.menu
	switch k.String() {
	case "esc", "q", "backspace":
		m.menu = nil
	case "up", "w", "k":
		mn.sel = (mn.sel + len(mn.items) - 1) % len(mn.items)
	case "down", "s", "j", "tab":
		mn.sel = (mn.sel + 1) % len(mn.items)
	case "enter", " ":
		return m.runMenuItem(mn.sel)
	case "c":
		if link := mn.items[mn.sel].link; link != "" {
			m.menu = nil
			return m, m.actions.Copy(link, "link")
		}
	case "ctrl+c":
		return m, tea.Quit
	}
	return m, nil
}

func (m Model) runMenuItem(i int) (tea.Model, tea.Cmd) {
	if m.menu == nil || i < 0 || i >= len(m.menu.items) {
		return m, nil
	}
	it := m.menu.items[i]
	m.menu = nil
	if it.detail != "" {
		return m.openDetail(it.detail)
	}
	return m, it.run()
}

func (m Model) menuView() string {
	mn := m.menu
	width := lipgloss.Width(mn.title) + 6
	for _, it := range mn.items {
		width = max(width, lipgloss.Width(it.label)+8)
	}
	lines := []string{bold.Render(mn.title), ""}
	for i, it := range mn.items {
		hint := ""
		if it.key != "" {
			hint = keyStyle.Render(it.key)
		}
		label := it.label
		gap := max(2, width-4-lipgloss.Width(label)-lipgloss.Width(hint))
		line := label + strings.Repeat(" ", gap) + hint
		if i == mn.sel {
			line = selected.Render("▸ "+label+strings.Repeat(" ", gap-2)) + hint
		}
		lines = append(lines, line)
	}
	keys := "enter run · esc close"
	if mn.items[mn.sel].link != "" {
		keys = "enter run · c copy link · esc close"
	}
	lines = append(lines, "", dim.Render(keys))
	return menuBox.Render(strings.Join(lines, "\n"))
}

// menuItemAt maps a click to a menu item, given where View centered the menu box.
func (m Model) menuItemAt(x, y, top, height int) int {
	box := m.menuView()
	bw, bh := lipgloss.Width(box), lipgloss.Height(box)
	left := (m.width - bw) / 2
	boxTop := top + (height-bh)/2
	// Border, title and a blank line come before the first item.
	i := y - boxTop - 3
	if x < left || x >= left+bw || i < 0 || i >= len(m.menu.items) {
		return -1
	}
	return i
}
