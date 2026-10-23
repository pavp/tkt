// Package tui is the interactive board shown in the Herdr popup: columns or list, filters, and actions.
package tui

import (
	"cmp"
	"fmt"
	"maps"
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
	// Fix sends a ticket's feedback to its agent; known, when set, is the board's copy of the ticket, whose
	// pull requests need no fetching. Steps arrive as StatusMsgs while it works.
	Fix(key ticket.Key, known *ticket.Ticket) tea.Cmd
	// Copy puts text on the clipboard; what names it in the confirmation, such as "key".
	Copy(text, what string) tea.Cmd
	CopyContext(key ticket.Key) tea.Cmd
	// Detail renders a ticket for reading inside the board; the result arrives as a DetailMsg.
	// known, when set, is the board's own copy of the ticket, whose pull requests need no fetching.
	Detail(key ticket.Key, known *ticket.Ticket) tea.Cmd
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
	// JiraBoards lists a space's scrum boards; they arrive as a JiraBoardsListedMsg.
	JiraBoards(space string) tea.Cmd
	// JiraStatuses lists a space's statuses; they arrive as a JiraStatusesListedMsg.
	JiraStatuses(space string) tea.Cmd
	// NativePicker reports whether PickFolder can show a folder chooser the user will see; without one,
	// ctrl+o browses folders inside the terminal.
	NativePicker() bool
	// SaveState remembers the board's state for its next opening; a failure arrives as a StatusMsg.
	SaveState(s BoardState) tea.Cmd
	// Transitions lists the statuses key can move to in Jira; they arrive as a TransitionsMsg.
	Transitions(key ticket.Key) tea.Cmd
	// Transition moves key in Jira through transition id to status to, filling its form with values and
	// adding comment when given; the outcome arrives as a TransitionedMsg. The board calls it only after the
	// user confirmed.
	Transition(key ticket.Key, id, to string, values []Value, comment string) tea.Cmd
	// Assign gives key to who in Jira; the outcome arrives as an AssignedMsg. The board calls it only after
	// the user confirmed.
	Assign(key ticket.Key, who Assignee) tea.Cmd
	// Users lists the users assignable to key that match query; they arrive as a UsersListedMsg.
	Users(key ticket.Key, query string) tea.Cmd
}

// BoardState is what the board remembers between openings.
type BoardState struct {
	// HiddenRepos lists the repositories ("owner/name") whose pull requests and tickets the board hides.
	HiddenRepos []string `json:"hidden_repos,omitempty"`
	// Collapsed lists the PRs filter's collapsed groups, such as "mine:owner/name" or "review:owner/name".
	Collapsed []string `json:"collapsed,omitempty"`
	// View is the view the user last chose, ViewColumns or ViewList.
	View string `json:"view,omitempty"`
	// Team asks the sync for the team's tickets too, those assigned to others or to nobody.
	Team bool `json:"team,omitempty"`
	// TeamPRs shows everyone else's open pull requests in the PRs filter; unset, it follows Team, as
	// one toggle did both before.
	TeamPRs *bool `json:"team_prs,omitempty"`
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
	// group, set on a group's title row in the PRs filter, names the group; such a row has no ticket or
	// pull request, only its title, how many pull requests it holds and how many of them need the user.
	group, title string
	count, needs int
	// parent, set on a subtask's own row or card, names the ticket it belongs to.
	parent ticket.Key
	// other marks someone else's pull request that asks nothing of the user, listed while Team is on.
	other bool
}

func (it item) review() bool  { return it.pr != nil && it.ticket == nil && !it.other }
func (it item) heading() bool { return it.group != "" }
func (it item) mine() bool    { return it.pr != nil && it.ticket != nil }

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
	// hiddenRepos holds the repositories ("owner/name") the user chose not to see.
	hiddenRepos map[string]bool
	// collapsed holds the PRs filter's collapsed groups.
	collapsed map[string]bool
	// team shows the team's tickets beside the user's.
	team bool
	// teamPRs shows every open pull request in the PRs filter, which its Requested toggle (lit while off)
	// narrows to the review requests; apart from team, which asks for the team's tickets.
	teamPRs  bool
	menu     *menu
	prompt   *formPrompt
	detail   *detailView
	settings *settingsView
	picker   *picker
	// colOffset is how far each column is scrolled; an array so copies of the model never share it.
	colOffset [8]int
	// inStrip means the keyboard cursor is on the review chips above the columns, at stripSel.
	inStrip  bool
	stripSel int
	// spin is the refreshing spinner's frame; spinning says its tick is already running.
	spin         int
	spinning     bool
	loadingSince time.Time
	// showBots also lists review requests on pull requests opened by bots.
	showBots bool
	// moves keeps the statuses each ticket could move to, asked when its menu opened, so Move to… opens
	// at once; shared by the model's copies and only touched in Update.
	moves map[ticket.Key]knownMoves
}

// tickMsg drives the "synced … ago" label and the periodic refresh.
type tickMsg time.Time

// spinMsg advances the refreshing spinner; it stops ticking once the board arrives.
type spinMsg struct{}

// SyncingMsg says the daemon started a sync; SyncFailedMsg that one failed. A finished sync arrives as
// a SnapshotMsg.
type SyncingMsg struct{}

type SyncFailedMsg struct{ Message string }

// loadingLimit bounds how long the board waits for a refresh before it stops saying it is refreshing.
const loadingLimit = 90 * time.Second

func (m *Model) beginLoading() tea.Cmd {
	m.loading, m.loadingSince = true, m.now()
	return m.startSpin()
}

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
	m := Model{snap: snap, loading: true, spinning: true, loadingSince: time.Now(), actions: actions, view: view, filter: FilterAll, width: 160, height: 40, now: time.Now,
		moves: map[ticket.Key]knownMoves{}}
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

// WithState restores what the board remembered from its last opening.
func (m Model) WithState(s BoardState) Model {
	m.hiddenRepos, m.collapsed = map[string]bool{}, map[string]bool{}
	for _, r := range s.HiddenRepos {
		m.hiddenRepos[r] = true
	}
	for _, g := range s.Collapsed {
		m.collapsed[g] = true
	}
	m.team, m.teamPRs = s.Team, s.Team
	if s.TeamPRs != nil {
		m.teamPRs = *s.TeamPRs
	}
	return m
}

// state is what the board remembers for its next opening.
func (m Model) state() BoardState {
	teamPRs := m.teamPRs
	return BoardState{HiddenRepos: trueKeys(m.hiddenRepos), Collapsed: trueKeys(m.collapsed), View: m.view, Team: m.team, TeamPRs: &teamPRs}
}

func trueKeys(set map[string]bool) []string {
	var out []string
	for k, on := range set {
		if on {
			out = append(out, k)
		}
	}
	slices.Sort(out)
	return out
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
	reviews := m.snap.Reviews
	if !m.showBots {
		reviews = m.snap.WithoutBots().Reviews
	}
	return slices.DeleteFunc(slices.Clone(reviews), func(pr ticket.PullRequest) bool { return m.repoHidden(pr.Repo) })
}

// prCount counts the PRs filter's pull requests: the user's, the review requests and, while shown, the team's.
func (m Model) prCount() int {
	n := len(m.myPRs()) + len(m.reviews())
	if m.teamPRs {
		n += len(m.others())
	}
	return n
}

// others are the open pull requests of everyone else, listed while Team is on, filtered as reviews are.
func (m Model) others() []ticket.PullRequest {
	others := m.snap.Others
	if !m.showBots {
		others = m.snap.WithoutBots().Others
	}
	return slices.DeleteFunc(slices.Clone(others), func(pr ticket.PullRequest) bool { return m.repoHidden(pr.Repo) })
}

// repoHidden reports whether the user hid repo; a pull request whose repository is unknown always shows.
func (m Model) repoHidden(repo string) bool { return repo != "" && m.hiddenRepos[repo] }

// ticketShown hides a ticket only when every one of its pull requests is in a hidden repository, as a ticket
// without pull requests belongs to no repository.
func (m Model) ticketShown(t ticket.Ticket) bool {
	return len(t.PRs) == 0 || slices.ContainsFunc(t.PRs, func(pr ticket.PullRequest) bool { return !m.repoHidden(pr.Repo) })
}

// shownTickets are the user's tickets the repository filter leaves.
// Team tickets show only while the team is on, after the user's own.
func (m Model) shownTickets() []ticket.Ticket {
	var mine, team []ticket.Ticket
	for _, t := range m.snap.Tickets {
		switch {
		case !m.ticketShown(t):
		case t.Team && m.team:
			team = append(team, t)
		case !t.Team:
			mine = append(mine, t)
		}
	}
	return append(mine, team...)
}

// boardRepos counts, per repository the board knows (the configured ones and any with pull requests), its
// open pull requests and review requests; the repository filter offers them.
func (m Model) boardRepos() map[string]int {
	repos := map[string]int{}
	add := func(pr ticket.PullRequest) {
		if pr.Repo == "" {
			return
		}
		if _, ok := repos[pr.Repo]; !ok {
			repos[pr.Repo] = 0
		}
		if pr.State == ticket.PROpen || pr.State == "" {
			repos[pr.Repo]++
		}
	}
	for _, t := range m.snap.Tickets {
		for _, pr := range t.PRs {
			add(pr)
		}
	}
	for _, pr := range m.snap.Reviews {
		add(pr)
	}
	cfg := m.actions.Config()
	for _, r := range cfg.Repos() {
		add(ticket.PullRequest{Repo: r.Name, State: ticket.PRMerged})
	}
	return repos
}

// repoNotice says how many repositories the filter leaves; it is empty while every one shows.
func (m Model) repoNotice() string {
	repos := m.boardRepos()
	hidden := 0
	for r := range repos {
		if m.repoHidden(r) {
			hidden++
		}
	}
	if hidden == 0 {
		return ""
	}
	return dim.Render(fmt.Sprintf("Showing %d of %d repositories · ", len(repos)-hidden, len(repos))) + keyStyle.Render("R") + dim.Render(" change")
}

func (m Model) hiddenBots() int {
	shown := m.snap.WithoutBots()
	return len(m.snap.Reviews) + len(m.snap.Others) - len(shown.Reviews) - len(shown.Others)
}

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
	return m.shownTickets()
}

// myPRs lists the open pull requests of the user's tickets by repository, repositories with one that needs
// the user first, then by name, and within each repository those that need the user first.
func (m Model) myPRs() []item {
	var out []item
	for _, t := range board.Sorted(m.snap.Tickets) {
		if t.Team {
			continue
		}
		for _, pr := range t.PRs {
			if pr.State == ticket.PROpen && !m.repoHidden(pr.Repo) {
				t, pr := t, pr
				out = append(out, item{key: ticket.Key(fmt.Sprintf("#%d", pr.Number)), ticket: &t, pr: &pr})
			}
		}
	}
	urgent := map[string]bool{}
	for _, it := range out {
		urgent[it.pr.Repo] = urgent[it.pr.Repo] || needsYou(*it.pr)
	}
	slices.SortStableFunc(out, func(a, b item) int {
		if c := boolRank(urgent[b.pr.Repo]) - boolRank(urgent[a.pr.Repo]); c != 0 {
			return c
		}
		if c := strings.Compare(a.pr.Repo, b.pr.Repo); c != 0 {
			return c
		}
		return boolRank(needsYou(*b.pr)) - boolRank(needsYou(*a.pr))
	})
	return out
}

// prItems lays the PRs filter out in groups, the user's pull requests per repository then the review
// requests per repository, each a title row followed by its pull requests unless the group is collapsed.
func (m Model) prItems() []item { return m.prLayout(false) }

// prLayout builds prItems; with every true it keeps collapsed groups' rows, which column widths measure so
// that collapsing a group never moves the columns.
func (m Model) prLayout(every bool) []item {
	var out []item
	add := func(group, title string, rows []item) {
		needs := 0
		for _, it := range rows {
			if it.review() || it.mine() && needsYou(*it.pr) {
				needs++
			}
		}
		out = append(out, item{key: ticket.Key("group:" + group), group: group, title: title, count: len(rows), needs: needs})
		if every || !m.collapsed[group] {
			out = append(out, rows...)
		}
	}
	mine := m.myPRs()
	for i := 0; i < len(mine); {
		j, repo := i, mine[i].pr.Repo
		for j < len(mine) && mine[j].pr.Repo == repo {
			j++
		}
		title := "Mine"
		if repo != "" {
			title += " · " + repo
		}
		add("mine:"+repo, title, mine[i:j])
		i = j
	}
	// Showing every pull request, review requests join the others in their repository's group, first and still
	// marked; with Requested on they have groups of their own.
	var rest []item
	for _, pr := range m.reviews() {
		rest = append(rest, item{key: ticket.Key(fmt.Sprintf("#%d", pr.Number)), pr: &pr})
	}
	group, title := "review:", "Review requested · "
	if m.teamPRs {
		for _, pr := range m.others() {
			rest = append(rest, item{key: ticket.Key(fmt.Sprintf("#%d", pr.Number)), pr: &pr, other: true})
		}
		group, title = "team:", ""
	}
	slices.SortStableFunc(rest, func(a, b item) int { return strings.Compare(a.pr.Repo, b.pr.Repo) })
	for i := 0; i < len(rest); {
		j, repo := i, rest[i].pr.Repo
		for j < len(rest) && rest[j].pr.Repo == repo {
			j++
		}
		add(group+repo, title+repo, rest[i:j])
		i = j
	}
	return out
}

// groupTitle renders a group's title row: ▾ while open, ▸ with how many need the user once collapsed.
func (m Model) groupTitle(it item, sel bool) string {
	mark, count := "▾ ", strconv.Itoa(it.count)
	if m.collapsed[it.group] {
		mark = "▸ "
		if it.needs > 0 {
			count += fmt.Sprintf(" · %d needs you", it.needs)
		}
	}
	if sel {
		return selected.Render(mark + it.title + "  " + count)
	}
	return mark + bold.Render(it.title) + "  " + dim.Render(count)
}

// hasTeamBoards reports whether any space names the boards the team comes from.
func (m Model) hasTeamBoards() bool {
	c := m.actions.Config()
	return slices.ContainsFunc(c.Spaces, func(s config.Space) bool { return len(s.TeamBoards) > 0 })
}

// toggleTeam shows or hides the team's tickets; turning them on saves the choice before the refresh, since
// the sync reads it to know whether to fetch them.
// teamButton names the toggle of the filter in view: Team for the team's tickets, Requested in the PRs
// filter, which narrows the pull requests to those asking for the user's review.
func (m Model) teamButton() string {
	if m.filter == FilterPRs {
		return "Requested"
	}
	return "Team"
}

// teamButtonOn reports whether the toggle in view is lit; Requested is lit while it narrows the list.
func (m Model) teamButtonOn() bool {
	if m.filter == FilterPRs {
		return !m.teamPRs
	}
	return m.team
}

func (m *Model) toggleTeam() tea.Cmd {
	if m.filter == FilterPRs {
		// Every open pull request comes with each sync, so showing them needs no refresh.
		m.teamPRs = !m.teamPRs
		m.reselect()
		m.status = "showing only your pull requests and review requests · t shows all"
		if m.teamPRs {
			m.status = "showing every open pull request in your repositories · t shows only requests"
		}
		return m.actions.SaveState(m.state())
	}
	m.team = !m.team
	m.reselect()
	save := m.actions.SaveState(m.state())
	if !m.team {
		m.status = "showing only your tickets"
		return save
	}
	m.status = "showing the team's tickets too · t hides them"
	if !m.hasTeamBoards() {
		m.status = "no team boards yet · add team boards in Settings (,)"
	}
	return tea.Batch(tea.Sequence(save, m.actions.Refresh()), m.beginLoading())
}

// toggleGroup collapses or expands a group and remembers it.
func (m *Model) toggleGroup(group string) tea.Cmd {
	// A copy, as models are values and an earlier copy must keep its own groups.
	m.collapsed = maps.Clone(m.collapsed)
	if m.collapsed == nil {
		m.collapsed = map[string]bool{}
	}
	m.collapsed[group] = !m.collapsed[group]
	m.reselect()
	return m.actions.SaveState(m.state())
}

func needsYou(pr ticket.PullRequest) bool {
	return len(pr.Attention()) > 0
}

func boolRank(b bool) int {
	if b {
		return 1
	}
	return 0
}

// prStatus words where one of the user's pull requests stands: what needs the user first, then whether it
// can merge, then the review, as the CI mark beside it already shows a running check.
func prStatus(pr ticket.PullRequest) string {
	switch {
	case pr.Checks == ticket.ChecksFail:
		return string(ticket.ReasonCIFailing)
	case pr.Review == ticket.ReviewChangesRequested:
		return string(ticket.ReasonChangesRequested)
	case pr.Merge == ticket.MergeConflicts:
		return string(ticket.ReasonConflicts)
	case pr.Unresolved > 0:
		return fmt.Sprintf("%d unresolved", pr.Unresolved)
	case pr.Draft:
		return "Draft"
	case pr.Merge == ticket.MergeReady:
		return "Ready to merge"
	case pr.Review == ticket.ReviewApproved:
		return "Approved"
	case pr.Review == ticket.ReviewRequired:
		return "Waiting review"
	case pr.Checks == ticket.ChecksPending:
		return "CI running"
	}
	return "Open"
}

// columns places each ticket in its column and each subtask in its own, done ones included as Jira does:
// right after its parent when they share a column, else after that column's tickets.
func (m Model) columns() [][]item {
	cols := make([][]item, len(ticket.Columns))
	ts := m.tickets()
	for ci, c := range ticket.Columns {
		var elsewhere []item
		for i := range ts {
			t := ts[i]
			if t.Category == c {
				cols[ci] = append(cols[ci], item{key: t.Key, ticket: &t})
			}
			for j := range t.Subtasks {
				st := t.Subtasks[j]
				if st.Category != c {
					continue
				}
				it := item{key: st.Key, ticket: &st, parent: t.Key}
				if t.Category == c {
					cols[ci] = append(cols[ci], it)
				} else {
					elsewhere = append(elsewhere, it)
				}
			}
		}
		cols[ci] = append(cols[ci], elsewhere...)
	}
	return cols
}

func (m Model) listItems() []item {
	if m.filter == FilterPRs {
		return m.prItems()
	}
	var out []item
	ts := m.tickets()
	team := slices.IndexFunc(ts, func(t ticket.Ticket) bool { return t.Team })
	if team < 0 {
		team = len(ts)
	}
	theirs := board.Sorted(ts[team:])
	slices.SortStableFunc(theirs, func(a, b ticket.Ticket) int { return strings.Compare(a.Board, b.Board) })
	for _, t := range append(board.Sorted(ts[:team]), theirs...) {
		t := t
		out = append(out, item{key: t.Key, ticket: &t})
		for j := range t.Subtasks {
			st := t.Subtasks[j]
			out = append(out, item{key: st.Key, ticket: &st, parent: t.Key})
		}
	}
	return out
}

func (m Model) current() (item, bool) {
	if m.inStrip {
		if _, spans := m.reviewChips(); m.stripSel < len(spans) {
			pr := spans[m.stripSel].pr
			return item{key: ticket.Key(fmt.Sprintf("#%d", pr.Number)), pr: &pr}, true
		}
		return item{}, false
	}
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
	if m.inStrip {
		if _, spans := m.reviewChips(); m.stripSel < len(spans) {
			return
		}
	}
	m.inStrip = false
	if m.effectiveView() == ViewList {
		items := m.listItems()
		m.row = max(0, slices.IndexFunc(items, func(it item) bool { return !it.heading() }))
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
	case ClonesFoundMsg, RepoFoundMsg, FolderPickedMsg, DirsListedMsg, ReposListedMsg, JiraFieldsListedMsg, JiraBoardsListedMsg:
		return m.settingsLookup(msg)
	case JiraStatusesListedMsg:
		return m.statusesListed(msg)
	case SnapshotMsg:
		if msg.Snap.Partial && msg.Err == nil {
			// Jira's board while GitHub answers: shown at once, the refresh still running, agents as they were.
			msg.Snap.Agents = m.snap.Agents
			m.snap = msg.Snap
			m.reselect()
			return m, nil
		}
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
	case TransitionsMsg:
		return m.transitions(msg)
	case movesFetchedMsg:
		return m.movesFetched(msg)
	case AssignedMsg:
		return m.assigned(msg)
	case TransitionedMsg:
		return m.transitioned(msg)
	case UsersListedMsg:
		return m.usersListed(msg)
	case formMsg:
		m.menu = m.formMenu(msg.form)
		m.status = msg.status
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
		if m.loading && m.now().Sub(m.loadingSince) > loadingLimit {
			m.loading = false
		}
		if !m.loading && m.now().Sub(m.snap.FetchedAt) >= refreshEvery {
			return m, tea.Batch(m.actions.Refresh(), tick(), m.beginLoading())
		}
		return m, tick()
	case SyncingMsg:
		return m, m.beginLoading()
	case SyncFailedMsg:
		m.loading = false
		m.status = "could not refresh: " + msg.Message
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
	if m.prompt != nil {
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
		return m, m.clickHeader(msg.X - x)
	}
	if m.repoNotice() != "" && msg.Y == headerLines-1 {
		m.picker = m.repoFilter()
		return m, nil
	}
	body := msg.Y - headerLines
	if body < 0 {
		return m, nil
	}
	strip, spans := m.reviewChips()
	if strip != "" {
		if body < lineCount(strip) {
			// A click on a review chip opens that pull request: following a link is harmless, unlike starting an agent.
			for _, sp := range spans {
				if msg.X >= sp.start && msg.X < sp.start+sp.width {
					return m, m.actions.Open(sp.pr.URL)
				}
			}
			return m, nil
		}
		if m.effectiveView() == ViewColumns {
			body -= lineCount(strip)
		}
	}
	if m.effectiveView() == ViewList {
		fixed, _, _, start := m.listLayout()
		_, at := m.listLines(func(k, s, a, p, t string) string { return "" }, func(item) string { return "" }, 0, 0, 0)
		// at counts the table's header and rule, which listLayout keeps among the fixed lines.
		i := 2 + start + body - len(fixed)
		if body < len(fixed) {
			i = -1
		}
		if i >= 0 && i < len(at) && at[i] == botToggleLine {
			m.toggleBots()
		} else if i >= 0 && i < len(at) && at[i] >= 0 {
			m.row = at[i]
			m.remember()
			if it, ok := m.current(); ok && it.heading() {
				return m, m.toggleGroup(it.group)
			}
			return m, m.selectedByClick()
		}
		return m, nil
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
		return m, m.selectedByClick()
	}
	return m, nil
}

const doubleClickWindow = 400 * time.Millisecond

// selectedByClick opens the actions menu when this click lands on the item the previous one selected,
// soon enough to be a double click.
func (m *Model) selectedByClick() tea.Cmd {
	it, ok := m.current()
	if !ok || it.heading() {
		return nil
	}
	now := m.now()
	if it.key == m.lastClick && now.Sub(m.lastClickAt) <= doubleClickWindow {
		m.lastClick = ""
		m.menu = m.buildMenu(it)
		return m.prefetchMoves(it)
	}
	m.lastClick, m.lastClickAt = it.key, now
	return nil
}

func (m *Model) openMenu() tea.Cmd {
	if it, ok := m.current(); ok && !it.heading() {
		m.menu = m.buildMenu(it)
		return m.prefetchMoves(it)
	}
	return nil
}

// clickHeader switches filter or view when x, relative to where the controls start, falls on a button.
func (m *Model) clickHeader(x int) tea.Cmd {
	_, _, _, _, _, spans := m.headerLayout()
	for _, sp := range spans {
		if x >= sp.start && x < sp.start+sp.width {
			if sp.repos {
				m.picker = m.repoFilter()
				return nil
			}
			if sp.team {
				return m.toggleTeam()
			}
			if sp.filter != "" {
				m.filter = sp.filter
			} else {
				m.view = sp.view
			}
			m.reselect()
			m.explainList()
			if sp.view != "" {
				return m.actions.SaveState(m.state())
			}
			return nil
		}
	}
	return nil
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
	if m.prompt != nil {
		return m.promptKey(k)
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
		return m, m.actions.SaveState(m.state())
	case "b":
		if m.filter == FilterPRs {
			m.toggleBots()
		}
	case "R":
		if len(m.boardRepos()) > 0 {
			m.picker = m.repoFilter()
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
		return m, tea.Batch(m.actions.Refresh(), m.beginLoading())
	case "enter", " ":
		if it, ok := m.current(); ok && it.heading() {
			return m, m.toggleGroup(it.group)
		}
		return m, m.openMenu()
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
		if it, ok := m.current(); ok && !it.heading() {
			if it.pr == nil && it.ticket != nil && len(it.ticket.PRs) > 1 {
				m.menu = m.prsMenu(*it.ticket)
				return m, nil
			}
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
	case "t":
		return m, m.toggleTeam()
	case "f":
		if it, ok := m.current(); ok && it.ticket != nil && it.ticket.Team {
			m.status = fmt.Sprintf("%s is %s's ticket", it.ticket.Key, cmp.Or(it.ticket.Assignee, "nobody"))
			return m, nil
		}
		if it, ok := m.current(); ok && it.ticket != nil {
			return m.startFix(it.ticket.Key)
		}
	}
	return m, nil
}

func (m *Model) move(dc, dr int) {
	if m.effectiveView() == ViewList && !m.inStrip {
		items := m.listItems()
		first := max(0, slices.IndexFunc(items, func(it item) bool { return !it.heading() }))
		if _, spans := m.reviewChips(); dr < 0 && m.row <= first && len(spans) > 0 {
			m.inStrip, m.stripSel = true, 0
			m.remember()
			return
		}
		m.row = clamp(m.row+dr, 0, len(items)-1)
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
	} else if m.prompt != nil {
		body = m.promptPlaced(m.height - lineCount(top) - lineCount(bottom0))
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
		body = strings.Join(m.listWindow(), "\n")
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
		top += "\n" + notice.Render(truncate("! "+strings.Join(m.snap.Warnings, " · "), m.width))
	}
	// The notice is the header's last line, where a click opens the repository filter.
	if notice := m.repoNotice(); notice != "" {
		top += "\n" + truncate(notice, m.width)
	}
	return top
}

type headerSpan struct {
	start, width int
	filter, view string
	repos, team  bool
}

var (
	filterOff = box.Foreground(colorSubtle)
	filterOn  = boxSel.Foreground(colorActive).Bold(true)
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
	// The Repos button shows only where the boxed buttons still fit with it; otherwise R and the notice open
	// the filter.
	specs := [][]headerItem{m.headerSpec(), m.headerSpec()}
	if len(m.boardRepos()) > 0 {
		specs[0] = append(specs[0], m.reposButton())
	}
	specs[0] = append(specs[0], headerItem{team: true, label: " " + m.teamButton() + " "})
	for pass := 0; pass < 2; pass++ {
		for _, spec := range specs {
			var row []string
			pos := 0
			spans = nil
			for i, it := range spec {
				gap := map[bool]int{true: 1, false: 0}[i > 0]
				if i == len(filterOrder) || it.repos || (it.team && !spec[i-1].repos) {
					gap = 2
				}
				row = append(row, strings.Repeat(" ", gap))
				pos += gap
				st := filterOff
				if (it.filter != "" && it.filter == m.filter) || (it.view != "" && it.view == m.effectiveView()) || (it.repos && m.repoNotice() != "") || (it.team && m.teamButtonOn()) {
					st = filterOn
				}
				chip := st.Render(strings.TrimSpace(it.label))
				spans = append(spans, headerSpan{start: pos, width: lipgloss.Width(chip), filter: it.filter, view: it.view, repos: it.repos, team: it.team})
				row = append(row, chip)
				pos += lipgloss.Width(chip)
			}
			controls = lipgloss.JoinHorizontal(lipgloss.Top, row...)
			cw := lipgloss.Width(controls)
			// The first pass looks for room on the title row, with the Repos button then without; the second
			// gives the controls their own row.
			switch {
			case pass == 0 && lipgloss.Width(title)+2+cw <= m.width:
				return title, controls, m.width - cw, 0, 3, spans
			case pass == 1 && cw <= m.width:
				return truncate(title, m.width), controls, 0, 1, 3, spans
			}
		}
	}
	items := m.headerItems()
	spans = spans[:0]
	pos := 0
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
	// repos marks the button that opens the repository filter, team the one that shows the team's tickets.
	repos, team bool
}

// headerSpec lists the filter buttons and, where the user can choose the view, the view buttons: the PRs
// filter and a pane too narrow for columns always show the list.
func (m Model) headerSpec() []headerItem {
	spec := []headerItem{
		{filter: FilterAll, label: fmt.Sprintf(" 1 Sprint %d ", len(m.shownTickets()))},
		{filter: FilterPRs, label: fmt.Sprintf(" 2 PRs %d ", m.prCount())},
	}
	if m.filter != FilterPRs && m.width >= narrowWidth {
		spec = append(spec, headerItem{view: ViewColumns, label: " Columns "}, headerItem{view: ViewList, label: " List "})
	}
	return spec
}

// reposButton opens the repository filter; it says how many repositories show while any is hidden.
func (m Model) reposButton() headerItem {
	repos := m.boardRepos()
	shown := 0
	for r := range repos {
		if !m.repoHidden(r) {
			shown++
		}
	}
	if shown < len(repos) {
		return headerItem{repos: true, label: fmt.Sprintf(" Repos %d/%d ", shown, len(repos))}
	}
	return headerItem{repos: true, label: " Repos "}
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

// syncSlot is the width of the sync state's widest wording, "✓ synced 59 min ago".
var syncSlot = lipgloss.Width("✓ synced 59 min ago")

// syncLabel says how fresh the board is: refreshing (with a spinner), synced N min ago, or cached when the
// last refresh failed.
func (m Model) syncLabel() string {
	switch {
	case m.loading && m.snap.Partial:
		return spinFrames[m.spin%len(spinFrames)] + " updating PRs…"
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

var reviewChip = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(colorReview).Padding(0, 1)

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
			chip = reviewChip.BorderForeground(colorAccent).Render(selected.Render("▸" + text))
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

// listLayout splits the list view into what stays put — the review strip with the blank line under it, and
// the table's header and rule — and the rows that scroll, which get rows lines, keeping one blank line
// between the table and the bottom. start is the first row shown.
func (m Model) listLayout() (fixed, rows []string, height, start int) {
	if strip := m.reviewStrip(); strip != "" {
		fixed = append(strings.Split(strip, "\n"), "")
	}
	all := strings.Split(strings.TrimRight(m.listView(), "\n"), "\n")
	_, at := m.listLines(func(k, s, a, p, t string) string { return "" }, func(item) string { return "" }, 0, 0, 0)
	if len(all) >= 2 {
		fixed, all, at = append(fixed, all[:2]...), all[2:], at[min(2, len(at)):]
	}
	height = m.height - lineCount(m.header()) - lineCount(m.preview()+"\n"+m.footer()) - len(fixed) - 1
	if height > 0 && len(all) > height {
		// The window follows the selected row; a collapsed group's ▸ must not pass for it.
		sel := 0
		if !m.inStrip {
			sel = max(0, slices.Index(at, m.row))
		}
		start = min(max(0, sel-height/2), len(all)-height)
		all = all[start : start+height]
	}
	return fixed, all, height, start
}

// listWindow is the list view as drawn: the fixed lines, then the rows in view.
func (m Model) listWindow() []string {
	fixed, rows, height, _ := m.listLayout()
	for len(rows) < height {
		rows = append(rows, "")
	}
	return append(append(fixed, rows...), "")
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
		points := 0.0
		for _, it := range cols[ci] {
			if it.ticket != nil {
				points += it.ticket.Points
			}
		}
		parts := columnHead(c.Title(), len(cols[ci]), points, inner)
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
			it := cols[ci][ri]
			parts = append(parts, card(cardTicket(it), inner, ci == m.col && ri == m.row && !m.inStrip, m.compact, m.snap.Agents[string(it.key)]))
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
// pointsText words story points for a card, "3pt" or "0.5pt"; empty when unestimated.
func pointsText(p float64) string {
	if p == 0 {
		return ""
	}
	return strconv.FormatFloat(p, 'f', -1, 64) + "pt"
}

// columnHead titles a column with its count and, when any of its tickets is estimated, their points.
func columnHead(name string, count int, points float64, width int) []string {
	n := strconv.Itoa(count)
	if points > 0 {
		n += " · " + strconv.FormatFloat(points, 'f', -1, 64) + " pts"
	}
	line := " " + name + strings.Repeat(" ", max(1, width-2-lipgloss.Width(name)-len(n))) + n + " "
	return []string{bold.Render(line), dim.Render(strings.Repeat("─", width))}
}

var (
	cardBox    = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(colorCard)
	cardBoxSel = cardBox.BorderForeground(colorAccent)
)

// card renders one ticket in its own bordered box of the given outer width.
var (
	cardBoxWarn = cardBox.BorderForeground(colorAttention)
	titleStyle  = lipgloss.NewStyle().Bold(true).Foreground(colorText)
	keyDim      = lipgloss.NewStyle().Foreground(colorSubtle)
)

// subtaskProgress words a parent's subtasks, "1/3 subtasks · 1 to do": how many are done and how many have
// not started though their parent has.
func subtaskProgress(t ticket.Ticket) string {
	done, todo := 0, 0
	for _, st := range t.Subtasks {
		switch {
		case st.Category == ticket.Done:
			done++
		case st.Category == ticket.ToDo && t.Category != ticket.ToDo:
			todo++
		}
	}
	out := fmt.Sprintf("%d/%d subtasks", done, len(t.Subtasks))
	if todo > 0 {
		out += fmt.Sprintf(" · %d to do", todo)
	}
	return out
}

// cardTicket is what an item's card shows: a subtask's key marked ↳ and its parent named where the type goes.
func cardTicket(it item) ticket.Ticket {
	t := *it.ticket
	if it.parent != "" {
		t.Key = ticket.Key("↳ " + string(t.Key))
		t.Type = "of " + string(it.parent)
	}
	return t
}

// card renders one ticket in its own bordered box: key and attention flag, title, then (unless compact)
// its pull requests with colored CI marks and a dimmed line with type and subtasks.
func card(t ticket.Ticket, outer int, sel, compact bool, agent string) string {
	width := outer - 2
	inner := width - 2
	head := keyDim.Render(string(t.Key))
	if k, ok := strings.CutPrefix(string(t.Key), "↳ "); ok {
		head = subtaskArrow(keyDim.Render(k), false)
	}
	if sel {
		head = selected.Render("▸ " + string(t.Key))
	}
	dot, dotW := "", 0
	if line, kind := board.AgentLine(agent); kind != board.AgentNone {
		glyph, _, _ := strings.Cut(line, " ")
		dot, dotW = agentStyle(agent).Render(glyph), 2
	}
	flagFor := func(head string) string {
		flag := ""
		if r := t.Attention(); len(r) > 0 {
			flag = warn.Render("! " + string(r[0]))
		}
		if icon, word, style := statusMark(t); icon != "" {
			// The word gives way to the icon alone when another flag is there or the card is too narrow for it.
			switch {
			case flag != "":
				flag += " " + style.Render(icon)
			case lipgloss.Width(head)+1+lipgloss.Width(word)+dotW <= inner:
				flag = style.Render(word)
			default:
				flag = style.Render(icon)
			}
		}
		if dot != "" {
			flag = strings.TrimPrefix(flag+" "+dot, " ")
		}
		return flag
	}
	// The points follow the key; when they and the flags do not both fit, the points give way.
	flag := flagFor(head)
	if pts := pointsText(t.Points); pts != "" {
		withPts := head + dim.Render(" "+pts)
		if f := flagFor(withPts); lipgloss.Width(withPts)+1+lipgloss.Width(f) <= inner {
			head, flag = withPts, f
		}
	}
	gap := max(1, inner-lipgloss.Width(head)-lipgloss.Width(flag))
	lines := []string{head + strings.Repeat(" ", gap) + flag}
	titleLines := 1
	if !compact {
		titleLines = 2
	}
	title := titleStyle
	if t.Team {
		title = dim
	}
	for _, l := range wrapWords(t.Summary, inner, titleLines) {
		lines = append(lines, title.Render(l))
	}
	if !compact {
		lines = append(lines, coloredPRs(t.PRs, inner))
		icon := typeGlyph(t.Type)
		if icon != "" {
			icon += " "
		}
		progress, name := "", t.Type
		if len(t.Subtasks) > 0 {
			progress = subtaskProgress(t)
		}
		if name == "" && progress == "" {
			name = t.Status
		}
		// The type's name shows whole or not at all, as its icon already tells it; the subtask progress
		// may be cut, and the assignee always stays.
		who := shortName(t.Assignee)
		room := inner - lipgloss.Width(icon) - lipgloss.Width(who) - 3
		info := strings.Trim(progress+" · "+name, " ·")
		if lipgloss.Width(info) > room {
			info = ""
			if progress != "" && room >= 4 {
				info = truncate(progress, room)
			}
		}
		if info != "" {
			who = info + " · " + who
		}
		lines = append(lines, icon+dim.Render(truncate(who, inner-lipgloss.Width(icon))))
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
		markText, style := checkMark(pr.Checks), checkStyle(pr.Checks)
		switch pr.State {
		case ticket.PRMerged:
			markText, style = "merged", dim
		case ticket.PRClosed:
			markText, style = "closed", dim
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
				parts = append(parts, dim.Render(prRef(pr)))
			}
		}
	} else if f := ticket.FinishedSummary(merged, closed); f != "" && used+len(f) <= width {
		parts = append(parts, dim.Render(f))
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

// checkStyle colors a CI mark: failing like what needs the user, passing like the accent, the rest dimmed.
func checkStyle(c ticket.CheckState) lipgloss.Style {
	switch c {
	case ticket.ChecksFail:
		return warn
	case ticket.ChecksPass:
		return ok
	}
	return dim
}

func checkMark(c ticket.CheckState) string {
	switch c {
	case ticket.ChecksFail:
		return "✕"
	case ticket.ChecksPending:
		return "…"
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
			mark = "…"
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
	// statusMaxWidth caps the STATUS column, which grows to fit a pull request's approvals.
	statusMaxWidth = 32
)

// statusText is a row's STATUS: a pull request's standing with its approvals, else the ticket's status.
func statusText(it item) string {
	var s string
	switch {
	case it.heading():
		return ""
	case it.review():
		s = "Review requested"
	case it.mine(), it.other:
		s = prStatus(*it.pr)
	default:
		return it.ticket.Status
	}
	if a := it.pr.ApprovalsText(); a != "" {
		s += " · " + a
	}
	return s
}

// statusStyle colors a row's STATUS: a ticket by its column, a pull request by where it stands.
func statusStyle(it item) lipgloss.Style {
	s := lipgloss.NewStyle()
	if it.pr == nil {
		return s.Foreground(columnColors[it.ticket.Category])
	}
	pr := *it.pr
	switch {
	case it.review():
		return s.Foreground(colorReview)
	case needsYou(pr):
		return warn
	case pr.Draft:
		return dim
	case pr.Merge == ticket.MergeReady, pr.Review == ticket.ReviewApproved:
		return ok
	case pr.Review == ticket.ReviewRequired:
		return s.Foreground(colorReview)
	case pr.Checks == ticket.ChecksPending:
		return s.Foreground(colorAccent)
	}
	return s
}

// subtaskArrow marks a subtask's key, colored unless the row is selected, whose background it would break.
func subtaskArrow(key string, sel bool) string {
	if sel {
		return "↳ " + key
	}
	return lipgloss.NewStyle().Foreground(colorSubtask).Render("↳") + " " + key
}

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
		switch {
		case it.heading():
			return ""
		case it.pr != nil:
			return checkMark(it.pr.Checks)
		}
		return prRefs(it.ticket.PRs)
	}
	measured := items
	if m.filter == FilterPRs {
		measured = m.prLayout(true)
	}
	prW := lipgloss.Width(m.listHeads()[2])
	for _, it := range measured {
		prW = max(prW, lipgloss.Width(prText(it)))
	}
	prW = min(prW, prWidth)
	statusW := statusWidth
	for _, it := range measured {
		statusW = max(statusW, lipgloss.Width(statusText(it)))
	}
	statusW = min(statusW, statusMaxWidth)
	agentW := 0
	// The agent belongs to the ticket, so in the PRs filter it would repeat on every pull request of one ticket.
	if len(m.snap.Agents) > 0 && m.filter != FilterPRs {
		agentW = agentWidth + 1
	}
	assigneeW := 0
	if m.filter == FilterPRs {
		for _, it := range measured {
			if it.pr != nil {
				assigneeW = max(assigneeW, lipgloss.Width("AUTHOR"), lipgloss.Width(it.pr.Author))
			}
		}
		if assigneeW > 0 {
			assigneeW = min(assigneeW, authorMaxWidth) + 1
		}
	} else {
		for _, it := range measured {
			if it.ticket != nil && !it.mine() {
				assigneeW = max(assigneeW, lipgloss.Width("ASSIGNEE"), lipgloss.Width(shortName(it.ticket.Assignee)))
			}
		}
		if assigneeW > 0 {
			assigneeW = min(assigneeW, assigneeMaxWidth) + 1
		}
	}
	pointsW := 0
	if m.filter != FilterPRs {
		for _, it := range measured {
			if it.ticket != nil && it.ticket.Points != 0 {
				pointsW = max(pointsW, lipgloss.Width("SP"), lipgloss.Width(pointsCell(it.ticket.Points, 1)))
			}
		}
		if pointsW > 0 {
			pointsW++
		}
	}
	// Narrow panes drop AGENT first, then shrink PR / CI, then STATUS, so the title always keeps some room.
	const minTitle = 12
	keyW := keyWidth
	if m.filter == FilterPRs {
		keyW = lipgloss.Width(m.listHeads()[0])
		for _, it := range measured {
			if !it.heading() {
				keyW = max(keyW, lipgloss.Width(string(it.key)))
			}
		}
	}
	room := func() int { return m.width - 2 - keyW - statusW - agentW - assigneeW - pointsW - prW - 3 }
	if minTitle > room() {
		agentW = 0
	}
	if minTitle > room() {
		assigneeW = 0
	}
	if minTitle > room() {
		pointsW = 0
	}
	if short := minTitle - room(); short > 0 {
		cut := max(0, min(short, prW-8))
		prW -= cut
		statusW -= min(short-cut, statusW-10)
	}
	titleWidth := max(minTitle, room())
	row := func(key, status, agent, pr, title string) string {
		return pad(key, keyW) + " " + pad(status, statusW) + " " + agent + pad(pr, prW) + " " + truncate(title, titleWidth)
	}
	lines, _ := m.listLines(row, prText, agentW, assigneeW, pointsW)
	return strings.Join(lines, "\n") + "\n"
}

// listHeads names the key, status and PR columns; the PRs filter lists pull requests by number with only
// their CI mark, so its headers say so and the mark's column stays narrow.
func (m Model) listHeads() [3]string {
	if m.filter == FilterPRs {
		return [3]string{"PR", "STATUS", "CI"}
	}
	return [3]string{"KEY", "STATUS", "PR / CI"}
}

// assigneeCell is a list row's assignee, padded to assigneeW (which includes its gap); empty without the column.
func assigneeCell(name string, assigneeW int) string {
	if assigneeW == 0 {
		return ""
	}
	return pad(truncate(shortName(name), assigneeW-1), assigneeW)
}

// assigneeMaxWidth caps the ASSIGNEE column, which shows short names such as "Ana L."; authorMaxWidth caps
// the PRs filter's AUTHOR column, which shows GitHub logins.
const (
	assigneeMaxWidth = 14
	authorMaxWidth   = 20
)

// pointsCell is a list row's story points, padded to pointsW (which includes its gap); blank when unestimated.
func pointsCell(p float64, pointsW int) string {
	if pointsW == 0 {
		return ""
	}
	n := ""
	if p != 0 {
		n = strconv.FormatFloat(p, 'f', -1, 64)
	}
	return pad(n, pointsW)
}

// authorCell is a pull request row's author, padded to authorW (which includes its gap).
func authorCell(login string, authorW int) string {
	if authorW == 0 {
		return ""
	}
	return pad(truncate(login, authorW-1), authorW)
}

// shortName shortens a person's name to the first name and the last name's initial, "Ana L.", for the
// board's narrow cells; an empty name means the ticket is unassigned.
func shortName(name string) string {
	parts := strings.Fields(name)
	switch len(parts) {
	case 0:
		return "unassigned"
	case 1:
		return parts[0]
	}
	last := []rune(parts[len(parts)-1])
	return parts[0] + " " + string(last[0]) + "."
}

// agentWidth fits the longest agent cell, "● needs you".
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
	return agentStyle(status).Render(cell)
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

var columnColors = map[ticket.Category]lipgloss.Color{ticket.ToDo: ctpGray, ticket.InProgress: ctpBlue, ticket.InReview: ctpMauve, ticket.Done: ctpGreen}

// statusBadge is a ticket's Jira status, with its mark's icon, on its column's color.
func statusBadge(t ticket.Ticket) string {
	text := t.Status
	if icon, _, _ := statusMark(t); icon != "" {
		text = icon + " " + text
	}
	return lipgloss.NewStyle().Background(columnColors[t.Category]).Foreground(colorOnBadge).Render(" " + text + " ")
}

// agentStyle colors an agent status as Herdr does: working yellow, blocked red, done teal, idle green.
func agentStyle(status string) lipgloss.Style {
	colors := map[string]lipgloss.Color{"working": ctpYellow, "blocked": ctpRed, "done": ctpTeal, "idle": ctpGreen}
	return lipgloss.NewStyle().Foreground(colors[status])
}

// markIcons draws each status mark; red is left out so a mark never reads as an agent that needs the user.
var markIcons = map[ticket.Mark]struct {
	glyph string
	color lipgloss.Color
}{
	ticket.MarkDiamond:  {"◇", ctpBlue},
	ticket.MarkBlocked:  {"⊘", ctpPeach},
	ticket.MarkClock:    {"◷", ctpTeal},
	ticket.MarkFlag:     {"⚑", ctpMauve},
	ticket.MarkStar:     {"★", ctpYellow},
	ticket.MarkTriangle: {"▲", ctpGreen},
}

// statusMark is the icon of a ticket's status mark, its wider wording with the status name, and its style;
// empty when unmarked.
func statusMark(t ticket.Ticket) (icon, word string, style lipgloss.Style) {
	ic, ok := markIcons[t.Mark]
	if !ok {
		return "", "", style
	}
	return ic.glyph, ic.glyph + " " + t.Status, lipgloss.NewStyle().Foreground(ic.color)
}

// typeIcons are matched in order against the lowercased Jira type name, in English or Spanish, so
// "Subtarea" is a subtask before it is a task.
var typeIcons = []struct {
	words []string
	glyph string
	color lipgloss.Color
}{
	{[]string{"bug", "error", "defect"}, "●", ctpRed},
	{[]string{"epic", "épica"}, "◆", ctpMauve},
	{[]string{"sub"}, "■", ctpTeal},
	{[]string{"story", "historia"}, "■", ctpGreen},
	{[]string{"task", "tarea"}, "■", ctpBlue},
}

// typeGlyph is a one-cell colored icon for a Jira issue type, like Jira's own; empty when there is no type.
func typeGlyph(typ string) string {
	if typ == "" {
		return ""
	}
	l := strings.ToLower(typ)
	for _, ic := range typeIcons {
		if slices.ContainsFunc(ic.words, func(w string) bool { return strings.Contains(l, w) }) {
			return lipgloss.NewStyle().Foreground(ic.color).Render(ic.glyph)
		}
	}
	return dim.Render("■")
}

// listLines lays the list out in sections, the user's tickets and review requests per repo, and returns for
// every line the index of the item on it (-1 for titles and spacing, botToggleLine for the bots toggle), so
// clicks map back to items.
func (m Model) listLines(row func(key, status, agent, pr, title string) string, prText func(item) string, agentW, assigneeW, pointsW int) ([]string, []int) {
	items := m.listItems()
	header := pad("", agentW)
	if agentW > 0 {
		header = pad("AGENT", agentW)
	}
	if assigneeW > 0 {
		label := "ASSIGNEE"
		if m.filter == FilterPRs {
			label = "AUTHOR"
		}
		header = pad(label, assigneeW) + header
	}
	if pointsW > 0 {
		header = pad("SP", pointsW) + header
	}
	heads := m.listHeads()
	lines := []string{"  " + dim.Render(row(heads[0], heads[1], header, heads[2], "TITLE")), dim.Render(strings.Repeat("─", m.width))}
	at := []int{-1, -1}
	if m.filter == FilterPRs && m.hiddenBots() > 0 {
		toggle := keyStyle.Render("b") + " " + dim.Render(fmt.Sprintf("show bots · %d hidden", m.hiddenBots()))
		if m.showBots {
			toggle = keyStyle.Render("b") + " " + dim.Render("hide bots")
		}
		lines, at = append(lines, "  "+toggle), append(at, botToggleLine)
	}
	nTickets := 0
	for _, it := range items {
		if it.ticket != nil && !it.ticket.Team && !it.mine() && it.parent == "" {
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
		if it.heading() {
			if len(lines) > 2 {
				lines, at = append(lines, ""), append(at, -1)
			}
			lines, at = append(lines, m.groupTitle(it, i == m.row)), append(at, i)
			continue
		}
		switch {
		case it.ticket != nil && it.ticket.Team && !it.mine() && (i == 0 || items[i-1].ticket == nil || !items[i-1].ticket.Team || items[i-1].ticket.Board != it.ticket.Board):
			n := 0
			for _, o := range items[i:] {
				if o.ticket != nil && o.ticket.Team && o.ticket.Board == it.ticket.Board {
					n++
				}
			}
			title := "Team"
			if it.ticket.Board != "" {
				title += " · " + it.ticket.Board
			}
			section(title, n)
		case i == 0 && it.ticket != nil:
			section("My tickets", nTickets)
		}
		var line string
		ci := prText(it)
		if it.pr != nil && i != m.row {
			ci = checkStyle(it.pr.Checks).Render(ci)
		}
		status := statusText(it)
		if i != m.row && !it.heading() {
			approvals := ""
			if it.pr != nil && it.pr.ApprovalsText() != "" {
				approvals = " · " + it.pr.ApprovalsText()
			}
			status = statusStyle(it).Render(strings.TrimSuffix(status, approvals)) + approvals
		}
		switch {
		case it.review(), it.other:
			line = row(string(it.key), status, pad("", pointsW)+authorCell(it.pr.Author, assigneeW)+pad("", agentW), ci, it.pr.Title)
		case it.mine():
			line = row(string(it.key), status, authorCell(it.pr.Author, assigneeW)+agentCell(m.snap.Agents[string(it.ticket.Key)], agentW, i == m.row), ci, cmp.Or(it.pr.Title, it.ticket.Summary))
		default:
			title, key := it.ticket.Summary, string(it.key)
			if (it.ticket.Team || it.ticket.Category == ticket.Done && it.parent != "") && i != m.row {
				title = dim.Render(title)
			}
			if it.parent != "" {
				key = subtaskArrow(key, i == m.row)
			}
			line = row(key, status, pointsCell(it.ticket.Points, pointsW)+assigneeCell(it.ticket.Assignee, assigneeW)+agentCell(m.snap.Agents[string(it.key)], agentW, i == m.row), prText(it), title)
		}
		var mark string
		var markStyle lipgloss.Style
		if it.ticket != nil {
			mark, _, markStyle = statusMark(*it.ticket)
		}
		switch {
		case i == m.row:
			line = "▸ " + selected.Render(line)
		case it.review(), it.mine() && needsYou(*it.pr):
			line = warn.Render("!") + " " + line
		case it.mine(), it.other:
			line = "  " + line
		case len(it.ticket.Attention()) > 0 && mark != "":
			// The wider mark takes one cell of the key's padding, so the columns stay aligned.
			line = warn.Render("!") + markStyle.Render(mark) + " " + strings.Replace(line, string(it.key)+" ", string(it.key), 1)
		case len(it.ticket.Attention()) > 0:
			line = warn.Render("!") + " " + line
		case mark != "":
			line = markStyle.Render(mark) + " " + line
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
	if it.heading() {
		verb := "collapse"
		if m.collapsed[it.group] {
			verb = "expand"
		}
		return titleStyle.Render(truncate(it.title, m.width)) + "\n" + dim.Render(truncate(fmt.Sprintf("%d pull requests · %d need you · enter or click to %s", it.count, it.needs, verb), m.width))
	}
	if it.mine() {
		title := titleStyle.Render(truncate(fmt.Sprintf("%s · %s · %s", it.key, it.ticket.Key, cmp.Or(it.pr.Title, it.ticket.Summary)), m.width))
		return title + "\n" + dim.Render(truncate(fmt.Sprintf("%s in %s · CI %s · enter for actions", statusText(it), it.pr.Repo, ciWord(it.pr.Checks)), m.width))
	}
	if it.pr != nil {
		title := titleStyle.Render(truncate(fmt.Sprintf("%s · %s", it.key, it.pr.Title), m.width))
		approvals := ""
		if a := it.pr.ApprovalsText(); a != "" {
			approvals = " · " + a
		}
		how := "review requested by"
		if it.other {
			how = "opened by"
		}
		return title + "\n" + dim.Render(truncate(fmt.Sprintf("%s %s in %s%s · CI %s · enter for actions", how, orDash(it.pr.Author), it.pr.Repo, approvals, ciWord(it.pr.Checks)), m.width))
	}
	t := it.ticket
	title := truncate(titleStyle.Render(string(t.Key))+" "+statusBadge(*t)+titleStyle.Render(" · "+t.Summary), m.width)
	parts := []string{cmp.Or(t.Assignee, "unassigned")}
	switch {
	case t.Points == 1:
		parts = append([]string{"1 point"}, parts...)
	case t.Points != 0:
		parts = append([]string{strconv.FormatFloat(t.Points, 'f', -1, 64) + " points"}, parts...)
	}
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
		return "running …"
	case ticket.ChecksPass:
		return "passing ✓"
	}
	return "not reported"
}

// actionHint tells what the most useful key does for this ticket, as the mockup's preview line does.
func actionHint(t ticket.Ticket) string {
	reasons := t.Attention()
	switch {
	case t.Team:
		return "enter for details"
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

var keyStyle = lipgloss.NewStyle().Bold(true).Foreground(colorAccent)

// footer is the key legend: each key highlighted, its meaning dimmed, with room between commands.
func (m Model) footer() string {
	// Each screen lists only the keys that do something there: the review strip only exists in columns,
	// density only changes cards, the view only toggles where columns can show.
	columns := m.effectiveView() == ViewColumns
	strip := m.filter != FilterPRs && len(m.reviews()) > 0
	move, moveHint, clickHint, enterHint := "↑↓ ws", "move", "select", "actions"
	if columns {
		move = "←↑↓→ wasd"
	}
	if strip {
		moveHint, clickHint = "move · ↑ to reviews", "select · open review"
	}
	if m.filter == FilterPRs {
		enterHint = "actions · fold group"
	}
	legend := [][2]string{{move, moveHint}}
	if m.filter == FilterPRs && m.hiddenBots() > 0 {
		legend = append(legend, [2]string{"b", "bots"})
	}
	legend = append(legend, [][2]string{
		{"click", clickHint}, {"double-click", "actions"}, {"enter", enterHint}, {"f", "send feedback"}, {"i", "details"},
		{"o", "Jira ↗"}, {"p", "PR ↗"}, {"O P", "copy link"}, {"tab 1-2", "filter"},
	}...)
	if m.filter != FilterPRs && m.width >= narrowWidth {
		legend = append(legend, [2]string{"v", "view"})
	}
	if columns {
		legend = append(legend, [2]string{"z", "density"})
	}
	legend = append(legend, [2]string{"t", strings.ToLower(m.teamButton())})
	if len(m.boardRepos()) > 0 {
		legend = append(legend, [2]string{"R", "repos"})
	}
	legend = append(legend, [][2]string{{",", "settings"}, {"g", "refresh"}, {"q", "close"}}...)
	items := make([]string, len(legend))
	for i, k := range legend {
		items[i] = keyStyle.Render(k[0]) + " " + dim.Render(k[1])
	}
	sync := dim.Render(m.syncLabel())
	if m.stale != nil {
		sync = notice.Render(m.syncLabel())
	}
	// The sync state sits at the bottom right: on the legend's last line when its widest wording fits,
	// else on its own. Deciding by the widest wording keeps it from jumping between lines, and the
	// board's height with it, as it turns from synced to refreshing.
	lines := strings.Split(wrapSep(items, m.width, "    "), "\n")
	last := lines[len(lines)-1]
	if m.width-lipgloss.Width(last)-syncSlot >= 4 {
		lines[len(lines)-1] = last + strings.Repeat(" ", m.width-lipgloss.Width(last)-lipgloss.Width(sync)) + sync
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
