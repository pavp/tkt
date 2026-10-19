// Package tui is the interactive board shown in the Herdr popup: columns or list, filters, and actions.
package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/pavp/tkt/internal/board"
	"github.com/pavp/tkt/internal/ticket"
)

// Actions are the side effects the board triggers; the CLI provides them.
type Actions interface {
	Refresh() tea.Cmd
	Start(key ticket.Key) tea.Cmd
	TicketPane(key ticket.Key) tea.Cmd
	Open(url string) tea.Cmd
	Fix(key ticket.Key) tea.Cmd
}

// SnapshotMsg delivers a fetched board; Stale explains why it came from the cache, Err why nothing came.
type SnapshotMsg struct {
	Snap  board.Snapshot
	Stale error
	Err   error
}

// StatusMsg shows a one-line result of an action in the footer.
type StatusMsg string

const (
	ViewColumns = "columns"
	ViewList    = "list"

	FilterAll     = "all"
	FilterNeeds   = "needs"
	FilterReviews = "reviews"

	// narrowWidth is the terminal width below which four columns no longer fit.
	narrowWidth = 100
)

type item struct {
	key    ticket.Key
	ticket *ticket.Ticket
	pr     *ticket.PullRequest
}

type Model struct {
	snap    board.Snapshot
	stale   error
	loading bool
	actions Actions

	view, filter string
	col, row     int
	selected     string
	width        int
	height       int
	status       string
}

func New(snap board.Snapshot, haveSnap bool, actions Actions, view string) Model {
	if view != ViewList {
		view = ViewColumns
	}
	m := Model{snap: snap, loading: true, actions: actions, view: view, filter: FilterAll, width: 160, height: 40}
	if !haveSnap {
		m.status = "loading…"
	}
	return m
}

func (m Model) Init() tea.Cmd { return m.actions.Refresh() }

// effectiveView falls back to the list when columns would not fit or when showing reviews.
func (m Model) effectiveView() string {
	if m.filter == FilterReviews || m.width < narrowWidth {
		return ViewList
	}
	return m.view
}

func (m Model) tickets() []ticket.Ticket {
	if m.filter == FilterReviews {
		return nil
	}
	var out []ticket.Ticket
	for _, t := range m.snap.Tickets {
		if m.filter == FilterNeeds && len(t.Attention()) == 0 {
			continue
		}
		out = append(out, t)
	}
	return out
}

func (m Model) columns() [][]item {
	cols := make([][]item, len(ticket.Columns))
	ts := m.tickets()
	for ci, c := range ticket.Columns {
		for i := range ts {
			if ts[i].Category == c {
				t := ts[i]
				cols[ci] = append(cols[ci], item{key: t.Key, ticket: &t})
			}
		}
	}
	return cols
}

func (m Model) listItems() []item {
	var out []item
	for _, t := range board.Sorted(m.tickets()) {
		t := t
		out = append(out, item{key: t.Key, ticket: &t})
	}
	for i := range m.snap.Reviews {
		pr := m.snap.Reviews[i]
		out = append(out, item{key: ticket.Key(fmt.Sprintf("#%d", pr.Number)), pr: &pr})
	}
	return out
}

func (m Model) current() (item, bool) {
	if m.effectiveView() == ViewList {
		items := m.listItems()
		if m.row >= 0 && m.row < len(items) {
			return items[m.row], true
		}
		return item{}, false
	}
	cols := m.columns()
	if m.col < len(cols) && m.row >= 0 && m.row < len(cols[m.col]) {
		return cols[m.col][m.row], true
	}
	return item{}, false
}

// reselect moves the cursor back onto the previously selected item after the data or view changed.
func (m *Model) reselect() {
	if m.effectiveView() == ViewList {
		items := m.listItems()
		m.row = 0
		for i, it := range items {
			if string(it.key) == m.selected {
				m.row = i
			}
		}
		return
	}
	cols := m.columns()
	for ci, col := range cols {
		for ri, it := range col {
			if string(it.key) == m.selected {
				m.col, m.row = ci, ri
				return
			}
		}
	}
	m.row = 0
	if m.col >= len(cols) || len(cols[m.col]) == 0 {
		for ci, col := range cols {
			if len(col) > 0 {
				m.col = ci
				break
			}
		}
	}
}

func (m *Model) remember() {
	if it, ok := m.current(); ok {
		m.selected = string(it.key)
	}
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.reselect()
	case SnapshotMsg:
		m.loading = false
		if msg.Err != nil {
			m.status = "could not refresh: " + msg.Err.Error()
			return m, nil
		}
		m.snap, m.stale = msg.Snap, msg.Stale
		m.status = ""
		m.reselect()
	case StatusMsg:
		m.status = string(msg)
	case tea.KeyMsg:
		return m.key(msg)
	}
	return m, nil
}

func (m Model) key(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch k.String() {
	case "q", "esc", "ctrl+c":
		return m, tea.Quit
	case "v":
		if m.view == ViewColumns {
			m.view = ViewList
		} else {
			m.view = ViewColumns
		}
		m.reselect()
	case "n", "r":
		want := map[string]string{"n": FilterNeeds, "r": FilterReviews}[k.String()]
		if m.filter == want {
			m.filter = FilterAll
		} else {
			m.filter = want
		}
		m.reselect()
	case "a":
		m.filter = FilterAll
		m.reselect()
	case "j", "down":
		m.move(0, 1)
	case "k", "up":
		m.move(0, -1)
	case "l", "right":
		m.move(1, 0)
	case "h", "left":
		m.move(-1, 0)
	case "g":
		m.loading = true
		m.status = "refreshing…"
		return m, m.actions.Refresh()
	case "enter":
		if it, ok := m.current(); ok {
			if it.pr != nil {
				return m, m.actions.Open(it.pr.URL)
			}
			return m, m.actions.Start(it.key)
		}
	case "i":
		if it, ok := m.current(); ok && it.ticket != nil {
			return m, m.actions.TicketPane(it.key)
		}
	case "o":
		if it, ok := m.current(); ok && it.ticket != nil && it.ticket.URL != "" {
			return m, m.actions.Open(it.ticket.URL)
		}
	case "p":
		if it, ok := m.current(); ok {
			if it.pr != nil {
				return m, m.actions.Open(it.pr.URL)
			}
			if it.ticket != nil && len(it.ticket.PRs) > 0 {
				return m, m.actions.Open(it.ticket.PRs[0].URL)
			}
			m.status = string(it.key) + " has no pull request yet"
		}
	case "f":
		if it, ok := m.current(); ok && it.ticket != nil {
			m.status = "sending feedback for " + string(it.key) + "…"
			return m, m.actions.Fix(it.key)
		}
	}
	return m, nil
}

func (m *Model) move(dc, dr int) {
	if m.effectiveView() == ViewList {
		n := len(m.listItems())
		m.row = clamp(m.row+dr, 0, n-1)
		m.remember()
		return
	}
	cols := m.columns()
	if dc != 0 {
		c := m.col
		for {
			c += dc
			if c < 0 || c >= len(cols) {
				break
			}
			if len(cols[c]) > 0 {
				m.col = c
				m.row = clamp(m.row, 0, len(cols[c])-1)
				break
			}
		}
	}
	if dr != 0 && m.col < len(cols) {
		m.row = clamp(m.row+dr, 0, len(cols[m.col])-1)
	}
	m.remember()
}

func clamp(v, lo, hi int) int {
	if hi < lo {
		return lo
	}
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

var (
	dim      = lipgloss.NewStyle().Foreground(lipgloss.Color("245"))
	warn     = lipgloss.NewStyle().Foreground(lipgloss.Color("214"))
	ok       = lipgloss.NewStyle().Foreground(lipgloss.Color("111"))
	bold     = lipgloss.NewStyle().Bold(true)
	selected = lipgloss.NewStyle().Background(lipgloss.Color("24")).Foreground(lipgloss.Color("255"))
	box      = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(lipgloss.Color("240")).Padding(0, 1)
	boxSel   = box.BorderForeground(lipgloss.Color("111"))
)

func (m Model) View() string {
	var b strings.Builder
	b.WriteString(m.header())
	b.WriteString("\n")
	if m.effectiveView() == ViewColumns {
		if strip := m.reviewStrip(); strip != "" {
			b.WriteString(strip + "\n")
		}
		b.WriteString(m.columnsView())
	} else {
		b.WriteString(m.listView())
	}
	b.WriteString("\n" + m.preview() + "\n" + m.footer())
	return b.String()
}

func (m Model) header() string {
	parts := []string{bold.Render("tkt")}
	for _, sp := range m.snap.Sprints {
		parts = append(parts, fmt.Sprintf("%s · %dd", sp.Name, board.DaysLeft(sp.End, m.snap.FetchedAt)))
	}
	filters := []string{}
	for _, f := range []struct{ id, label string }{{FilterAll, fmt.Sprintf("All %d", len(m.snap.Tickets))}, {FilterNeeds, fmt.Sprintf("Needs you %d", len(m.snap.NeedsYou()))}, {FilterReviews, fmt.Sprintf("Reviews %d", len(m.snap.Reviews))}} {
		if m.filter == f.id {
			filters = append(filters, selected.Render(" "+f.label+" "))
		} else {
			filters = append(filters, dim.Render(" "+f.label+" "))
		}
	}
	views := dim.Render("[" + m.effectiveView() + "]")
	sync := dim.Render("synced")
	switch {
	case m.loading:
		sync = dim.Render("refreshing…")
	case m.stale != nil:
		sync = warn.Render("cached · " + m.snap.FetchedAt.Local().Format("15:04"))
	}
	return strings.Join(parts, dim.Render(" · ")) + "   " + strings.Join(filters, " ") + "   " + views + "  " + sync
}

func (m Model) reviewStrip() string {
	if len(m.snap.Reviews) == 0 {
		return ""
	}
	var parts []string
	for _, pr := range m.snap.Reviews {
		parts = append(parts, warn.Render("!")+fmt.Sprintf(" #%d · %s", pr.Number, truncate(pr.Title, 40)))
	}
	return dim.Render("Review requested  ") + strings.Join(parts, "   ") + dim.Render("   (r to select)")
}

func (m Model) columnsView() string {
	cols := m.columns()
	w := (m.width - 2) / len(cols)
	if w < 20 {
		w = 20
	}
	inner := w - 4
	rendered := make([]string, len(cols))
	for ci, c := range ticket.Columns {
		lines := []string{bold.Render(fmt.Sprintf("%s %d", c.Title(), len(cols[ci])))}
		if len(cols[ci]) == 0 {
			lines = append(lines, dim.Render("— nothing here"))
		}
		for ri, it := range cols[ci] {
			sel := ci == m.col && ri == m.row
			lines = append(lines, card(*it.ticket, inner, sel)...)
		}
		st := box
		if ci == m.col {
			st = boxSel
		}
		rendered[ci] = st.Width(inner).Render(strings.Join(lines, "\n"))
	}
	return lipgloss.JoinHorizontal(lipgloss.Top, rendered...)
}

func card(t ticket.Ticket, width int, sel bool) []string {
	mark := " "
	if sel {
		mark = "▸"
	}
	flag := ""
	if r := t.Attention(); len(r) > 0 {
		flag = warn.Render("! " + string(r[0]))
	}
	head := fmt.Sprintf("%s %s", mark, t.Key)
	title := truncate(t.Summary, width-2)
	meta := prMeta(t)
	if len(t.Subtasks) > 0 {
		meta = strings.TrimSpace(meta + fmt.Sprintf(" · %d subtasks", len(t.Subtasks)))
	}
	lines := []string{head + "  " + flag, "  " + title, "  " + dim.Render(truncate(meta, width-2)), ""}
	if sel {
		lines[0] = selected.Render(head) + "  " + flag
	}
	return lines
}

func prMeta(t ticket.Ticket) string {
	var parts []string
	for _, pr := range t.PRs {
		mark := "·"
		switch {
		case pr.State == ticket.PRMerged:
			mark = "merged"
		case pr.Checks == ticket.ChecksFail:
			mark = "✕"
		case pr.Checks == ticket.ChecksPending:
			mark = "○"
		case pr.Checks == ticket.ChecksPass:
			mark = "✓"
		}
		parts = append(parts, fmt.Sprintf("#%d %s", pr.Number, mark))
	}
	if len(parts) == 0 {
		return "no PR"
	}
	return strings.Join(parts, " ")
}

func (m Model) listView() string {
	items := m.listItems()
	if len(items) == 0 {
		return dim.Render("  nothing matches this filter")
	}
	var b strings.Builder
	b.WriteString(dim.Render(fmt.Sprintf("    %-14s %-16s %-16s %s", "KEY", "STATUS", "PR / CI", "TITLE")) + "\n")
	for i, it := range items {
		var line string
		if it.pr != nil {
			line = fmt.Sprintf("%-14s %-16s %-16s %s", it.key, "Review requested", it.pr.Repo, it.pr.Title)
		} else {
			line = fmt.Sprintf("%-14s %-16s %-16s %s", it.key, truncate(it.ticket.Status, 16), truncate(prMeta(*it.ticket), 16), it.ticket.Summary)
		}
		line = truncate(line, m.width-6)
		mark := "  "
		if (it.ticket != nil && len(it.ticket.Attention()) > 0) || it.pr != nil {
			mark = warn.Render("! ")
		}
		if i == m.row {
			b.WriteString("▸ " + selected.Render(line) + "\n")
		} else {
			b.WriteString(mark + line + "\n")
		}
	}
	return b.String()
}

func (m Model) preview() string {
	if m.status != "" {
		return m.status
	}
	it, okSel := m.current()
	if !okSel {
		return dim.Render("nothing selected")
	}
	if it.pr != nil {
		return fmt.Sprintf("%s · review requested by %s in %s · Enter opens it ↗", it.key, orDash(it.pr.Author), it.pr.Repo)
	}
	t := it.ticket
	parts := []string{string(t.Key), t.Status}
	for _, r := range t.Attention() {
		parts = append(parts, warn.Render(string(r)))
	}
	if len(t.Attention()) == 0 && t.Checks() == ticket.ChecksPass {
		parts = append(parts, ok.Render("checks pass"))
	}
	parts = append(parts, t.Summary)
	return truncate(strings.Join(parts, " · "), m.width)
}

func (m Model) footer() string {
	move := "↑↓ move"
	if m.effectiveView() == ViewColumns {
		move = "h/l j/k move"
	}
	keys := []string{move, "enter Open agent · Start", "f fix CI", "i ticket pane", "o Jira ↗", "p PR ↗", "n needs you", "r reviews", "v view", "g refresh", "q close"}
	return dim.Render(strings.Join(keys, "  "))
}

func truncate(s string, n int) string {
	r := []rune(s)
	if n <= 1 || len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
}

func orDash(s string) string {
	if s == "" {
		return "—"
	}
	return s
}
