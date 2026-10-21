package tui

import (
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
func (r *recorder) Open(u string) tea.Cmd    { return r.cmd("open " + u) }
func (r *recorder) Fix(k ticket.Key) tea.Cmd { return r.cmd("fix " + string(k)) }
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
		{"f", "fix DEMO-1"},
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
	if len(titleCols) != 5 {
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
	for _, want := range []string{"DEMO · DEMO S42 · 4d left", "✓ synced 3 min ago", "1 All 4", "2 PRs 3", "│ Columns │ │ List │", "DEMO-3 · Export CSV\n To Do · unassigned · enter asks for repos and opens Claude Code"} {
		if !strings.Contains(view, want) {
			t.Errorf("view missing %q:\n%s", want, view)
		}
	}
	next = press(next, "l")
	if !strings.Contains(stripANSI(next.View()), "DEMO-1 · Filter accounts\n In Progress · unassigned · CI failing · #812 · f sends the failure to the agent") {
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
	if v := stripANSI(m.View()); !strings.Contains(v, "\n User Access Center · To Do · ") {
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
	if lineIndex(m.View(), "DEMO-3 · Export CSV") < 0 {
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
	if len(lines) != 15 || !strings.Contains(view, "▸ ") || !strings.Contains(lines[len(lines)-1], "close") || !strings.HasPrefix(lines[1], " tkt") {
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
	if !strings.Contains(line, "Review requested   ✓") || strings.Contains(line, "example-org") {
		t.Errorf("review row should show only the CI mark, as in the mockup: %q", line)
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
	for _, want := range []string{"#812 ✕", "Story · 1 subtasks"} {
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
	for _, want := range []string{"Actions for DEMO-1", "Start · open the agent", "Show details", "Open the ticket pane next to its agent", "Open in Jira ↗", "Open #812 ✕ in GitHub ↗", "Send the CI failure to the agent", "Copy the key DEMO-1", "Copy the ticket context (.ticket.md)", "enter run · esc close"} {
		if !strings.Contains(v, want) {
			t.Errorf("menu missing %q:\n%s", want, v)
		}
	}
	if strings.Contains(v, "Send the review comments") {
		t.Error("offered review comments for a ticket without requested changes")
	}
	if strings.Contains(v, "Copy the Jira link") || strings.Contains(v, "link") && strings.Contains(v, "Copy the #812") {
		t.Errorf("links are copied with c from their own row, not from extra rows:\n%s", v)
	}
	m = press(m, "s", "s", "s", "s", "s")
	_, msg := run(m, "enter")
	if r.last() != "fix DEMO-1" || msg != StatusMsg("fix DEMO-1") {
		t.Errorf("sixth item ran %v", r.calls)
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
	if !strings.Contains(stripANSI(next.View()), "Review request #821") {
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

func TestListSeparatesMyTicketsFromReviews(t *testing.T) {
	m := press(loaded(&recorder{}), "v")
	v := stripANSI(m.View())
	mine, reviews := strings.Index(v, "My tickets  3"), strings.Index(v, "Review requested · example-org/web  1")
	if mine < 0 || reviews < 0 || mine > reviews {
		t.Fatalf("sections missing or out of order:\n%s", v)
	}
	if i := strings.Index(v, "#820"); i < reviews {
		t.Errorf("review listed before its section:\n%s", v)
	}
	y := lineIndex(m.View(), "#820          Review requested")
	m = click(m, 6, y)
	if line := selectedLine(m.View()); !strings.Contains(line, "#820") {
		t.Errorf("click on the review row selected %q", line)
	}
	m = press(m, "1")
	if !strings.Contains(stripANSI(m.View()), "▸ ") {
		t.Error("lost the selection")
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
	v := stripANSI(m.View())
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
	if v := stripANSI(m.View()); strings.Contains(v, "#830") || !strings.Contains(v, "2 PRs 3") || !strings.Contains(v, "1 All 4") {
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
	snap.Tickets = append(snap.Tickets, ticket.Ticket{Key: "DEMO-7", Summary: "Wait for client", Status: "Blocked", Category: ticket.ToDo})
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
		{"room for the word", ticket.Ticket{Key: "DEMO-7", Status: "Blocked"}, 40, []string{"⊘ blocked"}, ""},
		{"too narrow", ticket.Ticket{Key: "DEMO-7", Status: "Blocked"}, 18, []string{"DEMO-7", "⊘"}, "blocked"},
		{"another flag", ticket.Ticket{Key: "DEMO-7", Status: "Blocked", PRs: failing}, 40, []string{"! CI failing", "⊘"}, "⊘ blocked"},
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
	snap.Tickets = append(snap.Tickets, ticket.Ticket{Key: "DEMO-7", Summary: "Wait for client", Status: "Blocked", Category: ticket.ToDo,
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
	if !slices.Contains(r.calls, "fix DEMO-4") || !slices.Contains(r.calls, "open https://github.com/example-org/web/pull/815") {
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
	if k, ti, ty := col(plain, "DEMO-1"), col(plain, "Filter"), col(plain, "Story"); k != ti || ti != ty {
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
	for _, want := range []string{"tkt", "│ 1 All 4 │", "│ 2 PRs 3 │", "│ Columns │", "│ List │"} {
		if !strings.Contains(first, want) {
			t.Errorf("title row lacks %q: %q", want, first)
		}
	}
	if !strings.Contains(lines[0], "╭") || !strings.Contains(lines[2], "╰") {
		t.Errorf("filters should be boxed:\n%s\n%s\n%s", lines[0], first, lines[2])
	}
	if strings.Index(first, "1 All") < strings.Index(first, "tkt") || strings.Contains(first, "synced") || strings.Contains(first, "refreshing") {
		t.Errorf("controls should sit right of the title, the sync state at the bottom: %q", first)
	}
	if !strings.HasPrefix(strings.TrimSpace(lines[3]), "──────") {
		t.Errorf("no rule under the header: %q", lines[3])
	}

	tray, _ := m.Update(tea.WindowSizeMsg{Width: 60, Height: 40})
	if tl := strings.Split(stripANSI(tray.View()), "\n"); !strings.Contains(tl[1], "tkt") || !strings.Contains(tl[1], "1 All") {
		t.Errorf("without the sync state, a 60-column pane keeps the controls on the title row:\n%s\n%s", tl[0], tl[1])
	}
	narrow, _ := m.Update(tea.WindowSizeMsg{Width: 40, Height: 40})
	nl := strings.Split(stripANSI(narrow.View()), "\n")
	if !strings.Contains(nl[0], "tkt") || !strings.Contains(nl[1], "1 All") {
		t.Errorf("a narrow pane should move the controls to the second row:\n%s\n%s", nl[0], nl[1])
	}
	for _, l := range nl[:3] {
		if lipgloss.Width(l) > 40 {
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
	for _, want := range []string{" My tickets  ", " Review requested · example-org/web  1"} {
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
	if !strings.Contains(v, "● working") || !strings.Contains(v, "! needs you") || strings.Contains(v, "claude") {
		t.Errorf("cards should show their agent in short form:\n%s", v)
	}
	if strings.Contains(v, "no agent") {
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
			for key, want := range map[string]string{"DEMO-1": "● working", "DEMO-2": "! needs you"} {
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
		short, _ := board.AgentLine(status)
		short = strings.Replace(short, "claude ", "", 1)
		if !strings.Contains(plain, "#731 ✓ #732 ✓ #733 ✓") || !strings.Contains(plain, short) {
			t.Errorf("%s: the card should keep its pull requests and show %q:\n%s", status, short, plain)
		}
		var last string
		for _, l := range strings.Split(plain, "\n") {
			if strings.Contains(l, short) {
				last = l
			}
		}
		if !strings.Contains(last, "Ana L.") {
			t.Errorf("%s: the agent belongs on the type and assignee line: %q", status, last)
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

func TestCardCutsTheTypeBeforeTheAgent(t *testing.T) {
	tk := ticket.Ticket{Key: "DEMO-1", Summary: "x", Type: "A very long issue type name", Status: "In Progress", Assignee: "Ana López"}
	plain := ansi.Strip(card(tk, 40, false, false, "blocked"))
	if !strings.Contains(plain, "! needs you") || !strings.Contains(plain, "Ana L.") || !strings.Contains(plain, "A very long") || strings.Contains(plain, "type name") {
		t.Errorf("the type should shrink first, never the assignee or the agent:\n%s", plain)
	}
	plain = ansi.Strip(card(tk, 30, false, false, "blocked"))
	if !strings.Contains(plain, "Ana L. · ! needs you") {
		t.Errorf("without room the type goes, the assignee and agent stay:\n%s", plain)
	}
	tk.Assignee = ""
	if plain := ansi.Strip(card(tk, 40, false, false, "")); !strings.Contains(plain, "unassigned") {
		t.Errorf("an unassigned ticket says so:\n%s", plain)
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
	if !strings.Contains(v, "1 All 3") {
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
	if v := stripANSI(narrow.View()); !strings.Contains(v, "╭") || !strings.Contains(v, "│ List │") {
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

func TestTeamTicketsOfferOnlyWhatIsHarmless(t *testing.T) {
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
	for _, want := range []string{"Actions for DEMO-9 · Bea Ruiz", "Show details", "Open in Jira ↗", "Copy the key DEMO-9"} {
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
