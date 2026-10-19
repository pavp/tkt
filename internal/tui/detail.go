package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/pavp/tkt/internal/ticket"
)

// DetailMsg delivers a ticket rendered for reading inside the board.
type DetailMsg struct {
	Key  ticket.Key
	Text string
	Err  error
}

// detailView shows one ticket's description, criteria, PRs and comments in place of the board.
type detailView struct {
	key     ticket.Key
	lines   []string
	offset  int
	loading bool
}

func (m Model) openDetail(key ticket.Key) (tea.Model, tea.Cmd) {
	m.detail = &detailView{key: key, loading: true}
	return m, m.actions.Detail(key)
}

func (m Model) detailKey(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	d := m.detail
	page := max(1, m.detailHeight()-1)
	switch k.String() {
	case "esc", "q", "i", "backspace", "left", "a", "h":
		m.detail = nil
	case "up", "w", "k":
		d.offset = max(0, d.offset-1)
	case "down", "s", "j":
		d.offset = min(m.maxOffset(), d.offset+1)
	case "pgup", "b":
		d.offset = max(0, d.offset-page)
	case "pgdown", " ", "f":
		d.offset = min(m.maxOffset(), d.offset+page)
	case "enter":
		if it, ok := m.current(); ok && it.ticket != nil {
			m.menu = m.buildMenu(it)
		}
	case "ctrl+c":
		return m, tea.Quit
	}
	return m, nil
}

func (m Model) detailHeight() int {
	return m.height - lineCount(m.header()) - lineCount(m.footer()) - 1
}

func (m Model) maxOffset() int {
	return max(0, len(m.detail.lines)-m.detailHeight())
}

func (m Model) detailBody() string {
	d := m.detail
	if d.loading {
		return dim.Render("  loading " + string(d.key) + "…")
	}
	end := min(len(d.lines), d.offset+m.detailHeight())
	visible := make([]string, 0, end-d.offset)
	for _, l := range d.lines[d.offset:end] {
		visible = append(visible, truncate(l, m.width))
	}
	return strings.Join(visible, "\n")
}

func (m Model) detailFooter() string {
	pos := ""
	if d := m.detail; !d.loading && len(d.lines) > m.detailHeight() {
		pos = dim.Render(fmt.Sprintf("  ·  line %d of %d", d.offset+1, len(d.lines)))
	}
	legend := [][2]string{{"↑↓ ws", "scroll"}, {"space b", "page"}, {"enter", "actions"}, {"esc i", "back to the board"}}
	items := make([]string, len(legend))
	for i, k := range legend {
		items[i] = keyStyle.Render(k[0]) + " " + dim.Render(k[1])
	}
	return wrapSep(items, m.width-lipgloss.Width(pos), "    ") + pos
}
