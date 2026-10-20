package tui

import (
	"fmt"
	"slices"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/pavp/tkt/internal/config"
)

const (
	sectionRepos = iota
	sectionRoots
)

var sectionNames = []string{"Spaces & repositories", "Local clones"}

type rowKind int

const (
	rowSpace rowKind = iota
	rowRepo
	rowInferred
	rowAddRepo
	rowRoot
	rowAddRoot
)

type settingsRow struct {
	kind  rowKind
	space int
	idx   int
	name  string
}

func (r settingsRow) selectable() bool { return r.kind != rowSpace }

// settingsView edits the repositories of each space and the folders holding local clones. Every
// confirmed change is saved at once; the view keeps showing the configuration as last written.
type settingsView struct {
	cfg      config.Config
	inferred map[string][]string
	section  int
	row      int
	form     *settingsForm
	confirm  *settingsConfirm
	saving   bool
	msg      string
	err      string
}

type settingsForm struct {
	title  string
	labels []string
	values []string
	focus  int
	apply  func(c *config.Config, values []string)
	// repo forms hold repository, base branch and path; folder forms only the folder.
	repo  bool
	found []string
	hint  string
	// cands are the paths a repeated Tab cycles through; pending is the path whose folders are being listed.
	cands   []string
	cand    int
	pending string
	browser *dirBrowser
}

const (
	fieldRepo = 0
	fieldPath = 1
)

// pathField is the field ctrl+o fills: the local path of a repository, or the folder itself.
func (f *settingsForm) pathField() int {
	if f.repo {
		return fieldPath
	}
	return 0
}

type settingsConfirm struct {
	question string
	// verb names what y does in the prompt's footer; empty means "remove".
	verb  string
	apply func(c *config.Config)
}

func (m Model) openSettings() (tea.Model, tea.Cmd) {
	s := &settingsView{cfg: m.actions.Config(), inferred: m.snap.Repos}
	s.row = s.firstSelectable()
	m.settings = s
	return m, nil
}

func (s *settingsView) rows() []settingsRow {
	var out []settingsRow
	if s.section == sectionRoots {
		for i, r := range s.cfg.RepoRoots {
			out = append(out, settingsRow{kind: rowRoot, idx: i, name: r})
		}
		return append(out, settingsRow{kind: rowAddRoot})
	}
	for si, sp := range s.cfg.Spaces {
		out = append(out, settingsRow{kind: rowSpace, space: si})
		for ri, r := range sp.Repos {
			out = append(out, settingsRow{kind: rowRepo, space: si, idx: ri, name: r.Name})
		}
		for _, name := range s.inferred[sp.Key] {
			if !sp.Ignores(name) && !slices.ContainsFunc(sp.Repos, func(r config.Repo) bool { return strings.EqualFold(r.Name, name) }) {
				out = append(out, settingsRow{kind: rowInferred, space: si, name: name})
			}
		}
		out = append(out, settingsRow{kind: rowAddRepo, space: si})
	}
	return out
}

func (s *settingsView) firstSelectable() int {
	for i, r := range s.rows() {
		if r.selectable() {
			return i
		}
	}
	return 0
}

func (s *settingsView) current() (settingsRow, bool) {
	rows := s.rows()
	if s.row < 0 || s.row >= len(rows) {
		return settingsRow{}, false
	}
	return rows[s.row], true
}

func (s *settingsView) move(d int) {
	rows := s.rows()
	for i := s.row + d; i >= 0 && i < len(rows); i += d {
		if rows[i].selectable() {
			s.row = i
			return
		}
	}
}

func (m Model) settingsKey(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	s := m.settings
	if k.String() == "ctrl+c" {
		return m, tea.Quit
	}
	if s.form != nil {
		return m.settingsFormKey(k)
	}
	if s.confirm != nil {
		c := s.confirm
		s.confirm = nil
		if strings.EqualFold(k.String(), "y") {
			return m.saveSettings(func(cfg *config.Config) { c.apply(cfg) })
		}
		return m, nil
	}
	s.msg, s.err = "", ""
	switch k.String() {
	case "esc", "q", "backspace", ",":
		m.settings = nil
	case "up", "w", "k":
		s.move(-1)
	case "down", "s", "j":
		s.move(1)
	case "tab", "left", "right", "h", "l":
		s.section = 1 - s.section
		s.row = s.firstSelectable()
	case "enter", " ", "e":
		return m.settingsAction("enter")
	case "a":
		return m.settingsAction("a")
	case "d", "x", "delete":
		return m.settingsAction("d")
	}
	return m, nil
}

// settingsAction runs enter (edit, pin or add), a (add) or d (remove) on the selected row.
func (m Model) settingsAction(action string) (tea.Model, tea.Cmd) {
	s := m.settings
	r, ok := s.current()
	if !ok {
		return m, nil
	}
	switch {
	case action == "d" && r.kind == rowRepo:
		sp := s.cfg.Spaces[r.space]
		s.confirm = &settingsConfirm{
			question: fmt.Sprintf("Remove %s from %s? (y/n)", r.name, sp.Key),
			apply: func(c *config.Config) {
				c.Spaces[r.space].Repos = slices.Delete(c.Spaces[r.space].Repos, r.idx, r.idx+1)
			},
		}
	case action == "d" && r.kind == rowRoot:
		s.confirm = &settingsConfirm{
			question: fmt.Sprintf("Remove the folder %s? (y/n)", r.name),
			apply:    func(c *config.Config) { c.RepoRoots = slices.Delete(c.RepoRoots, r.idx, r.idx+1) },
		}
	case action == "d" && r.kind == rowInferred:
		s.confirm = &settingsConfirm{
			question: fmt.Sprintf("Ignore %s in %s? tkt stops using it for CI and reviews (y/n)", r.name, s.cfg.Spaces[r.space].Key),
			verb:     "ignore",
			apply: func(c *config.Config) {
				c.Spaces[r.space].Ignored = append(c.Spaces[r.space].Ignored, r.name)
			},
		}
	case action == "d":
	case r.kind == rowRepo && action == "enter":
		repo := s.cfg.Spaces[r.space].Repos[r.idx]
		s.form = repoForm("Edit "+repo.Name, repo, func(c *config.Config, repo config.Repo) { c.Spaces[r.space].Repos[r.idx] = repo })
	case r.kind == rowInferred && action == "enter":
		s.form = repoForm("Pin "+r.name+" to "+s.cfg.Spaces[r.space].Key, config.Repo{Name: r.name}, func(c *config.Config, repo config.Repo) {
			c.Spaces[r.space].Repos = append(c.Spaces[r.space].Repos, repo)
		})
	case r.kind == rowRepo || r.kind == rowInferred || r.kind == rowAddRepo:
		s.form = repoForm("Add a repository to "+s.cfg.Spaces[r.space].Key, config.Repo{}, func(c *config.Config, repo config.Repo) {
			c.Spaces[r.space].Repos = append(c.Spaces[r.space].Repos, repo)
		})
	case r.kind == rowRoot && action == "enter":
		s.form = &settingsForm{title: "Edit folder", labels: []string{"Folder"}, values: []string{r.name}, apply: func(c *config.Config, v []string) {
			c.RepoRoots[r.idx] = v[0]
		}}
	default:
		s.form = &settingsForm{title: "Add a folder with local clones", labels: []string{"Folder"}, values: []string{""}, apply: func(c *config.Config, v []string) {
			c.RepoRoots = append(c.RepoRoots, v[0])
		}}
	}
	return m, m.lookupClones()
}

// lookupClones asks for clones of the form's repository when its path is still empty.
func (m Model) lookupClones() tea.Cmd {
	f := m.settings.form
	if f == nil || !f.repo || f.values[fieldPath] != "" || strings.Count(f.values[fieldRepo], "/") != 1 {
		return nil
	}
	return m.actions.FindClones(strings.TrimSpace(f.values[fieldRepo]))
}

// settingsLookup takes the answers of FindClones, RepoAt and PickFolder into the open form, so each
// half of a repository fills the other: its name finds its clone, a folder names its repository.
func (m Model) settingsLookup(msg tea.Msg) (tea.Model, tea.Cmd) {
	if m.settings == nil || m.settings.form == nil {
		return m, nil
	}
	f := m.settings.form
	switch msg := msg.(type) {
	case ClonesFoundMsg:
		if !f.repo || !strings.EqualFold(strings.TrimSpace(f.values[fieldRepo]), msg.Repo) {
			return m, nil
		}
		f.found = msg.Paths
		switch len(msg.Paths) {
		case 0:
			f.hint = "no clone found in your clone folders · ctrl+o to browse"
		case 1:
			f.hint = "clone found in your clone folders"
		default:
			f.hint = fmt.Sprintf("%d clones found · ctrl+n next", len(msg.Paths))
		}
		if f.values[fieldPath] == "" && len(msg.Paths) > 0 {
			f.values[fieldPath] = msg.Paths[0]
		}
	case RepoFoundMsg:
		if f.repo && f.values[fieldPath] == msg.Path && f.values[fieldRepo] == "" && msg.Repo != "" {
			f.values[fieldRepo] = msg.Repo
			f.hint = "repository read from the folder's origin"
		}
	case DirsListedMsg:
		if f.browser == nil || !f.browser.listed(msg) {
			f.completeFrom(msg)
		}
	case ReposListedMsg:
		if f.completeRepoFrom(msg) && f.values[fieldPath] == "" {
			return m, m.lookupClones()
		}
	case FolderPickedMsg:
		if msg.Err != nil {
			m.settings.err = msg.Err.Error()
			return m, nil
		}
		if msg.Path == "" {
			return m, nil
		}
		f.values[f.pathField()] = msg.Path
		if f.repo && f.values[fieldRepo] == "" {
			return m, m.actions.RepoAt(msg.Path)
		}
	}
	return m, nil
}

func repoForm(title string, r config.Repo, set func(c *config.Config, r config.Repo)) *settingsForm {
	return &settingsForm{
		repo:   true,
		title:  title,
		labels: []string{"Repository (owner/name)", "Local path (optional)"},
		values: []string{r.Name, r.Path},
		apply: func(c *config.Config, v []string) {
			set(c, config.Repo{Name: v[fieldRepo], Path: v[fieldPath], Base: r.Base})
		},
	}
}

func (m Model) settingsFormKey(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	f := m.settings.form
	if f.browser != nil {
		return m.browserKey(k)
	}
	switch k.Type {
	case tea.KeyEsc:
		m.settings.form = nil
	case tea.KeyEnter:
		for i := range f.values {
			f.values[i] = strings.TrimSpace(f.values[i])
		}
		if f.values[0] == "" {
			m.settings.err = f.labels[0] + " is required"
			return m, nil
		}
		m.settings.form = nil
		return m.saveSettings(func(c *config.Config) { f.apply(c, f.values) })
	case tea.KeyTab, tea.KeyDown, tea.KeyShiftTab, tea.KeyUp:
		if k.Type == tea.KeyTab && f.focus == f.pathField() {
			return m.completePath()
		}
		if k.Type == tea.KeyTab && f.repo && f.focus == fieldRepo {
			return m.completeRepo()
		}
		left := f.focus
		if k.Type == tea.KeyTab || k.Type == tea.KeyDown {
			f.focus = (f.focus + 1) % len(f.values)
		} else {
			f.focus = (f.focus + len(f.values) - 1) % len(f.values)
		}
		switch {
		case f.repo && left == fieldRepo:
			return m, m.lookupClones()
		case f.repo && left == fieldPath && f.values[fieldPath] != "" && strings.TrimSpace(f.values[fieldRepo]) == "":
			return m, m.actions.RepoAt(strings.TrimSpace(f.values[fieldPath]))
		}
	case tea.KeyCtrlO:
		if !m.actions.NativePicker() {
			return m.openBrowser()
		}
		start := f.values[f.pathField()]
		if start == "" && len(m.settings.cfg.RepoRoots) > 0 {
			start = m.settings.cfg.RepoRoots[0]
		}
		if start == "" {
			start = "~"
		}
		return m, m.actions.PickFolder(start)
	case tea.KeyCtrlN:
		f.resetCompletion()
		if f.repo && len(f.found) > 1 {
			i := (slices.Index(f.found, f.values[fieldPath]) + 1) % len(f.found)
			f.values[fieldPath] = f.found[i]
		}
	case tea.KeyBackspace:
		f.resetCompletion()
		if v := []rune(f.values[f.focus]); len(v) > 0 {
			f.values[f.focus] = string(v[:len(v)-1])
		}
	case tea.KeyCtrlU:
		f.resetCompletion()
		f.values[f.focus] = ""
	case tea.KeyRunes, tea.KeySpace:
		f.resetCompletion()
		f.values[f.focus] += string(k.Runes)
	}
	return m, nil
}

// saveSettings applies change to a copy of the configuration and saves it; the view only adopts the
// copy once the save succeeds, so a rejected change leaves the shown settings as they were on disk.
func (m Model) saveSettings(change func(c *config.Config)) (tea.Model, tea.Cmd) {
	c := m.settings.cfg.Clone()
	change(&c)
	m.settings.saving = true
	m.settings.err = ""
	return m, m.actions.SaveConfig(c)
}

func (m Model) settingsSaved(msg SettingsSavedMsg) (tea.Model, tea.Cmd) {
	s := m.settings
	if s == nil {
		return m, nil
	}
	s.saving = false
	if msg.Err != nil {
		s.err = "not saved: " + msg.Err.Error()
		return m, nil
	}
	s.cfg = msg.Config
	if rows := s.rows(); s.row >= len(rows) || !rows[s.row].selectable() {
		s.row = s.firstSelectable()
	}
	s.msg = "✓ saved to your config.toml · the board refreshes with it"
	m.loading = true
	return m, m.actions.Refresh()
}

// settingsLines renders the view and maps each line to its row index (-1 for others), for clicks.
func (m Model) settingsLines() ([]string, []int) {
	s := m.settings
	tabs := make([]string, len(sectionNames))
	for i, n := range sectionNames {
		tabs[i] = dim.Render(" " + n + " ")
		if i == s.section {
			tabs[i] = selected.Render(" " + n + " ")
		}
	}
	lines := []string{bold.Render("Settings") + dim.Render(" · changes are saved to your config.toml"), strings.Join(tabs, " "), ""}
	at := []int{-1, -1, -1}
	repoW, pathW := m.settingsColumns()
	rootW := m.rootsWidth()
	for i, r := range s.rows() {
		mark := "  "
		if i == s.row {
			mark = keyStyle.Render("▸ ")
		}
		var line string
		switch r.kind {
		case rowSpace:
			sp := s.cfg.Spaces[r.space]
			if i > 0 {
				lines, at = append(lines, ""), append(at, -1)
			}
			name := bold.Render(sp.Key)
			if sp.Name != "" {
				name += dim.Render(" · " + sp.Name)
			}
			lines, at = append(lines, name), append(at, -1)
			line = "  " + dim.Render(pad("REPOSITORY", repoW)+" "+pad("LOCAL PATH", pathW))
			lines, at = append(lines, line), append(at, -1)
			continue
		case rowRepo:
			repo := s.cfg.Spaces[r.space].Repos[r.idx]
			line = mark + pad(truncate(repo.Name, repoW), repoW) + " " + pad(truncate(orDash(repo.Path), pathW), pathW) + " " + removeMark
		case rowInferred:
			line = mark + dim.Render(pad(truncate(r.name, repoW), repoW)+" "+pad("—", pathW)) + " " + removeMark + dim.Render(" inferred from your tickets' pull requests · enter to pin")
		case rowAddRepo:
			line = mark + keyDim.Render("+ Add repository")
		case rowRoot:
			line = mark + pad(truncate(r.name, rootW), rootW) + " " + removeMark
		case rowAddRoot:
			line = mark + keyDim.Render("+ Add folder")
		}
		if s.section == sectionRoots && i == 0 {
			lines, at = append(lines, dim.Render("Folders where tkt looks for your local clones")), append(at, -1)
		}
		lines, at = append(lines, truncate(line, m.width)), append(at, i)
	}
	return lines, at
}

var removeMark = lipgloss.NewStyle().Foreground(lipgloss.Color("167")).Render("✕")

// settingsColumns sizes the repository and path columns to their longest value, within limits, so the
// ✕ that removes a row sits right after them, at removeX.
func (m Model) settingsColumns() (repoW, pathW int) {
	repoW, pathW = len("REPOSITORY"), len("LOCAL PATH")
	for _, sp := range m.settings.cfg.Spaces {
		for _, r := range sp.Repos {
			repoW = max(repoW, lipgloss.Width(r.Name))
			pathW = max(pathW, lipgloss.Width(r.Path))
		}
		for _, name := range m.settings.inferred[sp.Key] {
			repoW = max(repoW, lipgloss.Width(name))
		}
	}
	repoW = min(repoW, max(24, m.width*2/5))
	pathW = min(pathW, max(16, m.width-repoW-8))
	return repoW, pathW
}

// rootsWidth is the width of the folder column in the local clones section.
func (m Model) rootsWidth() int {
	w := 10
	for _, r := range m.settings.cfg.RepoRoots {
		w = max(w, lipgloss.Width(r))
	}
	return min(w, m.width-6)
}

func (m Model) removeX() int {
	if m.settings.section == sectionRoots {
		return 2 + m.rootsWidth() + 1
	}
	repoW, pathW := m.settingsColumns()
	return 2 + repoW + 1 + pathW + 1
}

func (m Model) settingsHeight() int {
	return m.height - lineCount(m.header()) - 2 - lineCount(m.settingsFooter()) - 1
}

func (m Model) settingsBody() string {
	if s := m.settings; s.form != nil || s.confirm != nil {
		box := m.settingsModal()
		return lipgloss.Place(m.width, max(m.settingsHeight(), lipgloss.Height(box)), lipgloss.Center, lipgloss.Center, box)
	}
	lines, at := m.settingsLines()
	h := m.settingsHeight()
	if h > 0 && len(lines) > h {
		sel := slices.Index(at, m.settings.row)
		start := min(max(0, sel-h/2), len(lines)-h)
		lines = lines[start : start+h]
	}
	for len(lines) < h {
		lines = append(lines, "")
	}
	return strings.Join(lines, "\n")
}

func (m Model) settingsModal() string {
	s := m.settings
	if s.confirm != nil {
		verb := s.confirm.verb
		if verb == "" {
			verb = "remove"
		}
		return menuBox.Render(s.confirm.question + "\n\n" + dim.Render("y "+verb+" · any other key keeps it"))
	}
	f := s.form
	if f.browser != nil {
		return f.browser.view()
	}
	lines := []string{bold.Render(f.title), ""}
	for i, l := range f.labels {
		v := f.values[i]
		if i == f.focus {
			lines = append(lines, keyStyle.Render("▸ "+l), "  "+selected.Render(" "+v+"▏"))
		} else {
			lines = append(lines, "  "+dim.Render(l), "   "+v)
		}
		if i == f.focus && f.hint != "" {
			lines = append(lines, "   "+keyDim.Render(f.hint))
		}
	}
	keys := "enter save · tab complete · ↑↓ fields · ctrl+o browse folders · esc cancel"
	lines = append(lines, "", dim.Render(keys))
	return menuBox.Render(strings.Join(lines, "\n"))
}

func (m Model) settingsFooter() string {
	s := m.settings
	status := ""
	switch {
	case s.saving:
		status = dim.Render("saving…")
	case s.err != "":
		status = warn.Render("! " + s.err)
	case s.msg != "":
		status = keyDim.Render(s.msg)
	}
	legend := [][2]string{{"↑↓ ws", "move"}, {"tab", "section"}, {"enter", "edit · pin"}, {"a", "add"}, {"d", "remove"}, {"esc ,", "back to the board"}}
	items := make([]string, len(legend))
	for i, k := range legend {
		items[i] = keyStyle.Render(k[0]) + " " + dim.Render(k[1])
	}
	return truncate(status, m.width) + "\n" + wrapSep(items, m.width, "    ")
}

func (m Model) settingsMouse(msg tea.MouseMsg) (tea.Model, tea.Cmd) {
	s := m.settings
	switch msg.Button {
	case tea.MouseButtonWheelUp:
		s.move(-1)
		return m, nil
	case tea.MouseButtonWheelDown:
		s.move(1)
		return m, nil
	case tea.MouseButtonLeft:
	default:
		return m, nil
	}
	if msg.Action != tea.MouseActionPress || s.form != nil || s.confirm != nil {
		return m, nil
	}
	if m.onBack(msg.X, msg.Y) {
		m.settings = nil
		return m, nil
	}
	lines, at := m.settingsLines()
	h := m.settingsHeight()
	start := 0
	if h > 0 && len(lines) > h {
		sel := slices.Index(at, s.row)
		start = min(max(0, sel-h/2), len(lines)-h)
	}
	i := start + msg.Y - lineCount(m.header()) - 2
	if i >= 0 && i < len(at) && at[i] >= 0 {
		s.row = at[i]
		if r, ok := s.current(); ok && (r.kind == rowRepo || r.kind == rowInferred || r.kind == rowRoot) && msg.X >= m.removeX() && msg.X < m.removeX()+2 {
			return m.settingsAction("d")
		}
	}
	return m, nil
}
