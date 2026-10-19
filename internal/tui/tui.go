// Package tui is the interactive board shown in the Herdr popup: columns or list, filters, and actions.
package tui

import (
	"fmt"
	"strings"
	"time"

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
	now          func() time.Time
}

// tickMsg drives the "synced … ago" label and the periodic refresh.
type tickMsg time.Time

const (
	tickEvery    = 30 * time.Second
	refreshEvery = 2 * time.Minute
)

var filterOrder = []string{FilterAll, FilterNeeds, FilterReviews}

func tick() tea.Cmd {
	return tea.Tick(tickEvery, func(t time.Time) tea.Msg { return tickMsg(t) })
}

func New(snap board.Snapshot, haveSnap bool, actions Actions, view string) Model {
	if view != ViewList {
		view = ViewColumns
	}
	m := Model{snap: snap, loading: true, actions: actions, view: view, filter: FilterAll, width: 160, height: 40, now: time.Now}
	if !haveSnap {
		m.status = "loading…"
	}
	return m
}

func (m Model) Init() tea.Cmd { return tea.Batch(m.actions.Refresh(), tick()) }

// WithClock replaces the clock, for tests.
func (m Model) WithClock(now func() time.Time) Model {
	m.now = now
	return m
}

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
	case tickMsg:
		if !m.loading && m.now().Sub(m.snap.FetchedAt) >= refreshEvery {
			m.loading = true
			return m, tea.Batch(m.actions.Refresh(), tick())
		}
		return m, tick()
	case tea.KeyMsg:
		return m.key(msg)
	case tea.MouseMsg:
		return m.mouse(msg)
	}
	return m, nil
}

// mouse selects what is under a click and scrolls with the wheel. Clicks only select: actions stay on
// the keyboard so a stray click never starts an agent.
func (m Model) mouse(msg tea.MouseMsg) (tea.Model, tea.Cmd) {
	switch msg.Button {
	case tea.MouseButtonWheelUp:
		m.move(0, -1)
		return m, nil
	case tea.MouseButtonWheelDown:
		m.move(0, 1)
		return m, nil
	case tea.MouseButtonLeft:
	default:
		return m, nil
	}
	if msg.Action != tea.MouseActionPress {
		return m, nil
	}
	headerLines := 1 + strings.Count(wrap(m.headerItems(), m.width), "\n") + 1
	if msg.Y == 1 {
		m.clickHeader(msg.X)
		return m, nil
	}
	body := msg.Y - headerLines
	if body < 0 {
		return m, nil
	}
	if m.effectiveView() == ViewList {
		items := m.listItems()
		if i := body - 1; i >= 0 && i < len(items) {
			m.row = i
			m.remember()
		}
		return m, nil
	}
	if strip := m.reviewStrip(); strip != "" {
		body -= strings.Count(strip, "\n") + 1
	}
	cols := m.columns()
	colWidth := m.columnWidth() + 2
	c := msg.X / colWidth
	// Inside a column box: top border, the column title, then four lines per card.
	r := (body - 2) / 4
	if body >= 2 && c >= 0 && c < len(cols) && r < len(cols[c]) {
		m.col, m.row = c, r
		m.remember()
	}
	return m, nil
}

// clickHeader switches filter or view when x falls on one of the header buttons (first header line).
func (m *Model) clickHeader(x int) {
	items := m.headerItems()
	spec := m.headerSpec()
	pos := 0
	for i, it := range items {
		w := lipgloss.Width(it)
		if i < 3 {
			if x >= pos && x < pos+w {
				m.filter = spec[i].filter
				m.reselect()
				return
			}
		} else {
			start := pos + 1
			for _, v := range spec[3:] {
				vw := lipgloss.Width(v.label)
				if x >= start && x < start+vw {
					m.view = v.view
					m.reselect()
					return
				}
				start += vw
			}
		}
		pos += w + 2
	}
}

// columnWidth is the inner width of one column box.
func (m Model) columnWidth() int {
	w := (m.width - 2) / len(ticket.Columns)
	if w < 20 {
		w = 20
	}
	return w - 4
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
	case "tab", "shift+tab":
		step := 1
		if k.String() == "shift+tab" {
			step = len(filterOrder) - 1
		}
		for i, f := range filterOrder {
			if f == m.filter {
				m.filter = filterOrder[(i+step)%len(filterOrder)]
				break
			}
		}
		m.reselect()
	case "1", "2", "3":
		m.filter = filterOrder[k.String()[0]-'1']
		m.reselect()
	case "down", "s", "j":
		m.move(0, 1)
	case "up", "w", "k":
		m.move(0, -1)
	case "right", "d", "l":
		m.move(1, 0)
	case "left", "a", "h":
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

// View pins the header to the top and the preview and key legend to the bottom; the board in between
// scrolls so the selected card or row stays visible.
func (m Model) View() string {
	top := m.header()
	var body string
	if m.effectiveView() == ViewColumns {
		if strip := m.reviewStrip(); strip != "" {
			body = strip + "\n"
		}
		body += m.columnsView()
	} else {
		body = m.listView()
	}
	bottom := m.preview() + "\n" + m.footer()
	avail := m.height - lineCount(top) - lineCount(bottom)
	lines := strings.Split(strings.TrimRight(body, "\n"), "\n")
	if avail > 0 && len(lines) > avail {
		lines = window(lines, avail)
	}
	for avail > 0 && len(lines) < avail {
		lines = append(lines, "")
	}
	return top + "\n" + strings.Join(lines, "\n") + "\n" + bottom
}

func lineCount(s string) int { return strings.Count(s, "\n") + 1 }

// window keeps n lines around the selection marker, so scrolling never hides the selected item.
func window(lines []string, n int) []string {
	sel := 0
	for i, l := range lines {
		if strings.Contains(l, "▸") {
			sel = i
			break
		}
	}
	start := sel - n/2
	if start < 0 {
		start = 0
	}
	if start+n > len(lines) {
		start = len(lines) - n
	}
	return lines[start : start+n]
}

func (m Model) header() string {
	parts := []string{bold.Render("tkt")}
	for _, sp := range m.snap.Sprints {
		parts = append(parts, sp.Space+" · "+board.SprintLabel(sp, m.snap.FetchedAt))
	}
	title := truncate(strings.Join(parts, " · "), m.width-28)
	sync := dim.Render(m.syncLabel())
	if m.stale != nil {
		sync = warn.Render(m.syncLabel())
	}
	gap := m.width - lipgloss.Width(title) - lipgloss.Width(sync)
	if gap < 2 {
		gap = 2
	}
	line2 := wrap(m.headerItems(), m.width)
	return title + strings.Repeat(" ", gap) + sync + "\n" + line2
}

type headerItem struct {
	filter, view string
	label        string
}

func (m Model) headerSpec() []headerItem {
	return []headerItem{
		{filter: FilterAll, label: fmt.Sprintf(" 1 All %d ", len(m.snap.Tickets)+len(m.snap.Reviews))},
		{filter: FilterNeeds, label: fmt.Sprintf(" 2 Needs you %d ", len(m.snap.NeedsYou()))},
		{filter: FilterReviews, label: fmt.Sprintf(" 3 Reviews %d ", len(m.snap.Reviews))},
		{view: ViewColumns, label: " Columns "},
		{view: ViewList, label: " List "},
	}
}

// headerItems renders the filter and view buttons; views sit together after a wider gap.
func (m Model) headerItems() []string {
	var out []string
	views := ""
	for _, it := range m.headerSpec() {
		on := (it.filter != "" && it.filter == m.filter) || (it.view != "" && it.view == m.effectiveView())
		r := dim.Render(it.label)
		if on {
			r = selected.Render(it.label)
		}
		if it.view != "" {
			views += r
			continue
		}
		out = append(out, r)
	}
	return append(out, " "+views+dim.Render(" v"))
}

// syncLabel says how fresh the board is: refreshing, synced N min ago, or cached when the last refresh failed.
func (m Model) syncLabel() string {
	switch {
	case m.loading:
		return "refreshing…"
	case m.snap.FetchedAt.IsZero():
		return "not synced yet"
	case m.stale != nil:
		return "cached · " + m.snap.FetchedAt.Local().Format("15:04")
	}
	d := m.now().Sub(m.snap.FetchedAt)
	switch {
	case d < time.Minute:
		return "synced just now"
	case d < time.Hour:
		return fmt.Sprintf("synced %d min ago", int(d.Minutes()))
	}
	return "synced " + m.snap.FetchedAt.Local().Format("15:04")
}

func (m Model) reviewStrip() string {
	if len(m.snap.Reviews) == 0 {
		return ""
	}
	parts := []string{dim.Render("Review requested")}
	for _, pr := range m.snap.Reviews {
		parts = append(parts, warn.Render("!")+fmt.Sprintf(" #%d · %s", pr.Number, truncate(pr.Title, 40)))
	}
	return wrap(append(parts, dim.Render("(r to select)")), m.width)
}

func (m Model) columnsView() string {
	cols := m.columns()
	inner := m.columnWidth()
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

func card(t ticket.Ticket, boxWidth int, sel bool) []string {
	// The column box spends two cells on padding, so lines must fit the narrower content area.
	width := boxWidth - 2
	mark := " "
	if sel {
		mark = "▸"
	}
	flag := ""
	if r := t.Attention(); len(r) > 0 {
		flag = warn.Render("! " + string(r[0]))
	}
	head := fmt.Sprintf("%s %s", mark, t.Key)
	if sel {
		head = selected.Render(head)
	}
	gap := max(1, width-lipgloss.Width(head)-lipgloss.Width(flag))
	title := truncate(t.Summary, width-2)
	meta := prMeta(t)
	if len(t.Subtasks) > 0 {
		meta = strings.TrimSpace(meta + fmt.Sprintf(" · %d subtasks", len(t.Subtasks)))
	}
	return []string{head + strings.Repeat(" ", gap) + flag, "  " + title, "  " + dim.Render(truncate(meta, width-2)), ""}
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

// List column widths, in terminal cells.
const (
	keyWidth    = 13
	statusWidth = 18
	prWidth     = 22
)

// pad truncates s to n cells and fills the rest with spaces, so columns line up.
func pad(s string, n int) string {
	s = truncate(s, n)
	return s + strings.Repeat(" ", max(0, n-lipgloss.Width(s)))
}

func (m Model) listView() string {
	items := m.listItems()
	if len(items) == 0 {
		return dim.Render("  nothing matches this filter")
	}
	titleWidth := max(10, m.width-2-keyWidth-statusWidth-prWidth-3)
	row := func(key, status, pr, title string) string {
		return pad(key, keyWidth) + " " + pad(status, statusWidth) + " " + pad(pr, prWidth) + " " + truncate(title, titleWidth)
	}
	var b strings.Builder
	b.WriteString("  " + dim.Render(row("KEY", "STATUS", "PR / CI", "TITLE")) + "\n")
	for i, it := range items {
		var line string
		if it.pr != nil {
			_, repo, _ := strings.Cut(it.pr.Repo, "/")
			line = row(string(it.key), "Review requested", repo, it.pr.Title)
		} else {
			line = row(string(it.key), it.ticket.Status, prMeta(*it.ticket), it.ticket.Summary)
		}
		switch {
		case i == m.row:
			b.WriteString("▸ " + selected.Render(line) + "\n")
		case it.pr != nil || len(it.ticket.Attention()) > 0:
			b.WriteString(warn.Render("!") + " " + line + "\n")
		default:
			b.WriteString("  " + line + "\n")
		}
	}
	return b.String()
}

func (m Model) preview() string {
	if m.status != "" {
		return truncate(m.status, m.width)
	}
	it, okSel := m.current()
	if !okSel {
		return dim.Render("nothing selected")
	}
	if it.pr != nil {
		return truncate(fmt.Sprintf("%s · review requested by %s in %s · enter opens it ↗", it.key, orDash(it.pr.Author), it.pr.Repo), m.width)
	}
	t := it.ticket
	parts := []string{string(t.Key), t.Status}
	for _, r := range t.Attention() {
		parts = append(parts, string(r))
	}
	if len(t.Attention()) == 0 && t.Checks() == ticket.ChecksPass {
		parts = append(parts, "checks pass")
	}
	if hint := actionHint(*t); hint != "" {
		parts = append(parts, hint)
	}
	return truncate(strings.Join(parts, " · "), m.width)
}

// actionHint tells what the most useful key does for this ticket, as the mockup's preview line does.
func actionHint(t ticket.Ticket) string {
	reasons := t.Attention()
	switch {
	case len(reasons) > 0 && reasons[0] == ticket.ReasonCIFailing:
		return "f sends the failure to the agent"
	case len(reasons) > 0:
		return "f sends the review comments to the agent"
	case len(t.PRs) == 0 && t.Category == ticket.ToDo:
		return "enter asks for repos and opens Claude Code"
	case len(t.PRs) == 0:
		return "enter opens the agent"
	}
	return "p opens the PR ↗"
}

func (m Model) footer() string {
	move := "↑↓/ws move"
	if m.effectiveView() == ViewColumns {
		move = "←↑↓→/wasd move"
	}
	keys := []string{move, "click selects", "enter Open agent · Start", "f fix CI", "i ticket pane", "o Jira ↗", "p PR ↗", "tab/1-3 filter", "v view", "g refresh", "q close"}
	return dim.Render(wrap(keys, m.width))
}

// wrap joins items with two spaces, starting a new line instead of overflowing width.
func wrap(items []string, width int) string {
	var lines []string
	cur := ""
	for _, it := range items {
		switch {
		case cur == "":
			cur = it
		case lipgloss.Width(cur)+2+lipgloss.Width(it) <= width:
			cur += "  " + it
		default:
			lines = append(lines, cur)
			cur = it
		}
	}
	return strings.Join(append(lines, cur), "\n")
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
