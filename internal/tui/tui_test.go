package tui

import (
	"errors"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/pavp/tkt/internal/board"
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
	for _, want := range []string{"To Do 1", "In Progress 1", "In Review 1", "Done 0", "— nothing here", "! CI failing", "#812 ✕", "Review requested", "#820 · Add pagination", "h/l j/k move"} {
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
	_, msg := run(m, "enter")
	if r.last() != "start DEMO-2" || msg != StatusMsg("start DEMO-2") {
		t.Errorf("enter = %v / %v", r.calls, msg)
	}
}

func TestToggleViewKeepsSelection(t *testing.T) {
	r := &recorder{}
	m := press(loaded(r), "l", "v")
	view := m.View()
	if !strings.Contains(view, "[list]") || !strings.Contains(view, "▸ ") || !strings.Contains(view, "↑↓ move") {
		t.Fatalf("not in list view:\n%s", view)
	}
	if line := selectedLine(view); !strings.Contains(line, "DEMO-1") {
		t.Errorf("selection lost after toggling: %q", line)
	}
	m = press(m, "v")
	if !strings.Contains(m.View(), "[columns]") || !strings.Contains(m.View(), "▸ DEMO-1") {
		t.Error("toggling back lost the columns or the selection")
	}
}

func selectedLine(view string) string {
	for _, l := range strings.Split(view, "\n") {
		if strings.HasPrefix(l, "▸ ") {
			return l
		}
	}
	return ""
}

func TestNarrowTerminalUsesList(t *testing.T) {
	r := &recorder{}
	m := loaded(r)
	m, _ = m.Update(tea.WindowSizeMsg{Width: 80, Height: 30})
	if !strings.Contains(m.View(), "[list]") {
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
	if strings.Contains(view, "DEMO-1") || !strings.Contains(view, "#820") || !strings.Contains(view, "[list]") {
		t.Errorf("reviews filter:\n%s", view)
	}
	_, _ = run(m, "enter")
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
		{"i", "pane DEMO-1"},
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
