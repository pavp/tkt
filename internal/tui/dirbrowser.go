package tui

import (
	"path"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

const browserRows = 12

// dirBrowser lets the user walk folders inside the form when no native folder picker can open, as over
// SSH or under WSL. Enter chooses the folder being shown, so an empty folder can be chosen too.
type dirBrowser struct {
	// req is the folder whose listing the browser waits for; dir and abs are the one shown.
	req    string
	dir    string
	abs    string
	names  []string
	cursor int
	offset int
	err    string
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
	b.req, b.dir, b.abs, b.cursor, b.offset, b.err = "", msg.Dir, msg.Abs, 0, 0, ""
	b.names = nil
	for _, n := range msg.Names {
		if !strings.HasPrefix(n, ".") {
			b.names = append(b.names, n)
		}
	}
	if msg.Err != nil {
		b.err = msg.Err.Error()
	}
	return true
}

func (m Model) browserKey(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	f := m.settings.form
	b := f.browser
	switch k.String() {
	case "esc":
		f.browser = nil
	case "enter":
		if b.req != "" {
			return m, nil
		}
		f.browser = nil
		chosen := b.dir
		if chosen != "/" {
			chosen = strings.TrimSuffix(chosen, "/")
		}
		f.values[f.pathField()] = chosen
		f.resetCompletion()
		if f.repo && strings.TrimSpace(f.values[fieldRepo]) == "" {
			return m, m.actions.RepoAt(chosen)
		}
	case "up", "k":
		b.move(-1)
	case "down", "j":
		b.move(1)
	case "right", "l":
		if b.cursor < len(b.names) {
			return m, b.list(m.actions, b.dir+b.names[b.cursor]+"/")
		}
	case "left", "h", "backspace":
		if b.abs != "" && b.abs != "/" {
			return m, b.list(m.actions, strings.TrimSuffix(path.Dir(strings.TrimSuffix(b.abs, "/")), "/")+"/")
		}
	}
	return m, nil
}

func (b *dirBrowser) move(d int) {
	b.cursor = max(0, min(len(b.names)-1, b.cursor+d))
	if b.cursor < b.offset {
		b.offset = b.cursor
	}
	if b.cursor >= b.offset+browserRows {
		b.offset = b.cursor - browserRows + 1
	}
}

func (b *dirBrowser) view() string {
	lines := []string{bold.Render("Choose a folder"), "", keyStyle.Render(b.dir), ""}
	switch {
	case b.err != "":
		lines = append(lines, warn.Render("! "+b.err))
	case b.req != "" && b.names == nil:
		lines = append(lines, dim.Render("loading…"))
	case len(b.names) == 0:
		lines = append(lines, dim.Render("no folders here"))
	}
	if b.offset > 0 {
		lines = append(lines, dim.Render("  ↑ more"))
	}
	end := min(len(b.names), b.offset+browserRows)
	for i := b.offset; i < end; i++ {
		if i == b.cursor {
			lines = append(lines, selected.Render("▸ "+b.names[i]+"/"))
		} else {
			lines = append(lines, "  "+b.names[i]+"/")
		}
	}
	if end < len(b.names) {
		lines = append(lines, dim.Render("  ↓ more"))
	}
	lines = append(lines, "", dim.Render("→ open · ← up · enter choose this folder · esc cancel"))
	return menuBox.Render(strings.Join(lines, "\n"))
}
