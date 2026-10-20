package tui

import (
	"path"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

const browserRows = 12

// dirBrowser lets the user walk folders inside the form when no native folder picker can open, as over
// SSH or under WSL. Enter chooses the highlighted folder and space the folder shown, so an empty folder
// can be chosen too.
type dirBrowser struct {
	// req is the folder whose listing the browser waits for; dir and abs are the one shown.
	req    string
	dir    string
	abs    string
	all    []string
	broken []string
	// rows are the folders shown: all and broken, narrowed by the hidden toggle and the filter.
	rows      []browserRow
	cursor    int
	offset    int
	err       string
	hidden    bool
	filtering bool
	filter    string
}

type browserRow struct {
	name   string
	broken bool
}

func (m Model) openBrowser() (tea.Model, tea.Cmd) {
	f := m.settings.form
	start := strings.TrimSpace(f.values[f.pathField()])
	if start == "" && len(m.settings.cfg.RepoRoots) > 0 {
		start = m.settings.cfg.RepoRoots[0]
	}
	if start == "" {
		start = "~"
	}
	if !strings.HasSuffix(start, "/") {
		start += "/"
	}
	f.browser = &dirBrowser{dir: start}
	return m, f.browser.list(m.actions, start)
}

func (b *dirBrowser) list(a Actions, dir string) tea.Cmd {
	b.req = dir
	return a.ListDirs(dir)
}

// listed takes in the folders the browser waits for and reports whether msg was for it.
func (b *dirBrowser) listed(msg DirsListedMsg) bool {
	if msg.Path != b.req {
		return false
	}
	b.req, b.dir, b.abs, b.err = "", msg.Dir, msg.Abs, ""
	b.all, b.broken = msg.Names, msg.Broken
	b.filtering, b.filter = false, ""
	if msg.Err != nil {
		b.err = msg.Err.Error()
	}
	b.refresh()
	return true
}

func (b *dirBrowser) refresh() {
	b.rows = nil
	keep := func(n string) bool {
		return (b.hidden || !strings.HasPrefix(n, ".")) && strings.Contains(strings.ToLower(n), strings.ToLower(b.filter))
	}
	for _, n := range b.all {
		if keep(n) {
			b.rows = append(b.rows, browserRow{name: n})
		}
	}
	for _, n := range b.broken {
		if keep(n) {
			b.rows = append(b.rows, browserRow{name: n, broken: true})
		}
	}
	b.cursor, b.offset = 0, 0
}

func (m Model) browserKey(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	f := m.settings.form
	b := f.browser
	if b.filtering {
		switch k.Type {
		case tea.KeyEsc:
			b.filtering, b.filter = false, ""
			b.refresh()
			return m, nil
		case tea.KeyBackspace:
			if r := []rune(b.filter); len(r) > 0 {
				b.filter = string(r[:len(r)-1])
				b.refresh()
			}
			return m, nil
		case tea.KeyRight:
			return m, b.open(m.actions)
		case tea.KeyEnter:
			return m.chooseHighlighted()
		case tea.KeyRunes, tea.KeySpace:
			b.filter += string(k.Runes)
			b.refresh()
			return m, nil
		}
	}
	switch k.String() {
	case "esc":
		f.browser = nil
	case "enter":
		return m.chooseHighlighted()
	case " ":
		if b.req == "" {
			return m.choose(b.dir)
		}
	case "up", "k":
		b.move(-1)
	case "down", "j":
		b.move(1)
	case "right", "l":
		return m, b.open(m.actions)
	case "left", "h", "backspace":
		if b.abs != "" && b.abs != "/" {
			return m, b.list(m.actions, strings.TrimSuffix(path.Dir(strings.TrimSuffix(b.abs, "/")), "/")+"/")
		}
	case "/":
		b.filtering = true
	case ".":
		b.hidden = !b.hidden
		b.refresh()
	}
	return m, nil
}

// chooseHighlighted takes the folder under the cursor, or the folder shown when it has none.
func (m Model) chooseHighlighted() (tea.Model, tea.Cmd) {
	b := m.settings.form.browser
	switch {
	case b.req != "":
		return m, nil
	case len(b.rows) == 0:
		return m.choose(b.dir)
	case b.rows[b.cursor].broken:
		return m, nil
	}
	return m.choose(b.dir + b.rows[b.cursor].name)
}

// choose closes the browser with dir in the path field, naming the repository when the form needs one.
func (m Model) choose(dir string) (tea.Model, tea.Cmd) {
	f := m.settings.form
	f.browser = nil
	if dir != "/" {
		dir = strings.TrimSuffix(dir, "/")
	}
	f.values[f.pathField()] = dir
	f.resetCompletion()
	if f.repo && strings.TrimSpace(f.values[fieldRepo]) == "" {
		return m, m.actions.RepoAt(dir)
	}
	return m, nil
}

// open enters the highlighted folder; a broken link has nothing to enter.
func (b *dirBrowser) open(a Actions) tea.Cmd {
	if b.cursor >= len(b.rows) || b.rows[b.cursor].broken {
		return nil
	}
	return b.list(a, b.dir+b.rows[b.cursor].name+"/")
}

func (b *dirBrowser) move(d int) {
	b.cursor = max(0, min(len(b.rows)-1, b.cursor+d))
	if b.cursor < b.offset {
		b.offset = b.cursor
	}
	if b.cursor >= b.offset+browserRows {
		b.offset = b.cursor - browserRows + 1
	}
}

func (b *dirBrowser) view() string {
	lines := []string{bold.Render("Choose a folder"), "", keyStyle.Render(b.dir)}
	if b.filtering || b.filter != "" {
		lines = append(lines, keyDim.Render("/"+b.filter+"▏"))
	}
	lines = append(lines, "")
	switch {
	case b.err != "":
		lines = append(lines, warn.Render("! "+b.err))
	case b.req != "" && b.rows == nil:
		lines = append(lines, dim.Render("loading…"))
	case len(b.rows) == 0 && b.filter != "":
		lines = append(lines, dim.Render("no folder matches "+b.filter))
	case len(b.rows) == 0:
		lines = append(lines, dim.Render("no folders here"))
	}
	if b.offset > 0 {
		lines = append(lines, dim.Render("  ↑ more"))
	}
	end := min(len(b.rows), b.offset+browserRows)
	for i := b.offset; i < end; i++ {
		r := b.rows[i]
		label := r.name + "/"
		if r.broken {
			label = r.name + " (broken link)"
		}
		switch {
		case i == b.cursor:
			lines = append(lines, selected.Render("▸ "+label))
		case r.broken:
			lines = append(lines, dim.Render("  "+label))
		default:
			lines = append(lines, "  "+label)
		}
	}
	if end < len(b.rows) {
		lines = append(lines, dim.Render("  ↓ more"))
	}
	keys := "→ open · ← up · enter choose ▸ · space choose this folder · / filter · . show hidden · esc cancel"
	if b.hidden {
		keys = strings.Replace(keys, ". show hidden", ". hide hidden", 1)
	}
	if b.filtering {
		keys = "type to filter · enter choose ▸ · → open · esc clear filter"
	}
	lines = append(lines, "", dim.Render(keys))
	return menuBox.Render(strings.Join(lines, "\n"))
}
