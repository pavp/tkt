package tui

import (
	"cmp"
	"errors"
	"fmt"
	"reflect"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/muesli/termenv"

	"github.com/pavp/tkt/internal/board"
	"github.com/pavp/tkt/internal/config"
	"github.com/pavp/tkt/internal/jira"
	"github.com/pavp/tkt/internal/ticket"
)

type recorder struct {
	calls []string
	// boards answers JiraBoards per space.
	boards map[string][]config.TeamBoard
	// statuses answers JiraStatuses per space, or statusesErr.
	statuses    map[string][]JiraStatus
	statusesErr error
	// states records every SaveState.
	states []BoardState
	// choices is what StartChoices reports.
	choices StartChoices
	cfg     config.Config
	saved   []config.Config
	// saveErr makes SaveConfig fail.
	saveErr error
	clones  map[string][]string
	origins map[string]string
	picked  string
	starts  []string
	// dirs answers ListDirs: the folders inside each listed path, keyed by the path as written ("~/src/"
	// or "/home/"); a missing key fails the listing. Home is /home/dev.
	dirs     map[string][]string
	listed   []string
	noNative bool
	broken   map[string][]string
	// repos answers ListRepos with the repositories of each owner.
	repos map[string][]string
	// jiraFields answers JiraFields.
	jiraFields []string
	// known is the ticket the last Detail was given.
	known *ticket.Ticket
	// transitions answers Transitions; values records the last Transition's.
	transitions []Transition
	values      []Value
	comment     string
	// users answers Users per query.
	users map[string][]User
}

func (r *recorder) Users(k ticket.Key, query string) tea.Cmd {
	r.calls = append(r.calls, "users "+query)
	users := r.users[query]
	return func() tea.Msg { return UsersListedMsg{Key: k, Query: query, Users: users} }
}

func (r *recorder) Transitions(k ticket.Key) tea.Cmd {
	r.calls = append(r.calls, "transitions "+string(k))
	tr := r.transitions
	return func() tea.Msg { return TransitionsMsg{Key: k, Transitions: tr} }
}

func (r *recorder) Assign(k ticket.Key, who Assignee) tea.Cmd {
	to := cmp.Or(who.ID, "nobody")
	if who.Me {
		to = "me"
	}
	r.calls = append(r.calls, "assign "+string(k)+" "+to)
	return func() tea.Msg { return AssignedMsg{Key: k, Who: who} }
}

func (r *recorder) Transition(k ticket.Key, id, to string, values []Value, comment string) tea.Cmd {
	r.values, r.comment = values, comment
	r.calls = append(r.calls, "transition "+string(k)+" "+id)
	return func() tea.Msg { return TransitionedMsg{Key: k, To: to} }
}

func (r *recorder) JiraBoards(space string) tea.Cmd {
	r.listed = append(r.listed, "boards "+space)
	boards := r.boards[space]
	return func() tea.Msg { return JiraBoardsListedMsg{Space: space, Boards: boards} }
}

func (r *recorder) JiraStatuses(space string) tea.Cmd {
	r.listed = append(r.listed, "statuses "+space)
	statuses, err := r.statuses[space], r.statusesErr
	return func() tea.Msg { return JiraStatusesListedMsg{Space: space, Statuses: statuses, Err: err} }
}

func (r *recorder) JiraFields() tea.Cmd {
	r.listed = append(r.listed, "jira fields")
	names := r.jiraFields
	return func() tea.Msg { return JiraFieldsListedMsg{Names: names} }
}

func (r *recorder) ListDirs(path string) tea.Cmd {
	r.listed = append(r.listed, path)
	return func() tea.Msg {
		abs := path
		if strings.HasPrefix(path, "~") {
			abs = "/home/dev" + strings.TrimPrefix(path, "~")
		}
		dir := abs
		if strings.HasPrefix(abs, "/home/dev/") {
			dir = "~" + strings.TrimPrefix(abs, "/home/dev")
		}
		names, ok := r.dirs[path]
		if !ok && r.dirs != nil {
			if names, ok = r.dirs[dir]; !ok {
				return DirsListedMsg{Path: path, Dir: dir, Abs: abs, Err: errors.New("no such folder")}
			}
		}
		return DirsListedMsg{Path: path, Dir: dir, Abs: abs, Names: names, Broken: r.broken[path]}
	}
}

func (r *recorder) NativePicker() bool { return !r.noNative }
func (r *recorder) SaveState(s BoardState) tea.Cmd {
	r.states = append(r.states, s)
	return nil
}

func (r *recorder) ListRepos(owner string) tea.Cmd {
	r.listed = append(r.listed, "repos "+owner)
	return func() tea.Msg {
		if names, ok := r.repos[owner]; ok {
			return ReposListedMsg{Owner: owner, Names: names}
		}
		return ReposListedMsg{Owner: owner, Err: errors.New("no GitHub organization or user named " + owner)}
	}
}

func (r *recorder) FindClones(repo string) tea.Cmd {
	return func() tea.Msg { return ClonesFoundMsg{Repo: repo, Paths: r.clones[repo]} }
}
func (r *recorder) RepoAt(path string) tea.Cmd {
	return func() tea.Msg { return RepoFoundMsg{Path: path, Repo: r.origins[path]} }
}
func (r *recorder) PickFolder(start string) tea.Cmd {
	r.starts = append(r.starts, start)
	return func() tea.Msg { return FolderPickedMsg{Path: r.picked} }
}

func (r *recorder) Config() config.Config { return r.cfg.Clone() }
func (r *recorder) SaveConfig(c config.Config) tea.Cmd {
	r.calls = append(r.calls, "save")
	return func() tea.Msg {
		if r.saveErr != nil {
			return SettingsSavedMsg{Err: r.saveErr}
		}
		r.saved = append(r.saved, c.Clone())
		r.cfg = c.Clone()
		return SettingsSavedMsg{Config: c.Clone()}
	}
}

func (r *recorder) cmd(s string) tea.Cmd {
	r.calls = append(r.calls, s)
	return func() tea.Msg { return StatusMsg(s) }
}
func (r *recorder) Refresh() tea.Cmd              { return r.cmd("refresh") }
func (r *recorder) Agents(board.Snapshot) tea.Cmd { return r.cmd("agents") }
func (r *recorder) Start(k ticket.Key, o StartOptions) tea.Cmd {
	call := "start " + string(k)
	if len(o.Repos) > 0 {
		call += " repos=" + strings.Join(o.Repos, ",")
	}
	if o.Worktree != "" {
		call += " worktree=" + o.Worktree
	}
	return r.cmd(call)
}
func (r *recorder) StartChoices(k ticket.Key) tea.Cmd {
	r.calls = append(r.calls, "choices "+string(k))
	choices := r.choices
	return func() tea.Msg { return StartChoicesMsg{Key: k, Choices: choices} }
}
func (r *recorder) TicketPane(k ticket.Key) tea.Cmd {
	return r.cmd("pane " + string(k))
}
func (r *recorder) Open(u string) tea.Cmd { return r.cmd("open " + u) }
func (r *recorder) Fix(k ticket.Key, known *ticket.Ticket) tea.Cmd {
	if known != nil {
		return r.cmd("fix " + string(k) + " known")
	}
	return r.cmd("fix " + string(k))
}
func (r *recorder) last() string {
	if len(r.calls) == 0 {
		return ""
	}
	return r.calls[len(r.calls)-1]
}

func snapshot() board.Snapshot {
	return board.Snapshot{
		FetchedAt: time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC),
		Tickets: []ticket.Ticket{
			{Key: "DEMO-3", Summary: "Export CSV", Status: "To Do", Category: ticket.ToDo, URL: "https://example.atlassian.net/browse/DEMO-3"},
			{Key: "DEMO-1", Summary: "Filter accounts", Status: "In Progress", Category: ticket.InProgress, URL: "https://example.atlassian.net/browse/DEMO-1",
				PRs: []ticket.PullRequest{{Number: 812, URL: "https://github.com/example-org/web/pull/812", State: ticket.PROpen, Checks: ticket.ChecksFail}}},
			{Key: "DEMO-2", Summary: "Keep sort", Status: "In Review", Category: ticket.InReview,
				PRs: []ticket.PullRequest{{Number: 806, State: ticket.PROpen, Checks: ticket.ChecksPass}}},
		},
		Reviews: []ticket.PullRequest{{Repo: "example-org/web", Number: 820, Title: "Add pagination", URL: "https://github.com/example-org/web/pull/820", Author: "alex"}},
	}
}

func press(m tea.Model, keys ...string) tea.Model {
	for _, k := range keys {
		var msg tea.KeyMsg
		switch k {
		case "enter":
			msg = tea.KeyMsg{Type: tea.KeyEnter}
		case "esc":
			msg = tea.KeyMsg{Type: tea.KeyEsc}
		default:
			msg = tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(k)}
		}
		m, _ = m.Update(msg)
	}
	return m
}

func run(m tea.Model, k string) (tea.Model, tea.Msg) {
	var msg tea.KeyMsg
	if k == "enter" {
		msg = tea.KeyMsg{Type: tea.KeyEnter}
	} else {
		msg = tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(k)}
	}
	m, cmd := m.Update(msg)
	if cmd == nil {
		return m, nil
	}
	return m, cmd()
}

func loaded(r *recorder) tea.Model {
	m := New(board.Snapshot{}, false, r, ViewColumns)
	next, _ := m.Update(tea.WindowSizeMsg{Width: 160, Height: 40})
	next, _ = next.Update(SnapshotMsg{Snap: snapshot()})
	return next
}

func TestInitReadsAgentsOfTheCachedBoardWithoutWaitingForTheRefresh(t *testing.T) {
	cases := []struct {
		name       string
		snap       board.Snapshot
		wantAgents bool
	}{
		{"cached board", snapshot(), true},
		{"no cached board", board.Snapshot{}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := &recorder{}
			New(c.snap, c.wantAgents, r, "").Init()
			if got := slices.Contains(r.calls, "agents"); got != c.wantAgents {
				t.Errorf("agents requested = %v, calls = %v", got, r.calls)
			}
		})
	}
}

func TestAgentsApplyOnlyToTheBoardTheyWereReadFor(t *testing.T) {
	cached := snapshot()
	fresh := snapshot()
	fresh.FetchedAt = cached.FetchedAt.Add(time.Minute)
	fresh.Agents = map[string]string{"DEMO-1": "working"}
	cases := []struct {
		name string
		snap board.Snapshot
		want string
	}{
		{"cached board still shown", cached, "blocked"},
		{"fresh board arrived first", fresh, "working"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			m := New(c.snap, true, &recorder{}, "")
			next, _ := m.Update(AgentsMsg{FetchedAt: cached.FetchedAt, Agents: map[string]string{"DEMO-1": "blocked"}})
			if got := next.(Model).snap.Agents["DEMO-1"]; got != c.want {
				t.Errorf("agent of DEMO-1 = %q, want %q", got, c.want)
			}
		})
	}
}

func TestStaleRefreshKeepsTheAgentsAlreadyRead(t *testing.T) {
	m := New(snapshot(), true, &recorder{}, "")
	next, _ := m.Update(AgentsMsg{FetchedAt: snapshot().FetchedAt, Agents: map[string]string{"DEMO-1": "blocked"}})
	next, _ = next.Update(SnapshotMsg{Snap: snapshot(), Stale: errors.New("offline")})
	if got := next.(Model).snap.Agents["DEMO-1"]; got != "blocked" {
		t.Errorf("agent of DEMO-1 = %q after a stale refresh", got)
	}
}

func TestInitRefreshes(t *testing.T) {
	r := &recorder{}
	m := New(board.Snapshot{}, false, r, "")
	if m.Init() == nil || r.last() != "refresh" {
		t.Errorf("Init should refresh, calls = %v", r.calls)
	}
	if !strings.Contains(m.View(), "loading…") {
		t.Error("no loading notice without a cached board")
	}
}

func TestColumnsViewAndNavigation(t *testing.T) {
	r := &recorder{}
	m := loaded(r)
	view := m.View()
	for _, want := range []string{"To Do", "In Progress", "In Review", "Done", "— nothing here", "! CI failing", "#812 ✕", "Review requested", "! #820", "←↑↓→ wasd move"} {
		if !strings.Contains(view, want) {
			t.Errorf("columns view missing %q", want)
		}
	}
	if !strings.Contains(view, "▸ DEMO-3") {
		t.Errorf("first column not selected:\n%s", view)
	}
	m = press(m, "l")
	if !strings.Contains(m.View(), "▸ DEMO-1") {
		t.Error("l did not move to In Progress")
	}
	m = press(m, "l", "l")
	if !strings.Contains(m.View(), "▸ DEMO-2") {
		t.Error("moving right should skip the empty Done column and stay on In Review")
	}
	m = press(m, "enter")
	if !strings.Contains(stripANSI(m.View()), "Actions for DEMO-2") {
		t.Fatalf("enter did not open the actions menu:\n%s", stripANSI(m.View()))
	}
	m, msg := run(m, "enter")
	if r.last() != "choices DEMO-2" {
		t.Fatalf("first menu item = %v", r.calls)
	}
	if _, cmd := m.Update(msg); cmd == nil || cmd() != StatusMsg("start DEMO-2") || r.last() != "start DEMO-2" {
		t.Errorf("nothing to ask, Start did not run: %v", r.calls)
	}
}

func TestToggleViewKeepsSelection(t *testing.T) {
	r := &recorder{}
	m := press(loaded(r), "l", "v")
	view := m.View()
	if !strings.Contains(view, "▸ ") || !strings.Contains(view, "↑↓ ws move") {
		t.Fatalf("not in list view:\n%s", view)
	}
	if line := selectedLine(view); !strings.Contains(line, "DEMO-1") {
		t.Errorf("selection lost after toggling: %q", line)
	}
	m = press(m, "v")
	if !strings.Contains(m.View(), "←↑↓→ wasd move") || !strings.Contains(m.View(), "▸ DEMO-1") {
		t.Error("toggling back lost the columns or the selection")
	}
}

func selectedLine(view string) string {
	for _, l := range strings.Split(view, "\n") {
		if strings.HasPrefix(l, " ▸ ") {
			return l
		}
	}
	return ""
}

func TestNarrowTerminalUsesList(t *testing.T) {
	r := &recorder{}
	m := loaded(r)
	m, _ = m.Update(tea.WindowSizeMsg{Width: 80, Height: 30})
	if !strings.Contains(m.View(), "↑↓ ws move") {
		t.Error("narrow terminal did not fall back to the list")
	}
}

func TestFilters(t *testing.T) {
	r := &recorder{}
	m := press(loaded(r), "n")
	view := stripANSI(m.View())
	if strings.Contains(view, "DEMO-3") || !strings.Contains(view, "#812") || !strings.Contains(view, "#820") || !strings.Contains(view, "↑↓ ws move") {
		t.Errorf("PRs filter should list my open PRs and the review requests:\n%s", view)
	}
	for i := 0; i < 10 && !strings.Contains(selectedLine(m.View()), "#820"); i++ {
		m = press(m, "s")
	}
	m = press(m, "enter")
	m, _ = run(m, "enter")
	if r.last() != "open https://github.com/example-org/web/pull/820" {
		t.Errorf("enter on a review = %v", r.calls)
	}
	m = press(m, "r")
	if !strings.Contains(m.View(), "DEMO-3") {
		t.Error("pressing r again should return to all")
	}
}

func TestActionKeys(t *testing.T) {
	r := &recorder{}
	m := press(loaded(r), "l")
	tests := []struct{ key, want string }{
		{"f", "fix DEMO-1 known"},
		{"o", "open https://example.atlassian.net/browse/DEMO-1"},
		{"p", "open https://github.com/example-org/web/pull/812"},
		{"O", "copy link https://example.atlassian.net/browse/DEMO-1"},
		{"P", "copy link https://github.com/example-org/web/pull/812"},
		{"g", "refresh"},
	}
	for _, tt := range tests {
		run(m, tt.key)
		if r.last() != tt.want {
			t.Errorf("%s → %q, want %q", tt.key, r.last(), tt.want)
		}
	}
	m = press(m, "h")
	before := len(r.calls)
	next, _ := run(m, "p")
	if len(r.calls) != before || !strings.Contains(next.View(), "DEMO-3 has no pull request yet") {
		t.Errorf("p without PR: calls %v\n%s", r.calls[before:], next.View())
	}
	next, _ = run(m, "P")
	if len(r.calls) != before || !strings.Contains(next.View(), "DEMO-3 has no pull request yet") {
		t.Errorf("P without PR: calls %v\n%s", r.calls[before:], next.View())
	}
}

func TestStatusAndErrors(t *testing.T) {
	r := &recorder{}
	m := loaded(r)
	m, _ = m.Update(StatusMsg("✓ Sent DEMO-1's feedback"))
	if !strings.Contains(m.View(), "✓ Sent DEMO-1's feedback") {
		t.Error("status not shown")
	}
	m, _ = m.Update(SnapshotMsg{Err: errors.New("offline")})
	if !strings.Contains(m.View(), "could not refresh: offline") || !strings.Contains(m.View(), "DEMO-1") {
		t.Error("failed refresh should keep the board and say why")
	}
	m, _ = m.Update(SnapshotMsg{Snap: snapshot(), Stale: errors.New("jira down")})
	if !strings.Contains(m.View(), "cached · ") {
		t.Error("stale board not marked")
	}
}

func TestQuit(t *testing.T) {
	for _, k := range []string{"q", "esc"} {
		m := loaded(&recorder{})
		var msg tea.KeyMsg
		if k == "esc" {
			msg = tea.KeyMsg{Type: tea.KeyEsc}
		} else {
			msg = tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(k)}
		}
		_, cmd := m.Update(msg)
		if cmd == nil {
			t.Fatalf("%s returned no command", k)
		}
		if _, ok := cmd().(tea.QuitMsg); !ok {
			t.Errorf("%s did not quit", k)
		}
	}
}

func TestArrowsAndWASDMove(t *testing.T) {
	for _, tt := range []struct {
		name  string
		right tea.KeyMsg
		down  tea.KeyMsg
	}{
		{"arrows", tea.KeyMsg{Type: tea.KeyRight}, tea.KeyMsg{Type: tea.KeyDown}},
		{"wasd", tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("d")}, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("s")}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			m := loaded(&recorder{})
			m, _ = m.Update(tt.right)
			if !strings.Contains(m.View(), "▸ DEMO-1") {
				t.Fatalf("right did not move to In Progress")
			}
			m = press(m, "v")
			m, _ = m.Update(tt.down)
			if line := selectedLine(m.View()); !strings.Contains(line, "DEMO-2") {
				t.Errorf("down in the list selected %q", line)
			}
		})
	}
}

func TestListColumnsLineUp(t *testing.T) {
	r := &recorder{}
	snap := snapshot()
	snap.Reviews[0].Repo = "example-organization/a-really-long-repository-name"
	snap.Reviews[0].Title = strings.Repeat("Long title ", 20)
	m := New(board.Snapshot{}, false, r, ViewList)
	next, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	next, _ = next.Update(SnapshotMsg{Snap: snap})
	var titleCols []int
	for _, l := range strings.Split(stripANSI(next.View()), "\n") {
		isRow := strings.HasPrefix(l, " ▸ ") || strings.HasPrefix(l, " ! ") || strings.HasPrefix(l, "   ")
		for _, word := range []string{"TITLE", "Filter accounts", "Keep sort", "Export CSV", "Long title"} {
			if i := strings.Index(l, word); i >= 0 && isRow {
				titleCols = append(titleCols, lipgloss.Width(l[:i]))
				break
			}
		}
		if lipgloss.Width(l) > 120 {
			t.Errorf("line wider than the terminal (%d): %q", lipgloss.Width(l), l)
		}
	}
	if len(titleCols) != 4 {
		t.Fatalf("found %d title columns: %v\n%s", len(titleCols), titleCols, stripANSI(next.View()))
	}
	for _, c := range titleCols {
		if c != titleCols[0] {
			t.Errorf("titles start at different columns: %v", titleCols)
			break
		}
	}
}

func stripANSI(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == 0x1b {
			for i < len(s) && s[i] != 'm' {
				i++
			}
			continue
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

func TestFooterWrapsToWidth(t *testing.T) {
	m := loaded(&recorder{})
	m, _ = m.Update(tea.WindowSizeMsg{Width: 70, Height: 30})
	for _, l := range strings.Split(stripANSI(m.View()), "\n") {
		if lipgloss.Width(l) > 70 {
			t.Errorf("line wider than 70 cells: %q", l)
		}
	}
}

func TestMatchesMockupDetails(t *testing.T) {
	r := &recorder{}
	snap := snapshot()
	snap.Sprints = []jira.Sprint{{Space: "DEMO", Name: "DEMO S42", End: snap.FetchedAt.Add(4 * 24 * time.Hour)}}
	m := New(board.Snapshot{}, false, r, ViewColumns).WithClock(func() time.Time { return snap.FetchedAt.Add(3 * time.Minute) })
	next, _ := m.Update(tea.WindowSizeMsg{Width: 160, Height: 40})
	next, _ = next.Update(SnapshotMsg{Snap: snap})
	view := stripANSI(next.View())
	for _, want := range []string{"DEMO · DEMO S42 · 4d left", "✓ synced 3 min ago", "1 Sprint 3", "2 PRs 3", "│ Columns │ │ List │", "DEMO-3  To Do  · Export CSV\n unassigned · enter asks for repos and opens Claude Code"} {
		if !strings.Contains(view, want) {
			t.Errorf("view missing %q:\n%s", want, view)
		}
	}
	next = press(next, "l")
	if !strings.Contains(stripANSI(next.View()), "DEMO-1  In Progress  · Filter accounts\n unassigned · CI failing · #812 · f sends the failure to the agent") {
		t.Errorf("preview hint missing:\n%s", stripANSI(next.View()))
	}
}

func TestTabCyclesFiltersAndNumbersJump(t *testing.T) {
	m := loaded(&recorder{})
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyTab})
	if v := stripANSI(m.View()); !strings.Contains(v, "▸ #812") || strings.Contains(v, "DEMO-3") {
		t.Errorf("tab should switch to PRs:\n%s", v)
	}
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyShiftTab})
	if !strings.Contains(stripANSI(m.View()), "DEMO-3") {
		t.Error("shift+tab should go back to All")
	}
	m = press(m, "2")
	if v := stripANSI(m.View()); strings.Contains(v, "DEMO-3") || !strings.Contains(v, "#820") {
		t.Errorf("2 should show PRs:\n%s", v)
	}
}

func TestSyncStateSitsBottomRight(t *testing.T) {
	snap := snapshot()
	for _, width := range []int{160, 60} {
		m := New(board.Snapshot{}, false, &recorder{}, ViewColumns).WithClock(func() time.Time { return snap.FetchedAt.Add(3 * time.Minute) })
		next, _ := m.Update(tea.WindowSizeMsg{Width: width, Height: 40})
		next, _ = next.Update(SnapshotMsg{Snap: snap})
		lines := strings.Split(strings.TrimRight(stripANSI(next.View()), "\n"), "\n")
		last := strings.TrimRight(lines[len(lines)-1], " ")
		if !strings.HasSuffix(last, "✓ synced 3 min ago") {
			t.Errorf("width %d: last line %q does not end with the sync state", width, last)
		}
		if lineIndex(next.View(), "synced") != len(lines)-1 {
			t.Errorf("width %d: sync state still in the header:\n%s", width, stripANSI(next.View()))
		}
		for i, l := range lines {
			if w := lipgloss.Width(l); w > width {
				t.Errorf("width %d: line %d is %d wide: %q", width, i, w, l)
			}
		}
	}
}

func TestRefreshingSpinsUntilTheBoardArrives(t *testing.T) {
	m := tea.Model(New(snapshot(), true, &recorder{}, ViewColumns))
	m, _ = m.Update(tea.WindowSizeMsg{Width: 160, Height: 40})
	if v := stripANSI(m.View()); !strings.Contains(v, "⠋ refreshing…") {
		t.Fatalf("no spinner while refreshing:\n%s", v)
	}
	m, cmd := m.Update(spinMsg{})
	if cmd == nil || !strings.Contains(stripANSI(m.View()), "⠙ refreshing…") {
		t.Errorf("spinner did not advance:\n%s", stripANSI(m.View()))
	}
	m, _ = m.Update(SnapshotMsg{Snap: snapshot()})
	if _, cmd := m.Update(spinMsg{}); cmd != nil {
		t.Error("spinner keeps ticking after the refresh")
	}
	if m, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("g")}); cmd == nil || !strings.Contains(stripANSI(m.View()), "refreshing…") {
		t.Error("g did not restart the spinner")
	}
}

func TestPreviewStartsWithTheTicketsLabel(t *testing.T) {
	snap := snapshot()
	snap.Tickets[0].Label = "User Access Center"
	m := tea.Model(New(snap, true, &recorder{}, ViewColumns))
	m, _ = m.Update(tea.WindowSizeMsg{Width: 160, Height: 40})
	if v := stripANSI(m.View()); !strings.Contains(v, "\n User Access Center · unassigned · ") {
		t.Errorf("preview lacks the label first:\n%s", v)
	}
}

func TestDaemonSyncsDriveTheSpinner(t *testing.T) {
	m := loaded(&recorder{})
	if v := stripANSI(m.View()); strings.Contains(v, "refreshing…") {
		t.Fatalf("spinning before any sync:\n%s", v)
	}
	m, _ = m.Update(SyncingMsg{})
	if !strings.Contains(stripANSI(m.View()), "refreshing…") {
		t.Fatalf("a daemon sync does not show:\n%s", stripANSI(m.View()))
	}
	m, _ = m.Update(SyncFailedMsg{Message: "rate limited"})
	if v := stripANSI(m.View()); strings.Contains(v, "refreshing…") || !strings.Contains(v, "could not refresh: rate limited") {
		t.Errorf("a failed sync does not show:\n%s", v)
	}
}

func TestRefreshKeepsTheSelectedTicketsPreview(t *testing.T) {
	m := press(loaded(&recorder{}), "g")
	lines := strings.Split(strings.TrimRight(stripANSI(m.View()), "\n"), "\n")
	if lineIndex(m.View(), "DEMO-3  To Do  · Export CSV") < 0 {
		t.Errorf("g replaced the selected ticket's preview:\n%s", strings.Join(lines, "\n"))
	}
	if n := strings.Count(stripANSI(m.View()), "refreshing…"); n != 1 || !strings.HasSuffix(strings.TrimRight(lines[len(lines)-1], " "), "refreshing…") {
		t.Errorf("refreshing… should show once, at the bottom right (%d times):\n%s", n, strings.Join(lines, "\n"))
	}
}

func TestDetailGetsTheBoardsTicket(t *testing.T) {
	r := &recorder{}
	press(loaded(r), "l", "i")
	if r.known == nil || r.known.Key != "DEMO-1" || len(r.known.PRs) != 1 || r.known.PRs[0].Number != 812 {
		t.Errorf("detail was not given the board's ticket: %+v", r.known)
	}
}

func TestSyncStateKeepsItsPlace(t *testing.T) {
	snap := snapshot()
	for width := 60; width <= 170; width++ {
		m := tea.Model(New(board.Snapshot{}, false, &recorder{}, ViewColumns).WithClock(func() time.Time { return snap.FetchedAt.Add(3 * time.Minute) }))
		m, _ = m.Update(tea.WindowSizeMsg{Width: width, Height: 40})
		m, _ = m.Update(SnapshotMsg{Snap: snap})
		synced := strings.Split(stripANSI(m.View()), "\n")
		m, _ = m.Update(SyncingMsg{})
		refreshing := strings.Split(stripANSI(m.View()), "\n")
		if a, b := lineIndex(strings.Join(synced, "\n"), "✓ synced"), lineIndex(strings.Join(refreshing, "\n"), "refreshing…"); a != b || len(synced) != len(refreshing) {
			t.Fatalf("width %d: synced on line %d of %d, refreshing on line %d of %d", width, a, len(synced), b, len(refreshing))
		}
	}
}

func TestAStuckRefreshGivesUp(t *testing.T) {
	base := snapshot().FetchedAt
	clock := base.Add(time.Minute)
	m := tea.Model(New(board.Snapshot{}, false, &recorder{}, ViewColumns).WithClock(func() time.Time { return clock }))
	fresh := snapshot()
	fresh.FetchedAt = clock.Add(90 * time.Second)
	m, _ = m.Update(SnapshotMsg{Snap: fresh})
	m, _ = m.Update(SyncingMsg{})
	clock = clock.Add(2 * time.Minute)
	m, _ = m.Update(tickMsg(clock))
	if v := stripANSI(m.View()); strings.Contains(v, "refreshing…") {
		t.Errorf("still refreshing after two minutes:\n%s", v)
	}
}

func TestTickRefreshesWhenStale(t *testing.T) {
	r := &recorder{}
	base := snapshot().FetchedAt
	clock := base.Add(time.Minute)
	m := New(board.Snapshot{}, false, r, ViewColumns).WithClock(func() time.Time { return clock })
	next, _ := m.Update(SnapshotMsg{Snap: snapshot()})
	r.calls = nil
	next, _ = next.Update(tickMsg(clock))
	if len(r.calls) != 0 {
		t.Errorf("refreshed a fresh board: %v", r.calls)
	}
	clock = base.Add(3 * time.Minute)
	next, _ = next.Update(tickMsg(clock))
	if r.last() != "refresh" || !strings.Contains(stripANSI(next.View()), "refreshing…") {
		t.Errorf("stale board not refreshed: %v", r.calls)
	}
}

func click(m tea.Model, x, y int) tea.Model {
	m, _ = m.Update(tea.MouseMsg{X: x, Y: y, Button: tea.MouseButtonLeft, Action: tea.MouseActionPress})
	return m
}

func lineIndex(view, needle string) int {
	for i, l := range strings.Split(stripANSI(view), "\n") {
		if strings.Contains(l, needle) {
			return i
		}
	}
	return -1
}

func TestMouseSelectsCardsRowsAndFilters(t *testing.T) {
	m := loaded(&recorder{})
	view := m.View()
	y := lineIndex(view, "DEMO-2")
	x := strings.Index(strings.Split(stripANSI(view), "\n")[y], "DEMO-2")
	m = click(m, lipgloss.Width(strings.Split(stripANSI(view), "\n")[y][:x]), y)
	if !strings.Contains(stripANSI(m.View()), "▸ DEMO-2") {
		t.Fatalf("click on DEMO-2's card did not select it:\n%s", stripANSI(m.View()))
	}

	header := strings.Split(stripANSI(m.View()), "\n")[1]
	m = click(m, lipgloss.Width(header[:strings.Index(header, "List")])+1, 1)
	if !strings.Contains(stripANSI(m.View()), "↑↓ ws move") {
		t.Fatalf("click on List did not switch view:\n%s", header)
	}
	view = m.View()
	y = lineIndex(view, "DEMO-3")
	m = click(m, 5, y)
	if line := selectedLine(m.View()); !strings.Contains(line, "DEMO-3") {
		t.Errorf("click on DEMO-3's row selected %q", line)
	}

	header = strings.Split(stripANSI(m.View()), "\n")[1]
	m = click(m, lipgloss.Width(header[:strings.Index(header, "PRs")]), 2)
	if v := stripANSI(m.View()); strings.Contains(v, "DEMO-3") || !strings.Contains(v, "#812") {
		t.Errorf("click on PRs did not filter:\n%s", v)
	}
}

func TestMouseWheelMoves(t *testing.T) {
	m := press(loaded(&recorder{}), "v")
	m, _ = m.Update(tea.MouseMsg{Button: tea.MouseButtonWheelDown, Action: tea.MouseActionPress})
	if line := selectedLine(m.View()); !strings.Contains(line, "DEMO-2") {
		t.Errorf("wheel down selected %q", line)
	}
	m, _ = m.Update(tea.MouseMsg{Button: tea.MouseButtonWheelUp, Action: tea.MouseActionPress})
	if line := selectedLine(m.View()); !strings.Contains(line, "DEMO-1") {
		t.Errorf("wheel up selected %q", line)
	}
}

func TestFooterStaysAtTheBottom(t *testing.T) {
	m := loaded(&recorder{})
	m, _ = m.Update(tea.WindowSizeMsg{Width: 160, Height: 50})
	lines := strings.Split(stripANSI(m.View()), "\n")
	if len(lines) != 50 {
		t.Fatalf("view has %d lines, want the full height 50", len(lines))
	}
	if !strings.Contains(lines[len(lines)-1], "q close") {
		t.Errorf("last line = %q", lines[len(lines)-1])
	}
}

func TestBoardScrollsToKeepSelectionVisible(t *testing.T) {
	r := &recorder{}
	snap := snapshot()
	for i := 10; i < 40; i++ {
		snap.Tickets = append(snap.Tickets, ticket.Ticket{Key: ticket.Key(fmt.Sprintf("DEMO-%d", i)), Summary: "filler", Status: "To Do", Category: ticket.ToDo})
	}
	m := New(board.Snapshot{}, false, r, ViewList)
	next, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 15})
	next, _ = next.Update(SnapshotMsg{Snap: snap})
	for i := 0; i < 25; i++ {
		next = press(next, "s")
	}
	view := stripANSI(next.View())
	lines := strings.Split(view, "\n")
	bottom := strings.Join(lines[len(lines)-2:], "\n")
	if len(lines) != 15 || !strings.Contains(view, "▸ ") || !strings.Contains(bottom, "close") || !strings.Contains(bottom, "synced") || !strings.HasPrefix(lines[1], " tkt") {
		t.Errorf("scrolled view lost the header, selection or legend:\n%s", view)
	}
}

func TestReviewsRenderLikeTheMockup(t *testing.T) {
	r := &recorder{}
	snap := snapshot()
	snap.Reviews[0].Checks = ticket.ChecksPass
	m := New(board.Snapshot{}, false, r, ViewColumns)
	next, _ := m.Update(tea.WindowSizeMsg{Width: 160, Height: 40})
	next, _ = next.Update(SnapshotMsg{Snap: snap})
	view := stripANSI(next.View())
	if !strings.Contains(view, "│ ! #820 │") {
		t.Errorf("review chip should be boxed with just the PR number:\n%s", view)
	}
	next = press(next, "2")
	line := ""
	for _, l := range strings.Split(stripANSI(next.View()), "\n") {
		if strings.Contains(l, "#820") && strings.Contains(l, "Review requested") {
			line = l
		}
	}
	if !strings.Contains(line, "Review requested   alex   ✓") || strings.Contains(line, "example-org") {
		t.Errorf("review row should show its author and the CI mark: %q", line)
	}
	for i := 0; i < 10 && !strings.Contains(selectedLine(next.View()), "#820"); i++ {
		next = press(next, "s")
	}
	if !strings.Contains(stripANSI(next.View()), "CI passing ✓") {
		t.Error("preview does not spell out the CI state")
	}
}

func TestTicketRowsNameRepoAndPR(t *testing.T) {
	snap := snapshot()
	snap.Tickets[2].PRs = []ticket.PullRequest{
		{Repo: "example-org/web", Number: 806, State: ticket.PROpen, Checks: ticket.ChecksPass},
		{Repo: "example-org/api", Number: 311, State: ticket.PROpen, Checks: ticket.ChecksPending},
	}
	m := New(board.Snapshot{}, false, &recorder{}, ViewList)
	next, _ := m.Update(tea.WindowSizeMsg{Width: 140, Height: 30})
	next, _ = next.Update(SnapshotMsg{Snap: snap})
	if v := stripANSI(next.View()); !strings.Contains(v, "#806 ✓ #311 …") || !strings.Contains(v, " —  ") {
		t.Errorf("PR / CI column:\n%s", v)
	}
}

func TestCardsShowDetailsAndDensityToggles(t *testing.T) {
	snap := snapshot()
	snap.Tickets[1].Type = "Story"
	snap.Tickets[1].PRs[0].Repo = "example-org/web"
	snap.Tickets[1].Subtasks = []ticket.Ticket{{Key: "DEMO-9"}}
	m := New(board.Snapshot{}, false, &recorder{}, ViewColumns)
	next, _ := m.Update(tea.WindowSizeMsg{Width: 160, Height: 40})
	next, _ = next.Update(SnapshotMsg{Snap: snap})
	v := stripANSI(next.View())
	for _, want := range []string{"#812 ✕", "■ 0/1 subtasks · unassigned"} {
		if !strings.Contains(v, want) {
			t.Errorf("detailed card missing %q:\n%s", want, v)
		}
	}
	next = press(next, "z")
	v = stripANSI(next.View())
	if strings.Contains(v, "#812 ✕") || !strings.Contains(v, "Filter accounts") || !strings.Contains(v, "compact cards") {
		t.Errorf("compact cards:\n%s", v)
	}
	next = click(next, 40, lineIndex(next.View(), "DEMO-1"))
	if !strings.Contains(stripANSI(next.View()), "▸ DEMO-1") {
		t.Error("click hit-testing broke in compact mode")
	}
}

func TestReviewChipShowsTicketKey(t *testing.T) {
	snap := snapshot()
	snap.Reviews[0].Branch = "refactor/demo-170448-users"
	m := New(board.Snapshot{}, false, &recorder{}, ViewColumns)
	next, _ := m.Update(tea.WindowSizeMsg{Width: 160, Height: 40})
	next, _ = next.Update(SnapshotMsg{Snap: snap})
	if v := stripANSI(next.View()); !strings.Contains(v, "! #820 · DEMO-170448") || strings.Contains(v, "Add pagination") {
		t.Errorf("chip:\n%s", v)
	}
}

func TestActionsMenuGoesToARunningAgent(t *testing.T) {
	tests := []struct {
		agent, want, dontWant string
	}{
		{"working", "Go to the agent in Herdr", "Start · open the agent"},
		{"idle", "Go to the agent in Herdr", "Start · open the agent"},
		{"", "Start · open the agent", "Go to the agent"},
	}
	for _, tt := range tests {
		t.Run(tt.agent, func(t *testing.T) {
			snap := snapshot()
			if tt.agent != "" {
				snap.Agents = map[string]string{"DEMO-1": tt.agent}
			}
			r := &recorder{}
			m := tea.Model(New(snap, true, r, ViewColumns))
			m, _ = m.Update(tea.WindowSizeMsg{Width: 160, Height: 40})
			m = press(m, "l", "enter")
			v := stripANSI(m.View())
			if !strings.Contains(v, tt.want) || strings.Contains(v, tt.dontWant) {
				t.Fatalf("menu:\n%s", v)
			}
			startFromMenu(m)
			if !slices.Contains(r.calls, "start DEMO-1") {
				t.Errorf("calls = %v", r.calls)
			}
		})
	}
}

// startFromMenu runs the menu's highlighted Start and feeds back what StartChoices reports.
func startFromMenu(m tea.Model) tea.Model {
	m, msg := run(m, "enter")
	if msg != nil {
		m, _ = m.Update(msg)
	}
	return m
}

func twoWorktrees() StartChoices {
	return StartChoices{Worktrees: []WorktreeChoice{
		{Branch: "feature/DEMO-1-a", Path: "/src/web-DEMO-1-a"},
		{Branch: "feature/DEMO-1-b", Path: "/src/web-DEMO-1-b", Agent: "working"},
	}}
}

func TestStartAsksWhichWorktreeInAModal(t *testing.T) {
	for _, width := range []int{160, 60} {
		r := &recorder{choices: twoWorktrees()}
		m := tea.Model(New(snapshot(), true, r, ViewList))
		m, _ = m.Update(tea.WindowSizeMsg{Width: width, Height: 40})
		m = startFromMenu(press(m, "enter"))
		v := stripANSI(m.View())
		for _, want := range []string{"Open DEMO-1", "Which worktree?", "feature/DEMO-1-a", "no agent", "feature/DEMO-1-b", "● working", "Open", "Cancel"} {
			if !strings.Contains(v, want) {
				t.Fatalf("width %d: modal lacks %q:\n%s", width, want, v)
			}
		}
		for i, l := range strings.Split(v, "\n") {
			if w := lipgloss.Width(l); w > width {
				t.Errorf("width %d: line %d is %d wide: %q", width, i, w, l)
			}
		}
		if slices.ContainsFunc(r.calls, func(c string) bool { return strings.HasPrefix(c, "start") }) {
			t.Fatalf("started before choosing: %v", r.calls)
		}
		press(m, "s", "enter")
		if r.last() != "start DEMO-1 worktree=/src/web-DEMO-1-b" {
			t.Errorf("width %d: calls = %v", width, r.calls)
		}
	}
}

func TestWorktreeModalCancels(t *testing.T) {
	r := &recorder{choices: twoWorktrees()}
	m := startFromMenu(press(loaded(r), "l", "enter"))
	if v := stripANSI(press(m, "esc").View()); strings.Contains(v, "Which worktree?") || r.last() != "choices DEMO-1" {
		t.Errorf("esc: calls %v\n%s", r.calls, v)
	}
}

func TestWorktreeModalWorksWithTheMouse(t *testing.T) {
	r := &recorder{choices: twoWorktrees()}
	m := startFromMenu(press(loaded(r), "l", "enter"))
	v := m.View()
	y := lineIndex(v, "feature/DEMO-1-b")
	x := strings.Index(strings.Split(stripANSI(v), "\n")[y], "feature/DEMO-1-b")
	click(m, x+2, y)
	if r.last() != "start DEMO-1 worktree=/src/web-DEMO-1-b" {
		t.Errorf("click on a worktree: calls = %v", r.calls)
	}
	m = startFromMenu(press(loaded(r), "l", "enter"))
	v = m.View()
	y = lineIndex(v, "Cancel")
	x = strings.Index(strings.Split(stripANSI(v), "\n")[y], "Cancel")
	if v := stripANSI(click(m, x+1, y).View()); strings.Contains(v, "Which worktree?") || strings.HasPrefix(r.last(), "start") {
		t.Errorf("click on Cancel: calls %v\n%s", r.calls, v)
	}
}

func twoRepos() StartChoices {
	return StartChoices{Repos: []RepoChoice{{Name: "example-org/web"}, {Name: "example-org/api", LastUsed: true, Checked: true}}}
}

func TestStartAsksWhichRepositoriesInAModal(t *testing.T) {
	for _, width := range []int{160, 56} {
		r := &recorder{choices: twoRepos()}
		m := tea.Model(New(snapshot(), true, r, ViewList))
		m, _ = m.Update(tea.WindowSizeMsg{Width: width, Height: 40})
		m = startFromMenu(press(m, "enter"))
		v := stripANSI(m.View())
		for _, want := range []string{"Start DEMO-1", "Which repositories?", "[ ] example-org/web", "[x] example-org/api", "last used", "Start", "Cancel", "space toggle"} {
			if !strings.Contains(v, want) {
				t.Fatalf("width %d: modal lacks %q:\n%s", width, want, v)
			}
		}
		for i, l := range strings.Split(v, "\n") {
			if w := lipgloss.Width(l); w > width {
				t.Errorf("width %d: line %d is %d wide: %q", width, i, w, l)
			}
		}
		if press(m, "enter"); r.last() != "start DEMO-1 repos=example-org/api" {
			t.Errorf("width %d: default = %v", width, r.calls)
		}
	}
	r := &recorder{choices: twoRepos()}
	m := startFromMenu(press(loaded(r), "l", "enter"))
	if press(m, " ", "enter"); r.last() != "start DEMO-1 repos=example-org/web,example-org/api" {
		t.Errorf("space on web, then enter: %v", r.calls)
	}
	m = startFromMenu(press(loaded(r), "l", "enter"))
	m = press(m, "s", " ", "enter")
	if v := stripANSI(m.View()); !strings.Contains(v, "pick at least one repository") || !strings.Contains(v, "Which repositories?") || strings.HasPrefix(r.last(), "start") {
		t.Errorf("nothing checked: calls %v\n%s", r.calls, v)
	}
}

func TestRepositoryModalWorksWithTheMouse(t *testing.T) {
	r := &recorder{choices: twoRepos()}
	m := startFromMenu(press(loaded(r), "l", "enter"))
	at := func(m tea.Model, s string) (int, int) {
		v := m.View()
		y := lineIndex(v, s)
		return strings.Index(strings.Split(stripANSI(v), "\n")[y], s) + 1, y
	}
	x, y := at(m, "example-org/web")
	m = click(m, x, y)
	if v := stripANSI(m.View()); !strings.Contains(v, "[x] example-org/web") || strings.HasPrefix(r.last(), "start") {
		t.Fatalf("click on a repository should toggle it, not start:\n%s", v)
	}
	_, y = at(m, "Cancel")
	click(m, strings.Index(strings.Split(stripANSI(m.View()), "\n")[y], "Start")+1, y)
	if r.last() != "start DEMO-1 repos=example-org/web,example-org/api" {
		t.Errorf("click on Start: calls = %v", r.calls)
	}
}

func TestStartFromTheDetailViewShowsTheModal(t *testing.T) {
	r := &recorder{choices: twoWorktrees()}
	m := press(loaded(r), "l", "i")
	m, _ = m.Update(DetailMsg{Key: "DEMO-1", Text: "DEMO-1 details"})
	m = startFromMenu(press(m, "enter"))
	if v := stripANSI(m.View()); !strings.Contains(v, "Which worktree?") {
		t.Fatalf("modal hidden behind the detail view:\n%s", v)
	}
	v := m.View()
	y := lineIndex(v, "feature/DEMO-1-b")
	click(m, strings.Index(strings.Split(stripANSI(v), "\n")[y], "feature/DEMO-1-b")+2, y)
	if r.last() != "start DEMO-1 worktree=/src/web-DEMO-1-b" {
		t.Errorf("click on the modal over the detail view: calls = %v", r.calls)
	}
}

func TestModalButtonsWorkWithTheMouse(t *testing.T) {
	button := func(m tea.Model, label string) (int, int) {
		v := stripANSI(m.View())
		y := lineIndex(v, "Cancel")
		return strings.Index(strings.Split(v, "\n")[y], label) + 1, y
	}
	r := &recorder{choices: twoWorktrees()}
	m := startFromMenu(press(loaded(r), "l", "enter"))
	x, y := button(m, "Open")
	if click(m, x, y); r.last() != "start DEMO-1 worktree=/src/web-DEMO-1-a" {
		t.Errorf("Open button: calls = %v", r.calls)
	}
	r = &recorder{choices: twoRepos()}
	m = startFromMenu(press(loaded(r), "l", "enter"))
	x, y = button(m, "Cancel")
	if v := stripANSI(click(m, x, y).View()); strings.Contains(v, "Which repositories?") || strings.HasPrefix(r.last(), "start") {
		t.Errorf("Cancel button: calls %v\n%s", r.calls, v)
	}
}

func TestWorktreeRowsNameTheirRepositoryWhenTheyDiffer(t *testing.T) {
	r := &recorder{choices: StartChoices{Worktrees: []WorktreeChoice{
		{Repo: "example-org/web", Branch: "feature/DEMO-1", Path: "/src/web-DEMO-1"},
		{Repo: "example-org/api", Branch: "feature/DEMO-1", Path: "/src/api-DEMO-1"},
	}}}
	v := stripANSI(startFromMenu(press(loaded(r), "l", "enter")).View())
	if !strings.Contains(v, "web · feature/DEMO-1") || !strings.Contains(v, "api · feature/DEMO-1") {
		t.Errorf("rows do not tell the repositories apart:\n%s", v)
	}
}

func TestModalClicksInAShortPane(t *testing.T) {
	r := &recorder{choices: twoWorktrees()}
	m := tea.Model(New(snapshot(), true, r, ViewList))
	m, _ = m.Update(tea.WindowSizeMsg{Width: 120, Height: 14})
	m = startFromMenu(press(m, "enter"))
	v := m.View()
	y := lineIndex(v, "feature/DEMO-1-b")
	if y < 0 {
		t.Skip("row not visible in so short a pane")
	}
	click(m, strings.Index(strings.Split(stripANSI(v), "\n")[y], "feature/DEMO-1-b")+2, y)
	if r.last() != "start DEMO-1 worktree=/src/web-DEMO-1-b" {
		t.Errorf("click in a short pane: calls = %v", r.calls)
	}
}

func TestStartWithNothingToAskRunsAtOnce(t *testing.T) {
	r := &recorder{}
	startFromMenu(press(loaded(r), "l", "enter"))
	if r.last() != "start DEMO-1" {
		t.Errorf("calls = %v", r.calls)
	}
}

func TestActionsMenu(t *testing.T) {
	r := &recorder{}
	m := press(loaded(r), "l", "enter")
	v := stripANSI(m.View())
	for _, want := range []string{"Actions for DEMO-1 · In Progress", "Start · open the agent", "Show details", "Open the ticket pane next to its agent", "Open in Jira ↗", "Open #812 ✕ in GitHub ↗", "Send the CI failure to the agent", "Copy the key DEMO-1", "Copy the ticket context (.ticket.md)", "enter run · esc close"} {
		if !strings.Contains(v, want) {
			t.Errorf("menu missing %q:\n%s", want, v)
		}
	}
	if strings.Contains(v, "Send the review comments") {
		t.Error("offered review comments for a ticket without requested changes")
	}
	if strings.Contains(v, "Copy the Jira link") {
		t.Errorf("the Jira link is copied with c from its own row, not from an extra row:\n%s", v)
	}
	m = press(m, "s", "s", "s", "s", "s")
	_, msg := run(m, "enter")
	if r.last() != "fix DEMO-1 known" || msg != StatusMsg("fix DEMO-1 known") {
		t.Errorf("sixth item ran %v", r.calls)
	}
	if _, msg := run(selectMenuItem(t, press(loaded(r), "l", "enter"), "Copy the PR link"), "enter"); msg != StatusMsg("copy link https://github.com/example-org/web/pull/812") {
		t.Errorf("the pull request's link should be copyable from a row: %v", r.calls)
	}
	m = press(loaded(r), "enter", "w")
	run(m, "enter")
	if r.last() != "context DEMO-3" {
		t.Errorf("w from the top should wrap to the last item: %v", r.calls)
	}
	m = press(loaded(r), "enter")
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if strings.Contains(stripANSI(m.View()), "Actions for") {
		t.Error("esc did not close the menu")
	}
	if _, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEsc}); cmd == nil {
		t.Error("esc outside the menu should still close the board")
	}
}

func TestActionsMenuClick(t *testing.T) {
	r := &recorder{}
	m := press(loaded(r), "enter")
	v := m.View()
	y := lineIndex(v, "Open in Jira ↗")
	line := strings.Split(stripANSI(v), "\n")[y]
	m = click(m, lipgloss.Width(line[:strings.Index(line, "Open in Jira")]), y)
	if r.last() != "open https://example.atlassian.net/browse/DEMO-3" {
		t.Errorf("click ran %v", r.calls)
	}
}

func (r *recorder) Copy(text, what string) tea.Cmd   { return r.cmd("copy " + what + " " + text) }
func (r *recorder) CopyContext(k ticket.Key) tea.Cmd { return r.cmd("context " + string(k)) }

func TestPRColumnMatchesMockup(t *testing.T) {
	cases := []struct {
		prs  []ticket.PullRequest
		want string
	}{
		{nil, "—"},
		{[]ticket.PullRequest{{Number: 812, State: ticket.PROpen, Checks: ticket.ChecksFail}}, "#812 ✕"},
		{[]ticket.PullRequest{{Number: 806, State: ticket.PROpen, Checks: ticket.ChecksPass}, {Number: 311, State: ticket.PROpen, Checks: ticket.ChecksPending}}, "#806 ✓ #311 …"},
		{[]ticket.PullRequest{{Number: 790, State: ticket.PRMerged}}, "#790 merged"},
		{[]ticket.PullRequest{{Number: 1, State: ticket.PRMerged}, {Number: 2, State: ticket.PRMerged}}, "2 merged"},
	}
	for _, c := range cases {
		if got := prRefs(c.prs); got != c.want {
			t.Errorf("prRefs = %q, want %q", got, c.want)
		}
	}
}

func TestWrapWords(t *testing.T) {
	cases := []struct {
		s     string
		width int
		n     int
		want  []string
	}{
		{"Filter accounts", 20, 2, []string{"Filter accounts", ""}},
		{"Homogenize the organizations module (pilot)", 20, 2, []string{"Homogenize the", "organizations modul…"}},
		{"Adapt tests outside modules", 14, 2, []string{"Adapt tests", "outside modul…"}},
		{"short", 10, 1, []string{"short"}},
		{"Supercalifragilisticexpialidocious word", 10, 2, []string{"Supercali…", "word"}},
	}
	for _, c := range cases {
		got := wrapWords(c.s, c.width, c.n)
		if strings.Join(got, "|") != strings.Join(c.want, "|") {
			t.Errorf("wrapWords(%q, %d, %d) = %q, want %q", c.s, c.width, c.n, got, c.want)
		}
		for _, l := range got {
			if lipgloss.Width(l) > c.width {
				t.Errorf("line %q exceeds %d", l, c.width)
			}
		}
	}
}

func TestPRColumnFitsItsContent(t *testing.T) {
	m := press(loaded(&recorder{}), "v")
	for _, l := range strings.Split(stripANSI(m.View()), "\n") {
		if strings.Contains(l, "DEMO-1") && strings.Contains(l, "#812 ✕") {
			gap := l[strings.Index(l, "#812 ✕")+len("#812 ✕"):]
			if spaces := len(gap) - len(strings.TrimLeft(gap, " ")); spaces > 4 {
				t.Errorf("PR / CI column wastes %d spaces: %q", spaces, l)
			}
			return
		}
	}
	t.Fatal("row not found")
}

func TestClickOnReviewChipOpensThePR(t *testing.T) {
	r := &recorder{}
	m := loaded(r)
	v := m.View()
	y := lineIndex(v, "! #820")
	line := strings.Split(stripANSI(v), "\n")[y]
	x := lipgloss.Width(line[:strings.Index(line, "#820")])
	_, cmd := m.Update(tea.MouseMsg{X: x, Y: y, Button: tea.MouseButtonLeft, Action: tea.MouseActionPress})
	if cmd == nil {
		t.Fatal("click on the chip did nothing")
	}
	cmd()
	if r.last() != "open https://github.com/example-org/web/pull/820" {
		t.Errorf("click ran %v", r.calls)
	}
	before := len(r.calls)
	m, cmd = m.Update(tea.MouseMsg{X: 2, Y: y, Button: tea.MouseButtonLeft, Action: tea.MouseActionPress})
	if cmd != nil || len(r.calls) != before {
		t.Error("click on the strip label should do nothing")
	}
}

func TestKeyboardReachesReviewChips(t *testing.T) {
	r := &recorder{}
	snap := snapshot()
	snap.Reviews = append(snap.Reviews, ticket.PullRequest{Repo: "example-org/api", Number: 821, Title: "Second", URL: "https://github.com/example-org/api/pull/821"})
	m := New(board.Snapshot{}, false, r, ViewColumns)
	next, _ := m.Update(tea.WindowSizeMsg{Width: 160, Height: 40})
	next, _ = next.Update(SnapshotMsg{Snap: snap})
	next = press(next, "w")
	v := stripANSI(next.View())
	if !strings.Contains(v, "▸ #820") || !strings.Contains(v, "#820 · Add pagination") {
		t.Fatalf("up from the top card should select the first review chip:\n%s", v)
	}
	if strings.Contains(v, "▸ DEMO-") {
		t.Errorf("a card stays selected while the cursor is on the reviews:\n%s", v)
	}
	next = press(next, "d")
	if !strings.Contains(stripANSI(next.View()), "▸ #821") {
		t.Fatalf("right should move to the next chip:\n%s", stripANSI(next.View()))
	}
	next = press(next, "enter")
	if v := stripANSI(next.View()); !strings.Contains(v, "Pull request #821 · example-org/api  ") || !strings.Contains(v, "unknown author · review requested") {
		t.Fatalf("enter on a chip should open its menu:\n%s", stripANSI(next.View()))
	}
	next, _ = run(next, "enter")
	if r.last() != "open https://github.com/example-org/api/pull/821" {
		t.Errorf("menu ran %v", r.calls)
	}
	next = press(next, "s")
	if v := stripANSI(next.View()); strings.Contains(v, "▸ #821") || !strings.Contains(v, "▸ DEMO-") {
		t.Errorf("down should return to the columns:\n%s", v)
	}
}

func (r *recorder) Detail(k ticket.Key, known *ticket.Ticket) tea.Cmd {
	r.calls = append(r.calls, "detail "+string(k))
	r.known = known
	return func() tea.Msg {
		return DetailMsg{Key: k, Text: string(k) + " · Story · In Progress\nTitle line\n\nDescription\n" + strings.Repeat("body line\n", 60)}
	}
}

func TestDetailViewInsideTheBoard(t *testing.T) {
	r := &recorder{}
	m := press(loaded(r), "l")
	m, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("i")})
	if !strings.Contains(stripANSI(m.View()), "loading DEMO-1") || cmd == nil {
		t.Fatalf("i should start loading the detail:\n%s", stripANSI(m.View()))
	}
	m, _ = m.Update(cmd())
	v := stripANSI(m.View())
	if !strings.Contains(v, "DEMO-1 · Story · In Progress") || !strings.Contains(v, "back to the board") || strings.Contains(v, "To Do 1") {
		t.Fatalf("detail view:\n%s", v)
	}
	m = press(m, "s", "s", "s")
	if !strings.Contains(stripANSI(m.View()), "line 4 of") {
		t.Errorf("scrolling did not move:\n%s", stripANSI(m.View()))
	}
	m = press(m, "enter")
	if !strings.Contains(stripANSI(m.View()), "Actions for DEMO-1") {
		t.Errorf("enter in the detail should open the actions menu")
	}
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if v := stripANSI(m.View()); !strings.Contains(v, "In Progress") || strings.Contains(v, "Actions for") {
		t.Errorf("esc should return to the board:\n%s", v)
	}
}

func TestMenuShowDetailsOpensTheDetail(t *testing.T) {
	r := &recorder{}
	m := press(loaded(r), "enter", "s")
	m, cmd := run(m, "enter")
	if cmd == nil || r.last() != "detail DEMO-3" {
		t.Fatalf("show details ran %v", r.calls)
	}
	m, _ = m.Update(cmd)
	if !strings.Contains(stripANSI(m.View()), "DEMO-3 · Story") {
		t.Errorf("detail not shown:\n%s", stripANSI(m.View()))
	}
}

func TestListShowsReviewRequestsInTheStrip(t *testing.T) {
	r := &recorder{}
	m := press(loaded(r), "v")
	v := stripANSI(m.View())
	strip, head := lineIndex(v, "│ ! #820 │"), lineIndex(v, "KEY")
	if strip < 0 || head < 0 || strip > head || strings.Contains(v, "#820          Review requested") || strings.Contains(v, "Review requested · ") {
		t.Fatalf("the list holds only tickets, under the review strip:\n%s", v)
	}
	if lines := strings.Split(v, "\n"); head != strip+3 || strings.TrimSpace(lines[head-1]) != "" {
		t.Errorf("a blank line should part the strip from the list's header:\n%s", v)
	}
	if row := click(m, 6, lineIndex(m.View(), "DEMO-2 ")); !strings.Contains(selectedLine(row.View()), "DEMO-2") {
		t.Errorf("a click on a ticket under the strip selects it: %q", selectedLine(row.View()))
	}
	for i := 0; i < 5 && !strings.Contains(stripANSI(m.View()), "▸ #820"); i++ {
		m = press(m, "w")
	}
	if !strings.Contains(stripANSI(m.View()), "▸ #820") {
		t.Fatalf("up from the first ticket should reach the strip:\n%s", stripANSI(m.View()))
	}
	if v := stripANSI(press(m, "enter").View()); !strings.Contains(v, "Pull request #820") {
		t.Errorf("enter on a chip opens its menu:\n%s", v)
	}
	if line := selectedLine(press(m, "s").View()); !strings.Contains(line, "DEMO-") {
		t.Errorf("down from the strip goes back to the tickets: %q", line)
	}
	y := lineIndex(m.View(), "#820 │")
	x := strings.Index(strings.Split(stripANSI(m.View()), "\n")[y], "#820")
	_, cmd := m.Update(tea.MouseMsg{X: x, Y: y, Button: tea.MouseButtonLeft, Action: tea.MouseActionPress})
	if cmd == nil || cmd() != StatusMsg("open https://github.com/example-org/web/pull/820") {
		t.Errorf("a click on a chip opens its pull request")
	}
}

func TestListGroupsReviewsByRepo(t *testing.T) {
	snap := snapshot()
	snap.Reviews = []ticket.PullRequest{
		{Repo: "example-org/api", Number: 4, Title: "Rate limit"},
		{Repo: "example-org/web", Number: 2, Title: "Fix header"},
		{Repo: "example-org/web", Number: 9, Title: "Add pagination"},
	}
	m := tea.Model(New(snap, false, &recorder{}, ViewList))
	m, _ = m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	v := stripANSI(press(m, "2").View())
	api, web := strings.Index(v, "Review requested · example-org/api  1"), strings.Index(v, "Review requested · example-org/web  2")
	if api < 0 || web < 0 || api > web {
		t.Fatalf("repo sections missing or out of order:\n%s", v)
	}
	if i := strings.Index(v, "#4 "); i < api || i > web {
		t.Errorf("#4 not under its repo:\n%s", v)
	}
	if i, j := strings.Index(v, "#2 "), strings.Index(v, "#9 "); i < web || j < web {
		t.Errorf("web reviews not under their repo:\n%s", v)
	}
}

func TestPRsFilterTogglesBots(t *testing.T) {
	snap := snapshot()
	snap.Reviews = append(snap.Reviews, ticket.PullRequest{Repo: "example-org/web", Number: 830, Title: "Bump vite", Author: "dependabot", AuthorBot: true})
	m := tea.Model(New(snap, false, &recorder{}, ViewColumns))
	m, _ = m.Update(tea.WindowSizeMsg{Width: 160, Height: 40})
	if v := stripANSI(m.View()); strings.Contains(v, "#830") || !strings.Contains(v, "2 PRs 3") || !strings.Contains(v, "1 Sprint 3") {
		t.Fatalf("bot review shown by default:\n%s", v)
	}
	if v := stripANSI(press(m, "b").View()); strings.Contains(v, "#830") {
		t.Errorf("b outside the PRs filter showed bots:\n%s", v)
	}
	m = press(m, "2")
	v := stripANSI(m.View())
	if strings.Contains(v, "#830") || !strings.Contains(v, "b show bots · 1 hidden") || !strings.Contains(v, "b bots") {
		t.Fatalf("reviews filter before b:\n%s", v)
	}
	shown := press(m, "b")
	if v := stripANSI(shown.View()); !strings.Contains(v, "#830") || !strings.Contains(v, "b hide bots") || !strings.Contains(v, "2 PRs 4") {
		t.Errorf("b did not show bots:\n%s", v)
	}
	if v := stripANSI(press(shown, "b").View()); strings.Contains(v, "#830") {
		t.Errorf("second b did not hide bots:\n%s", v)
	}
	clicked := click(m, 6, lineIndex(m.View(), "b show bots"))
	if v := stripANSI(clicked.View()); !strings.Contains(v, "#830") {
		t.Errorf("click on the toggle did not show bots:\n%s", v)
	}
	if v := stripANSI(click(clicked, 6, lineIndex(clicked.View(), "b hide bots")).View()); strings.Contains(v, "#830") {
		t.Errorf("second click did not hide bots:\n%s", v)
	}
}

func TestBotToggleHidesWithoutBotReviews(t *testing.T) {
	m := press(loaded(&recorder{}), "3")
	if v := stripANSI(m.View()); strings.Contains(v, "show bots") {
		t.Errorf("toggle shown with no bot reviews:\n%s", v)
	}
}

func TestBlockedTicketsAreMarked(t *testing.T) {
	snap := snapshot()
	snap.Tickets = append(snap.Tickets, ticket.Ticket{Key: "DEMO-7", Summary: "Wait for client", Status: "Blocked", Mark: ticket.MarkBlocked, Category: ticket.ToDo})
	for _, view := range []string{ViewColumns, ViewList} {
		m := tea.Model(New(snap, false, &recorder{}, view))
		m, _ = m.Update(tea.WindowSizeMsg{Width: 160, Height: 40})
		var line string
		for _, l := range strings.Split(stripANSI(m.View()), "\n") {
			if strings.Contains(l, "DEMO-7") {
				line = l
			}
		}
		if !strings.Contains(line, "⊘") {
			t.Errorf("%s: blocked ticket not marked: %q", view, line)
		}
		for _, l := range strings.Split(stripANSI(m.View()), "\n") {
			if strings.Contains(l, "DEMO-3") && strings.Contains(l, "⊘") {
				t.Errorf("%s: unblocked ticket marked: %q", view, l)
			}
		}
	}
}

func TestBlockedMarkShrinksToItsIcon(t *testing.T) {
	failing := []ticket.PullRequest{{State: ticket.PROpen, Checks: ticket.ChecksFail}}
	tests := []struct {
		name     string
		tk       ticket.Ticket
		outer    int
		want     []string
		dontWant string
	}{
		{"room for the word", ticket.Ticket{Key: "DEMO-7", Status: "Blocked", Mark: ticket.MarkBlocked}, 40, []string{"⊘ Blocked"}, ""},
		{"too narrow", ticket.Ticket{Key: "DEMO-7", Status: "Blocked", Mark: ticket.MarkBlocked}, 18, []string{"DEMO-7", "⊘"}, "Blocked"},
		{"another flag", ticket.Ticket{Key: "DEMO-7", Status: "Blocked", Mark: ticket.MarkBlocked, PRs: failing}, 40, []string{"! CI failing", "⊘"}, "⊘ Blocked"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			lines := strings.Split(stripANSI(card(tt.tk, tt.outer, false, true, "")), "\n")
			plain := tt.tk
			plain.Status, plain.PRs = "To Do", nil
			if n := strings.Count(card(plain, tt.outer, false, true, ""), "\n") + 1; len(lines) != n {
				t.Errorf("card is %d lines, want %d:\n%s", len(lines), n, strings.Join(lines, "\n"))
			}
			first := lines[1]
			for _, w := range tt.want {
				if !strings.Contains(first, w) {
					t.Errorf("first line %q lacks %q", first, w)
				}
			}
			if tt.dontWant != "" && strings.Contains(first, tt.dontWant) {
				t.Errorf("first line %q has %q", first, tt.dontWant)
			}
			if w := lipgloss.Width(first); w > tt.outer {
				t.Errorf("first line is %d wide, card is %d: %q", w, tt.outer, first)
			}
		})
	}
}

func TestListMarksBlockedTicketsThatNeedYou(t *testing.T) {
	snap := snapshot()
	snap.Tickets = append(snap.Tickets, ticket.Ticket{Key: "DEMO-7", Summary: "Wait for client", Status: "Blocked", Mark: ticket.MarkBlocked, Category: ticket.ToDo,
		PRs: []ticket.PullRequest{{State: ticket.PROpen, Checks: ticket.ChecksFail}}})
	m := tea.Model(New(snap, false, &recorder{}, ViewList))
	m, _ = m.Update(tea.WindowSizeMsg{Width: 160, Height: 40})
	lines := strings.Split(stripANSI(m.View()), "\n")
	line, other := lines[lineIndex(m.View(), "DEMO-7")], lines[lineIndex(m.View(), "DEMO-1 ")]
	if !strings.Contains(line, "!⊘ DEMO-7") {
		t.Errorf("row = %q", line)
	}
	col := func(l, s string) int { return lipgloss.Width(l[:strings.Index(l, s)]) }
	if a, b := col(line, "Blocked"), col(other, "In Progress"); a != b {
		t.Errorf("STATUS at %d, other rows at %d:\n%q\n%q", a, b, line, other)
	}
}

func TestPRsInColumnsExplainsTheList(t *testing.T) {
	m := press(loaded(&recorder{}), "2", "v", "v")
	if v := stripANSI(m.View()); !strings.Contains(v, "the PRs filter always shows a list") {
		t.Errorf("no explanation:\n%s", v)
	}
}

func prsSnapshot() board.Snapshot {
	snap := snapshot()
	snap.Tickets = append(snap.Tickets, ticket.Ticket{Key: "DEMO-4", Summary: "Fix login redirect", Status: "In Review", Category: ticket.InReview, URL: "https://example.atlassian.net/browse/DEMO-4",
		PRs: []ticket.PullRequest{
			{Repo: "example-org/web", Number: 815, Title: "Redirect after login", URL: "https://github.com/example-org/web/pull/815", State: ticket.PROpen, Checks: ticket.ChecksPass, Review: ticket.ReviewChangesRequested},
			{Repo: "example-org/api", Number: 816, Title: "Session endpoint", State: ticket.PROpen, Checks: ticket.ChecksPending, Review: ticket.ReviewApproved, Approvals: 2},
			{Repo: "example-org/web", Number: 700, Title: "Old attempt", State: ticket.PRMerged},
		}})
	return snap
}

func TestPRsFilterListsMyOpenPRsThenReviews(t *testing.T) {
	for _, view := range []string{ViewColumns, ViewList} {
		m := tea.Model(New(prsSnapshot(), false, &recorder{}, view))
		m, _ = m.Update(tea.WindowSizeMsg{Width: 160, Height: 40})
		m = press(m, "2")
		v := stripANSI(m.View())
		if !strings.Contains(v, "2 PRs 5") || strings.Contains(v, "Reviews 1") || strings.Contains(v, "Needs you") {
			t.Fatalf("%s: header:\n%s", view, v)
		}
		mine, reviews := strings.Index(v, "Mine"), strings.Index(v, "Review requested · example-org/web  1")
		if mine < 0 || reviews < mine {
			t.Fatalf("%s: sections missing or out of order:\n%s", view, v)
		}
		rows := map[string]string{"#812": "CI failing", "#815": "Changes requested", "#816": "Approved · 2 approvals", "#806": "Open"}
		for pr, status := range rows {
			line := strings.Split(v, "\n")[lineIndex(v, pr+" ")]
			if !strings.Contains(line, status) {
				t.Errorf("%s: %s row %q lacks %q", view, pr, line, status)
			}
		}
		if line := strings.Split(v, "\n")[lineIndex(v, "#815 ")]; !strings.Contains(line, "Redirect after login") || strings.Contains(line, "DEMO-4 ·") {
			t.Errorf("%s: #815 row %q should show the pull request's own title", view, line)
		}
		if strings.Contains(v, "#700") {
			t.Errorf("%s: merged PR listed:\n%s", view, v)
		}
		if a, b := lineIndex(v, "#815 "), lineIndex(v, "#816 "); a > b || lineIndex(v, "#812 ") > lineIndex(v, "#806 ") {
			t.Errorf("%s: PRs that need you are not first:\n%s", view, v)
		}
	}
}

func TestPRsFilterActsOnTheRowsTicket(t *testing.T) {
	r := &recorder{}
	m := tea.Model(New(prsSnapshot(), false, r, ViewList))
	m, _ = m.Update(tea.WindowSizeMsg{Width: 160, Height: 40})
	m = press(m, "2")
	for i := 0; i < 10 && !strings.Contains(selectedLine(m.View()), "#815"); i++ {
		m = press(m, "s")
	}
	press(m, "f")
	press(m, "p")
	if !slices.Contains(r.calls, "fix DEMO-4 known") || !slices.Contains(r.calls, "open https://github.com/example-org/web/pull/815") {
		t.Errorf("calls = %v", r.calls)
	}
}

func TestPRMenuLeadsWithThePullRequest(t *testing.T) {
	r := &recorder{}
	m := tea.Model(New(prsSnapshot(), false, r, ViewList))
	m, _ = m.Update(tea.WindowSizeMsg{Width: 160, Height: 40})
	m = press(m, "2")
	for i := 0; i < 10 && !strings.Contains(selectedLine(m.View()), "#815"); i++ {
		m = press(m, "s")
	}
	m = press(m, "enter")
	v := stripANSI(m.View())
	for _, want := range []string{"Pull request #815 · DEMO-4", "Open #815 in GitHub ↗", "Send the review comments to the agent", "Start · open the agent", "Open the ticket pane next to its agent", "Show details of DEMO-4", "Open DEMO-4 in Jira ↗"} {
		if !strings.Contains(v, want) {
			t.Errorf("PR menu missing %q:\n%s", want, v)
		}
	}
	for _, unwanted := range []string{"Actions for", "#816", "Send the CI failure"} {
		if strings.Contains(v, unwanted) {
			t.Errorf("PR menu offers %q:\n%s", unwanted, v)
		}
	}
	if _, msg := run(m, "enter"); msg != StatusMsg("open https://github.com/example-org/web/pull/815") {
		t.Errorf("first item ran %v", r.calls)
	}
}

func TestPRStatus(t *testing.T) {
	tests := []struct {
		pr   ticket.PullRequest
		want string
	}{
		{ticket.PullRequest{Checks: ticket.ChecksFail, Merge: ticket.MergeConflicts, Draft: true}, "CI failing"},
		{ticket.PullRequest{Review: ticket.ReviewChangesRequested, Merge: ticket.MergeConflicts}, "Changes requested"},
		{ticket.PullRequest{Review: ticket.ReviewApproved, Merge: ticket.MergeConflicts, Unresolved: 2}, "Conflicts"},
		{ticket.PullRequest{Review: ticket.ReviewApproved, Merge: ticket.MergeReady, Unresolved: 2}, "2 unresolved"},
		{ticket.PullRequest{Draft: true, Checks: ticket.ChecksPass}, "Draft"},
		{ticket.PullRequest{Review: ticket.ReviewApproved, Merge: ticket.MergeReady}, "Ready to merge"},
		{ticket.PullRequest{Checks: ticket.ChecksPass, Merge: ticket.MergeReady}, "Ready to merge"},
		{ticket.PullRequest{Review: ticket.ReviewApproved}, "Approved"},
		{ticket.PullRequest{Review: ticket.ReviewRequired}, "Waiting review"},
		{ticket.PullRequest{Checks: ticket.ChecksPending}, "CI running"},
		{ticket.PullRequest{Checks: ticket.ChecksPass}, "Open"},
	}
	for _, tt := range tests {
		if got := prStatus(tt.pr); got != tt.want {
			t.Errorf("prStatus(%+v) = %q, want %q", tt.pr, got, tt.want)
		}
	}
}

func TestPRMenuOffersTheFixForItsReason(t *testing.T) {
	tests := []struct {
		pr   ticket.PullRequest
		want string
	}{
		{ticket.PullRequest{Checks: ticket.ChecksFail}, "Send the CI failure to the agent"},
		{ticket.PullRequest{Review: ticket.ReviewChangesRequested}, "Send the review comments to the agent"},
		{ticket.PullRequest{Unresolved: 1}, "Send the unresolved comments to the agent"},
		{ticket.PullRequest{Merge: ticket.MergeConflicts}, "Ask the agent to resolve the conflicts"},
	}
	for _, tt := range tests {
		pr := tt.pr
		pr.Repo, pr.Number, pr.State, pr.URL = "example-org/web", 900, ticket.PROpen, "https://github.com/example-org/web/pull/900"
		snap := prsSnapshot()
		snap.Tickets = []ticket.Ticket{{Key: "DEMO-9", Summary: "Rename", Status: "In Review", Category: ticket.InReview, PRs: []ticket.PullRequest{pr}}}
		m := tea.Model(New(snap, false, &recorder{}, ViewList))
		m, _ = m.Update(tea.WindowSizeMsg{Width: 160, Height: 40})
		v := stripANSI(press(m, "2", "enter").View())
		if !strings.Contains(v, tt.want) {
			t.Errorf("%+v: menu lacks %q:\n%s", tt.pr, tt.want, v)
		}
		row := strings.Split(stripANSI(press(m, "2", "s").View()), "\n")
		if line := row[lineIndex(strings.Join(row, "\n"), "#900 ")]; !strings.HasPrefix(strings.TrimSpace(line), "!") {
			t.Errorf("%+v: the row should be marked as needing the user: %q", tt.pr, line)
		}
	}
}

func TestPRsListGroupsMyPullRequestsByRepository(t *testing.T) {
	snap := prsSnapshot()
	pr := func(repo string, n int, c ticket.CheckState, r ticket.ReviewDecision) ticket.PullRequest {
		return ticket.PullRequest{Repo: "example-org/" + repo, Number: n, Title: fmt.Sprintf("refactor: DEMO-9 step %d", n), State: ticket.PROpen, Checks: c, Review: r}
	}
	snap.Tickets = []ticket.Ticket{{Key: "DEMO-9", Summary: "Split the module", Status: "In Review", Category: ticket.InReview, PRs: []ticket.PullRequest{
		pr("alpha", 1, ticket.ChecksPass, ""), pr("zeta", 2, ticket.ChecksPass, ""), pr("alpha", 3, ticket.ChecksFail, ""), pr("beta", 4, ticket.ChecksPass, ticket.ReviewChangesRequested),
	}}}
	m := tea.Model(New(snap, false, &recorder{}, ViewList))
	m, _ = m.Update(tea.WindowSizeMsg{Width: 160, Height: 40})
	v := stripANSI(press(m, "2").View())
	order := []string{"Mine · example-org/alpha  2", "#3 ", "#1 ", "Mine · example-org/beta  1", "#4 ", "Mine · example-org/zeta  1", "#2 ", "Review requested · example-org/web  1"}
	last := -1
	for _, want := range order {
		at := lineIndex(v, want)
		if at <= last {
			t.Fatalf("%q out of order (repositories that need the user first, then by name):\n%s", want, v)
		}
		last = at
	}
	if line := strings.Split(v, "\n")[lineIndex(v, "#2 ")]; !strings.Contains(line, "refactor: DEMO-9 step 2") || strings.Contains(line, "DEMO-9 · ") {
		t.Errorf("the title should be the pull request's own: %q", line)
	}
}

func TestPRsListLeavesOutTheAgent(t *testing.T) {
	snap := prsSnapshot()
	snap.Agents = map[string]string{"DEMO-1": "working", "DEMO-4": "working"}
	m := tea.Model(New(snap, false, &recorder{}, ViewList))
	m, _ = m.Update(tea.WindowSizeMsg{Width: 160, Height: 40})
	if v := stripANSI(m.View()); !strings.Contains(v, "AGENT") {
		t.Fatalf("All should show the agent:\n%s", v)
	}
	if v := stripANSI(press(m, "2").View()); strings.Contains(v, "AGENT") || strings.Contains(v, "working") {
		t.Errorf("the PRs filter should leave out the agent, which belongs to the ticket:\n%s", v)
	}
}

func TestPRsListFitsThePRColumnAndColorsCI(t *testing.T) {
	prev := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.ANSI256)
	t.Cleanup(func() { lipgloss.SetColorProfile(prev) })
	m := tea.Model(New(prsSnapshot(), false, &recorder{}, ViewList))
	m, _ = m.Update(tea.WindowSizeMsg{Width: 160, Height: 40})
	m = press(m, "2")
	raw := m.View()
	v := stripANSI(raw)
	if head := strings.Split(v, "\n")[lineIndex(v, "TITLE")]; !strings.Contains(head, "  PR   STATUS") {
		t.Errorf("the PR column should fit its numbers: %q", head)
	}
	if line := strings.Split(v, "\n")[lineIndex(v, "#815 ")]; !strings.Contains(line, "#815 Changes requested") {
		t.Errorf("#815 row = %q", line)
	}
	for _, l := range strings.Split(raw, "\n") {
		plain := ansi.Strip(l)
		switch {
		case strings.Contains(plain, "#815 ") && !strings.Contains(l, ok.Render("✓")):
			t.Errorf("a passing check should be colored: %q", l)
		case strings.Contains(plain, "#812 ") && strings.Contains(l, warn.Render("✕")):
			t.Errorf("the selected row keeps its highlight unbroken: %q", l)
		}
	}
}

func TestPRsListHeadsItsColumns(t *testing.T) {
	m := tea.Model(New(prsSnapshot(), false, &recorder{}, ViewList))
	m, _ = m.Update(tea.WindowSizeMsg{Width: 160, Height: 40})
	if v := stripANSI(m.View()); !strings.Contains(v, "KEY") || !strings.Contains(v, "PR / CI") {
		t.Fatalf("All keeps its headers:\n%s", v)
	}
	m = press(m, "2")
	v := stripANSI(m.View())
	lines := strings.Split(v, "\n")
	head := lines[lineIndex(v, "TITLE")]
	if strings.Contains(head, "KEY") || strings.Contains(head, "PR / CI") || !strings.Contains(head, " PR ") || !strings.Contains(head, "CI TITLE") {
		t.Fatalf("PRs headers = %q", head)
	}
	for _, pr := range []string{"#812", "#806"} {
		row := []rune(lines[lineIndex(v, pr+" ")])
		mark, title := strings.Index(head, "CI TITLE"), strings.Index(head, "TITLE")
		if mark < 0 || string(row[len([]rune(head[:mark]))]) == " " || string(row[len([]rune(head[:title]))]) == " " {
			t.Errorf("%s row does not sit under its headers:\n%q\n%q", pr, head, string(row))
		}
	}
}

func TestEveryRowSharesOneLeftMargin(t *testing.T) {
	for _, view := range []string{ViewColumns, ViewList} {
		m := tea.Model(New(snapshot(), true, &recorder{}, view))
		m, _ = m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
		for i, line := range strings.Split(stripANSI(m.View()), "\n") {
			if line == "" {
				continue
			}
			if !strings.HasPrefix(line, " ") || strings.HasPrefix(line, "  tkt") {
				t.Errorf("%s line %d lacks the one-column margin: %q", view, i, line)
			}
			if w := lipgloss.Width(line); w > 120 {
				t.Errorf("%s line %d is %d wide: %q", view, i, w, line)
			}
		}
		if l := strings.Split(stripANSI(m.View()), "\n")[1]; !strings.HasPrefix(l, " tkt") {
			t.Errorf("%s header should start at the margin: %q", view, l)
		}
	}
}

func TestCardLinesStartAtTheSameColumn(t *testing.T) {
	tk := ticket.Ticket{Key: "DEMO-1", Summary: "Filter accounts", Type: "Story", Category: ticket.InProgress}
	col := func(s, sub string) int {
		for _, l := range strings.Split(stripANSI(s), "\n") {
			if i := strings.Index(l, sub); i >= 0 {
				return lipgloss.Width(l[:i])
			}
		}
		return -1
	}
	plain := card(tk, 30, false, false, "")
	if k, ti, ty := col(plain, "DEMO-1"), col(plain, "Filter"), col(plain, "■ Story"); k != ti || ti != ty {
		t.Errorf("unselected card columns key=%d title=%d type=%d:\n%s", k, ti, ty, stripANSI(plain))
	}
	sel := card(tk, 30, true, false, "")
	if m, ti := col(sel, "▸ DEMO-1"), col(sel, "Filter"); m != ti {
		t.Errorf("selected card: mark at %d, title at %d:\n%s", m, ti, stripANSI(sel))
	}
}

func TestHeaderControlsShareTheTitleRow(t *testing.T) {
	m := loaded(&recorder{})
	lines := strings.Split(stripANSI(m.View()), "\n")
	first := lines[1]
	for _, want := range []string{"tkt", "│ 1 Sprint 3 │", "│ 2 PRs 3 │", "│ Columns │", "│ List │"} {
		if !strings.Contains(first, want) {
			t.Errorf("title row lacks %q: %q", want, first)
		}
	}
	if !strings.Contains(lines[0], "╭") || !strings.Contains(lines[2], "╰") {
		t.Errorf("filters should be boxed:\n%s\n%s\n%s", lines[0], first, lines[2])
	}
	if strings.Index(first, "1 Sprint") < strings.Index(first, "tkt") || strings.Contains(first, "synced") || strings.Contains(first, "refreshing") {
		t.Errorf("controls should sit right of the title, the sync state at the bottom: %q", first)
	}
	if !strings.HasPrefix(strings.TrimSpace(lines[3]), "──────") {
		t.Errorf("no rule under the header: %q", lines[3])
	}

	tray, _ := m.Update(tea.WindowSizeMsg{Width: 60, Height: 40})
	if tl := strings.Split(stripANSI(tray.View()), "\n"); !strings.Contains(tl[1], "tkt") || !strings.Contains(tl[1], "1 Sprint") {
		t.Errorf("without the sync state, a 60-column pane keeps the controls on the title row:\n%s\n%s", tl[0], tl[1])
	}
	narrow, _ := m.Update(tea.WindowSizeMsg{Width: 24, Height: 40})
	nl := strings.Split(stripANSI(narrow.View()), "\n")
	if !strings.Contains(nl[0], "tkt") || !strings.Contains(nl[1], "1 Sprint") {
		t.Errorf("a narrow pane should move the controls to the second row:\n%s\n%s", nl[0], nl[1])
	}
	for _, l := range nl[:3] {
		if lipgloss.Width(l) > 24 {
			t.Errorf("header line wider than the pane: %q", l)
		}
	}
}

func TestColumnTitlesPutTheCountRightAboveARule(t *testing.T) {
	lines := strings.Split(stripANSI(loaded(&recorder{}).View()), "\n")
	y := lineIndex(strings.Join(lines, "\n"), "In Progress")
	if y < 0 || y+1 >= len(lines) {
		t.Fatalf("no column titles:\n%s", strings.Join(lines, "\n"))
	}
	for _, seg := range strings.Split(strings.Trim(strings.TrimSpace(lines[y]), "│"), "││") {
		f := strings.Fields(seg)
		count := f[len(f)-1]
		if _, err := strconv.Atoi(count); err != nil || !strings.HasSuffix(strings.TrimRight(seg, " "), "  "+count) || len(seg)-len(strings.TrimRight(seg, " ")) > 1 {
			t.Errorf("count should sit at the right edge, apart from the name: %q", seg)
		}
	}
	if strings.Count(lines[y+1], "│──") < 4 {
		t.Errorf("no rule under the column titles: %q", lines[y+1])
	}
}

func TestListHeaderHasARuleAndNeutralSections(t *testing.T) {
	m := New(board.Snapshot{}, false, &recorder{}, ViewList)
	next, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	next, _ = next.Update(SnapshotMsg{Snap: snapshot()})
	lines := strings.Split(stripANSI(next.View()), "\n")
	y := lineIndex(strings.Join(lines, "\n"), "KEY")
	if y < 0 || !strings.HasPrefix(strings.TrimSpace(lines[y+1]), "──────") {
		t.Fatalf("no rule under the list header:\n%s", strings.Join(lines, "\n"))
	}
	if lipgloss.Width(lines[y+1]) > 120 {
		t.Errorf("rule wider than the pane: %d", lipgloss.Width(lines[y+1]))
	}
	for _, want := range []string{" My tickets  "} {
		if !strings.Contains(strings.Join(lines, "\n"), want) {
			t.Errorf("missing section title %q:\n%s", want, strings.Join(lines, "\n"))
		}
	}
}

func TestDoubleClickOpensTheActionsMenu(t *testing.T) {
	now := time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC)
	start := func(view string) tea.Model {
		m := New(board.Snapshot{}, false, &recorder{}, view).WithClock(func() time.Time { return now })
		next, _ := m.Update(tea.WindowSizeMsg{Width: 160, Height: 40})
		next, _ = next.Update(SnapshotMsg{Snap: snapshot()})
		return next
	}
	at := func(m tea.Model, key string) (int, int) {
		lines := strings.Split(stripANSI(m.View()), "\n")
		y := lineIndex(m.View(), key)
		return lipgloss.Width(lines[y][:strings.Index(lines[y], key)]), y
	}

	m := start(ViewColumns)
	x, y := at(m, "DEMO-2")
	m = click(m, x, y)
	now = now.Add(200 * time.Millisecond)
	m = click(m, x, y)
	if v := stripANSI(m.View()); !strings.Contains(v, "Actions for DEMO-2") {
		t.Fatalf("double click on a card should open its menu:\n%s", v)
	}

	m = start(ViewList)
	x, y = at(m, "DEMO-3")
	m = click(m, x, y)
	now = now.Add(200 * time.Millisecond)
	if m = click(m, x, y); !strings.Contains(stripANSI(m.View()), "Actions for DEMO-3") {
		t.Errorf("double click on a list row should open its menu:\n%s", stripANSI(m.View()))
	}

	m = start(ViewColumns)
	x, y = at(m, "DEMO-2")
	m = click(m, x, y)
	now = now.Add(time.Second)
	if m = click(m, x, y); strings.Contains(stripANSI(m.View()), "Actions for") {
		t.Errorf("two slow clicks are not a double click")
	}
	x2, y2 := at(m, "DEMO-3")
	m = click(m, x2, y2)
	now = now.Add(100 * time.Millisecond)
	if m = click(m, x, y); strings.Contains(stripANSI(m.View()), "Actions for") {
		t.Errorf("clicks on two different tickets are not a double click")
	}

	m, cmd := m.Update(tea.MouseMsg{X: x, Y: y, Button: tea.MouseButtonRight, Action: tea.MouseActionPress})
	if cmd != nil || strings.Contains(stripANSI(m.View()), "Actions for") {
		t.Errorf("right click should do nothing")
	}
}

func TestBackButtonLeavesTheDetail(t *testing.T) {
	r := &recorder{}
	m := press(loaded(r), "l")
	m, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("i")})
	m, _ = m.Update(cmd())
	view := m.View()
	y := lineIndex(view, "← Back to the board")
	if y < 0 {
		t.Fatalf("detail has no back button:\n%s", stripANSI(view))
	}
	line := strings.Split(stripANSI(view), "\n")[y]
	if next := strings.Split(stripANSI(view), "\n")[y+1]; strings.TrimSpace(next) != "" {
		t.Errorf("the back button should be apart from the text, got %q under it", next)
	}
	x := lipgloss.Width(line[:strings.Index(line, "Back")])
	if m = click(m, x+30, y+3); !strings.Contains(stripANSI(m.View()), "← Back to the board") {
		t.Fatalf("a click elsewhere should keep the detail open")
	}
	if m = click(m, x, y); strings.Contains(stripANSI(m.View()), "← Back to the board") || !strings.Contains(stripANSI(m.View()), "In Progress") {
		t.Errorf("clicking back should return to the board:\n%s", stripANSI(m.View()))
	}
}

func TestColumnsRequestOnANarrowPaneExplainsWhy(t *testing.T) {
	m := New(snapshot(), true, &recorder{}, ViewList)
	next, _ := m.Update(tea.WindowSizeMsg{Width: 90, Height: 40})
	next = press(next, "v")
	if v := stripANSI(next.View()); !strings.Contains(v, "columns need a pane at least 100 columns wide") {
		t.Errorf("v on a narrow pane should say why the list stays:\n%s", v)
	}
	if v := stripANSI(press(next, "s").View()); strings.Contains(v, "columns need a pane") {
		t.Errorf("the explanation should clear on the next key:\n%s", v)
	}
}

func TestBoardShowsSnapshotWarningsUnderTheHeader(t *testing.T) {
	snap := snapshot()
	snap.Warnings = []string{"DEMO has no repositories yet"}
	m := New(board.Snapshot{}, false, &recorder{}, ViewColumns)
	next, _ := m.Update(tea.WindowSizeMsg{Width: 160, Height: 40})
	next, _ = next.Update(SnapshotMsg{Snap: snap})
	lines := strings.Split(stripANSI(next.View()), "\n")
	if !strings.HasPrefix(strings.TrimSpace(lines[4]), "! DEMO has no repositories yet") {
		t.Errorf("warning not under the header rule:\n%s", strings.Join(lines[:6], "\n"))
	}
	y := lineIndex(next.View(), "DEMO-2")
	x := lipgloss.Width(lines[y][:strings.Index(lines[y], "DEMO-2")])
	if v := stripANSI(click(next, x, y).View()); !strings.Contains(v, "▸ DEMO-2") {
		t.Errorf("clicks should still map to cards under the warning:\n%s", v)
	}
}

func TestWarningsReviewRequestsAndAttentionHaveTheirOwnColors(t *testing.T) {
	prev := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	t.Cleanup(func() { lipgloss.SetColorProfile(prev) })
	snap := snapshot()
	snap.Warnings = []string{"pull requests unavailable"}
	m, _ := New(snap, false, &recorder{}, ViewList).Update(tea.WindowSizeMsg{Width: 160, Height: 40})
	lines := strings.Split(m.View(), "\n")
	banner := lines[lineIndex(m.View(), "! pull requests unavailable")]
	chip := lines[lineIndex(m.View(), "#820")-1]
	fg := func(c lipgloss.Color) string {
		s := lipgloss.NewStyle().Foreground(c).Render("x")
		return s[:strings.Index(s, "x")]
	}
	if !strings.Contains(banner, fg(colorWarning)) || strings.Contains(banner, fg(colorAttention)) || strings.Contains(banner, fg(colorReview)) {
		t.Errorf("the warning banner should be the warning color alone: %q", banner)
	}
	if !strings.Contains(chip, fg(colorReview)) || strings.Contains(chip, fg(colorWarning)) {
		t.Errorf("a review request's chip should be framed in the review color: %q", chip)
	}
	if colorWarning == colorAttention || colorWarning == colorReview || colorAttention == colorReview || colorError == colorWarning {
		t.Error("warnings, attention, review requests and errors must not share a color")
	}
}

func tallBoard(t *testing.T) tea.Model {
	t.Helper()
	snap := snapshot()
	for i := 10; i < 22; i++ {
		snap.Tickets = append(snap.Tickets, ticket.Ticket{Key: ticket.Key(fmt.Sprintf("DEMO-%d", i)), Summary: "filler", Status: "To Do", Category: ticket.ToDo})
	}
	m := New(board.Snapshot{}, false, &recorder{}, ViewColumns)
	next, _ := m.Update(tea.WindowSizeMsg{Width: 160, Height: 40})
	next, _ = next.Update(SnapshotMsg{Snap: snap})
	return next
}

func TestColumnsScrollOnTheirOwn(t *testing.T) {
	m := tallBoard(t)
	v := stripANSI(m.View())
	lines := strings.Split(v, "\n")
	if len(lines) != 40 || !strings.Contains(lines[1], "tkt") || !strings.Contains(lines[len(lines)-1], "close") {
		t.Fatalf("header or legend scrolled away (%d lines):\n%s", len(lines), v)
	}
	if !strings.Contains(v, "↓ ") || !strings.Contains(v, " more") || !strings.Contains(v, "Filter accounts") {
		t.Errorf("the tall column should mark hidden cards while the others stay:\n%s", v)
	}
	for i := 0; i < 12; i++ {
		m = press(m, "s")
	}
	v = stripANSI(m.View())
	if !strings.Contains(v, "▸ DEMO-") || !strings.Contains(v, "↑ ") || !strings.Contains(v, "In Progress") {
		t.Errorf("moving down should scroll only that column and keep the selection visible:\n%s", v)
	}
	if lines := strings.Split(v, "\n"); !strings.Contains(lines[1], "tkt") {
		t.Errorf("the header moved:\n%s", v)
	}
}

func TestWheelScrollsTheColumnUnderThePointer(t *testing.T) {
	m := tallBoard(t)
	m = press(m, "l")
	if !strings.Contains(stripANSI(m.View()), "▸ DEMO-1") {
		t.Fatalf("selection should be in In Progress:\n%s", stripANSI(m.View()))
	}
	y := lineIndex(m.View(), "To Do") + 3
	m, _ = m.Update(tea.MouseMsg{X: 5, Y: y, Button: tea.MouseButtonWheelDown, Action: tea.MouseActionPress})
	v := stripANSI(m.View())
	if !strings.Contains(v, "↑ 1 more") || !strings.Contains(v, "▸ DEMO-1") {
		t.Errorf("the wheel over To Do should scroll To Do and leave the selection:\n%s", v)
	}
	lines := strings.Split(v, "\n")
	cy := -1
	for i, l := range lines {
		if strings.Contains(l, "DEMO-11") {
			cy = i
			break
		}
	}
	if cy < 0 {
		t.Fatalf("DEMO-11 not visible after scrolling:\n%s", v)
	}
	if v := stripANSI(click(m, 5, cy).View()); !strings.Contains(v, "▸ DEMO-11") {
		t.Errorf("a click on a scrolled column should select the card under it:\n%s", v)
	}
}

func TestReviewStripKeepsRoomForTheMoreLabel(t *testing.T) {
	snap := snapshot()
	snap.Reviews = nil
	for i := 0; i < 8; i++ {
		snap.Reviews = append(snap.Reviews, ticket.PullRequest{Repo: "example-org/web", Number: 700 + i, Branch: fmt.Sprintf("feature/DEMO-17044%d", i), State: ticket.PROpen})
	}
	for _, width := range []int{120, 140, 160, 200} {
		m := New(board.Snapshot{}, false, &recorder{}, ViewColumns)
		next, _ := m.Update(tea.WindowSizeMsg{Width: width, Height: 40})
		next, _ = next.Update(SnapshotMsg{Snap: snap})
		lines := strings.Split(stripANSI(next.View()), "\n")
		y := lineIndex(next.View(), "Review requested")
		if y < 0 {
			t.Fatalf("width %d: no review strip", width)
		}
		if !strings.Contains(lines[y], "more · press 2") {
			t.Errorf("width %d: the more label is cut: %q", width, lines[y])
		}
		if w := lipgloss.Width(lines[y]); w > width {
			t.Errorf("width %d: strip is %d wide", width, w)
		}
	}
}

func TestCardsShowTheAgentState(t *testing.T) {
	snap := snapshot()
	snap.Agents = map[string]string{"DEMO-1": "working", "DEMO-2": "blocked", "DEMO-3": "unknown"}
	m := New(board.Snapshot{}, false, &recorder{}, ViewColumns)
	next, _ := m.Update(tea.WindowSizeMsg{Width: 160, Height: 40})
	next, _ = next.Update(SnapshotMsg{Snap: snap})
	v := stripANSI(next.View())
	for key, dot := range map[string]bool{"DEMO-1": true, "DEMO-2": true, "DEMO-3": false} {
		y := lineIndex(next.View(), key)
		row := strings.Split(v, "\n")[y]
		row = row[strings.Index(row, key):]
		if strings.Contains(row[:strings.Index(row, "│")], "●") != dot {
			t.Errorf("%s: agent dot %v expected:\n%s", key, dot, v)
		}
	}
	if strings.Contains(v, "no agent") || strings.Contains(v, "claude") {
		t.Errorf("cards without an agent say nothing about it:\n%s", v)
	}
}

func TestStatusKeepsAHyperlinkWholeWhenCut(t *testing.T) {
	const url = "https://example.atlassian.net/browse/DEMO-1234567890"
	link := "\x1b]8;;" + url + "\x1b\\" + url + "\x1b]8;;\x1b\\"
	m := loaded(&recorder{})
	m, _ = m.Update(tea.WindowSizeMsg{Width: 40, Height: 40})
	m, _ = m.Update(StatusMsg("✓ link copied · " + link))
	p := m.(Model).preview()
	if w := ansi.StringWidth(p); w > 40 {
		t.Errorf("status is %d cells wide in a 40-cell pane", w)
	}
	if !strings.Contains(p, "\x1b]8;;"+url+"\x1b\\") || !strings.Contains(p, "\x1b]8;;\x1b\\") {
		t.Errorf("hyperlink broken by the cut: %q", p)
	}
}

func TestMenuCopiesTheHighlightedLinkWithC(t *testing.T) {
	cases := []struct {
		name   string
		keys   []string
		copied string
		footer string
	}{
		{"Jira row", []string{"s", "s", "s"}, "copy link https://example.atlassian.net/browse/DEMO-1", "enter run · c copy link · esc close"},
		{"pull request row", []string{"s", "s", "s", "s"}, "copy link https://github.com/example-org/web/pull/812", "enter run · c copy link · esc close"},
		{"row without a link", nil, "", "enter run · esc close"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := &recorder{}
			m := press(loaded(r), "l", "enter")
			m = press(m, c.keys...)
			if v := stripANSI(m.View()); !strings.Contains(v, c.footer) || (c.copied == "" && strings.Contains(v, "c copy link")) {
				t.Errorf("footer should read %q:\n%s", c.footer, v)
			}
			before := len(r.calls)
			m, _ = run(m, "c")
			if c.copied == "" {
				if len(r.calls) != before {
					t.Errorf("c on a row without a link ran %v", r.calls[before:])
				}
				return
			}
			if r.last() != c.copied {
				t.Errorf("c ran %q, want %q", r.last(), c.copied)
			}
			if strings.Contains(stripANSI(m.View()), "Actions for") {
				t.Error("copying should close the menu")
			}
		})
	}
}

func TestListShowsAgentStates(t *testing.T) {
	cases := []struct {
		name      string
		width     int
		agents    map[string]string
		wantAgent bool
	}{
		{"inside Herdr", 140, map[string]string{"DEMO-1": "working", "DEMO-2": "blocked"}, true},
		{"no agents outside Herdr", 140, nil, false},
		{"a narrow pane drops the column first", 66, map[string]string{"DEMO-1": "working"}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			snap := snapshot()
			snap.Agents = c.agents
			m := New(board.Snapshot{}, false, &recorder{}, ViewList)
			next, _ := m.Update(tea.WindowSizeMsg{Width: c.width, Height: 40})
			next, _ = next.Update(SnapshotMsg{Snap: snap})
			v := stripANSI(next.View())
			if got := strings.Contains(v, "AGENT"); got != c.wantAgent {
				t.Fatalf("AGENT column shown = %v:\n%s", got, v)
			}
			titleCols := map[int]bool{}
			for _, l := range strings.Split(v, "\n") {
				if lipgloss.Width(l) > c.width {
					t.Errorf("line wider than the pane: %q", l)
				}
				isRow := strings.HasPrefix(l, " ▸ ") || strings.HasPrefix(l, " ! ") || strings.HasPrefix(l, "   ")
				for _, word := range []string{"TITLE", "Filter accounts", "Keep sort", "Export CSV"} {
					if i := strings.Index(l, word); i >= 0 && isRow {
						titleCols[lipgloss.Width(l[:i])] = true
					}
				}
			}
			if len(titleCols) != 1 {
				t.Errorf("titles start at different columns %v:\n%s", titleCols, v)
			}
			if !c.wantAgent {
				return
			}
			for key, want := range map[string]string{"DEMO-1": "● working", "DEMO-2": "● needs you"} {
				y := lineIndex(next.View(), key+" ")
				if y < 0 || !strings.Contains(strings.Split(v, "\n")[y], want) {
					t.Errorf("%s row lacks %q:\n%s", key, want, v)
				}
			}
			y := lineIndex(next.View(), "DEMO-3 ")
			if row := strings.Split(v, "\n")[y]; strings.Contains(row, "●") || strings.Contains(row, "○") || strings.Contains(row, "needs you") {
				t.Errorf("DEMO-3 has no agent: %q", row)
			}
		})
	}
}

// brokenEscape finds a color sequence cut before its final "m", which terminals draw as garbage.
var brokenEscape = regexp.MustCompile(`\x1b\[[0-9;]*[^0-9;m]`)

func TestCardFitsPullRequestsBesideTheAgentWithoutBreakingColors(t *testing.T) {
	tk := ticket.Ticket{Key: "DEMO-1", Summary: "Homogenize the organizations module", Status: "In Progress", Category: ticket.InProgress, Assignee: "Ana López",
		PRs: []ticket.PullRequest{
			{Number: 731, State: ticket.PROpen, Checks: ticket.ChecksPass},
			{Number: 732, State: ticket.PROpen, Checks: ticket.ChecksPass},
			{Number: 733, State: ticket.PROpen, Checks: ticket.ChecksPass},
		}}
	prev := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.ANSI256)
	t.Cleanup(func() { lipgloss.SetColorProfile(prev) })
	for _, status := range []string{"idle", "blocked", "working"} {
		out := card(tk, 30, false, false, status)
		plain := ansi.Strip(out)
		if brokenEscape.MatchString(out) || strings.ContainsRune(plain, utf8.RuneError) || !utf8.ValidString(out) {
			t.Errorf("%s: a cut escape code leaks into the card: %q", status, out)
		}
		if !strings.Contains(plain, "#731 ✓ #732 ✓ #733 ✓") || !strings.ContainsAny(strings.Split(plain, "\n")[1], "●○") {
			t.Errorf("%s: the card should keep its pull requests and show the agent's dot:\n%s", status, plain)
		}
		for _, l := range strings.Split(out, "\n") {
			if lipgloss.Width(l) > 30 {
				t.Errorf("%s: line wider than the card: %q", status, ansi.Strip(l))
			}
		}
	}
}

func TestTruncateKeepsColorCodesWhole(t *testing.T) {
	prev := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.ANSI256)
	t.Cleanup(func() { lipgloss.SetColorProfile(prev) })
	cases := []struct {
		in   string
		n    int
		want string
	}{
		{"plain text here", 6, "plain…"},
		{"short", 10, "short"},
		{warn.Render("! CI failing on unit tests"), 8, "! CI fa…"},
		{keyStyle.Render("▸ ") + "example-org/web " + warn.Render("✕"), 10, "▸ example…"},
	}
	for _, c := range cases {
		got := truncate(c.in, c.n)
		if brokenEscape.MatchString(got) || ansi.Strip(got) != c.want || lipgloss.Width(got) > c.n {
			t.Errorf("truncate(%q, %d) = %q", c.in, c.n, got)
		}
	}
}

func TestCardShowsTheTypeWholeOrOnlyItsIcon(t *testing.T) {
	tk := ticket.Ticket{Key: "DEMO-1", Summary: "Homogenize the organizations module", Type: "Historia", Status: "In Progress", Assignee: "Pedro Pérez",
		PRs: []ticket.PullRequest{{Number: 731, State: ticket.PRMerged}}}
	cases := []struct {
		outer int
		want  string
	}{
		{40, "■ Historia · Pedro P."},
		{24, "■ Pedro P."},
	}
	for _, c := range cases {
		lines := strings.Split(ansi.Strip(card(tk, c.outer, true, false, "idle")), "\n")
		meta := lines[len(lines)-2]
		if !strings.Contains(meta, c.want) || strings.Contains(meta, "…") {
			t.Errorf("width %d: meta line %q, want %q uncut", c.outer, meta, c.want)
		}
	}
	tk.Assignee = ""
	if plain := ansi.Strip(card(tk, 40, false, false, "")); !strings.Contains(plain, "unassigned") {
		t.Errorf("an unassigned ticket says so:\n%s", plain)
	}
}

func TestCardShowsAStatusMovedToItsColumn(t *testing.T) {
	tk := ticket.Ticket{Key: "DEMO-1", Summary: "x", Status: "Pending test", Mark: ticket.MarkDiamond, Assignee: "Ana López"}
	if head := ansi.Strip(strings.Split(card(tk, 40, false, false, "working"), "\n")[1]); !strings.Contains(head, "◇ Pending test ●") {
		t.Errorf("a moved status shows its name before the agent: %q", head)
	}
	if head := ansi.Strip(strings.Split(card(tk, 24, true, false, "working"), "\n")[1]); !strings.Contains(head, "◇ ●") || strings.Contains(head, "Pending") {
		t.Errorf("without room only the mark stays: %q", head)
	}
	tk.Mark = ticket.MarkNone
	if head := ansi.Strip(strings.Split(card(tk, 40, false, false, ""), "\n")[1]); strings.Contains(head, "◇") || strings.Contains(head, "Pending") {
		t.Errorf("an unmarked status shows nothing: %q", head)
	}
}

func TestStatusMarksShowTheirIconAndColor(t *testing.T) {
	prev := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	t.Cleanup(func() { lipgloss.SetColorProfile(prev) })
	cases := []struct {
		mark      ticket.Mark
		icon, hex string
	}{
		{ticket.MarkDiamond, "◇", "#89b4fa"},
		{ticket.MarkBlocked, "⊘", "#fab387"},
		{ticket.MarkClock, "◷", "#94e2d5"},
		{ticket.MarkFlag, "⚑", "#cba6f7"},
		{ticket.MarkStar, "★", "#f9e2af"},
		{ticket.MarkTriangle, "▲", "#a6e3a1"},
	}
	for _, c := range cases {
		tk := ticket.Ticket{Key: "DEMO-1", Summary: "x", Status: "On hold", Mark: c.mark}
		head := strings.Split(card(tk, 40, false, false, ""), "\n")[1]
		if want := lipgloss.NewStyle().Foreground(lipgloss.Color(c.hex)).Render(c.icon + " On hold"); !strings.Contains(head, want) {
			t.Errorf("%s: header %q lacks %q", c.mark, head, want)
		}
	}
	snap := snapshot()
	snap.Tickets = append(snap.Tickets, ticket.Ticket{Key: "DEMO-7", Summary: "Wait for client", Status: "On hold", Mark: ticket.MarkClock, Category: ticket.ToDo})
	m := tea.Model(New(snap, false, &recorder{}, ViewList))
	m, _ = m.Update(tea.WindowSizeMsg{Width: 160, Height: 40})
	if row := strings.Split(stripANSI(m.View()), "\n")[lineIndex(m.View(), "DEMO-7")]; !strings.Contains(row, "◷ DEMO-7") {
		t.Errorf("the list shows any mark's icon: %q", row)
	}
}

func TestTypeIconsUseJiraColors(t *testing.T) {
	prev := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	t.Cleanup(func() { lipgloss.SetColorProfile(prev) })
	cases := []struct{ typ, glyph, hex string }{
		{"Story", "■", "#a6e3a1"},
		{"Historia", "■", "#a6e3a1"},
		{"Bug QA", "●", "#f38ba8"},
		{"Error", "●", "#f38ba8"},
		{"Task (unplanned)", "■", "#89b4fa"},
		{"Subtarea", "■", "#94e2d5"},
		{"Epic", "◆", "#cba6f7"},
	}
	for _, c := range cases {
		want := lipgloss.NewStyle().Foreground(lipgloss.Color(c.hex)).Render(c.glyph)
		if got := typeGlyph(c.typ); got != want {
			t.Errorf("typeGlyph(%q) = %q, want %q", c.typ, got, want)
		}
	}
	if got := ansi.Strip(typeGlyph("Postmortem")); got != "■" {
		t.Errorf("an unknown type still gets an icon, got %q", got)
	}
	if typeGlyph("") != "" {
		t.Error("no type, no icon")
	}
}

func TestCardShowsTheAgentAsHerdrsDot(t *testing.T) {
	prev := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	t.Cleanup(func() { lipgloss.SetColorProfile(prev) })
	tk := ticket.Ticket{Key: "DEMO-1", Summary: "x", Type: "Story", Assignee: "Ana López"}
	for status, want := range map[string][2]string{"working": {"●", "#f9e2af"}, "blocked": {"●", "#f38ba8"}, "done": {"●", "#94e2d5"}, "idle": {"○", "#a6e3a1"}} {
		out := card(tk, 30, false, false, status)
		head := strings.Split(out, "\n")[1]
		if dot := lipgloss.NewStyle().Foreground(lipgloss.Color(want[1])).Render(want[0]); !strings.Contains(head, dot) {
			t.Errorf("%s: header should end with %s in %s, as Herdr draws it: %q", status, want[0], want[1], head)
		}
		if plain := ansi.Strip(out); strings.Contains(plain, status) || strings.Contains(plain, "needs you") {
			t.Errorf("%s: the card shows the dot, not the word:\n%s", status, plain)
		}
	}
	if head := ansi.Strip(strings.Split(card(tk, 30, false, false, "unknown"), "\n")[1]); strings.Contains(head, "●") {
		t.Errorf("an unknown agent shows no dot: %q", head)
	}
	tk.Status, tk.Mark = "Blocked", ticket.MarkBlocked
	head := strings.Split(card(tk, 24, false, false, "blocked"), "\n")[1]
	if icon := lipgloss.NewStyle().Foreground(lipgloss.Color("#fab387")).Render("⊘ Blocked"); !strings.Contains(head, icon) {
		t.Errorf("a blocked ticket's icon is orange, apart from the agent's red dot: %q", head)
	}
}

func TestRepoFilterHidesRepositoriesInThePRsFilter(t *testing.T) {
	r := &recorder{}
	m := tea.Model(New(prsSnapshot(), false, r, ViewList))
	m, _ = m.Update(tea.WindowSizeMsg{Width: 160, Height: 40})
	m = press(m, "2", "R")
	v := stripANSI(m.View())
	for _, want := range []string{"Repositories", "[x] example-org/api", "[x] example-org/web", "a all", "Apply"} {
		if !strings.Contains(v, want) {
			t.Fatalf("filter modal lacks %q:\n%s", want, v)
		}
	}
	m = press(m, " ", "enter")
	v = stripANSI(m.View())
	if strings.Contains(v, "#816") || !strings.Contains(v, "#815") || !strings.Contains(v, "Showing 1 of 2 repositories · R change") {
		t.Errorf("api should be hidden with a notice:\n%s", v)
	}
	if len(r.states) == 0 || !slices.Equal(r.states[len(r.states)-1].HiddenRepos, []string{"example-org/api"}) {
		t.Errorf("saved states = %+v", r.states)
	}
	m = press(m, "R", "a", "a", "enter")
	if v := stripANSI(m.View()); !strings.Contains(v, "pick at least one repository") {
		t.Errorf("unchecking every repository should be refused:\n%s", v)
	}
	m = press(m, "esc", "R", "a", "enter")
	if v := stripANSI(m.View()); !strings.Contains(v, "#816") || strings.Contains(v, "Showing") {
		t.Errorf("a all should show every repository again:\n%s", v)
	}
}

func TestRepoFilterAppliesToAll(t *testing.T) {
	m := tea.Model(New(prsSnapshot(), false, &recorder{}, ViewList).WithState(BoardState{HiddenRepos: []string{"example-org/web"}}))
	m, _ = m.Update(tea.WindowSizeMsg{Width: 160, Height: 40})
	v := stripANSI(m.View())
	if !strings.Contains(v, "DEMO-4") || strings.Contains(v, "#820") || !strings.Contains(v, "Showing 1 of 2 repositories") {
		t.Fatalf("a ticket with a pull request in a shown repository stays; the web review hides:\n%s", v)
	}
	m = tea.Model(New(prsSnapshot(), false, &recorder{}, ViewList).WithState(BoardState{HiddenRepos: []string{"example-org/web", "example-org/api"}}))
	m, _ = m.Update(tea.WindowSizeMsg{Width: 160, Height: 40})
	v = stripANSI(m.View())
	if strings.Contains(v, "DEMO-4") || !strings.Contains(v, "DEMO-1") || !strings.Contains(v, "DEMO-3") {
		t.Errorf("a ticket hides only when all its pull requests are in hidden repositories:\n%s", v)
	}
	if !strings.Contains(v, "1 Sprint 3") {
		t.Errorf("All counts what it shows:\n%s", v)
	}
	for _, view := range []string{ViewColumns} {
		m := tea.Model(New(prsSnapshot(), false, &recorder{}, view).WithState(BoardState{HiddenRepos: []string{"example-org/web"}}))
		m, _ = m.Update(tea.WindowSizeMsg{Width: 160, Height: 40})
		if v := stripANSI(m.View()); strings.Contains(v, "#820") || !strings.Contains(v, "Showing 1 of 2 repositories") {
			t.Errorf("%s: the review strip hides hidden repositories with the notice:\n%s", view, v)
		}
	}
}

func TestRepoFilterNoticeOpensTheModalAndFitsTheTrayPanel(t *testing.T) {
	m := tea.Model(New(prsSnapshot(), false, &recorder{}, ViewList).WithState(BoardState{HiddenRepos: []string{"example-org/api"}}))
	m, _ = m.Update(tea.WindowSizeMsg{Width: 56, Height: 30})
	v := stripANSI(m.View())
	y := lineIndex(v, "Showing 1 of 2")
	if y < 0 {
		t.Fatalf("no notice:\n%s", v)
	}
	m = click(m, 5, y)
	v = stripANSI(m.View())
	if !strings.Contains(v, "[ ] example-org/api") {
		t.Fatalf("a click on the notice should open the filter:\n%s", v)
	}
	for _, l := range strings.Split(v, "\n") {
		if lipgloss.Width(l) > 56 {
			t.Errorf("wider than the tray panel: %q", l)
		}
	}
}

func TestPRGroupsCollapse(t *testing.T) {
	r := &recorder{}
	m := tea.Model(New(prsSnapshot(), false, r, ViewList))
	m, _ = m.Update(tea.WindowSizeMsg{Width: 160, Height: 40})
	m = press(m, "2")
	if line := selectedLine(stripANSI(m.View())); !strings.Contains(line, "#812") {
		t.Fatalf("the PRs filter should select its first pull request, not a title: %q", line)
	}
	if v := stripANSI(m.View()); !strings.Contains(v, "▾ Mine  2") || !strings.Contains(v, "▾ Review requested · example-org/web  1") {
		t.Fatalf("expanded titles:\n%s", v)
	}
	m = press(m, "w", "enter")
	v := stripANSI(m.View())
	if strings.Contains(v, "#812") || strings.Contains(v, "#806") || !strings.Contains(v, "▸ Mine  2 · 1 needs you") {
		t.Fatalf("enter on a title should collapse it and say what needs the user:\n%s", v)
	}
	if strings.Contains(v, "Actions for") || strings.Contains(v, "Pull request #") {
		t.Errorf("a title opens no menu:\n%s", v)
	}
	if len(r.states) == 0 || !slices.Equal(r.states[len(r.states)-1].Collapsed, []string{"mine:"}) {
		t.Errorf("saved states = %+v", r.states)
	}
	m = press(m, " ")
	if v = stripANSI(m.View()); !strings.Contains(v, "#812") || !strings.Contains(v, "▾ Mine  2") {
		t.Errorf("space should expand it again:\n%s", v)
	}
	if v := stripANSI(press(m, "w", "enter").View()); !strings.Contains(m.(Model).groupTitle(m.(Model).prItems()[0], false), "▾") || !strings.Contains(v, "▸ Mine") {
		t.Errorf("a toggle on a copy of the board must not change the original:\n%s", v)
	}
	y := lineIndex(v, "Review requested · example-org/web")
	m = click(m, 4, y)
	if v := stripANSI(m.View()); strings.Contains(v, "#820") || !strings.Contains(v, "▸ Review requested · example-org/web  1 · 1 needs you") {
		t.Errorf("a click on a title should collapse it:\n%s", v)
	}
	m = press(m, "p")
	if v := stripANSI(m.View()); strings.Contains(v, "has no pull request") {
		t.Errorf("p on a title should do nothing:\n%s", v)
	}
}

func TestCollapsedGroupsAreRestoredAndFitTheTrayPanel(t *testing.T) {
	m := tea.Model(New(prsSnapshot(), false, &recorder{}, ViewList).WithState(BoardState{Collapsed: []string{"review:example-org/web"}}))
	m, _ = m.Update(tea.WindowSizeMsg{Width: 56, Height: 30})
	v := stripANSI(press(m, "2").View())
	if strings.Contains(v, "#820") || !strings.Contains(v, "▸ Review requested · example-org/web") {
		t.Errorf("the remembered collapse should apply:\n%s", v)
	}
	for _, l := range strings.Split(v, "\n") {
		if lipgloss.Width(l) > 56 {
			t.Errorf("wider than the tray panel: %q", l)
		}
	}
}

func TestRepoFilterListsTheConfiguredRepositories(t *testing.T) {
	r := &recorder{cfg: config.Config{Spaces: []config.Space{{Key: "DEMO", Repos: []config.Repo{{Name: "example-org/docs"}, {Name: "example-org/web"}}}}}}
	m := tea.Model(New(prsSnapshot(), false, r, ViewList))
	m, _ = m.Update(tea.WindowSizeMsg{Width: 160, Height: 40})
	v := stripANSI(press(m, "R").View())
	for _, want := range []string{"[x] example-org/docs", "0 open", "[x] example-org/api", "[x] example-org/web"} {
		if !strings.Contains(v, want) {
			t.Errorf("filter lacks %q:\n%s", want, v)
		}
	}
}

func TestCollapsingKeepsTheColumns(t *testing.T) {
	m := tea.Model(New(prsSnapshot(), false, &recorder{}, ViewList))
	m, _ = m.Update(tea.WindowSizeMsg{Width: 160, Height: 40})
	m = press(m, "2")
	v := stripANSI(m.View())
	head := strings.Split(v, "\n")[lineIndex(v, "TITLE")]
	for i := 0; i < 10; i++ {
		if it, _ := m.(Model).current(); it.group == "mine:example-org/api" {
			break
		}
		m = press(m, "s")
	}
	v = stripANSI(press(m, "enter").View())
	if !strings.Contains(v, "▸ Mine · example-org/api") {
		t.Fatalf("api group not collapsed:\n%s", v)
	}
	if got := strings.Split(v, "\n")[lineIndex(v, "TITLE")]; got != head {
		t.Errorf("collapsing moved the columns:\n%q\n%q", head, got)
	}
}

func TestReposButtonOpensAndClosesTheFilter(t *testing.T) {
	m := tea.Model(New(prsSnapshot(), false, &recorder{}, ViewList))
	m, _ = m.Update(tea.WindowSizeMsg{Width: 160, Height: 40})
	v := stripANSI(m.View())
	y := lineIndex(v, "Repos")
	if y < 0 {
		t.Fatalf("no Repos button:\n%s", v)
	}
	line := strings.Split(v, "\n")[y]
	x := lipgloss.Width(line[:strings.Index(line, "Repos")])
	m = click(m, x+1, y)
	if v := stripANSI(m.View()); !strings.Contains(v, "[x] example-org/api") {
		t.Fatalf("a click on Repos should open the filter:\n%s", v)
	}
	m = click(m, x+1, y)
	if v := stripANSI(m.View()); strings.Contains(v, "[x] example-org/api") {
		t.Errorf("another click on Repos should close it:\n%s", v)
	}
	narrow := tea.Model(New(prsSnapshot(), false, &recorder{}, ViewList))
	narrow, _ = narrow.Update(tea.WindowSizeMsg{Width: 56, Height: 30})
	if v := stripANSI(narrow.View()); !strings.Contains(v, "╭") || !strings.Contains(v, "│ Repos │") {
		t.Errorf("the tray panel keeps its boxed buttons:\n%s", v)
	}
}

func TestViewChoiceIsRemembered(t *testing.T) {
	r := &recorder{}
	m := tea.Model(New(prsSnapshot(), false, r, ViewColumns).WithState(BoardState{HiddenRepos: []string{"example-org/api"}}))
	m, _ = m.Update(tea.WindowSizeMsg{Width: 160, Height: 40})
	m = press(m, "v")
	if len(r.states) == 0 || r.states[len(r.states)-1].View != ViewList || !slices.Equal(r.states[len(r.states)-1].HiddenRepos, []string{"example-org/api"}) {
		t.Fatalf("v should remember the list and keep the rest: %+v", r.states)
	}
	v := stripANSI(m.View())
	y := lineIndex(v, "Columns")
	line := strings.Split(v, "\n")[y]
	m = click(m, lipgloss.Width(line[:strings.Index(line, "Columns")])+1, y)
	if got := r.states[len(r.states)-1].View; got != ViewColumns {
		t.Errorf("a click on Columns should remember it: %q", got)
	}
}

// selectMenuItem moves the open menu's selection to the first item whose label contains label.
func selectMenuItem(t *testing.T, m tea.Model, label string) tea.Model {
	t.Helper()
	for i := 0; i < 20; i++ {
		mn := m.(Model).menu
		if mn == nil {
			t.Fatalf("no menu open looking for %q", label)
		}
		if strings.Contains(mn.items[mn.sel].label, label) {
			return m
		}
		m = press(m, "s")
	}
	t.Fatalf("no menu item %q", label)
	return m
}

func TestTicketMenuGathersSeveralPullRequestsInASubmenu(t *testing.T) {
	r := &recorder{}
	m := tea.Model(New(prsSnapshot(), false, r, ViewList))
	m, _ = m.Update(tea.WindowSizeMsg{Width: 160, Height: 40})
	for i := 0; i < 10 && !strings.Contains(selectedLine(m.View()), "DEMO-4"); i++ {
		m = press(m, "s")
	}
	m = press(m, "enter")
	v := stripANSI(m.View())
	if !strings.Contains(v, "Pull requests (3) ▸") || strings.Contains(v, "Open #815") {
		t.Fatalf("several pull requests should sit behind one item:\n%s", v)
	}
	m = press(selectMenuItem(t, m, "Pull requests (3)"), "enter")
	v = stripANSI(m.View())
	first, second, merged := strings.Index(v, "#815 Changes requested · web"), strings.Index(v, "#816 Approved · api"), strings.Index(v, "#700 merged · web")
	if !strings.Contains(v, "Pull requests of DEMO-4") || first < 0 || second < first || merged < second {
		t.Fatalf("submenu should list them, those that need the user first, finished ones last:\n%s", v)
	}
	if v := stripANSI(press(m, "esc").View()); !strings.Contains(v, "Actions for DEMO-4") {
		t.Errorf("esc should go back to the ticket's menu:\n%s", v)
	}
	if _, msg := run(m, "enter"); msg != StatusMsg("open https://github.com/example-org/web/pull/815") {
		t.Errorf("enter ran %v", r.calls)
	}
	if strings.Contains(v, "Copy the link of #816") {
		t.Errorf("a pull request without a link offers nothing to copy:\n%s", v)
	}
	if _, msg := run(selectMenuItem(t, m, "Copy the link of #815"), "enter"); msg != StatusMsg("copy link https://github.com/example-org/web/pull/815") {
		t.Errorf("each pull request's link should be copyable from a row: %v", r.calls)
	}
}

func TestPOpensTheOnlyPullRequestOrTheSubmenu(t *testing.T) {
	r := &recorder{}
	m := tea.Model(New(prsSnapshot(), false, r, ViewList))
	m, _ = m.Update(tea.WindowSizeMsg{Width: 160, Height: 40})
	for i := 0; i < 10 && !strings.Contains(selectedLine(m.View()), "DEMO-1"); i++ {
		m = press(m, "s")
	}
	if _, msg := run(m, "p"); msg != StatusMsg("open https://github.com/example-org/web/pull/812") {
		t.Errorf("p on a ticket with one pull request ran %v", r.calls)
	}
	for i := 0; i < 10 && !strings.Contains(selectedLine(m.View()), "DEMO-4"); i++ {
		m = press(m, "s")
	}
	for _, k := range []string{"p", "P"} {
		if v := stripANSI(press(m, k).View()); !strings.Contains(v, "Pull requests of DEMO-4") {
			t.Errorf("%s on a ticket with several pull requests should open the submenu:\n%s", k, v)
		}
	}
}

func TestPRMenuCopiesItsLinksAndKey(t *testing.T) {
	r := &recorder{}
	m := tea.Model(New(prsSnapshot(), false, r, ViewList))
	m, _ = m.Update(tea.WindowSizeMsg{Width: 160, Height: 40})
	m = press(m, "2")
	for i := 0; i < 10 && !strings.Contains(selectedLine(m.View()), "#815"); i++ {
		m = press(m, "s")
	}
	m = press(m, "enter")
	v := stripANSI(m.View())
	for _, want := range []string{"Copy the PR link", "Copy the Jira link", "Copy the key DEMO-4"} {
		if !strings.Contains(v, want) {
			t.Errorf("PR menu lacks %q:\n%s", want, v)
		}
	}
	for label, want := range map[string]string{
		"Copy the PR link":    "copy link https://github.com/example-org/web/pull/815",
		"Copy the Jira link":  "copy link https://example.atlassian.net/browse/DEMO-4",
		"Copy the key DEMO-4": "copy key DEMO-4",
	} {
		if _, msg := run(selectMenuItem(t, m, label), "enter"); msg != StatusMsg(want) {
			t.Errorf("%s ran %v", label, msg)
		}
	}
}

func TestTicketsShowTheirAssignee(t *testing.T) {
	snap := prsSnapshot()
	snap.Tickets[0].Assignee = "Ana López"
	snap.Tickets[1].Assignee = ""
	for _, view := range []string{ViewColumns, ViewList} {
		m := tea.Model(New(snap, false, &recorder{}, view))
		m, _ = m.Update(tea.WindowSizeMsg{Width: 160, Height: 40})
		v := stripANSI(m.View())
		if !strings.Contains(v, "Ana L.") || !strings.Contains(v, "unassigned") {
			t.Errorf("%s: every ticket should show its assignee:\n%s", view, v)
		}
	}
	m := tea.Model(New(snap, true, &recorder{}, ViewList))
	m, _ = m.Update(tea.WindowSizeMsg{Width: 160, Height: 40})
	v := stripANSI(m.View())
	if !strings.Contains(strings.Split(v, "\n")[lineIndex(v, "TITLE")], "ASSIGNEE") {
		t.Errorf("the list should head its assignee column:\n%s", v)
	}
	for !strings.Contains(selectedLine(m.View()), string(snap.Tickets[0].Key)) {
		m = press(m, "s")
	}
	if v := stripANSI(m.View()); !strings.Contains(v, "Ana López") {
		t.Errorf("the preview should name the assignee in full:\n%s", v)
	}
	narrow := tea.Model(New(snap, false, &recorder{}, ViewList))
	narrow, _ = narrow.Update(tea.WindowSizeMsg{Width: 56, Height: 30})
	for _, l := range strings.Split(stripANSI(narrow.View()), "\n") {
		if lipgloss.Width(l) > 56 {
			t.Errorf("wider than the tray panel: %q", l)
		}
	}
}

func TestShortName(t *testing.T) {
	for in, want := range map[string]string{"Ana López": "Ana L.", "Ana María López Ruiz": "Ana R.", "dev": "dev", "": "unassigned"} {
		if got := shortName(in); got != want {
			t.Errorf("shortName(%q) = %q, want %q", in, got, want)
		}
	}
}

// teamSnapshot adds a teammate's ticket with a failing pull request to prsSnapshot.
func teamSnapshot() board.Snapshot {
	snap := prsSnapshot()
	snap.Tickets = append(snap.Tickets, ticket.Ticket{Key: "DEMO-9", Summary: "Their export", Status: "To Do", Category: ticket.ToDo, Team: true, Assignee: "Bea Ruiz",
		URL: "https://example.atlassian.net/browse/DEMO-9",
		PRs: []ticket.PullRequest{{Repo: "example-org/web", Number: 990, URL: "https://github.com/example-org/web/pull/990", State: ticket.PROpen, Checks: ticket.ChecksFail}}})
	return snap
}

func TestTeamToggleShowsTheTeamsTickets(t *testing.T) {
	r := &recorder{}
	m := tea.Model(New(teamSnapshot(), true, r, ViewList))
	m, _ = m.Update(tea.WindowSizeMsg{Width: 160, Height: 40})
	if v := stripANSI(m.View()); strings.Contains(v, "DEMO-9") {
		t.Fatalf("off by default:\n%s", v)
	}
	m, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("t")})
	if len(r.states) == 0 || !r.states[len(r.states)-1].Team {
		t.Fatalf("t should remember the team: %+v", r.states)
	}
	if cmd == nil {
		t.Fatal("turning the team on should sync")
	}
	drain(cmd)
	if !slices.Contains(r.calls, "refresh") {
		t.Errorf("turning the team on should refresh after saving: %v", r.calls)
	}
	v := stripANSI(m.View())
	team := lineIndex(v, "Team  1")
	if team < 0 || lineIndex(v, "DEMO-9") < team || lineIndex(v, "DEMO-1") > team || !strings.Contains(v, "Bea R.") {
		t.Errorf("team tickets in their own section after the user's:\n%s", v)
	}
	if line := strings.Split(v, "\n")[lineIndex(v, "DEMO-9")]; strings.HasPrefix(strings.TrimSpace(line), "!") {
		t.Errorf("a team ticket never needs the user: %q", line)
	}
	if v := stripANSI(press(m, "2").View()); strings.Contains(v, "#990") {
		t.Errorf("the PRs filter keeps to the user's pull requests:\n%s", v)
	}
	m = press(m, "v")
	if !r.states[len(r.states)-1].Team {
		t.Errorf("saving the view must keep the team: %+v", r.states[len(r.states)-1])
	}
	if v := stripANSI(m.View()); !strings.Contains(v, "DEMO-9") {
		t.Errorf("the columns show team tickets too:\n%s", v)
	}
	m = press(m, "t")
	if v := stripANSI(m.View()); strings.Contains(v, "DEMO-9") || r.states[len(r.states)-1].Team {
		t.Errorf("t again hides them at once:\n%s", v)
	}
}

func TestTeamTicketsOfferNoAgentActions(t *testing.T) {
	r := &recorder{}
	m := tea.Model(New(teamSnapshot(), true, r, ViewList).WithState(BoardState{Team: true}))
	m, _ = m.Update(tea.WindowSizeMsg{Width: 160, Height: 40})
	for i := 0; i < 10 && !strings.Contains(selectedLine(m.View()), "DEMO-9"); i++ {
		m = press(m, "s")
	}
	before := len(r.calls)
	m = press(m, "f")
	if len(r.calls) != before || !strings.Contains(stripANSI(m.View()), "DEMO-9 is Bea Ruiz's ticket") {
		t.Errorf("f on a team ticket does nothing but say why: %v\n%s", r.calls, stripANSI(m.View()))
	}
	v := stripANSI(press(m, "enter").View())
	for _, want := range []string{"Actions for DEMO-9 · To Do · Bea Ruiz", "Show details", "Open in Jira ↗", "Assign to…", "Copy the key DEMO-9"} {
		if !strings.Contains(v, want) {
			t.Errorf("menu lacks %q:\n%s", want, v)
		}
	}
	var box []string
	for _, l := range strings.Split(v, "\n") {
		if strings.Contains(l, "│") {
			box = append(box, l)
		}
	}
	for _, unwanted := range []string{"Start", "agent", "ticket pane", "Send"} {
		if strings.Contains(strings.Join(box, "\n"), unwanted) {
			t.Errorf("a team ticket's menu offers %q:\n%s", unwanted, v)
		}
	}
}

func TestTeamButtonTogglesAndFitsTheTrayPanel(t *testing.T) {
	r := &recorder{}
	m := tea.Model(New(teamSnapshot(), true, r, ViewList))
	m, _ = m.Update(tea.WindowSizeMsg{Width: 160, Height: 40})
	v := stripANSI(m.View())
	y := lineIndex(v, "Team")
	if y < 0 || y > 2 {
		t.Fatalf("no Team button in the header:\n%s", v)
	}
	line := strings.Split(v, "\n")[y]
	m = click(m, lipgloss.Width(line[:strings.Index(line, "Team")])+1, y)
	if len(r.states) == 0 || !r.states[len(r.states)-1].Team {
		t.Errorf("a click on Team should turn it on: %+v", r.states)
	}
	narrow := tea.Model(New(teamSnapshot(), true, &recorder{}, ViewList).WithState(BoardState{Team: true}))
	narrow, _ = narrow.Update(tea.WindowSizeMsg{Width: 56, Height: 30})
	for _, l := range strings.Split(stripANSI(narrow.View()), "\n") {
		if lipgloss.Width(l) > 56 {
			t.Errorf("wider than the tray panel: %q", l)
		}
	}
}

// drain runs cmd and every command it batches or sequences, as Bubble Tea would, skipping ticks.
func drain(cmd tea.Cmd) {
	if cmd == nil {
		return
	}
	done := make(chan tea.Msg, 1)
	go func() { done <- cmd() }()
	var msg tea.Msg
	select {
	case msg = <-done:
	case <-time.After(50 * time.Millisecond):
		return
	}
	v := reflect.ValueOf(msg)
	if v.Kind() != reflect.Slice {
		return
	}
	for i := 0; i < v.Len(); i++ {
		if c, ok := v.Index(i).Interface().(tea.Cmd); ok {
			drain(c)
		}
	}
}

func TestAPartialBoardKeepsRefreshing(t *testing.T) {
	snap := snapshot()
	snap.Agents = map[string]string{"DEMO-1": "working"}
	m := tea.Model(New(snap, true, &recorder{}, ViewList))
	m, _ = m.Update(tea.WindowSizeMsg{Width: 160, Height: 40})
	m = press(m, "g")
	partial := snapshot()
	partial.FetchedAt = snap.FetchedAt.Add(time.Minute)
	partial.Partial = true
	partial.Tickets = append(partial.Tickets, ticket.Ticket{Key: "DEMO-8", Summary: "Just assigned", Status: "To Do", Category: ticket.ToDo})
	m, _ = m.Update(SnapshotMsg{Snap: partial, Quiet: true})
	v := stripANSI(m.View())
	if !strings.Contains(v, "DEMO-8") || !strings.Contains(v, "updating PRs…") || !strings.Contains(v, "working") {
		t.Errorf("Jira's board shows at once, still updating, agents kept:\n%s", v)
	}
	full := partial
	full.Partial = false
	m, _ = m.Update(SnapshotMsg{Snap: full})
	if v := stripANSI(m.View()); strings.Contains(v, "updating PRs") || strings.Contains(v, "refreshing") {
		t.Errorf("the full board ends the refresh:\n%s", v)
	}
}

func TestTeamListIsSplitByBoard(t *testing.T) {
	snap := teamSnapshot()
	snap.Tickets[len(snap.Tickets)-1].Board = "Authorization Center"
	snap.Tickets = append(snap.Tickets, ticket.Ticket{Key: "DEMO-10", Summary: "Access review", Status: "To Do", Category: ticket.ToDo, Team: true, Board: "User Access Center"})
	m := tea.Model(New(snap, true, &recorder{cfg: config.Config{Spaces: []config.Space{{Key: "DEMO", TeamBoards: []config.TeamBoard{{ID: 1, Name: "x"}}}}}}, ViewList).WithState(BoardState{Team: true}))
	m, _ = m.Update(tea.WindowSizeMsg{Width: 160, Height: 40})
	v := stripANSI(m.View())
	auth, access := lineIndex(v, "Team · Authorization Center  1"), lineIndex(v, "Team · User Access Center  1")
	if auth < 0 || access < 0 || lineIndex(v, "DEMO-9") < auth || lineIndex(v, "DEMO-10") < access {
		t.Errorf("the team should be split by board:\n%s", v)
	}
}

func TestTeamWithoutBoardsSaysWhereToAddThem(t *testing.T) {
	m := tea.Model(New(teamSnapshot(), true, &recorder{}, ViewList))
	m, _ = m.Update(tea.WindowSizeMsg{Width: 160, Height: 40})
	if v := stripANSI(press(m, "t").View()); !strings.Contains(v, "add team boards in Settings (,)") {
		t.Errorf("turning the team on without boards should say where to add them:\n%s", v)
	}
}

// subtaskSnapshot gives DEMO-1 (In Progress) a subtask to do, one in progress and one done.
func subtaskSnapshot() board.Snapshot {
	snap := snapshot()
	i := slices.IndexFunc(snap.Tickets, func(t ticket.Ticket) bool { return t.Key == "DEMO-1" })
	snap.Tickets[i].Subtasks = []ticket.Ticket{
		{Key: "DEMO-11", Summary: "Write the migration", Status: "To Do", Category: ticket.ToDo, Parent: "DEMO-1"},
		{Key: "DEMO-12", Summary: "Agree the schema", Status: "Done", Category: ticket.Done, Parent: "DEMO-1"},
		{Key: "DEMO-13", Summary: "Wire the filter", Status: "In Progress", Category: ticket.InProgress, Parent: "DEMO-1"},
	}
	return snap
}

func TestCardsShowSubtaskProgress(t *testing.T) {
	snap := subtaskSnapshot()
	tk := snap.Tickets[slices.IndexFunc(snap.Tickets, func(t ticket.Ticket) bool { return t.Key == "DEMO-1" })]
	tk.Type = "Story"
	plain := ansi.Strip(card(tk, 42, false, false, ""))
	if !strings.Contains(plain, "1/3 subtasks · 1 to do") {
		t.Errorf("the parent should show its progress and what is still to do:\n%s", plain)
	}
}

func TestSubtasksHaveCardsInTheirOwnColumn(t *testing.T) {
	m := tea.Model(New(subtaskSnapshot(), true, &recorder{}, ViewColumns))
	m, _ = m.Update(tea.WindowSizeMsg{Width: 160, Height: 60})
	cols := m.(Model).columns()
	keys := func(col []item) []string {
		var out []string
		for _, it := range col {
			out = append(out, string(it.key))
		}
		return out
	}
	todo, doing := keys(cols[0]), keys(cols[1])
	if !slices.Contains(todo, "DEMO-11") || slices.Index(doing, "DEMO-13") != slices.Index(doing, "DEMO-1")+1 {
		t.Errorf("open subtasks go to their own column, right after a parent sharing it: to do %v, in progress %v", todo, doing)
	}
	if done := keys(cols[3]); !slices.Contains(done, "DEMO-12") {
		t.Errorf("a done subtask has its card in Done, as in Jira: %v", done)
	}
	v := stripANSI(m.View())
	if !strings.Contains(v, "↳ DEMO-11") || !strings.Contains(v, "of DEMO-1") {
		t.Errorf("a subtask card names itself and its parent:\n%s", v)
	}
}

func TestListShowsSubtasksUnderTheirParent(t *testing.T) {
	r := &recorder{}
	m := tea.Model(New(subtaskSnapshot(), true, r, ViewList))
	m, _ = m.Update(tea.WindowSizeMsg{Width: 160, Height: 40})
	v := stripANSI(m.View())
	parent := lineIndex(v, "DEMO-1 ")
	for i, k := range []string{"↳ DEMO-11", "↳ DEMO-12", "↳ DEMO-13"} {
		if lineIndex(v, k) != parent+1+i {
			t.Errorf("%s should sit under DEMO-1, in order:\n%s", k, v)
		}
	}
	for i := 0; i < 10 && !strings.Contains(selectedLine(m.View()), "DEMO-11"); i++ {
		m = press(m, "s")
	}
	if v := stripANSI(press(m, "enter").View()); !strings.Contains(v, "Actions for DEMO-11") {
		t.Errorf("a subtask has its own actions:\n%s", v)
	}
}

func TestMoveToListsJiraStatusesAndConfirmsBeforeMoving(t *testing.T) {
	r := &recorder{transitions: []Transition{
		{ID: "31", To: "In Review"},
		{ID: "32", To: "In Review"},
		{ID: "11", To: "To Do"},
		{ID: "51", To: "Blocked", NeedsForm: true},
	}}
	m := selectMenuItem(t, press(loaded(r), "l", "enter"), "Move to…")
	m, msg := run(m, "enter")
	if r.last() != "transitions DEMO-1" {
		t.Fatalf("Move to… ran %v", r.calls)
	}
	m, _ = m.Update(msg)
	v := stripANSI(m.View())
	if !strings.Contains(v, "Move DEMO-1 to") || strings.Count(v, "In Review") != 1 || !strings.Contains(v, "To Do") || !strings.Contains(v, "Blocked · in Jira ↗") {
		t.Fatalf("the statuses Jira allows, each once, should be listed:\n%s", v)
	}

	confirm := press(selectMenuItem(t, m, "In Review"), "enter")
	if v := stripANSI(confirm.View()); !strings.Contains(v, "Move DEMO-1 to In Review?") || !strings.Contains(v, "Yes, move it") {
		t.Fatalf("moving should ask first:\n%s", v)
	}
	if slices.ContainsFunc(r.calls, func(c string) bool { return strings.HasPrefix(c, "transition ") }) {
		t.Fatalf("moved before the confirmation: %v", r.calls)
	}
	if v := stripANSI(press(confirm, "esc").View()); !strings.Contains(v, "Move DEMO-1 to") {
		t.Errorf("esc should go back to the statuses:\n%s", v)
	}
	if v := stripANSI(press(selectMenuItem(t, confirm, "Cancel"), "enter").View()); strings.Contains(v, "Move DEMO-1") {
		t.Errorf("Cancel should close without moving:\n%s", v)
	}

	m, msg = run(selectMenuItem(t, confirm, "Yes, move it"), "enter")
	if r.last() != "transition DEMO-1 31" {
		t.Fatalf("Yes ran %v", r.calls)
	}
	m, cmd := m.Update(msg)
	if !strings.Contains(stripANSI(m.View()), "DEMO-1 moved to In Review") || cmd == nil {
		t.Fatalf("a move should say so and refresh:\n%s", stripANSI(m.View()))
	}
	cmd()
	if !slices.Contains(r.calls, "refresh") {
		t.Errorf("no refresh after the move: %v", r.calls)
	}

	_, msg = run(selectMenuItem(t, press(loaded(r), "l", "enter"), "Move to…"), "enter")
	m, _ = awaitingMoves(loaded(r)).Update(msg)
	if _, msg := run(selectMenuItem(t, m, "Blocked"), "enter"); msg != StatusMsg("open https://example.atlassian.net/browse/DEMO-1") {
		t.Errorf("a status with a required form should open Jira, got %v", msg)
	}
}

func TestStatusBadgeUsesItsColumnsColorAndMark(t *testing.T) {
	prev := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	t.Cleanup(func() { lipgloss.SetColorProfile(prev) })
	cases := []struct {
		t    ticket.Ticket
		text string
		hex  string
	}{
		{ticket.Ticket{Status: "Tareas por hacer", Category: ticket.ToDo}, " Tareas por hacer ", "#9399b2"},
		{ticket.Ticket{Status: "En curso", Category: ticket.InProgress}, " En curso ", "#89b4fa"},
		{ticket.Ticket{Status: "Pending test", Category: ticket.InReview, Mark: ticket.MarkDiamond}, " ◇ Pending test ", "#cba6f7"},
		{ticket.Ticket{Status: "Finalizada", Category: ticket.Done}, " Finalizada ", "#a6e3a1"},
	}
	for _, c := range cases {
		got := statusBadge(c.t)
		want := lipgloss.NewStyle().Background(lipgloss.Color(c.hex)).Foreground(lipgloss.Color("#1e1e2e")).Render(c.text)
		if got != want {
			t.Errorf("%s: badge %q, want %q", c.t.Status, got, want)
		}
	}
}

func TestTeamModeListsEveryOpenPullRequestWithItsAuthor(t *testing.T) {
	snap := prsSnapshot()
	snap.Others = []ticket.PullRequest{{Repo: "example-org/api", Number: 901, Title: "Rename the export", Author: "sam", URL: "https://github.com/example-org/api/pull/901", State: ticket.PROpen, Checks: ticket.ChecksPass}}
	r := &recorder{}
	m := tea.Model(New(snap, false, r, ViewList))
	m, _ = m.Update(tea.WindowSizeMsg{Width: 160, Height: 40})
	mm := m.(Model)
	mm.teamPRs = true
	m = press(mm, "2")
	v := stripANSI(m.View())
	for _, want := range []string{"AUTHOR", "▾ example-org/api  1", "▾ example-org/web  1"} {
		if !strings.Contains(v, want) {
			t.Errorf("missing %q:\n%s", want, v)
		}
	}
	if strings.Contains(v, "Review requested · ") || lineIndex(m.View(), "▾ example-org/api") < lineIndex(m.View(), "Mine") {
		t.Errorf("with Team on the user's pull requests come first, then the team's by repository, requests among them:\n%s", v)
	}
	if requested := strings.Split(v, "\n")[lineIndex(m.View(), "#820")]; !strings.Contains(requested, "! #820 Review requested") {
		t.Errorf("a requested review keeps its mark and wording inside its repository's group: %q", requested)
	}
	row := strings.Split(v, "\n")[lineIndex(m.View(), "#901")]
	if !strings.Contains(row, "sam") || !strings.Contains(row, "Rename the export") || strings.Contains(row, "Review requested") || strings.HasPrefix(strings.TrimSpace(row), "!") {
		t.Errorf("another person's pull request shows its author, without the review mark: %q", row)
	}
	for i := 0; i < 20 && !strings.Contains(selectedLine(m.View()), "#901"); i++ {
		m = press(m, "s")
	}
	if v := stripANSI(press(m, "enter").View()); !strings.Contains(v, "Pull request #901 · example-org/api  ") || !strings.Contains(v, "  sam  ") || !strings.Contains(v, "Open the pull request in GitHub ↗") {
		t.Errorf("its menu opens it in GitHub:\n%s", v)
	}
	off := press(New(snap, false, r, ViewList), "2")
	if v := stripANSI(off.View()); !strings.Contains(v, "Review requested · example-org/web") || strings.Contains(v, "▾ example-org/api") {
		t.Errorf("with Team off only the review requests follow the user's pull requests:\n%s", v)
	}
}

func TestViewButtonsShowOnlyWhereTheyApply(t *testing.T) {
	m := tea.Model(New(snapshot(), true, &recorder{}, ViewColumns))
	m, _ = m.Update(tea.WindowSizeMsg{Width: 160, Height: 40})
	if v := stripANSI(m.View()); !strings.Contains(v, "│ Columns │ │ List │") {
		t.Errorf("the tickets' view offers both views:\n%s", v)
	}
	if head := strings.Join(strings.Split(stripANSI(press(m, "2").View()), "\n")[:4], "\n"); strings.Contains(head, "Columns") || strings.Contains(head, "List") {
		t.Errorf("the PRs filter is always a list, so it offers no view:\n%s", head)
	}
	m, _ = m.Update(tea.WindowSizeMsg{Width: 90, Height: 40})
	if head := strings.Join(strings.Split(stripANSI(m.View()), "\n")[:4], "\n"); strings.Contains(head, "Columns") {
		t.Errorf("a pane too narrow for columns offers no view:\n%s", head)
	}
}

func TestPRMenusPutTheirDetailsOnASecondLine(t *testing.T) {
	r := &recorder{}
	pr := ticket.PullRequest{Repo: "example-org/web", Number: 820, Author: "alex", URL: "https://github.com/example-org/web/pull/820"}
	m := loaded(r).(Model)
	m.menu = m.buildMenu(item{key: "#820", pr: &pr})
	lines := strings.Split(stripANSI(m.menuView()), "\n")
	if !strings.Contains(lines[1], "Pull request #820 · example-org/web") || !strings.Contains(lines[2], "alex · review requested") {
		t.Fatalf("title then details:\n%s", strings.Join(lines, "\n"))
	}
	if w := lipgloss.Width(m.menuView()); w > lipgloss.Width("Pull request #820 · example-org/web")+12 {
		t.Errorf("the menu should be as wide as its first line needs, got %d", w)
	}
	y := lineIndex(m.View(), "Open the pull request in GitHub")
	_, cmd := m.Update(tea.MouseMsg{X: m.width / 2, Y: y, Button: tea.MouseButtonLeft, Action: tea.MouseActionPress})
	if cmd == nil || cmd() != StatusMsg("open https://github.com/example-org/web/pull/820") {
		t.Errorf("a click on the first item should still run it")
	}
	y = lineIndex(m.View(), "Copy the PR link")
	_, cmd = m.Update(tea.MouseMsg{X: m.width / 2, Y: y, Button: tea.MouseButtonLeft, Action: tea.MouseActionPress})
	if cmd == nil || cmd() != StatusMsg("copy link https://github.com/example-org/web/pull/820") {
		t.Errorf("another person's pull request link should be copyable with a click:\n%s", stripANSI(m.menuView()))
	}
}

func TestStoryPointsShowOnCardsColumnsListAndPreview(t *testing.T) {
	tk := ticket.Ticket{Key: "ACTO-170450", Summary: "Homogenize", Status: "En curso", Category: ticket.InProgress, Points: 3, Mark: ticket.MarkDiamond}
	if head := ansi.Strip(strings.Split(card(tk, 40, false, false, "idle"), "\n")[1]); !strings.Contains(head, "ACTO-170450 3pt") || !strings.Contains(head, "○") {
		t.Errorf("points sit beside the key, before the marks: %q", head)
	}
	if head := ansi.Strip(strings.Split(card(tk, 26, true, false, "idle"), "\n")[1]); !strings.Contains(head, "▸ ACTO-170450 3pt") || !strings.Contains(head, "◇ ○") || strings.Contains(head, "En curso") {
		t.Errorf("on a narrow card the mark shrinks to its icon first: %q", head)
	}
	if head := ansi.Strip(strings.Split(card(tk, 24, true, false, "idle"), "\n")[1]); strings.Contains(head, "3pt") || !strings.Contains(head, "◇ ○") || lipgloss.Width(head) > 24 {
		t.Errorf("without room for both, the points give way: %q", head)
	}
	tk.Points = 0.5
	if head := ansi.Strip(strings.Split(card(tk, 40, false, false, ""), "\n")[1]); !strings.Contains(head, "0.5pt") {
		t.Errorf("half points: %q", head)
	}
	tk.Points = 0
	if head := ansi.Strip(strings.Split(card(tk, 40, false, false, ""), "\n")[1]); strings.Contains(head, "pt") {
		t.Errorf("an unestimated ticket shows no points: %q", head)
	}

	snap := snapshot()
	for i := range snap.Tickets {
		if snap.Tickets[i].Key == "DEMO-1" {
			snap.Tickets[i].Points = 3
		}
		if snap.Tickets[i].Key == "DEMO-3" {
			snap.Tickets[i].Points = 2
		}
	}
	m := tea.Model(New(snap, true, &recorder{}, ViewColumns))
	m, _ = m.Update(tea.WindowSizeMsg{Width: 160, Height: 40})
	v := stripANSI(m.View())
	if !strings.Contains(v, "In Progress           1 · 3 pts") && !strings.Contains(v, "1 · 3 pts") {
		t.Errorf("a column's title adds up its points:\n%s", v)
	}
	if strings.Contains(v, "In Review") && strings.Contains(v, "1 · 0 pts") {
		t.Errorf("a column without points shows only its count:\n%s", v)
	}
	if !strings.Contains(v, "DEMO-3  To Do  · Export CSV\n 2 points · unassigned") {
		t.Errorf("the preview names the selected ticket's points:\n%s", v)
	}
	list := stripANSI(press(m, "v").View())
	if !strings.Contains(list, " SP ") || !strings.Contains(strings.Split(list, "\n")[lineIndex(list, "DEMO-1 ")], " 3 ") {
		t.Errorf("the list has a points column:\n%s", list)
	}
	if prs := stripANSI(press(m, "2").View()); strings.Contains(prs, " SP ") {
		t.Errorf("the PRs filter has no points column:\n%s", prs)
	}
}

func TestScrolledListKeepsItsHeaderAndAGapAbove(t *testing.T) {
	snap := snapshot()
	for i := 10; i < 40; i++ {
		snap.Tickets = append(snap.Tickets, ticket.Ticket{Key: ticket.Key(fmt.Sprintf("DEMO-%d", i)), Summary: "filler", Status: "To Do", Category: ticket.ToDo})
	}
	for _, filter := range []string{"1", "2"} {
		m := tea.Model(New(snap, true, &recorder{}, ViewList))
		m, _ = m.Update(tea.WindowSizeMsg{Width: 120, Height: 24})
		m = press(m, filter)
		for i := 0; i < 25; i++ {
			m = press(m, "s")
		}
		lines := strings.Split(stripANSI(m.View()), "\n")
		head := lineIndex(m.View(), "STATUS")
		if head < 0 || !strings.HasPrefix(strings.TrimSpace(lines[head+1]), "────") {
			t.Fatalf("filter %s: the list's header should stay while it scrolls:\n%s", filter, strings.Join(lines, "\n"))
		}
		sel := lineIndex(m.View(), "▸ ")
		if sel <= head+1 {
			t.Errorf("filter %s: the selection should show under the header:\n%s", filter, strings.Join(lines, "\n"))
		}
		preview := len(lines) - lineCount(m.(Model).preview()+"\n"+m.(Model).footer())
		if strings.TrimSpace(lines[preview-1]) != "" || filter == "1" && strings.TrimSpace(lines[preview-2]) == "" {
			t.Errorf("filter %s: one blank line should part the full list from the bottom:\n%s", filter, strings.Join(lines, "\n"))
		}
		row := lineIndex(m.View(), "DEMO-")
		if row > 0 {
			key := strings.Fields(strings.TrimLeft(lines[row], " ▸!"))[0]
			if got := click(m, 6, row); !strings.Contains(selectedLine(got.View()), key) {
				t.Errorf("filter %s: a click on %s selected %q", filter, key, selectedLine(got.View()))
			}
		}
	}
}

func TestPRsListScrollsToTheSelectionPastACollapsedGroup(t *testing.T) {
	snap := prsSnapshot()
	for i := 0; i < 25; i++ {
		snap.Others = append(snap.Others, ticket.PullRequest{Repo: "example-org/api", Number: 900 + i, Title: "other", Author: "sam", State: ticket.PROpen})
	}
	m := tea.Model(New(snap, true, &recorder{}, ViewList).WithState(BoardState{Team: true, Collapsed: []string{"mine:"}}))
	m, _ = m.Update(tea.WindowSizeMsg{Width: 110, Height: 24})
	m = press(m, "2")
	for i := 0; i < 28; i++ {
		m = press(m, "s")
	}
	v := stripANSI(m.View())
	if !strings.Contains(v, "▸ #92") || strings.Contains(v, "▸ Mine  2") {
		t.Errorf("the list should follow the selection, not a collapsed group's mark:\n%s", v)
	}
	if !strings.Contains(v, "opened by sam in example-org/api") || strings.Contains(v, "review requested by sam") {
		t.Errorf("someone else's pull request is not a review request:\n%s", v)
	}
}

func TestLegendFitsEachScreen(t *testing.T) {
	open := func(snap board.Snapshot, view string, keys ...string) string {
		m := tea.Model(New(snap, true, &recorder{}, view))
		m, _ = m.Update(tea.WindowSizeMsg{Width: 220, Height: 40})
		return stripANSI(press(m, keys...).View())
	}
	noReviews := snapshot()
	noReviews.Reviews = nil
	noRepos := board.Snapshot{FetchedAt: snapshot().FetchedAt, Tickets: []ticket.Ticket{{Key: "DEMO-3", Summary: "Export CSV", Status: "To Do", Category: ticket.ToDo}}}
	cases := []struct {
		name       string
		v          string
		want, dont []string
	}{
		{"columns", open(snapshot(), ViewColumns), []string{"←↑↓→ wasd move · ↑ to reviews", "click select · open review", "enter actions", "f send feedback", "i details", "v view", "z density", "t team", "R repos"},
			[]string{"ticket pane", "fix CI", "fold group"}},
		{"columns without reviews", open(noReviews, ViewColumns), []string{"←↑↓→ wasd move", "click select"}, []string{"↑ to reviews", "open review"}},
		{"list", open(snapshot(), ViewList), []string{"↑↓ ws move · ↑ to reviews", "click select · open review", "v view", "t team"}, []string{"z density"}},
		{"PRs", open(snapshot(), ViewColumns, "2"), []string{"↑↓ ws move", "click select", "enter actions · fold group", "f send feedback", "t requested", "R repos"}, []string{"v view", "z density", "open review"}},
		{"no repositories", open(noRepos, ViewColumns), []string{"t team"}, []string{"R repos"}},
	}
	for _, c := range cases {
		for _, w := range c.want {
			if !strings.Contains(c.v, w) {
				t.Errorf("%s: legend lacks %q:\n%s", c.name, w, c.v)
			}
		}
		for _, d := range c.dont {
			if strings.Contains(c.v, d) {
				t.Errorf("%s: legend should not offer %q:\n%s", c.name, d, c.v)
			}
		}
	}
}

func TestTeamToggleIsPerView(t *testing.T) {
	snap := prsSnapshot()
	snap.Others = []ticket.PullRequest{{Repo: "example-org/api", Number: 901, Title: "Rename the export", Author: "sam", State: ticket.PROpen}}
	r := &recorder{}
	m := tea.Model(New(snap, true, r, ViewList).WithState(BoardState{}))
	m, _ = m.Update(tea.WindowSizeMsg{Width: 160, Height: 40})
	m = press(m, "2")
	if v := stripANSI(m.View()); strings.Contains(v, "▾ example-org/api") || !strings.Contains(v, "│ Requested │") || !strings.Contains(v, "t requested") {
		t.Fatalf("the PRs filter starts with only the review requests, under a Requested toggle:\n%s", v)
	}
	before := stripANSI(m.View())
	m, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("t")})
	if !strings.Contains(before, "2 PRs 5") || !strings.Contains(stripANSI(m.View()), "2 PRs 6") {
		t.Errorf("the PRs counter should count the team's pull requests while they show:\n%s", stripANSI(m.View()))
	}
	if v := stripANSI(m.View()); !strings.Contains(v, "▾ example-org/api") || !strings.Contains(v, "showing every open pull request in your repositories · t shows only requests") {
		t.Fatalf("t in the PRs filter shows every open pull request at once:\n%s", v)
	}
	if cmd != nil {
		cmd()
	}
	if slices.Contains(r.calls, "refresh") {
		t.Errorf("the team's pull requests need no refresh: %v", r.calls)
	}
	if last := r.states[len(r.states)-1]; last.Team || last.TeamPRs == nil || !*last.TeamPRs {
		t.Errorf("only the PRs filter's toggle should be saved on: %+v", last)
	}
	m = press(m, "1")
	if m.(Model).team {
		t.Error("the tickets' toggle stays as it was")
	}
	if _, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("t")}); cmd == nil {
		t.Error("t in the tickets' view refreshes, since the team's tickets come from Jira")
	}
}

func TestTeamPRsFollowTheTeamToggleUntilChosen(t *testing.T) {
	on := true
	if !New(board.Snapshot{}, false, &recorder{}, ViewList).WithState(BoardState{Team: true}).teamPRs {
		t.Error("a board saved before the PRs toggle existed keeps showing the team's pull requests")
	}
	if New(board.Snapshot{}, false, &recorder{}, ViewList).WithState(BoardState{Team: true, TeamPRs: new(bool)}).teamPRs {
		t.Error("a saved choice wins")
	}
	if !New(board.Snapshot{}, false, &recorder{}, ViewList).WithState(BoardState{TeamPRs: &on}).teamPRs {
		t.Error("a saved choice wins")
	}
}

func TestPRMenuNamesTheTicketsStatus(t *testing.T) {
	snap := prsSnapshot()
	tk, _ := snap.Find("DEMO-4")
	m := tea.Model(New(snap, false, &recorder{}, ViewList))
	m, _ = m.Update(tea.WindowSizeMsg{Width: 160, Height: 40})
	m = press(m, "2")
	for i := 0; i < 10 && !strings.Contains(selectedLine(m.View()), "#815"); i++ {
		m = press(m, "s")
	}
	if v := stripANSI(press(m, "enter").View()); !strings.Contains(v, "Pull request #815 · DEMO-4  ") || !strings.Contains(v, "  "+tk.Status+"  ") {
		t.Errorf("the PR menu should name its ticket's status %q:\n%s", tk.Status, v)
	}
}

func TestAssignToMeAfterConfirming(t *testing.T) {
	r := &recorder{}
	m := press(selectMenuItem(t, press(loaded(r), "l", "enter"), "Assign to…"), "enter")
	v := stripANSI(m.View())
	if !strings.Contains(v, "Assign DEMO-1 · now unassigned") || !strings.Contains(v, "Me") || !strings.Contains(v, "Search someone…") || strings.Contains(v, "Unassign") {
		t.Fatalf("assign menu:\n%s", v)
	}
	m = press(selectMenuItem(t, m, "Me"), "enter")
	if v := stripANSI(m.View()); !strings.Contains(v, "Assign DEMO-1 to you?") || slices.ContainsFunc(r.calls, func(c string) bool { return strings.HasPrefix(c, "assign") }) {
		t.Fatalf("assigning should ask first:\n%s\n%v", v, r.calls)
	}
	m, msg := run(selectMenuItem(t, m, "Yes, assign it"), "enter")
	if r.last() != "assign DEMO-1 me" {
		t.Fatalf("calls %v", r.calls)
	}
	m, cmd := m.Update(msg)
	if v := stripANSI(m.View()); !strings.Contains(v, "DEMO-1 assigned to you") || cmd == nil {
		t.Fatalf("an assignment should say so and refresh:\n%s", v)
	}
	cmd()
	if !slices.Contains(r.calls, "refresh") {
		t.Errorf("no refresh after assigning: %v", r.calls)
	}
}

func TestAssignToSomeoneSearchedByName(t *testing.T) {
	r := &recorder{users: map[string][]User{"be": {{ID: "acc-7", Name: "Bea Ruiz"}}}}
	m := press(selectMenuItem(t, press(loaded(r), "l", "enter"), "Assign to…"), "enter")
	m = press(selectMenuItem(t, m, "Search someone…"), "enter")
	if v := stripANSI(m.View()); !strings.Contains(v, "Assign DEMO-1 to") || !strings.Contains(v, "type to search") {
		t.Fatalf("no search box:\n%s", v)
	}
	m, msg := run(press(m, "b"), "e")
	if r.last() != "users be" {
		t.Fatalf("calls %v", r.calls)
	}
	m, _ = m.Update(msg)
	m = press(m, "enter")
	if v := stripANSI(m.View()); !strings.Contains(v, "Assign DEMO-1 to Bea Ruiz?") {
		t.Fatalf("picking someone should ask first:\n%s", v)
	}
	run(selectMenuItem(t, m, "Yes, assign it"), "enter")
	if r.last() != "assign DEMO-1 acc-7" {
		t.Errorf("calls %v", r.calls)
	}
}

func TestTeamTicketsCanBeAssignedOrUnassigned(t *testing.T) {
	snap := teamSnapshot()
	i := slices.IndexFunc(snap.Tickets, func(t ticket.Ticket) bool { return t.Team })
	r := &recorder{}
	m := tea.Model(New(snap, true, r, ViewList))
	m, _ = m.Update(tea.WindowSizeMsg{Width: 160, Height: 40})
	mm := m.(Model)
	mm.menu = mm.buildMenu(item{key: snap.Tickets[i].Key, ticket: &snap.Tickets[i]})
	m = press(selectMenuItem(t, mm, "Assign to…"), "enter")
	if v := stripANSI(m.View()); !strings.Contains(v, "Assign DEMO-9 · now Bea Ruiz") || !strings.Contains(v, "Unassign") {
		t.Fatalf("a teammate's ticket can be taken or left unassigned:\n%s", v)
	}
	m = press(selectMenuItem(t, m, "Unassign"), "enter")
	if v := stripANSI(m.View()); !strings.Contains(v, "Leave DEMO-9 unassigned?") {
		t.Fatalf("unassigning should ask first:\n%s", v)
	}
	run(selectMenuItem(t, m, "Yes, unassign it"), "enter")
	if r.last() != "assign DEMO-9 nobody" {
		t.Errorf("calls %v", r.calls)
	}
}

func TestAssignReportsFailures(t *testing.T) {
	m, cmd := loaded(&recorder{}).Update(AssignedMsg{Key: "DEMO-1", Who: Assignee{ID: "acc-7", Name: "Bea Ruiz"}, Err: errors.New("403 Forbidden")})
	if v := stripANSI(m.View()); !strings.Contains(v, "could not assign DEMO-1 to Bea Ruiz: 403 Forbidden") || cmd != nil {
		t.Errorf("failed assignment:\n%s", v)
	}
	m, _ = m.Update(AssignedMsg{Key: "DEMO-1"})
	if v := stripANSI(m.View()); !strings.Contains(v, "DEMO-1 is now unassigned") {
		t.Errorf("unassigned:\n%s", v)
	}
}

func TestMoveToOpensAtOnceWhenTheStatusesArrivedWithTheMenu(t *testing.T) {
	r := &recorder{transitions: []Transition{{ID: "31", To: "In Review"}}}
	m, cmd := press(loaded(r), "l").Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil || r.last() != "transitions DEMO-1" {
		t.Fatalf("opening the menu should ask Jira for the statuses: %v", r.calls)
	}
	m, _ = m.Update(cmd())
	m, cmd = selectMenuItem(t, m, "Move to…").Update(tea.KeyMsg{Type: tea.KeyEnter})
	if v := stripANSI(m.View()); cmd != nil || !strings.Contains(v, "Move DEMO-1 to") || !strings.Contains(v, "In Review") {
		t.Fatalf("Move to… should open at once:\n%s", v)
	}
	if n := strings.Count(strings.Join(r.calls, ","), "transitions"); n != 1 {
		t.Errorf("Jira asked %d times", n)
	}
	if v := stripANSI(press(m, "esc").View()); !strings.Contains(v, "Actions for DEMO-1") {
		t.Errorf("esc should go back to the actions:\n%s", v)
	}
}

func TestMoveToSaysItIsLoadingUntilJiraAnswers(t *testing.T) {
	r := &recorder{transitions: []Transition{{ID: "31", To: "In Review"}}}
	m, cmd := selectMenuItem(t, press(loaded(r), "l", "enter"), "Move to…").Update(tea.KeyMsg{Type: tea.KeyEnter})
	if v := stripANSI(m.View()); cmd == nil || !strings.Contains(v, "loading the statuses Jira allows…") {
		t.Fatalf("the submenu should open at once, loading:\n%s", v)
	}
	m, _ = m.Update(cmd())
	if v := stripANSI(m.View()); !strings.Contains(v, "Move DEMO-1 to") || strings.Contains(v, "loading") {
		t.Fatalf("the statuses should replace the loading row:\n%s", v)
	}
	if v := stripANSI(press(m, "esc").View()); !strings.Contains(v, "Actions for DEMO-1") {
		t.Errorf("esc should go back to the actions:\n%s", v)
	}
}

func TestFixFromTheMenuSaysItIsWorking(t *testing.T) {
	r := &recorder{}
	m := selectMenuItem(t, press(loaded(r), "l", "enter"), "Send the CI failure to the agent")
	m = press(m, "enter")
	if r.last() != "fix DEMO-1 known" {
		t.Fatalf("fix should reuse the board's ticket: %v", r.calls)
	}
	if v := stripANSI(m.View()); !strings.Contains(v, "DEMO-1 · sending its feedback to the agent…") {
		t.Errorf("the board should say it is working:\n%s", v)
	}
}

// awaitingMoves shows DEMO-1's Move to… menu waiting for its statuses, as choosing it does.
func awaitingMoves(m tea.Model) tea.Model {
	mm := m.(Model)
	mm.menu = &menu{title: "Move DEMO-1 to", loading: "DEMO-1"}
	return mm
}

func TestMoveToStaysClosedWhenJiraAnswersLate(t *testing.T) {
	r := &recorder{transitions: []Transition{{ID: "31", To: "In Review"}}}
	m, cmd := selectMenuItem(t, press(loaded(r), "l", "enter"), "Move to…").Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = press(m, "esc", "esc")
	m, _ = m.Update(cmd())
	if v := stripANSI(m.View()); strings.Contains(v, "Move DEMO-1 to") {
		t.Errorf("a menu the user closed should not open again:\n%s", v)
	}
	m, cmd = selectMenuItem(t, press(m, "enter"), "Move to…").Update(tea.KeyMsg{Type: tea.KeyEnter})
	if v := stripANSI(m.View()); cmd != nil || !strings.Contains(v, "In Review") {
		t.Errorf("the late answer should still be kept for the next Move to…:\n%s", v)
	}
}

func TestMoveToReportsFailures(t *testing.T) {
	m := loaded(&recorder{})
	m, _ = awaitingMoves(m).Update(TransitionsMsg{Key: "DEMO-1", Err: errors.New("jira down")})
	if v := stripANSI(m.View()); !strings.Contains(v, "could not load the statuses of DEMO-1: jira down") {
		t.Errorf("failure to list:\n%s", v)
	}
	m, _ = awaitingMoves(m).Update(TransitionsMsg{Key: "DEMO-1"})
	if v := stripANSI(m.View()); !strings.Contains(v, "Jira offers no status change for DEMO-1") {
		t.Errorf("nothing to offer:\n%s", v)
	}
	m, cmd := m.Update(TransitionedMsg{Key: "DEMO-1", To: "In Review", Err: errors.New("403 Forbidden")})
	if v := stripANSI(m.View()); !strings.Contains(v, "could not move DEMO-1 to In Review: 403 Forbidden") || cmd != nil {
		t.Errorf("failed move:\n%s", v)
	}
}

func TestTeamTicketsCannotBeMoved(t *testing.T) {
	snap := teamSnapshot()
	i := slices.IndexFunc(snap.Tickets, func(t ticket.Ticket) bool { return t.Team })
	mn := New(snap, true, &recorder{}, ViewList).buildMenu(item{key: snap.Tickets[i].Key, ticket: &snap.Tickets[i]})
	for _, it := range mn.items {
		if strings.Contains(it.label, "Move to") {
			t.Errorf("a teammate's ticket offered %q", it.label)
		}
	}
}

func TestMenuKeepsItsSizeWhileMovingOverLinks(t *testing.T) {
	r := &recorder{transitions: []Transition{{ID: "31", To: "Done"}, {ID: "51", To: "Blocked", NeedsForm: true}}}
	m, _ := awaitingMoves(loaded(r)).Update(TransitionsMsg{Key: "DEMO-1", Transitions: r.transitions})
	box := func(m tea.Model) (int, int) { v := m.(Model).menuView(); return lipgloss.Width(v), lipgloss.Height(v) }
	w, h := box(m)
	if w2, h2 := box(press(m, "s")); w2 != w || h2 != h {
		t.Errorf("menu went from %dx%d to %dx%d on an item with a link", w, h, w2, h2)
	}
}

func blockedTransition() Transition {
	return Transition{ID: "51", To: "Blocked", Fields: []Field{
		{ID: "cf_reason", Name: "Reason", Kind: KindOption, Required: true, Options: []Option{{ID: "9001", Value: "Waiting on a decision"}, {ID: "9002", Value: "No access"}}},
		{ID: "cf_source", Name: "Source", Kind: KindOption, Options: []Option{{ID: "9101", Value: "Customer"}}},
		{ID: "cf_owner", Name: "Owner", Kind: KindUser},
	}}
}

func TestMoveFillsTheFormBeforeMoving(t *testing.T) {
	r := &recorder{transitions: []Transition{blockedTransition()}}
	m, _ := awaitingMoves(loaded(r)).Update(TransitionsMsg{Key: "DEMO-1", Transitions: r.transitions})
	m = press(selectMenuItem(t, m, "Blocked"), "enter")
	v := stripANSI(m.View())
	for _, want := range []string{"Move DEMO-1 to Blocked", "Reason *: —", "Source: —", "Move DEMO-1 to Blocked now", "Cancel"} {
		if !strings.Contains(v, want) {
			t.Fatalf("form missing %q:\n%s", want, v)
		}
	}

	m, msg := run(selectMenuItem(t, m, "now"), "enter")
	m, _ = m.Update(msg)
	if v := stripANSI(m.View()); !strings.Contains(v, "choose Reason first") || !strings.Contains(v, "Reason *: —") {
		t.Fatalf("moving without a required field should keep the form open and say so:\n%s", v)
	}
	if slices.ContainsFunc(r.calls, func(c string) bool { return strings.HasPrefix(c, "transition ") }) {
		t.Fatalf("moved without a required field: %v", r.calls)
	}

	m = press(selectMenuItem(t, m, "Reason"), "enter")
	if v := stripANSI(m.View()); !strings.Contains(v, "Waiting on a decision") || !strings.Contains(v, "No access") {
		t.Fatalf("a list field should offer its options:\n%s", v)
	}
	m, msg = run(selectMenuItem(t, m, "No access"), "enter")
	m, _ = m.Update(msg)
	if v := stripANSI(m.View()); !strings.Contains(v, "Reason *: No access") {
		t.Fatalf("the chosen option should show in the form:\n%s", v)
	}
	m, msg = run(selectMenuItem(t, m, "now"), "enter")
	if r.last() != "transition DEMO-1 51" || !reflect.DeepEqual(r.values, []Value{{Field: "cf_reason", Kind: KindOption, ID: "9002"}}) {
		t.Fatalf("Move ran %v with %+v", r.calls, r.values)
	}
	m, _ = m.Update(msg)
	if !strings.Contains(stripANSI(m.View()), "DEMO-1 moved to Blocked") {
		t.Errorf("no word of the move:\n%s", stripANSI(m.View()))
	}
}

func TestMoveLeavesToJiraAFormItCannotFill(t *testing.T) {
	tr := blockedTransition()
	tr.Fields = append(tr.Fields, Field{ID: "issuelinks", Name: "Linked issues", Kind: "array", Required: true})
	m, _ := awaitingMoves(loaded(&recorder{})).Update(TransitionsMsg{Key: "DEMO-1", Transitions: []Transition{tr}})
	if v := stripANSI(m.View()); !strings.Contains(v, "Blocked · in Jira ↗") {
		t.Errorf("a required field the board cannot fill should leave the move to Jira:\n%s", v)
	}
}

func TestMoveFormFitsANarrowPane(t *testing.T) {
	tr := blockedTransition()
	tr.Fields[0].Options[0].Value = "Pending delivery from another product of the company"
	m := tea.Model(New(snapshot(), false, &recorder{}, ViewList))
	m, _ = m.Update(tea.WindowSizeMsg{Width: 56, Height: 30})
	m, _ = awaitingMoves(m).Update(TransitionsMsg{Key: "DEMO-1", Transitions: []Transition{tr}})
	m = press(selectMenuItem(t, m, "Blocked"), "enter")
	m = press(selectMenuItem(t, m, "Reason"), "enter")
	m, msg := run(selectMenuItem(t, m, "Pending"), "enter")
	m, _ = m.Update(msg)
	for _, l := range strings.Split(m.View(), "\n") {
		if w := lipgloss.Width(l); w > 56 {
			t.Errorf("line %d cells wide in a 56-column pane: %q", w, stripANSI(l))
		}
	}
}

// typeKeys types text into the board one key at a time, delivering what each key's command answers.
func typeKeys(m tea.Model, text string) tea.Model {
	for _, r := range text {
		var cmd tea.Cmd
		m, cmd = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
		if cmd != nil {
			if msg := cmd(); msg != nil {
				m, _ = m.Update(msg)
			}
		}
	}
	return m
}

func TestMoveFormPicksAUserAndWritesAComment(t *testing.T) {
	r := &recorder{transitions: []Transition{blockedTransition()}, users: map[string][]User{"an": {{ID: "acc-7", Name: "Ana López"}, {ID: "acc-8", Name: "Andrés Gil"}}}}
	m, _ := awaitingMoves(loaded(r)).Update(TransitionsMsg{Key: "DEMO-1", Transitions: r.transitions})
	m = press(selectMenuItem(t, m, "Blocked"), "enter")
	if v := stripANSI(m.View()); !strings.Contains(v, "Owner: —") || !strings.Contains(v, "Comment: —") {
		t.Fatalf("the form should offer the user field and a comment:\n%s", v)
	}

	m = typeKeys(press(selectMenuItem(t, m, "Owner"), "enter"), "an")
	if !slices.Contains(r.calls, "users an") {
		t.Fatalf("typing should search the assignable users: %v", r.calls)
	}
	m, _ = m.Update(UsersListedMsg{Key: "DEMO-1", Query: "a", Users: []User{{ID: "old", Name: "Stale Result"}}})
	if v := stripANSI(m.View()); !strings.Contains(v, "Ana López") || strings.Contains(v, "Stale Result") {
		t.Fatalf("the current search's users, not an older one's, should be listed:\n%s", v)
	}
	m = press(send(m, tea.KeyMsg{Type: tea.KeyDown}), "enter")
	if v := stripANSI(m.View()); !strings.Contains(v, "Owner: Andrés Gil") {
		t.Fatalf("the picked user should show in the form:\n%s", v)
	}

	m = typeKeys(press(selectMenuItem(t, m, "Comment"), "enter"), "Waiting for keys")
	m = press(m, "enter")
	if v := stripANSI(m.View()); !strings.Contains(v, "Comment: Waiting for keys") {
		t.Fatalf("the comment should show in the form:\n%s", v)
	}
	m = press(typeKeys(press(selectMenuItem(t, m, "Comment"), "enter"), " now"), "esc")
	if v := stripANSI(m.View()); !strings.Contains(v, "Comment: Waiting for keys") || strings.Contains(v, "keys now") {
		t.Fatalf("esc should leave the comment as it was:\n%s", v)
	}

	m = press(selectMenuItem(t, m, "Reason"), "enter")
	m, msg := run(selectMenuItem(t, m, "No access"), "enter")
	m, _ = m.Update(msg)
	run(selectMenuItem(t, m, "now"), "enter")
	want := []Value{{Field: "cf_reason", Kind: KindOption, ID: "9002"}, {Field: "cf_owner", Kind: KindUser, ID: "acc-8"}}
	if r.last() != "transition DEMO-1 51" || !reflect.DeepEqual(r.values, want) || r.comment != "Waiting for keys" {
		t.Errorf("moved with %+v and comment %q (%v)", r.values, r.comment, r.calls)
	}
}

func TestUserSearchScrollsToTheSelection(t *testing.T) {
	var many []User
	for i := 1; i <= 12; i++ {
		many = append(many, User{ID: strconv.Itoa(i), Name: fmt.Sprintf("Person %02d", i)})
	}
	r := &recorder{users: map[string][]User{"pe": many}}
	m, _ := awaitingMoves(loaded(r)).Update(TransitionsMsg{Key: "DEMO-1", Transitions: []Transition{blockedTransition()}})
	m = typeKeys(press(selectMenuItem(t, press(selectMenuItem(t, m, "Blocked"), "enter"), "Owner"), "enter"), "pe")
	for i := 0; i < 11; i++ {
		m = send(m, tea.KeyMsg{Type: tea.KeyDown})
	}
	v := stripANSI(m.View())
	if !strings.Contains(v, "▸ Person 12") || strings.Contains(v, "Person 01") || !strings.Contains(v, "↑ 4 more") {
		t.Errorf("the list should scroll to the selected user and say what is hidden above:\n%s", v)
	}
	if v := stripANSI(send(m, tea.KeyMsg{Type: tea.KeyDown}).View()); !strings.Contains(v, "▸ Person 12") {
		t.Errorf("down on the last user should stay on it:\n%s", v)
	}
}
