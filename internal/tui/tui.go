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
	// Copy puts text on the clipboard; what names it in the confirmation, such as "key".
	Copy(text, what string) tea.Cmd
	CopyContext(key ticket.Key) tea.Cmd
	// Detail renders a ticket for reading inside the board; the result arrives as a DetailMsg.
	Detail(key ticket.Key) tea.Cmd
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
	compact      bool
	menu         *menu
	detail       *detailView
	// inStrip means the keyboard cursor is on the review chips above the columns, at stripSel.
	inStrip  bool
	stripSel int
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
	if m.inStrip {
		if _, spans := m.reviewChips(); m.stripSel < len(spans) {
			pr := spans[m.stripSel].pr
			return item{key: ticket.Key(fmt.Sprintf("#%d", pr.Number)), pr: &pr}, true
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
	if m.inStrip && m.effectiveView() == ViewColumns {
		if _, spans := m.reviewChips(); m.stripSel < len(spans) {
			return
		}
	}
	m.inStrip = false
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
	case DetailMsg:
		if m.detail == nil || m.detail.key != msg.Key {
			return m, nil
		}
		if msg.Err != nil {
			m.detail = nil
			m.status = "could not load " + string(msg.Key) + ": " + msg.Err.Error()
			return m, nil
		}
		m.detail.loading = false
		m.detail.lines = strings.Split(strings.TrimRight(msg.Text, "\n"), "\n")
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
	if m.detail != nil && m.menu == nil {
		switch msg.Button {
		case tea.MouseButtonWheelUp:
			m.detail.offset = max(0, m.detail.offset-3)
		case tea.MouseButtonWheelDown:
			m.detail.offset = min(m.maxOffset(), m.detail.offset+3)
		}
		return m, nil
	}
	if m.menu != nil {
		switch {
		case msg.Button == tea.MouseButtonWheelUp:
			m.menu.sel = max(0, m.menu.sel-1)
		case msg.Button == tea.MouseButtonWheelDown:
			m.menu.sel = min(len(m.menu.items)-1, m.menu.sel+1)
		case msg.Button == tea.MouseButtonLeft && msg.Action == tea.MouseActionPress:
			top := lineCount(m.header())
			height := m.height - top - lineCount(m.preview()+"\n"+m.footer())
			if i := m.menuItemAt(msg.X, msg.Y, top, height); i >= 0 {
				return m.runMenuItem(i)
			}
			m.menu = nil
		}
		return m, nil
	}
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
	if strip, spans := m.reviewChips(); strip != "" {
		if body < lineCount(strip) {
			// A click on a review chip opens that pull request: following a link is harmless, unlike starting an agent.
			for _, sp := range spans {
				if msg.X >= sp.start && msg.X < sp.start+sp.width {
					return m, m.actions.Open(sp.pr.URL)
				}
			}
			return m, nil
		}
		body -= lineCount(strip)
	}
	cols := m.columns()
	colWidth := m.columnWidth() + 2
	c := msg.X / colWidth
	// Inside a column box: top border, the column title, then one bordered card after another.
	r := (body - 2) / m.cardLines()
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

// columnWidth is the width of one column box without its border, so four boxes fill the pane.
func (m Model) columnWidth() int {
	w := m.width / len(ticket.Columns)
	if w < 20 {
		w = 20
	}
	return w - 2
}

func (m Model) key(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.menu != nil {
		return m.menuKey(k)
	}
	if m.detail != nil {
		return m.detailKey(k)
	}
	switch k.String() {
	case "q", "esc", "ctrl+c":
		return m, tea.Quit
	case "z":
		m.compact = !m.compact
		if m.compact {
			m.status = "compact cards · z for details"
		} else {
			m.status = "detailed cards · z for compact"
		}
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
	case "enter", " ":
		if it, ok := m.current(); ok {
			m.menu = m.buildMenu(it)
		}
	case "i":
		if it, ok := m.current(); ok && it.ticket != nil {
			return m.openDetail(it.key)
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
	if m.inStrip {
		_, spans := m.reviewChips()
		switch {
		case dr > 0:
			m.inStrip = false
		case dc != 0:
			m.stripSel = clamp(m.stripSel+dc, 0, len(spans)-1)
		}
		m.remember()
		return
	}
	cols := m.columns()
	atTop := m.col >= len(cols) || len(cols[m.col]) == 0 || m.row == 0
	if dr < 0 && atTop {
		if _, spans := m.reviewChips(); len(spans) > 0 {
			m.inStrip, m.stripSel = true, 0
			m.remember()
			return
		}
	}
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
	if m.detail != nil && m.menu == nil {
		body := m.detailBody()
		bottom := m.detailFooter()
		lines := strings.Split(body, "\n")
		for len(lines) < m.detailHeight() {
			lines = append(lines, "")
		}
		return top + "\n" + strings.Join(lines, "\n") + "\n\n" + bottom
	}
	var body string
	bottom0 := m.preview() + "\n" + m.footer()
	if m.menu != nil {
		height := m.height - lineCount(top) - lineCount(bottom0)
		body = lipgloss.Place(m.width, max(height, lipgloss.Height(m.menuView())), lipgloss.Center, lipgloss.Center, m.menuView())
	} else if m.effectiveView() == ViewColumns {
		height := m.height - lineCount(top) - lineCount(bottom0)
		if strip := m.reviewStrip(); strip != "" {
			body = strip + "\n"
			height -= lineCount(strip)
		}
		body += m.columnsView(height)
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

var reviewChip = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(lipgloss.Color("214")).Padding(0, 1)

// reviewStrip shows review requests above the columns, each in its own orange chip as in the mockup.
func (m Model) reviewStrip() string {
	row, _ := m.reviewChips()
	return row
}

// chipSpan is where one review chip sits on the strip, for clicks.
type chipSpan struct {
	start, width int
	pr           ticket.PullRequest
}

// reviewChips renders the strip and returns each visible chip's horizontal span.
func (m Model) reviewChips() (string, []chipSpan) {
	if len(m.snap.Reviews) == 0 {
		return "", nil
	}
	label := dim.Render("Review requested")
	row := []string{lipgloss.NewStyle().PaddingTop(1).Render(label)}
	used := lipgloss.Width(label)
	var spans []chipSpan
	for i, pr := range m.snap.Reviews {
		text := fmt.Sprintf(" #%d", pr.Number)
		if keys := ticket.BranchKeys(pr.Branch); len(keys) > 0 {
			text += " · " + string(keys[0])
		}
		chip := reviewChip.Render(warn.Render("!") + text)
		if m.inStrip && len(spans) == m.stripSel {
			chip = reviewChip.BorderForeground(lipgloss.Color("111")).Render(selected.Render("▸" + text))
		}
		if used+2+lipgloss.Width(chip) > m.width {
			more := lipgloss.NewStyle().PaddingTop(1).Render(dim.Render(fmt.Sprintf("+%d more · press 3", len(m.snap.Reviews)-i)))
			row = append(row, " ", more)
			break
		}
		row = append(row, "  ", chip)
		spans = append(spans, chipSpan{start: used + 2, width: lipgloss.Width(chip), pr: pr})
		used += 2 + lipgloss.Width(chip)
	}
	return lipgloss.JoinHorizontal(lipgloss.Top, row...), spans
}

// cardLines is how many lines one bordered card takes: two border lines plus its content lines.
func (m Model) cardLines() int {
	if m.compact {
		return 4
	}
	return 7
}

// columnsView draws the four columns as boxes reaching height lines, each ticket in its own card.
func (m Model) columnsView(height int) string {
	cols := m.columns()
	inner := m.columnWidth()
	rendered := make([]string, len(cols))
	for ci, c := range ticket.Columns {
		parts := []string{" " + bold.Render(fmt.Sprintf("%s %d", c.Title(), len(cols[ci])))}
		if len(cols[ci]) == 0 {
			parts = append(parts, " "+dim.Render("— nothing here"))
		}
		for ri, it := range cols[ci] {
			parts = append(parts, card(*it.ticket, inner, ci == m.col && ri == m.row && !m.inStrip, m.compact))
		}
		// Cards run edge to edge inside their column, with no padding between them and the border.
		st := box.Padding(0)
		if ci == m.col {
			st = boxSel.Padding(0)
		}
		st = st.Width(inner)
		if height > 2 {
			st = st.Height(height - 2)
		}
		rendered[ci] = st.Render(lipgloss.JoinVertical(lipgloss.Left, parts...))
	}
	return lipgloss.JoinHorizontal(lipgloss.Top, rendered...)
}

var (
	cardBox    = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(lipgloss.Color("238"))
	cardBoxSel = cardBox.BorderForeground(lipgloss.Color("111"))
)

// card renders one ticket in its own bordered box of the given outer width.
var (
	cardBoxWarn = cardBox.BorderForeground(lipgloss.Color("130"))
	titleStyle  = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("255"))
	keyDim      = lipgloss.NewStyle().Foreground(lipgloss.Color("250"))
)

// card renders one ticket in its own bordered box: key and attention flag, title, then (unless compact)
// its pull requests with colored CI marks and a dimmed line with type and subtasks.
func card(t ticket.Ticket, outer int, sel, compact bool) string {
	width := outer - 2
	mark := " "
	if sel {
		mark = "▸"
	}
	flag := ""
	if r := t.Attention(); len(r) > 0 {
		flag = warn.Render("! " + string(r[0]))
	}
	head := keyDim.Render(fmt.Sprintf("%s %s", mark, t.Key))
	if sel {
		head = selected.Render(fmt.Sprintf("%s %s", mark, t.Key))
	}
	gap := max(1, width-lipgloss.Width(head)-lipgloss.Width(flag))
	lines := []string{head + strings.Repeat(" ", gap) + flag}
	titleLines := 1
	if !compact {
		titleLines = 2
	}
	for _, l := range wrapWords(t.Summary, width-2, titleLines) {
		lines = append(lines, "  "+titleStyle.Render(l))
	}
	if !compact {
		lines = append(lines, "  "+coloredPRs(t.PRs, width-2))
		info := t.Type
		if len(t.Subtasks) > 0 {
			info = strings.TrimSpace(fmt.Sprintf("%s · %d subtasks", info, len(t.Subtasks)))
		}
		if info == "" {
			info = t.Status
		}
		lines = append(lines, "  "+dim.Render(truncate(info, width-2)))
	}
	st := cardBox
	switch {
	case sel:
		st = cardBoxSel
	case len(t.Attention()) > 0:
		st = cardBoxWarn
	}
	return st.Width(width).Render(strings.Join(lines, "\n"))
}

// wrapWords breaks s into at most n lines of width cells, cutting the last line with "…" when text
// remains, and always returns n lines so cards keep the same height.
func wrapWords(s string, width, n int) []string {
	words := strings.Fields(s)
	lines := make([]string, 0, n)
	cur := ""
	i := 0
	for ; i < len(words) && len(lines) < n; i++ {
		w := words[i]
		switch {
		case cur == "":
			cur = w
		case lipgloss.Width(cur)+1+lipgloss.Width(w) <= width:
			cur += " " + w
		default:
			lines = append(lines, cur)
			cur = w
		}
	}
	if len(lines) < n {
		lines = append(lines, cur)
		cur = ""
	} else if cur != "" {
		i--
	}
	last := len(lines) - 1
	if i < len(words) || cur != "" {
		lines[last] = truncate(lines[last]+" "+strings.Join(words[i:], " "), width)
		if lipgloss.Width(lines[last]) < width && !strings.HasSuffix(lines[last], "…") {
			lines[last] = truncate(lines[last], width-1) + "…"
		}
	}
	for j := range lines {
		lines[j] = truncate(lines[j], width)
	}
	for len(lines) < n {
		lines = append(lines, "")
	}
	return lines
}

// coloredPRs lists PRs as "repo#n" with the CI mark colored by state, cut to width.
func coloredPRs(prs []ticket.PullRequest, width int) string {
	if len(prs) == 0 {
		return dim.Render("—")
	}
	open, merged, closed := ticket.SplitPRs(prs)
	var parts []string
	used := 0
	for _, pr := range open {
		ref := fmt.Sprintf("#%d", pr.Number)
		markText, style := checkMark(pr.Checks), dim
		switch {
		case pr.State == ticket.PRMerged:
			markText, style = "merged", ok
		case pr.State == ticket.PRClosed:
			markText = "closed"
		case pr.Checks == ticket.ChecksFail:
			style = warn
		case pr.Checks == ticket.ChecksPass:
			style = ok
		}
		w := lipgloss.Width(ref) + 1 + lipgloss.Width(markText)
		if used > 0 && used+1+w > width {
			parts = append(parts, dim.Render("…"))
			break
		}
		if w > width {
			ref = truncate(ref, max(4, width-1-lipgloss.Width(markText)))
		}
		parts = append(parts, ref+" "+style.Render(markText))
		used += w + 1
	}
	if merged+closed == 1 {
		for _, pr := range prs {
			if pr.State != ticket.PROpen {
				parts = append(parts, ok.Render(prRef(pr)))
			}
		}
	} else if f := ticket.FinishedSummary(merged, closed); f != "" && used+len(f) <= width {
		parts = append(parts, ok.Render(f))
	}
	return strings.Join(parts, " ")
}

// prRef is a PR as the board mockup shows it: number and state, such as "#812 ✕" or "#790 merged".
func prRef(pr ticket.PullRequest) string {
	switch pr.State {
	case ticket.PRMerged:
		return fmt.Sprintf("#%d merged", pr.Number)
	case ticket.PRClosed:
		return fmt.Sprintf("#%d closed", pr.Number)
	}
	return fmt.Sprintf("#%d %s", pr.Number, checkMark(pr.Checks))
}

// prRefs lists open PRs; finished ones show by number when there is one, as a count when there are several.
func prRefs(prs []ticket.PullRequest) string {
	if len(prs) == 0 {
		return "—"
	}
	open, merged, closed := ticket.SplitPRs(prs)
	parts := make([]string, 0, len(open)+1)
	for _, pr := range open {
		parts = append(parts, prRef(pr))
	}
	if merged+closed == 1 {
		for _, pr := range prs {
			if pr.State != ticket.PROpen {
				parts = append(parts, prRef(pr))
			}
		}
	} else if f := ticket.FinishedSummary(merged, closed); f != "" {
		parts = append(parts, f)
	}
	return strings.Join(parts, " ")
}

func checkMark(c ticket.CheckState) string {
	switch c {
	case ticket.ChecksFail:
		return "✕"
	case ticket.ChecksPending:
		return "○"
	case ticket.ChecksPass:
		return "✓"
	}
	return "—"
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
	// prWidth caps the PR / CI column, which otherwise fits its widest value.
	prWidth = 22
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
	prText := func(it item) string {
		if it.pr != nil {
			return checkMark(it.pr.Checks)
		}
		return prRefs(it.ticket.PRs)
	}
	prW := lipgloss.Width("PR / CI")
	for _, it := range items {
		prW = max(prW, lipgloss.Width(prText(it)))
	}
	prW = min(prW, prWidth)
	// Narrow panes shrink PR / CI first, then STATUS, so the title always keeps some room.
	statusW := statusWidth
	const minTitle = 12
	if short := minTitle - (m.width - 2 - keyWidth - statusW - prW - 3); short > 0 {
		cut := max(0, min(short, prW-8))
		prW -= cut
		statusW -= min(short-cut, statusW-10)
	}
	titleWidth := max(minTitle, m.width-2-keyWidth-statusW-prW-3)
	row := func(key, status, pr, title string) string {
		return pad(key, keyWidth) + " " + pad(status, statusW) + " " + pad(pr, prW) + " " + truncate(title, titleWidth)
	}
	var b strings.Builder
	b.WriteString("  " + dim.Render(row("KEY", "STATUS", "PR / CI", "TITLE")) + "\n")
	for i, it := range items {
		var line string
		if it.pr != nil {
			line = row(string(it.key), "Review requested", prText(it), it.pr.Title)
		} else {
			line = row(string(it.key), it.ticket.Status, prText(it), it.ticket.Summary)
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
		title := titleStyle.Render(truncate(fmt.Sprintf("%s · %s", it.key, it.pr.Title), m.width))
		return title + "\n" + dim.Render(truncate(fmt.Sprintf("review requested by %s in %s · CI %s · enter for actions", orDash(it.pr.Author), it.pr.Repo, ciWord(it.pr.Checks)), m.width))
	}
	t := it.ticket
	title := titleStyle.Render(truncate(fmt.Sprintf("%s · %s", t.Key, t.Summary), m.width))
	parts := []string{t.Status}
	for _, r := range t.Attention() {
		parts = append(parts, string(r))
	}
	if len(t.Attention()) == 0 && t.Checks() == ticket.ChecksPass {
		parts = append(parts, "checks pass")
	}
	for _, pr := range t.PRs {
		if pr.State == ticket.PROpen {
			parts = append(parts, pr.Repo+"#"+fmt.Sprint(pr.Number))
		}
	}
	if hint := actionHint(*t); hint != "" {
		parts = append(parts, hint)
	}
	return title + "\n" + dim.Render(truncate(strings.Join(parts, " · "), m.width))
}

func ciWord(c ticket.CheckState) string {
	switch c {
	case ticket.ChecksFail:
		return "failing ✕"
	case ticket.ChecksPending:
		return "running ○"
	case ticket.ChecksPass:
		return "passing ✓"
	}
	return "not reported"
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

var keyStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("111"))

// footer is the key legend: each key highlighted, its meaning dimmed, with room between commands.
func (m Model) footer() string {
	move := "↑↓ ws"
	if m.effectiveView() == ViewColumns {
		move = "←↑↓→ wasd"
	}
	legend := [][2]string{
		{move, "move · ↑ to reviews"}, {"click", "select · open review"}, {"enter", "actions"}, {"f", "fix CI"}, {"i", "ticket pane"},
		{"o", "Jira ↗"}, {"p", "PR ↗"}, {"tab 1-3", "filter"}, {"v", "view"}, {"z", "density"}, {"g", "refresh"}, {"q", "close"},
	}
	items := make([]string, len(legend))
	for i, k := range legend {
		items[i] = keyStyle.Render(k[0]) + " " + dim.Render(k[1])
	}
	return wrapSep(items, m.width, "    ")
}

// wrap joins items with two spaces, starting a new line instead of overflowing width.
func wrap(items []string, width int) string {
	return wrapSep(items, width, "  ")
}

func wrapSep(items []string, width int, sep string) string {
	var lines []string
	cur := ""
	for _, it := range items {
		switch {
		case cur == "":
			cur = it
		case lipgloss.Width(cur)+lipgloss.Width(sep)+lipgloss.Width(it) <= width:
			cur += sep + it
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
