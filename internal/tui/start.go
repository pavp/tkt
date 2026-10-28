package tui

import (
	"cmp"
	"fmt"
	"maps"
	"slices"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/pavp/tkt/internal/ticket"
)

// StartChoices is what a Start must ask before it runs: several worktrees to choose from, or the
// repositories to start in. Both empty means Start can run without asking.
type StartChoices struct {
	Worktrees []WorktreeChoice
	Repos     []RepoChoice
	// Opens says Start opens an agent rather than going to one already running, so it asks which.
	Opens bool
}

// WorktreeChoice is one worktree of the ticket; Agent is the status of the agent working in it, if any.
type WorktreeChoice struct {
	Repo, Branch, Path, Agent string
}

// RepoChoice is one repository of the ticket's space; Checked ones are offered preselected.
type RepoChoice struct {
	Name              string
	LastUsed, Checked bool
}

// StartOptions carry the answers to StartChoices into Start.
type StartOptions struct {
	Repos    []string
	Worktree string
	// Agent is the agent kind to open, empty for Start to pick the only one installed.
	Agent string
}

// StartChoicesMsg carries what a Start of Key would ask; the board asks it in a modal before Start runs.
type StartChoicesMsg struct {
	Key     ticket.Key
	Choices StartChoices
	Err     error
}

// picker is the modal that asks a Start's question: one worktree to open, or (multi) the repositories.
type picker struct {
	key             ticket.Key
	title, question string
	rows            []pickerRow
	multi           bool
	// filter marks the repository or board filter, which applies the checked rows instead of starting.
	filter string
	// start holds the answers given so far when the picker asks which agent Start opens.
	start *StartOptions
	sel   int
}

type pickerRow struct {
	label, hint, value string
	checked            bool
	// opens marks a worktree with no agent running, where Start opens one.
	opens bool
}

func worktreePicker(key ticket.Key, ws []WorktreeChoice) *picker {
	p := &picker{key: key, title: "Open " + string(key), question: "Which worktree?"}
	repos := map[string]bool{}
	for _, w := range ws {
		repos[w.Repo] = true
	}
	for _, w := range ws {
		label := w.Branch
		if len(repos) > 1 {
			_, name, _ := strings.Cut(w.Repo, "/")
			label = cmp.Or(name, w.Repo) + " · " + w.Branch
		}
		hint := "no agent"
		if w.Agent != "" {
			hint = "● " + w.Agent
		}
		p.rows = append(p.rows, pickerRow{label: label, hint: hint, value: w.Path, opens: w.Agent == ""})
	}
	return p
}

func repoPicker(key ticket.Key, rs []RepoChoice) *picker {
	p := &picker{key: key, title: "Start " + string(key), question: "Which repositories?", multi: true}
	for _, r := range rs {
		hint := ""
		if r.LastUsed {
			hint = "last used"
		}
		p.rows = append(p.rows, pickerRow{label: r.Name, hint: hint, value: r.Name, checked: r.Checked})
	}
	return p
}

// agentPicker asks which of the installed agents Start opens, the last one chosen preselected.
func agentPicker(key ticket.Key, opts StartOptions, kinds []string, last string) *picker {
	p := &picker{key: key, title: "Start " + string(key), question: "Which agent?", start: &opts, sel: max(slices.Index(kinds, last), 0)}
	for _, k := range kinds {
		hint := ""
		if k == last {
			hint = "last used"
		}
		p.rows = append(p.rows, pickerRow{label: k, hint: hint, value: k})
	}
	return p
}

// repoFilter asks which repositories the board shows, each with its open pull requests.
func (m Model) repoFilter() *picker {
	counts := m.boardRepos()
	names := slices.Sorted(maps.Keys(counts))
	p := &picker{title: "Repositories", question: "Show the pull requests of", multi: true, filter: filterRepos}
	for _, r := range names {
		p.rows = append(p.rows, pickerRow{label: r, hint: fmt.Sprintf("%d open", counts[r]), value: r, checked: !m.repoHidden(r)})
	}
	return p
}

const (
	filterRepos  = "repos"
	filterBoards = "boards"
)

// boardFilter asks which team boards' tickets the board shows, each with how many it has.
func (m Model) boardFilter() *picker {
	counts := m.boardTickets()
	p := &picker{title: "Boards", question: "Show the tickets of", multi: true, filter: filterBoards}
	for _, b := range slices.Sorted(maps.Keys(counts)) {
		hint := fmt.Sprintf("%d tickets", counts[b])
		if counts[b] == 1 {
			hint = "1 ticket"
		}
		p.rows = append(p.rows, pickerRow{label: b, hint: hint, value: b, checked: !m.hiddenBoards[b]})
	}
	return p
}

func (p *picker) action() string {
	switch {
	case p.filter != "":
		return "Apply"
	case p.multi:
		return "Start"
	}
	return "Open"
}

func (p *picker) rowLabel(r pickerRow) string {
	switch {
	case !p.multi:
		return r.label
	case r.checked:
		return "[x] " + r.label
	}
	return "[ ] " + r.label
}

// startChoices runs Start at once when there is nothing to ask, otherwise opens the picker.
func (m Model) startChoices(msg StartChoicesMsg) (tea.Model, tea.Cmd) {
	switch {
	case msg.Err != nil:
		m.status = "start " + string(msg.Key) + ": " + msg.Err.Error()
	case len(msg.Choices.Worktrees) > 1:
		m.picker = worktreePicker(msg.Key, msg.Choices.Worktrees)
	case len(msg.Choices.Repos) > 1:
		m.picker = repoPicker(msg.Key, msg.Choices.Repos)
	default:
		return m.start(msg.Key, StartOptions{}, msg.Choices.Opens)
	}
	return m, nil
}

// start runs Start, first asking which agent it opens when it opens one and several are installed.
func (m Model) start(key ticket.Key, opts StartOptions, opens bool) (tea.Model, tea.Cmd) {
	if opens && len(m.agentKinds) > 1 {
		m.picker = agentPicker(key, opts, m.agentKinds, m.lastAgent)
		return m, nil
	}
	return m, m.actions.Start(key, opts)
}

func (m Model) finishPicker() (tea.Model, tea.Cmd) {
	p := m.picker
	if !p.multi {
		m.picker = nil
		row := p.rows[p.sel]
		if p.start != nil {
			opts := *p.start
			opts.Agent, m.lastAgent = row.value, row.value
			return m, m.actions.Start(p.key, opts)
		}
		return m.start(p.key, StartOptions{Worktree: row.value}, row.opens)
	}
	var repos []string
	for _, r := range p.rows {
		if r.checked {
			repos = append(repos, r.value)
		}
	}
	if len(repos) == 0 {
		m.status = "pick at least one repository"
		if p.filter == filterBoards {
			m.status = "pick at least one board"
		}
		return m, nil
	}
	m.picker = nil
	if p.filter != "" {
		return m.applyFilter(p)
	}
	return m.start(p.key, StartOptions{Repos: repos}, true)
}

func (m Model) pickerKey(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	p := m.picker
	switch k.String() {
	case "esc", "q", "backspace":
		m.picker = nil
	case "up", "w", "k":
		p.sel = (p.sel + len(p.rows) - 1) % len(p.rows)
	case "down", "s", "j", "tab":
		p.sel = (p.sel + 1) % len(p.rows)
	case " ":
		if p.multi {
			p.rows[p.sel].checked = !p.rows[p.sel].checked
		}
	case "a":
		if p.filter != "" {
			all := !slices.ContainsFunc(p.rows, func(r pickerRow) bool { return !r.checked })
			for i := range p.rows {
				p.rows[i].checked = !all
			}
		}
	case "enter":
		return m.finishPicker()
	case "ctrl+c":
		return m, tea.Quit
	}
	return m, nil
}

// pickerLayout renders the modal within width and says where its rows and buttons sit, relative to the
// box's top-left corner, for clicks.
func (m Model) pickerLayout() (box string, firstRow, buttonsRow int, buttons []chipSpan) {
	p := m.picker
	inner := 0
	for _, r := range p.rows {
		inner = max(inner, 2+lipgloss.Width(p.rowLabel(r))+3+lipgloss.Width(r.hint))
	}
	keys := "enter " + strings.ToLower(p.action()) + " · esc cancel"
	switch {
	case p.filter != "":
		keys = "space toggle · a all · " + keys
	case p.multi:
		keys = "space toggle · " + keys
	}
	inner = max(inner, lipgloss.Width(p.title), lipgloss.Width(p.question), lipgloss.Width(keys))
	// Border and padding take 6 cells; a narrow pane (the tray panel) gets a narrower box, cutting labels.
	inner = min(inner, max(20, m.width-6))
	lines := []string{bold.Render(truncate(p.title, inner)), "", truncate(p.question, inner), ""}
	firstRow = 1 + len(lines)
	for i, r := range p.rows {
		hint := r.hint
		label := truncate(p.rowLabel(r), max(4, inner-2-3-lipgloss.Width(hint)))
		gap := max(3, inner-2-lipgloss.Width(label)-lipgloss.Width(hint))
		line := "  " + label + strings.Repeat(" ", gap) + dim.Render(hint)
		if i == p.sel {
			line = selected.Render("▸ "+label+strings.Repeat(" ", gap)) + hint
		}
		lines = append(lines, line)
	}
	lines = append(lines, "")
	buttonsRow = 1 + len(lines)
	act, cancel := selected.Render(" "+p.action()+" "), dim.Render(" Cancel ")
	// The box's left border and padding put its content 3 cells in.
	buttons = []chipSpan{{start: 3, width: lipgloss.Width(act)}, {start: 3 + lipgloss.Width(act) + 2, width: lipgloss.Width(cancel)}}
	lines = append(lines, act+"  "+cancel, "", dim.Render(truncate(keys, inner)))
	return menuBox.Render(strings.Join(lines, "\n")), firstRow, buttonsRow, buttons
}

func (m Model) pickerView() string {
	box, _, _, _ := m.pickerLayout()
	return box
}

func (m Model) pickerMouse(msg tea.MouseMsg) (tea.Model, tea.Cmd) {
	p := m.picker
	switch {
	case msg.Button == tea.MouseButtonWheelUp:
		p.sel = max(0, p.sel-1)
	case msg.Button == tea.MouseButtonWheelDown:
		p.sel = min(len(p.rows)-1, p.sel+1)
	case msg.Button == tea.MouseButtonLeft && msg.Action == tea.MouseActionPress:
		box, firstRow, buttonsRow, buttons := m.pickerLayout()
		top := lineCount(m.header())
		height := m.height - top - lineCount(m.preview()+"\n"+m.footer())
		left := (m.width - lipgloss.Width(box)) / 2
		// View centers the box in at least its own height and, in a short pane, shows only the window of
		// lines around the selected row, so clicks map through the same window.
		bodyTop := (max(height, lipgloss.Height(box)) - lipgloss.Height(box)) / 2
		if body := strings.Split(lipgloss.Place(m.width, max(height, lipgloss.Height(box)), lipgloss.Center, lipgloss.Center, box), "\n"); height > 0 && len(body) > height {
			bodyTop -= windowStart(body, height)
		}
		y := msg.Y - (top + bodyTop)
		x := msg.X - left
		if x < 0 || x >= lipgloss.Width(box) || y < 0 || y >= lipgloss.Height(box) {
			m.picker = nil
			return m, nil
		}
		if i := y - firstRow; i >= 0 && i < len(p.rows) {
			p.sel = i
			if p.multi {
				p.rows[i].checked = !p.rows[i].checked
				return m, nil
			}
			return m.finishPicker()
		}
		if y == buttonsRow {
			switch {
			case x >= buttons[0].start && x < buttons[0].start+buttons[0].width:
				return m.finishPicker()
			case x >= buttons[1].start && x < buttons[1].start+buttons[1].width:
				m.picker = nil
			}
		}
	}
	return m, nil
}

// applyFilter hides the repositories or boards left unchecked and remembers them for the board's next opening.
func (m Model) applyFilter(p *picker) (tea.Model, tea.Cmd) {
	hidden := map[string]bool{}
	for _, r := range p.rows {
		if !r.checked {
			hidden[r.value] = true
		}
	}
	if p.filter == filterBoards {
		m.hiddenBoards = hidden
	} else {
		m.hiddenRepos = hidden
	}
	m.reselect()
	return m, m.actions.SaveState(m.state())
}
