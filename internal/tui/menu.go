package tui

import (
	"cmp"
	"fmt"
	"slices"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

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
	// sub, when set, opens another menu, from which esc comes back.
	sub func() *menu
	// prompt, when set, opens a form's text box.
	prompt *formPrompt
	// move and fix, when set, open that ticket's Move to… menu or send its feedback to its agent.
	move ticket.Key
	fix  ticket.Key
}

// menu lists what can be done with the selected ticket or review request.
type menu struct {
	title string
	items []menuItem
	sel   int
	// back is the menu esc returns to; nil closes.
	back *menu
	// loading names the ticket whose statuses this menu waits for.
	loading ticket.Key
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
	if t.Team {
		return m.teamMenu(t)
	}
	items := []menuItem{
		{label: m.startLabel(t.Key), run: func() tea.Cmd { return a.StartChoices(t.Key) }},
		{label: "Show details", key: "i", detail: t.Key},
		{label: "Open the ticket pane next to its agent", run: func() tea.Cmd { return a.TicketPane(t.Key) }},
	}
	if t.URL != "" {
		items = append(items, menuItem{label: "Open in Jira ↗", key: "o", link: t.URL, run: func() tea.Cmd { return a.Open(t.URL) }})
	}
	switch len(t.PRs) {
	case 0:
	case 1:
		pr := t.PRs[0]
		items = append(items, menuItem{label: "Open " + prRef(pr) + " in GitHub ↗", key: "p", link: pr.URL, run: func() tea.Cmd { return a.Open(pr.URL) }})
	default:
		items = append(items, menuItem{label: fmt.Sprintf("Pull requests (%d) ▸", len(t.PRs)), key: "p", sub: func() *menu { return m.prsMenu(t) }})
	}
	for _, r := range t.Attention() {
		items = append(items, menuItem{label: fixLabel(r), key: "f", fix: t.Key})
	}
	items = append(items, m.moveItem(t.Key))
	items = append(items,
		menuItem{label: "Copy the key " + string(t.Key), run: func() tea.Cmd { return a.Copy(string(t.Key), "key") }},
		menuItem{label: "Copy the ticket context (.ticket.md)", run: func() tea.Cmd { return a.CopyContext(t.Key) }},
	)
	return &menu{title: "Actions for " + string(t.Key) + " · " + t.Status, items: items}
}

// prMenu acts on one of the user's pull requests first, then on its ticket, leaving out the ticket's other
// pull requests, which have rows of their own in the PRs filter.
func (m Model) prMenu(it item) *menu {
	a := m.actions
	pr, t := *it.pr, *it.ticket
	items := []menuItem{{label: fmt.Sprintf("Open #%d in GitHub ↗", pr.Number), key: "p", link: pr.URL, run: func() tea.Cmd { return a.Open(pr.URL) }}}
	if reasons := pr.Attention(); len(reasons) > 0 {
		items = append(items, menuItem{label: fixLabel(reasons[0]), key: "f", fix: t.Key})
	}
	items = append(items,
		menuItem{label: m.startLabel(t.Key), run: func() tea.Cmd { return a.StartChoices(t.Key) }},
		menuItem{label: "Open the ticket pane next to its agent", run: func() tea.Cmd { return a.TicketPane(t.Key) }},
		menuItem{label: "Show details of " + string(t.Key), key: "i", detail: t.Key},
	)
	if t.URL != "" {
		items = append(items, menuItem{label: "Open " + string(t.Key) + " in Jira ↗", key: "o", link: t.URL, run: func() tea.Cmd { return a.Open(t.URL) }})
	}
	items = append(items, m.moveItem(t.Key))
	if pr.URL != "" {
		items = append(items, menuItem{label: "Copy the PR link", run: func() tea.Cmd { return a.Copy(pr.URL, "link") }})
	}
	if t.URL != "" {
		items = append(items, menuItem{label: "Copy the Jira link", run: func() tea.Cmd { return a.Copy(t.URL, "link") }})
	}
	items = append(items, menuItem{label: "Copy the key " + string(t.Key), run: func() tea.Cmd { return a.Copy(string(t.Key), "key") }})
	return &menu{title: fmt.Sprintf("Pull request #%d · %s · %s", pr.Number, t.Key, t.Status), items: items}
}

// moveItem opens where the ticket can move from its current status.
func (m Model) moveItem(k ticket.Key) menuItem {
	return menuItem{label: "Move to… ▸", move: k}
}

// fixLabel words the fix for a reason; every fix sends all of the ticket's feedback at once.
func fixLabel(r ticket.Reason) string {
	switch r {
	case ticket.ReasonChangesRequested:
		return "Send the review comments to the agent"
	case ticket.ReasonUnresolved:
		return "Send the unresolved comments to the agent"
	case ticket.ReasonConflicts:
		return "Ask the agent to resolve the conflicts"
	}
	return "Send the CI failure to the agent"
}

// teamMenu offers what is harmless on someone else's ticket: reading it, opening it in Jira, copying its key.
func (m Model) teamMenu(t ticket.Ticket) *menu {
	a := m.actions
	items := []menuItem{{label: "Show details", key: "i", detail: t.Key}}
	if t.URL != "" {
		items = append(items, menuItem{label: "Open in Jira ↗", key: "o", link: t.URL, run: func() tea.Cmd { return a.Open(t.URL) }})
	}
	items = append(items, menuItem{label: "Copy the key " + string(t.Key), run: func() tea.Cmd { return a.Copy(string(t.Key), "key") }})
	return &menu{title: "Actions for " + string(t.Key) + " · " + t.Status + " · " + cmp.Or(t.Assignee, "unassigned"), items: items}
}

// prsMenu lists a ticket's pull requests to open one, those that need the user first and finished ones last.
func (m Model) prsMenu(t ticket.Ticket) *menu {
	a := m.actions
	prs := slices.Clone(t.PRs)
	rank := func(pr ticket.PullRequest) int {
		switch {
		case pr.State != ticket.PROpen:
			return 2
		case needsYou(pr):
			return 0
		}
		return 1
	}
	slices.SortStableFunc(prs, func(x, y ticket.PullRequest) int { return rank(x) - rank(y) })
	var items []menuItem
	for _, pr := range prs {
		status := prStatus(pr)
		if pr.State != ticket.PROpen {
			status = string(pr.State)
		}
		_, repo, _ := strings.Cut(pr.Repo, "/")
		label := fmt.Sprintf("#%d %s", pr.Number, status)
		if repo != "" {
			label += " · " + repo
		}
		items = append(items, menuItem{label: label, link: pr.URL, run: func() tea.Cmd { return a.Open(pr.URL) }})
	}
	return &menu{title: "Pull requests of " + string(t.Key), items: items}
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
		m.menu = mn.back
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
	it, parent := m.menu.items[i], m.menu
	m.menu = nil
	if it.sub != nil {
		m.menu = it.sub()
		m.menu.back = parent
		return m, nil
	}
	if it.detail != "" {
		return m.openDetail(it.detail)
	}
	if it.prompt != nil {
		p := *it.prompt
		m.prompt = &p
		return m, nil
	}
	if it.move != "" {
		return m.openMoves(it.move, parent)
	}
	if it.fix != "" {
		return m.startFix(it.fix)
	}
	if it.run == nil {
		m.menu = parent
		return m, nil
	}
	return m, it.run()
}

func (m Model) menuView() string {
	mn := m.menu
	const linkKeys = "enter run · c copy link · esc close"
	width := lipgloss.Width(mn.title) + 6
	for _, it := range mn.items {
		width = max(width, lipgloss.Width(it.label)+8)
		// The footer grows on an item with a link; sizing for it up front keeps the box still.
		if it.link != "" {
			width = max(width, lipgloss.Width(linkKeys)+4)
		}
	}
	// Border and padding take 6 cells around the content, which is width-4 wide.
	if m.width > 0 {
		width = min(width, m.width-2)
	}
	lines := []string{bold.Render(ansi.Truncate(mn.title, width-4, "…")), ""}
	for i, it := range mn.items {
		hint := ""
		if it.key != "" {
			hint = keyStyle.Render(it.key)
		}
		label := ansi.Truncate(it.label, width-8, "…")
		gap := max(2, width-4-lipgloss.Width(label)-lipgloss.Width(hint))
		line := label + strings.Repeat(" ", gap) + hint
		if i == mn.sel {
			line = selected.Render("▸ "+label+strings.Repeat(" ", gap-2)) + hint
		}
		lines = append(lines, line)
	}
	keys := "enter run · esc close"
	if mn.items[mn.sel].link != "" {
		keys = linkKeys
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
