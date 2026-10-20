// Package tui is the interactive board shown in the Herdr popup: columns or list, filters, and actions.
package tui

import (
	"cmp"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/pavp/tkt/internal/board"
	"github.com/pavp/tkt/internal/config"
	"github.com/pavp/tkt/internal/ticket"
)

// Actions are the side effects the board triggers; the CLI provides them.
type Actions interface {
	Refresh() tea.Cmd
	// Agents reads the Herdr agent states of snap's tickets; they arrive as an AgentsMsg.
	Agents(snap board.Snapshot) tea.Cmd
	// Start runs Start for key with the answers to its choices; StartChoices first reports, as a
	// StartChoicesMsg, what Start would ask.
	Start(key ticket.Key, opts StartOptions) tea.Cmd
	StartChoices(key ticket.Key) tea.Cmd
	TicketPane(key ticket.Key) tea.Cmd
	Open(url string) tea.Cmd
	Fix(key ticket.Key) tea.Cmd
	// Copy puts text on the clipboard; what names it in the confirmation, such as "key".
	Copy(text, what string) tea.Cmd
	CopyContext(key ticket.Key) tea.Cmd
	// Detail renders a ticket for reading inside the board; the result arrives as a DetailMsg.
	Detail(key ticket.Key) tea.Cmd
	// Config returns a copy of the configuration in use, for the settings view to edit.
	Config() config.Config
	// SaveConfig writes the configuration; the outcome arrives as a SettingsSavedMsg.
	SaveConfig(c config.Config) tea.Cmd
	// FindClones looks for local clones of repo ("owner/name"); they arrive as a ClonesFoundMsg.
	FindClones(repo string) tea.Cmd
	// RepoAt reads the GitHub repository a folder's origin points to; it arrives as a RepoFoundMsg.
	RepoAt(path string) tea.Cmd
	// PickFolder shows the system's folder chooser; the choice arrives as a FolderPickedMsg.
	PickFolder(start string) tea.Cmd
	// ListDirs lists the folders inside path ("~/dev/"); they arrive as a DirsListedMsg.
	ListDirs(path string) tea.Cmd
	// ListRepos lists owner's GitHub repositories as "owner/name"; they arrive as a ReposListedMsg.
	ListRepos(owner string) tea.Cmd
	// JiraFields lists the names of the Jira site's fields; they arrive as a JiraFieldsListedMsg.
	JiraFields() tea.Cmd
	// NativePicker reports whether PickFolder can show a folder chooser the user will see; without one,
	// ctrl+o browses folders inside the terminal.
	NativePicker() bool
}

type ClonesFoundMsg struct {
	Repo  string
	Paths []string
}

type RepoFoundMsg struct {
	Path, Repo string
}

// FolderPickedMsg carries the chosen folder; an empty Path means the user cancelled.
type FolderPickedMsg struct {
	Path string
	Err  error
}

// SettingsSavedMsg reports a settings save: the configuration as written, with defaults filled in, or why
// nothing was written.
type SettingsSavedMsg struct {
	Config config.Config
	Err    error
}

// SnapshotMsg delivers a fetched board; Stale explains why it came from the cache, Err why nothing came.
// Quiet marks a board the daemon pushed on its own, which leaves the status line alone.
type SnapshotMsg struct {
	Snap  board.Snapshot
	Stale error
	Err   error
	Quiet bool
}

// AgentsMsg delivers the agent states read for the board fetched at FetchedAt.
type AgentsMsg struct {
	FetchedAt time.Time
	Agents    map[string]string
}

// StatusMsg shows a one-line result of an action in the footer.
type StatusMsg string

const (
	ViewColumns = "columns"
	ViewList    = "list"

	FilterAll = "all"
	// FilterPRs lists the user's own open pull requests, then the review requests.
	FilterPRs = "prs"

	// narrowWidth is the terminal width below which four columns no longer fit.
	narrowWidth = 100
)

// item is a ticket, a review request (pr alone), or one of the user's pull requests (pr with its ticket).
type item struct {
	key    ticket.Key
	ticket *ticket.Ticket
	pr     *ticket.PullRequest
}

func (it item) review() bool { return it.pr != nil && it.ticket == nil }
func (it item) mine() bool   { return it.pr != nil && it.ticket != nil }

type Model struct {
	// lastClick and lastClickAt detect a double click, which the terminal does not report.
	lastClick   ticket.Key
	lastClickAt time.Time

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
	settings     *settingsView
	picker       *picker
	// colOffset is how far each column is scrolled; an array so copies of the model never share it.
	colOffset [8]int
	// inStrip means the keyboard cursor is on the review chips above the columns, at stripSel.
	inStrip  bool
	stripSel int
	// spin is the refreshing spinner's frame; spinning says its tick is already running.
	spin     int
	spinning bool
	// showBots also lists review requests on pull requests opened by bots.
	showBots bool
}

// tickMsg drives the "synced … ago" label and the periodic refresh.
type tickMsg time.Time

// spinMsg advances the refreshing spinner; it stops ticking once the board arrives.
type spinMsg struct{}

var spinFrames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

const spinEvery = 100 * time.Millisecond

func (m *Model) startSpin() tea.Cmd {
	if m.spinning {
		return nil
	}
	m.spinning = true
	return tea.Tick(spinEvery, func(time.Time) tea.Msg { return spinMsg{} })
}

const (
	tickEvery    = 30 * time.Second
	refreshEvery = 2 * time.Minute
)

var filterOrder = []string{FilterAll, FilterPRs}

func tick() tea.Cmd {
	return tea.Tick(tickEvery, func(t time.Time) tea.Msg { return tickMsg(t) })
}

func New(snap board.Snapshot, haveSnap bool, actions Actions, view string) Model {
	if view != ViewList {
		view = ViewColumns
	}
	m := Model{snap: snap, loading: true, spinning: true, actions: actions, view: view, filter: FilterAll, width: 160, height: 40, now: time.Now}
	if !haveSnap {
		m.status = "loading…"
	}
	return m
}

// Init reads the cached board's agent states apart from the refresh, which waits on Jira and GitHub.
func (m Model) Init() tea.Cmd {
	cmds := []tea.Cmd{m.actions.Refresh(), tick(), tea.Tick(spinEvery, func(time.Time) tea.Msg { return spinMsg{} })}
	if !m.snap.FetchedAt.IsZero() {
		cmds = append(cmds, m.actions.Agents(m.snap))
	}
	return tea.Batch(cmds...)
}

// WithClock replaces the clock, for tests.
func (m Model) WithClock(now func() time.Time) Model {
	m.now = now
	return m
}

// effectiveView falls back to the list when columns would not fit or when showing pull requests, which
// have no column of their own.
func (m Model) effectiveView() string {
	if m.filter == FilterPRs || m.width < narrowWidth {
		return ViewList
	}
	return m.view
}

// explainList says why the board still shows the list after the user asked for columns.
func (m *Model) explainList() {
	switch {
	case m.view != ViewColumns || m.effectiveView() == ViewColumns:
	case m.filter == FilterPRs:
		m.status = "the PRs filter always shows a list · press 1 for all tickets"
	default:
		m.status = fmt.Sprintf("columns need a pane at least %d columns wide (this one has %d) · showing the list", narrowWidth, m.width)
	}
}

func (m Model) reviews() []ticket.PullRequest {
	if m.showBots {
		return m.snap.Reviews
	}
	return m.snap.WithoutBots().Reviews
}

func (m Model) hiddenBots() int { return len(m.snap.Reviews) - len(m.snap.WithoutBots().Reviews) }

// botToggleLine is listLines' marker for the reviews filter's "show bots" line, which a click toggles.
const botToggleLine = -2

func (m *Model) toggleBots() {
	m.showBots = !m.showBots
	m.reselect()
}

func (m Model) tickets() []ticket.Ticket {
	if m.filter == FilterPRs {
		return nil
	}
	return m.snap.Tickets
}

// myPRs lists the open pull requests of the user's tickets, those that need the user first.
func (m Model) myPRs() []item {
	var out []item
	for _, t := range board.Sorted(m.snap.Tickets) {
		for _, pr := range t.PRs {
			if pr.State == ticket.PROpen {
				t, pr := t, pr
				out = append(out, item{key: ticket.Key(fmt.Sprintf("#%d", pr.Number)), ticket: &t, pr: &pr})
			}
		}
	}
	slices.SortStableFunc(out, func(a, b item) int {
		return boolRank(needsYou(*b.pr)) - boolRank(needsYou(*a.pr))
	})
	return out
}

func needsYou(pr ticket.PullRequest) bool {
	return pr.Checks == ticket.ChecksFail || pr.Review == ticket.ReviewChangesRequested
}

func boolRank(b bool) int {
	if b {
		return 1
	}
	return 0
}

// prStatus words where one of the user's pull requests stands: what needs the user first, then the review,
// as the CI mark beside it already shows a running check.
func prStatus(pr ticket.PullRequest) string {
	switch {
	case pr.Checks == ticket.ChecksFail:
		return string(ticket.ReasonCIFailing)
	case pr.Review == ticket.ReviewChangesRequested:
		return string(ticket.ReasonChangesRequested)
	case pr.Review == ticket.ReviewApproved:
		return "Approved"
	case pr.Review == ticket.ReviewRequired:
		return "Waiting review"
	case pr.Checks == ticket.ChecksPending:
		return "CI running"
	}
	return "Open"
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
	if m.filter == FilterPRs {
		out = m.myPRs()
	}
	for _, t := range board.Sorted(m.tickets()) {
		t := t
		out = append(out, item{key: t.Key, ticket: &t})
	}
	for _, pr := range m.reviews() {
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
		m.width, m.height = max(0, msg.Width-2*margin), msg.Height
		m.reselect()
	case SettingsSavedMsg:
		return m.settingsSaved(msg)
	case ClonesFoundMsg, RepoFoundMsg, FolderPickedMsg, DirsListedMsg, ReposListedMsg, JiraFieldsListedMsg:
		return m.settingsLookup(msg)
	case SnapshotMsg:
		m.loading = false
		if msg.Err != nil {
			m.status = "could not refresh: " + msg.Err.Error()
			return m, nil
		}
		if msg.Snap.Agents == nil && msg.Snap.FetchedAt.Equal(m.snap.FetchedAt) {
			msg.Snap.Agents = m.snap.Agents
		}
		m.snap, m.stale = msg.Snap, msg.Stale
		if !msg.Quiet {
			m.status = ""
		}
		m.reselect()
	case AgentsMsg:
		if msg.FetchedAt.Equal(m.snap.FetchedAt) {
			m.snap.Agents = msg.Agents
		}
	case StatusMsg:
		m.status = string(msg)
	case StartChoicesMsg:
		return m.startChoices(msg)
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
			return m, tea.Batch(m.actions.Refresh(), tick(), m.startSpin())
		}
		return m, tick()
	case spinMsg:
		if !m.loading {
			m.spinning = false
			return m, nil
		}
		m.spin++
		return m, tea.Tick(spinEvery, func(time.Time) tea.Msg { return spinMsg{} })
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
	msg.X -= margin
	if m.settings != nil {
		return m.settingsMouse(msg)
	}
	if m.detail != nil && m.menu == nil && m.picker == nil {
		switch msg.Button {
		case tea.MouseButtonWheelUp:
			m.detail.offset = max(0, m.detail.offset-3)
		case tea.MouseButtonWheelDown:
			m.detail.offset = min(m.maxOffset(), m.detail.offset+3)
		case tea.MouseButtonLeft:
			if msg.Action == tea.MouseActionPress && m.onBack(msg.X, msg.Y) {
				m.detail = nil
			}
		}
		return m, nil
	}
	if m.picker != nil {
		return m.pickerMouse(msg)
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
	case tea.MouseButtonWheelUp, tea.MouseButtonWheelDown:
		d := 1
		if msg.Button == tea.MouseButtonWheelUp {
			d = -1
		}
		if c := msg.X / (m.columnWidth() + 2); m.effectiveView() == ViewColumns && !m.inStrip && c != m.col && c >= 0 && c < len(m.colOffset) {
			cols := m.columns()
			if c < len(cols) {
				capacity, _ := m.columnCapacity(m.columnsHeight(), len(cols[c]))
				m.colOffset[c] = clamp(m.columnOffset(c, len(cols[c]), capacity)+d, 0, len(cols[c])-capacity)
			}
			return m, nil
		}
		m.move(0, d)
		return m, nil
	case tea.MouseButtonLeft:
	default:
		return m, nil
	}
	if msg.Action != tea.MouseActionPress {
		return m, nil
	}
	headerLines := lineCount(m.header())
	if _, _, x, y, h, _ := m.headerLayout(); msg.Y >= y && msg.Y < y+h {
		m.clickHeader(msg.X - x)
		return m, nil
	}
	body := msg.Y - headerLines
	if body < 0 {
		return m, nil
	}
	if m.effectiveView() == ViewList {
		lines := strings.Split(strings.TrimRight(m.listView(), "\n"), "\n")
		_, at := m.listLines(func(k, s, a, p, t string) string { return "" }, func(item) string { return "" }, 0)
		avail := m.height - lineCount(m.header()) - lineCount(m.preview()+"\n"+m.footer())
		start := 0
		if avail > 0 && len(lines) > avail {
			start = windowStart(lines, avail)
		}
		if i := start + body; i >= 0 && i < len(at) && at[i] == botToggleLine {
			m.toggleBots()
		} else if i >= 0 && i < len(at) && at[i] >= 0 {
			m.row = at[i]
			m.remember()
			m.selectedByClick()
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
	// Inside a column box: top border, the column title and its rule, the "↑ more" line when cards are
	// hidden above, then one bordered card after another.
	first := 3
	r := -1
	if c >= 0 && c < len(cols) {
		capacity, _ := m.columnCapacity(m.columnsHeight(), len(cols[c]))
		off := m.columnOffset(c, len(cols[c]), capacity)
		if off > 0 {
			first++
		}
		if body >= first {
			if i := (body - first) / m.cardLines(); i < capacity {
				r = off + i
			}
		}
	}
	if r >= 0 && c >= 0 && c < len(cols) && r < len(cols[c]) {
		m.col, m.row = c, r
		m.inStrip = false
		m.remember()
		m.selectedByClick()
	}
	return m, nil
}

const doubleClickWindow = 400 * time.Millisecond

// selectedByClick opens the actions menu when this click lands on the item the previous one selected,
// soon enough to be a double click.
func (m *Model) selectedByClick() {
	it, ok := m.current()
	if !ok {
		return
	}
	now := m.now()
	if it.key == m.lastClick && now.Sub(m.lastClickAt) <= doubleClickWindow {
		m.lastClick = ""
		m.menu = m.buildMenu(it)
		return
	}
	m.lastClick, m.lastClickAt = it.key, now
}

func (m *Model) openMenu() {
	if it, ok := m.current(); ok {
		m.menu = m.buildMenu(it)
	}
}

// clickHeader switches filter or view when x, relative to where the controls start, falls on a button.
func (m *Model) clickHeader(x int) {
	_, _, _, _, _, spans := m.headerLayout()
	for _, sp := range spans {
		if x >= sp.start && x < sp.start+sp.width {
			if sp.filter != "" {
				m.filter = sp.filter
			} else {
				m.view = sp.view
			}
			m.reselect()
			m.explainList()
			return
		}
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
	if m.settings != nil {
		return m.settingsKey(k)
	}
	if m.picker != nil {
		return m.pickerKey(k)
	}
	if m.menu != nil {
		return m.menuKey(k)
	}
	if m.detail != nil {
		return m.detailKey(k)
	}
	if m.status != "loading…" {
		m.status = ""
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
		m.explainList()
	case "b":
		if m.filter == FilterPRs {
			m.toggleBots()
		}
	case "n", "r":
		if m.filter == FilterPRs {
			m.filter = FilterAll
		} else {
			m.filter = FilterPRs
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
	case "1", "2":
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
		return m, tea.Batch(m.actions.Refresh(), m.startSpin())
	case "enter", " ":
		m.openMenu()
	case ",":
		return m.openSettings()
	case "i":
		if it, ok := m.current(); ok && it.ticket != nil {
			return m.openDetail(it.ticket.Key)
		}
	case "o":
		if it, ok := m.current(); ok && it.ticket != nil && it.ticket.URL != "" {
			return m, m.actions.Open(it.ticket.URL)
		}
	case "O":
		if it, ok := m.current(); ok && it.ticket != nil && it.ticket.URL != "" {
			return m, m.actions.Copy(it.ticket.URL, "link")
		}
	case "p", "P":
		if it, ok := m.current(); ok {
			url := ""
			if it.pr != nil {
				url = it.pr.URL
			} else if it.ticket != nil && len(it.ticket.PRs) > 0 {
				url = it.ticket.PRs[0].URL
			}
			switch {
			case url == "":
				m.status = string(it.key) + " has no pull request yet"
			case k.String() == "P":
				return m, m.actions.Copy(url, "link")
			default:
				return m, m.actions.Open(url)
			}
		}
	case "f":
		if it, ok := m.current(); ok && it.ticket != nil {
			m.status = "sending feedback for " + string(it.ticket.Key) + "…"
			return m, m.actions.Fix(it.ticket.Key)
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
// margin is the left and right padding around the whole board, like the prototype's popup padding;
// m.width is the width inside it.
const margin = 1

func (m Model) View() string {
	pad := strings.Repeat(" ", margin)
	lines := strings.Split(m.board(), "\n")
	for i, l := range lines {
		lines[i] = pad + l
	}
	return strings.Join(lines, "\n")
}

func (m Model) board() string {
	top := m.header()
	if m.settings != nil {
		return top + "\n" + m.detailBack() + "\n\n" + m.settingsBody() + "\n\n" + m.settingsFooter()
	}
	if m.detail != nil && m.menu == nil && m.picker == nil {
		body := m.detailBody()
		bottom := m.detailFooter()
		lines := strings.Split(body, "\n")
		for len(lines) < m.detailHeight() {
			lines = append(lines, "")
		}
		return top + "\n" + m.detailBack() + "\n\n" + strings.Join(lines, "\n") + "\n\n" + bottom
	}
	var body string
	bottom0 := m.preview() + "\n" + m.footer()
	if m.picker != nil {
		height := m.height - lineCount(top) - lineCount(bottom0)
		body = lipgloss.Place(m.width, max(height, lipgloss.Height(m.pickerView())), lipgloss.Center, lipgloss.Center, m.pickerView())
	} else if m.menu != nil {
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
	start := windowStart(lines, n)
	return lines[start : start+n]
}

func windowStart(lines []string, n int) int {
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
	return start
}

func (m Model) header() string {
	title, controls, x, y, h, _ := m.headerLayout()
	var top string
	switch {
	case y == 0 && h == 1:
		top = title + strings.Repeat(" ", x-lipgloss.Width(title)) + controls
	case y == 0:
		top = lipgloss.JoinHorizontal(lipgloss.Top, lipgloss.NewStyle().PaddingTop(1).Render(title), strings.Repeat(" ", x-lipgloss.Width(title)), controls)
	default:
		top = title + "\n" + controls
	}
	top += "\n" + dim.Render(strings.Repeat("─", m.width))
	if len(m.snap.Warnings) > 0 {
		top += "\n" + warn.Render(truncate("! "+strings.Join(m.snap.Warnings, " · "), m.width))
	}
	return top
}

type headerSpan struct {
	start, width int
	filter, view string
}

var (
	filterOff = box.Foreground(lipgloss.Color("250"))
	filterOn  = boxSel.Foreground(lipgloss.Color("153")).Bold(true)
)

// headerLayout places the filter and view buttons, boxed as in the prototype, right of the title; on a
// narrower pane they move to their own row, and unboxed when even that is too narrow.
// x and y say where the controls start, h how many rows they take, spans where each button sits from x.
func (m Model) headerLayout() (title, controls string, x, y, h int, spans []headerSpan) {
	parts := []string{bold.Render("tkt")}
	for _, sp := range m.snap.Sprints {
		parts = append(parts, sp.Space+" · "+board.SprintLabel(sp, m.snap.FetchedAt))
	}
	title = strings.Join(parts, " · ")
	var row []string
	pos := 0
	for i, it := range m.headerSpec() {
		gap := map[bool]int{true: 1, false: 0}[i > 0]
		if i == len(filterOrder) {
			gap = 2
		}
		row = append(row, strings.Repeat(" ", gap))
		pos += gap
		st := filterOff
		if (it.filter != "" && it.filter == m.filter) || (it.view != "" && it.view == m.effectiveView()) {
			st = filterOn
		}
		chip := st.Render(strings.TrimSpace(it.label))
		spans = append(spans, headerSpan{start: pos, width: lipgloss.Width(chip), filter: it.filter, view: it.view})
		row = append(row, chip)
		pos += lipgloss.Width(chip)
	}
	controls = lipgloss.JoinHorizontal(lipgloss.Top, row...)
	cw := lipgloss.Width(controls)
	switch {
	case lipgloss.Width(title)+2+cw <= m.width:
		return title, controls, m.width - cw, 0, 3, spans
	case cw <= m.width:
		return truncate(title, m.width), controls, 0, 1, 3, spans
	}
	items := m.headerItems()
	spans = spans[:0]
	pos = 0
	for i, it := range m.headerSpec() {
		w := lipgloss.Width(it.label)
		if i < len(filterOrder) {
			w = lipgloss.Width(items[i])
		}
		if pos+w > m.width {
			break
		}
		spans = append(spans, headerSpan{start: pos, width: w, filter: it.filter, view: it.view})
		pos += w
		if i < len(filterOrder)-1 {
			pos += 2
		} else if i == len(filterOrder)-1 {
			pos += 3
		}
	}
	return truncate(title, m.width), wrap(items, m.width), 0, 1, 1, spans
}

type headerItem struct {
	filter, view string
	label        string
}

func (m Model) headerSpec() []headerItem {
	return []headerItem{
		{filter: FilterAll, label: fmt.Sprintf(" 1 All %d ", len(m.snap.Tickets)+len(m.reviews()))},
		{filter: FilterPRs, label: fmt.Sprintf(" 2 PRs %d ", len(m.myPRs())+len(m.reviews()))},
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

// syncLabel says how fresh the board is: refreshing (with a spinner), synced N min ago, or cached when the
// last refresh failed.
func (m Model) syncLabel() string {
	switch {
	case m.loading:
		return spinFrames[m.spin%len(spinFrames)] + " refreshing…"
	case m.snap.FetchedAt.IsZero():
		return "not synced yet"
	case m.stale != nil:
		return "! cached · " + m.snap.FetchedAt.Local().Format("15:04")
	}
	d := m.now().Sub(m.snap.FetchedAt)
	switch {
	case d < time.Minute:
		return "✓ synced just now"
	case d < time.Hour:
		return fmt.Sprintf("✓ synced %d min ago", int(d.Minutes()))
	}
	return "✓ synced " + m.snap.FetchedAt.Local().Format("15:04")
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
	reviews := m.reviews()
	if len(reviews) == 0 || m.filter == FilterPRs {
		return "", nil
	}
	label := dim.Render("Review requested")
	row := []string{lipgloss.NewStyle().PaddingTop(1).Render(label)}
	used := lipgloss.Width(label)
	var spans []chipSpan
	for i, pr := range reviews {
		text := fmt.Sprintf(" #%d", pr.Number)
		if keys := ticket.BranchKeys(pr.Branch); len(keys) > 0 {
			text += " · " + string(keys[0])
		}
		chip := reviewChip.Render(warn.Render("!") + text)
		if m.inStrip && len(spans) == m.stripSel {
			chip = reviewChip.BorderForeground(lipgloss.Color("111")).Render(selected.Render("▸" + text))
		}
		// A chip only fits if the "+N more" label for the chips after it still fits beside it.
		need := used + 2 + lipgloss.Width(chip)
		if rest := len(reviews) - i - 1; rest > 0 {
			need += 1 + lipgloss.Width(moreReviews(rest))
		}
		if need > m.width {
			row = append(row, " ", lipgloss.NewStyle().PaddingTop(1).Render(dim.Render(moreReviews(len(reviews)-i))))
			break
		}
		row = append(row, "  ", chip)
		spans = append(spans, chipSpan{start: used + 2, width: lipgloss.Width(chip), pr: pr})
		used += 2 + lipgloss.Width(chip)
	}
	return lipgloss.JoinHorizontal(lipgloss.Top, row...), spans
}

func moreReviews(n int) string { return fmt.Sprintf("+%d more · press 2", n) }

// cardLines is how many lines one bordered card takes: two border lines plus its content lines.
func (m Model) cardLines() int {
	if m.compact {
		return 4
	}
	return 7
}

// columnsView draws the four columns as boxes reaching height lines, each ticket in its own card.
// columnsHeight is the height the column boxes get between the header, the review strip and the legend.
func (m Model) columnsHeight() int {
	h := m.height - lineCount(m.header()) - lineCount(m.preview()+"\n"+m.footer())
	if strip := m.reviewStrip(); strip != "" {
		h -= lineCount(strip)
	}
	return h
}

// columnCapacity says how many of n cards fit in a column box of height lines; when they do not all
// fit, a line above and below is kept for the "more" markers.
func (m Model) columnCapacity(height, n int) (int, bool) {
	inner := height - 4
	if n == 0 || n*m.cardLines() <= inner {
		return n, false
	}
	return max(1, (inner-2)/m.cardLines()), true
}

// columnOffset is the first visible card of column ci: its own scroll, moved just enough to keep the
// selected card visible when the selection is in that column.
func (m Model) columnOffset(ci, n, capacity int) int {
	off := 0
	if ci < len(m.colOffset) {
		off = m.colOffset[ci]
	}
	if ci == m.col && !m.inStrip {
		off = min(off, m.row)
		off = max(off, m.row-capacity+1)
	}
	return clamp(off, 0, n-capacity)
}

func (m Model) columnsView(height int) string {
	cols := m.columns()
	inner := m.columnWidth()
	rendered := make([]string, len(cols))
	for ci, c := range ticket.Columns {
		parts := columnHead(c.Title(), len(cols[ci]), inner)
		if len(cols[ci]) == 0 {
			parts = append(parts, " "+dim.Render("— nothing here"))
		}
		n := len(cols[ci])
		capacity, scroll := m.columnCapacity(height, n)
		off := m.columnOffset(ci, n, capacity)
		end := min(n, off+capacity)
		if off > 0 {
			parts = append(parts, moreLine("↑", off))
		}
		for ri := off; ri < end; ri++ {
			parts = append(parts, card(*cols[ci][ri].ticket, inner, ci == m.col && ri == m.row && !m.inStrip, m.compact, m.snap.Agents[string(cols[ci][ri].key)]))
		}
		if scroll {
			parts = append(parts, moreLine("↓", n-end))
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

func moreLine(arrow string, n int) string {
	if n <= 0 {
		return ""
	}
	return dim.Render(fmt.Sprintf(" %s %d more", arrow, n))
}

// columnHead is a column's header: name left, count right, a rule below. Every column looks the same;
// the box border alone shows where the selection is.
func columnHead(name string, count, width int) []string {
	n := strconv.Itoa(count)
	line := " " + name + strings.Repeat(" ", max(1, width-2-lipgloss.Width(name)-len(n))) + n + " "
	return []string{bold.Render(line), dim.Render(strings.Repeat("─", width))}
}

var (
	cardBox    = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(lipgloss.Color("238"))
	cardBoxSel = cardBox.BorderForeground(lipgloss.Color("111"))
)

// card renders one ticket in its own bordered box of the given outer width.
var (
	cardBoxWarn = cardBox.BorderForeground(lipgloss.Color("130"))
	blocked     = lipgloss.NewStyle().Foreground(lipgloss.Color("203"))
	titleStyle  = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("255"))
	keyDim      = lipgloss.NewStyle().Foreground(lipgloss.Color("250"))
)

// card renders one ticket in its own bordered box: key and attention flag, title, then (unless compact)
// its pull requests with colored CI marks and a dimmed line with type and subtasks.
func card(t ticket.Ticket, outer int, sel, compact bool, agent string) string {
	width := outer - 2
	inner := width - 2
	head := keyDim.Render(string(t.Key))
	if sel {
		head = selected.Render("▸ " + string(t.Key))
	}
	flag := ""
	if r := t.Attention(); len(r) > 0 {
		flag = warn.Render("! " + string(r[0]))
	}
	if t.Blocked() {
		// The word gives way to the icon alone when another flag is there or the card is too narrow for it.
		switch word := blocked.Render("⊘ blocked"); {
		case flag != "":
			flag += " " + blocked.Render("⊘")
		case lipgloss.Width(head)+1+lipgloss.Width(word) <= inner:
			flag = word
		default:
			flag = blocked.Render("⊘")
		}
	}
	gap := max(1, inner-lipgloss.Width(head)-lipgloss.Width(flag))
	lines := []string{head + strings.Repeat(" ", gap) + flag}
	titleLines := 1
	if !compact {
		titleLines = 2
	}
	for _, l := range wrapWords(t.Summary, inner, titleLines) {
		lines = append(lines, titleStyle.Render(l))
	}
	if !compact {
		lines = append(lines, coloredPRs(t.PRs, inner))
		info := t.Type
		if len(t.Subtasks) > 0 {
			info = strings.TrimSpace(fmt.Sprintf("%s · %d subtasks", info, len(t.Subtasks)))
		}
		if info == "" {
			info = t.Status
		}
		// The agent shares the type's line, which has room to spare, so the pull requests keep theirs;
		// when both do not fit, the type gives way.
		if text, kind := agentShort(agent); kind != board.AgentNone {
			room := max(1, inner-lipgloss.Width(text)-3)
			lines = append(lines, dim.Render(truncate(info, room)+" · ")+agentStyle(kind).Render(text))
		} else {
			lines = append(lines, dim.Render(truncate(info, inner)))
		}
	}
	for i, l := range lines {
		lines[i] = " " + l
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
	statusW := statusWidth
	agentW := 0
	if len(m.snap.Agents) > 0 {
		agentW = agentWidth + 1
	}
	// Narrow panes drop AGENT first, then shrink PR / CI, then STATUS, so the title always keeps some room.
	const minTitle = 12
	room := func() int { return m.width - 2 - keyWidth - statusW - agentW - prW - 3 }
	if minTitle > room() {
		agentW = 0
	}
	if short := minTitle - room(); short > 0 {
		cut := max(0, min(short, prW-8))
		prW -= cut
		statusW -= min(short-cut, statusW-10)
	}
	titleWidth := max(minTitle, room())
	row := func(key, status, agent, pr, title string) string {
		return pad(key, keyWidth) + " " + pad(status, statusW) + " " + agent + pad(pr, prW) + " " + truncate(title, titleWidth)
	}
	lines, _ := m.listLines(row, prText, agentW)
	return strings.Join(lines, "\n") + "\n"
}

// agentWidth fits the longest agent cell, "! needs you".
const agentWidth = 11

// agentCell words a ticket's agent for the list, padded to agentW (which includes its gap) and colored
// like the cards unless plain, as on the selected row, whose highlight a color would break.
func agentCell(status string, agentW int, plain bool) string {
	if agentW == 0 {
		return ""
	}
	text, kind := agentShort(status)
	cell := pad(text, agentW)
	if plain || kind == board.AgentNone {
		return cell
	}
	return agentStyle(kind).Render(cell)
}

// agentShort words an agent status without the agent's name, as the list and cards have little room;
// it is empty when no agent runs.
func agentShort(status string) (string, board.AgentKind) {
	line, kind := board.AgentLine(status)
	if kind == board.AgentNone {
		return "", kind
	}
	return strings.Replace(line, "claude ", "", 1), kind
}

func agentStyle(kind board.AgentKind) lipgloss.Style {
	return map[board.AgentKind]lipgloss.Style{board.AgentWorking: keyStyle, board.AgentNeedsYou: warn, board.AgentIdle: dim}[kind]
}

// listLines lays the list out in sections, the user's tickets and review requests per repo, and returns for
// every line the index of the item on it (-1 for titles and spacing, botToggleLine for the bots toggle), so
// clicks map back to items.
func (m Model) listLines(row func(key, status, agent, pr, title string) string, prText func(item) string, agentW int) ([]string, []int) {
	items := m.listItems()
	header := pad("", agentW)
	if agentW > 0 {
		header = pad("AGENT", agentW)
	}
	lines := []string{"  " + dim.Render(row("KEY", "STATUS", header, "PR / CI", "TITLE")), dim.Render(strings.Repeat("─", m.width))}
	at := []int{-1, -1}
	if m.filter == FilterPRs && m.hiddenBots() > 0 {
		toggle := keyStyle.Render("b") + " " + dim.Render(fmt.Sprintf("show bots · %d hidden", m.hiddenBots()))
		if m.showBots {
			toggle = keyStyle.Render("b") + " " + dim.Render("hide bots")
		}
		lines, at = append(lines, "  "+toggle), append(at, botToggleLine)
	}
	nTickets, nMine := 0, 0
	for _, it := range items {
		switch {
		case it.mine():
			nMine++
		case it.ticket != nil:
			nTickets++
		}
	}
	section := func(title string, n int) {
		if len(lines) > 2 {
			lines, at = append(lines, ""), append(at, -1)
		}
		lines = append(lines, bold.Render(title)+"  "+dim.Render(strconv.Itoa(n)))
		at = append(at, -1)
	}
	for i, it := range items {
		switch {
		case i == 0 && it.mine():
			section("Mine", nMine)
		case i == 0 && it.ticket != nil:
			section("My tickets", nTickets)
		}
		if it.review() && (i == 0 || !items[i-1].review() || items[i-1].pr.Repo != it.pr.Repo) {
			n := 0
			for _, o := range items[i:] {
				if o.review() && o.pr.Repo == it.pr.Repo {
					n++
				}
			}
			section("Review requested · "+it.pr.Repo, n)
		}
		var line string
		switch {
		case it.review():
			line = row(string(it.key), "Review requested", pad("", agentW), prText(it), it.pr.Title)
		case it.mine():
			status := prStatus(*it.pr)
			if i != m.row && needsYou(*it.pr) {
				status = warn.Render(status)
			}
			title := cmp.Or(it.pr.Title, it.ticket.Summary)
			line = row(string(it.key), status, agentCell(m.snap.Agents[string(it.ticket.Key)], agentW, i == m.row), prText(it), string(it.ticket.Key)+" · "+title)
		default:
			line = row(string(it.key), it.ticket.Status, agentCell(m.snap.Agents[string(it.key)], agentW, i == m.row), prText(it), it.ticket.Summary)
		}
		switch {
		case i == m.row:
			line = "▸ " + selected.Render(line)
		case it.review(), it.mine() && needsYou(*it.pr):
			line = warn.Render("!") + " " + line
		case it.mine():
			line = "  " + line
		case len(it.ticket.Attention()) > 0 && it.ticket.Blocked():
			// The wider mark takes one cell of the key's padding, so the columns stay aligned.
			line = warn.Render("!") + blocked.Render("⊘") + " " + strings.Replace(line, string(it.key)+" ", string(it.key), 1)
		case len(it.ticket.Attention()) > 0:
			line = warn.Render("!") + " " + line
		case it.ticket.Blocked():
			line = blocked.Render("⊘") + " " + line
		default:
			line = "  " + line
		}
		lines, at = append(lines, line), append(at, i)
	}
	return lines, at
}

func (m Model) preview() string {
	if m.status != "" {
		return ansi.Truncate(m.status, m.width, "…")
	}
	it, okSel := m.current()
	if !okSel {
		return dim.Render("nothing selected")
	}
	if it.mine() {
		title := titleStyle.Render(truncate(fmt.Sprintf("%s · %s · %s", it.key, it.ticket.Key, cmp.Or(it.pr.Title, it.ticket.Summary)), m.width))
		return title + "\n" + dim.Render(truncate(fmt.Sprintf("%s in %s · CI %s · enter for actions", prStatus(*it.pr), it.pr.Repo, ciWord(it.pr.Checks)), m.width))
	}
	if it.pr != nil {
		title := titleStyle.Render(truncate(fmt.Sprintf("%s · %s", it.key, it.pr.Title), m.width))
		return title + "\n" + dim.Render(truncate(fmt.Sprintf("review requested by %s in %s · CI %s · enter for actions", orDash(it.pr.Author), it.pr.Repo, ciWord(it.pr.Checks)), m.width))
	}
	t := it.ticket
	title := titleStyle.Render(truncate(fmt.Sprintf("%s · %s", t.Key, t.Summary), m.width))
	parts := []string{t.Status}
	if t.Label != "" {
		parts = append([]string{t.Label}, parts...)
	}
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
	moveHint, clickHint := "move · ↑ to reviews", "select · open review"
	if m.filter == FilterPRs {
		moveHint, clickHint = "move", "select · open review"
	}
	legend := [][2]string{
		{move, moveHint}, {"click", clickHint}, {"double-click", "actions"}, {"enter", "actions"}, {"f", "fix CI"}, {"i", "ticket pane"},
		{"o", "Jira ↗"}, {"p", "PR ↗"}, {"O P", "copy link"}, {"tab 1-2", "filter"}, {"v", "view"}, {",", "settings"}, {"z", "density"}, {"g", "refresh"}, {"q", "close"},
	}
	if m.filter == FilterPRs && m.hiddenBots() > 0 {
		legend = append(legend[:1], append([][2]string{{"b", "bots"}}, legend[1:]...)...)
	}
	items := make([]string, len(legend))
	for i, k := range legend {
		items[i] = keyStyle.Render(k[0]) + " " + dim.Render(k[1])
	}
	sync := dim.Render(m.syncLabel())
	if m.stale != nil {
		sync = warn.Render(m.syncLabel())
	}
	// The sync state sits at the bottom right: on the legend's last line when it fits, else on its own.
	lines := strings.Split(wrapSep(items, m.width, "    "), "\n")
	last := lines[len(lines)-1]
	if gap := m.width - lipgloss.Width(last) - lipgloss.Width(sync); gap >= 4 {
		lines[len(lines)-1] = last + strings.Repeat(" ", gap) + sync
	} else {
		lines = append(lines, strings.Repeat(" ", max(0, m.width-lipgloss.Width(sync)))+sync)
	}
	return strings.Join(lines, "\n")
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

// truncate cuts s to n cells with "…", keeping color codes whole: a cut through one shows as garbage.
func truncate(s string, n int) string {
	if n <= 1 || ansi.StringWidth(s) <= n {
		return s
	}
	return ansi.Truncate(s, n, "…")
}

func orDash(s string) string {
	if s == "" {
		return "—"
	}
	return s
}
