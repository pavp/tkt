package tui

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/pavp/tkt/internal/board"
	"github.com/pavp/tkt/internal/jira"
	"github.com/pavp/tkt/internal/ticket"
)

type recorder struct{ calls []string }

func (r *recorder) cmd(s string) tea.Cmd {
	r.calls = append(r.calls, s)
	return func() tea.Msg { return StatusMsg(s) }
}
func (r *recorder) Refresh() tea.Cmd           { return r.cmd("refresh") }
func (r *recorder) Start(k ticket.Key) tea.Cmd { return r.cmd("start " + string(k)) }
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
	for _, want := range []string{"To Do 1", "In Progress 1", "In Review 1", "Done 0", "— nothing here", "! CI failing", "#812 ✕", "Review requested", "! #820", "←↑↓→ wasd move"} {
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
	_, msg := run(m, "enter")
	if r.last() != "start DEMO-2" || msg != StatusMsg("start DEMO-2") {
		t.Errorf("first menu item = %v / %v", r.calls, msg)
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
	m := press(loaded(r), "n", "v")
	view := m.View()
	if strings.Contains(view, "DEMO-3") || !strings.Contains(view, "DEMO-1") || !strings.Contains(view, "#820") {
		t.Errorf("needs-you filter:\n%s", view)
	}
	m = press(m, "r")
	view = m.View()
	if strings.Contains(view, "DEMO-1") || !strings.Contains(view, "#820") || !strings.Contains(view, "↑↓ ws move") {
		t.Errorf("reviews filter:\n%s", view)
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
	for _, want := range []string{"DEMO · DEMO S42 · 4d left", "synced 3 min ago", "1 All 4", "2 Needs you 2", " Columns  List ", "DEMO-3 · Export CSV\n To Do · enter asks for repos and opens Claude Code"} {
		if !strings.Contains(view, want) {
			t.Errorf("view missing %q:\n%s", want, view)
		}
	}
	next = press(next, "l")
	if !strings.Contains(stripANSI(next.View()), "DEMO-1 · Filter accounts\n In Progress · CI failing · #812 · f sends the failure to the agent") {
		t.Errorf("preview hint missing:\n%s", stripANSI(next.View()))
	}
}

func TestTabCyclesFiltersAndNumbersJump(t *testing.T) {
	m := loaded(&recorder{})
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyTab})
	if !strings.Contains(stripANSI(m.View()), "▸ DEMO-1") || strings.Contains(stripANSI(m.View()), "DEMO-3") {
		t.Errorf("tab should switch to Needs you:\n%s", stripANSI(m.View()))
	}
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyShiftTab})
	if !strings.Contains(stripANSI(m.View()), "DEMO-3") {
		t.Error("shift+tab should go back to All")
	}
	m = press(m, "3")
	if v := stripANSI(m.View()); strings.Contains(v, "DEMO-1") || !strings.Contains(v, "#820") {
		t.Errorf("3 should show reviews:\n%s", v)
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
	m = click(m, strings.Index(header, "List")+1, 1)
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
	m = click(m, strings.Index(header, "Needs you"), 1)
	if v := stripANSI(m.View()); strings.Contains(v, "DEMO-3") || !strings.Contains(v, "DEMO-1") {
		t.Errorf("click on Needs you did not filter:\n%s", v)
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
	if len(lines) != 15 || !strings.Contains(view, "▸ ") || !strings.Contains(lines[len(lines)-1], "close") || !strings.HasPrefix(lines[0], " tkt") {
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
	next = press(next, "3")
	line := ""
	for _, l := range strings.Split(stripANSI(next.View()), "\n") {
		if strings.Contains(l, "#820") && strings.Contains(l, "Review requested") {
			line = l
		}
	}
	if !strings.Contains(line, "Review requested   ✓") || strings.Contains(line, "example-org") {
		t.Errorf("review row should show only the CI mark, as in the mockup: %q", line)
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
	if v := stripANSI(next.View()); !strings.Contains(v, "#806 ✓ #311 ○") || !strings.Contains(v, " —  ") {
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
	m = press(m, "s", "s", "s", "s", "s")
	_, msg := run(m, "enter")
	if r.last() != "fix DEMO-1" || msg != StatusMsg("fix DEMO-1") {
		t.Errorf("fifth item ran %v", r.calls)
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
		{[]ticket.PullRequest{{Number: 806, State: ticket.PROpen, Checks: ticket.ChecksPass}, {Number: 311, State: ticket.PROpen, Checks: ticket.ChecksPending}}, "#806 ✓ #311 ○"},
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

func (r *recorder) Detail(k ticket.Key) tea.Cmd {
	r.calls = append(r.calls, "detail "+string(k))
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
	if v := stripANSI(m.View()); !strings.Contains(v, "In Progress 1") {
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
	mine, reviews := strings.Index(v, "My tickets · 3"), strings.Index(v, "Review requested · 1")
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
		if !strings.HasPrefix(stripANSI(m.View()), " tkt") {
			t.Errorf("%s header should start at the margin: %q", view, strings.SplitN(stripANSI(m.View()), "\n", 2)[0])
		}
	}
}
