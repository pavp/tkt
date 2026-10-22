package tui

import (
	"cmp"
	"slices"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/pavp/tkt/internal/config"
	"github.com/pavp/tkt/internal/ticket"
)

// JiraStatus is one of a space's statuses with its Jira category ("new", "indeterminate" or "done").
type JiraStatus struct {
	Name     string
	Category string
}

// JiraStatusesListedMsg carries a space's statuses, listed by JiraStatuses.
type JiraStatusesListedMsg struct {
	Space    string
	Statuses []JiraStatus
	Err      error
}

// columnWidth fits the longest column cell, "In Progress · auto".
const columnWidth = 18

// loadStatuses asks once for the statuses of every space, when the columns section shows.
func (m Model) loadStatuses() tea.Cmd {
	s := m.settings
	if s.section != sectionColumns {
		return nil
	}
	var cmds []tea.Cmd
	for _, sp := range s.cfg.Spaces {
		if !s.asked[sp.Key] {
			s.asked[sp.Key] = true
			cmds = append(cmds, m.actions.JiraStatuses(sp.Key))
		}
	}
	if len(cmds) == 1 {
		return cmds[0]
	}
	return tea.Batch(cmds...)
}

func (m Model) statusesListed(msg JiraStatusesListedMsg) (tea.Model, tea.Cmd) {
	s := m.settings
	if s == nil {
		return m, nil
	}
	if msg.Err != nil {
		s.statusErrs[msg.Space] = msg.Err.Error()
	} else {
		s.statuses[msg.Space] = msg.Statuses
	}
	if r, ok := s.current(); !ok || !r.selectable() {
		s.row = s.firstSelectable()
	}
	return m, nil
}

func (s *settingsView) columnRows() []settingsRow {
	var out []settingsRow
	for si, sp := range s.cfg.Spaces {
		out = append(out, settingsRow{kind: rowSpace, space: si})
		statuses, ok := s.statuses[sp.Key]
		switch {
		case s.statusErrs[sp.Key] != "":
			out = append(out, settingsRow{kind: rowStatusInfo, space: si, name: "cannot list the statuses of " + sp.Key + ": " + s.statusErrs[sp.Key]})
		case !ok:
			out = append(out, settingsRow{kind: rowStatusInfo, space: si, name: "loading the statuses of " + sp.Key + "…"})
		case len(statuses) == 0:
			out = append(out, settingsRow{kind: rowStatusInfo, space: si, name: sp.Key + " has no statuses"})
		}
		for i, st := range statuses {
			out = append(out, settingsRow{kind: rowStatus, space: si, idx: i, name: st.Name})
		}
	}
	return out
}

func (s *settingsView) status(r settingsRow) JiraStatus {
	return s.statuses[s.cfg.Spaces[r.space].Key][r.idx]
}

// statusPlace is the column and mark a status gets, and whether each is the automatic one.
func statusPlace(sp config.Space, st JiraStatus) (col ticket.Category, colAuto bool, mark ticket.Mark, markAuto bool) {
	overrides, marks := sp.Categories(), sp.Marks()
	col = ticket.Column(st.Name, st.Category, overrides)
	_, moved := overrides[st.Name]
	_, marked := marks[st.Name]
	return col, !moved || col == ticket.Column(st.Name, st.Category, nil), ticket.MarkFor(st.Name, st.Category, overrides, marks), !marked
}

// cycleColumn moves a status to the next column; back in its automatic one, it is no longer saved.
func (m Model) cycleColumn(r settingsRow) (tea.Model, tea.Cmd) {
	st := m.settings.status(r)
	col, _, _, _ := statusPlace(m.settings.cfg.Spaces[r.space], st)
	next := ticket.Columns[(slices.Index(ticket.Columns, col)+1)%len(ticket.Columns)]
	return m.saveSettings(func(c *config.Config) {
		sp := &c.Spaces[r.space]
		if next == ticket.Column(st.Name, st.Category, nil) {
			delete(sp.StatusOverrides, st.Name)
			return
		}
		if sp.StatusOverrides == nil {
			sp.StatusOverrides = map[string]string{}
		}
		sp.StatusOverrides[st.Name] = string(next)
	})
}

// cycleMark gives a status the next icon, none after the last; the automatic one is no longer saved.
func (m Model) cycleMark(r settingsRow) (tea.Model, tea.Cmd) {
	sp, st := m.settings.cfg.Spaces[r.space], m.settings.status(r)
	_, _, mark, _ := statusPlace(sp, st)
	next := ticket.Marks[(slices.Index(ticket.Marks, mark)+1)%len(ticket.Marks)]
	auto := ticket.MarkFor(st.Name, st.Category, sp.Categories(), nil)
	return m.saveSettings(func(c *config.Config) {
		sp := &c.Spaces[r.space]
		if next == auto {
			delete(sp.StatusMarks, st.Name)
			return
		}
		if sp.StatusMarks == nil {
			sp.StatusMarks = map[string]string{}
		}
		sp.StatusMarks[st.Name] = cmp.Or(string(next), "none")
	})
}

// resetStatus brings a status back to its automatic column and mark.
func (m Model) resetStatus(r settingsRow) (tea.Model, tea.Cmd) {
	name := m.settings.status(r).Name
	return m.saveSettings(func(c *config.Config) {
		delete(c.Spaces[r.space].StatusOverrides, name)
		delete(c.Spaces[r.space].StatusMarks, name)
	})
}

func (m Model) statusLine(r settingsRow, sel string) string {
	st := m.settings.status(r)
	col, colAuto, mark, markAuto := statusPlace(m.settings.cfg.Spaces[r.space], st)
	colText := col.Title()
	if colAuto {
		colText += " · auto"
	}
	markText := "—"
	if ic, ok := markIcons[mark]; ok {
		markText = lipgloss.NewStyle().Foreground(ic.color).Render(ic.glyph)
	}
	if markAuto {
		markText += dim.Render(" · auto")
	}
	w := m.statusWidth()
	return sel + pad(truncate(st.Name, w), w) + " " + pad(colText, columnWidth) + " " + markText
}

func (m Model) statusHeader() string {
	return "  " + dim.Render(pad("STATUS", m.statusWidth())+" "+pad("COLUMN", columnWidth)+" MARK")
}

// statusWidth fits the longest status name, leaving room for the column and mark cells.
func (m Model) statusWidth() int {
	w := len("STATUS")
	for _, statuses := range m.settings.statuses {
		for _, st := range statuses {
			w = max(w, lipgloss.Width(st.Name))
		}
	}
	return max(6, min(w, m.width-2-1-columnWidth-1-8))
}
