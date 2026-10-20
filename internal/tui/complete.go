package tui

import (
	"fmt"
	"math"
	"slices"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// DirsListedMsg carries the folders inside Path, as listed by ListDirs. Dir is that folder as tkt writes
// it ("~/src/"), Abs its absolute form, both ending in "/"; Abs is set even when the listing failed.
// Broken holds the symlinks there whose target is gone.
type DirsListedMsg struct {
	Path   string
	Dir    string
	Abs    string
	Names  []string
	Broken []string
	Err    error
}

// dropRows is how many matches the dropdown shows at once.
const dropRows = 8

// completePath runs Tab in a path field, like a shell: an empty field offers the clones and clone folders
// tkt knows, a typed path completes from the folders beside it, and a repeated Tab cycles the matches.
func (m Model) completePath() (tea.Model, tea.Cmd) {
	f := m.settings.form
	pf := f.pathField()
	if len(f.cands) > 0 {
		f.cand = (f.cand + 1) % len(f.cands)
		f.values[pf] = f.cands[f.cand]
		return m, nil
	}
	v := f.values[pf]
	switch {
	case v == "":
		known := slices.Clone(f.found)
		for _, r := range m.settings.cfg.RepoRoots {
			if !slices.Contains(known, r) {
				known = append(known, r)
			}
		}
		if len(known) == 0 {
			f.hint = "type a path starting with / or ~/"
			return m, nil
		}
		f.open(known, known, "")
		f.cand = 0
		f.values[pf] = known[0]
		return m, nil
	case v == "~":
		f.values[pf] = "~/"
		return m, nil
	case !strings.Contains(v, "/"):
		f.hint = "type a path starting with / or ~/"
		return m, nil
	}
	f.pending = v
	return m, m.actions.ListDirs(v[:strings.LastIndex(v, "/")+1])
}

// completeFrom finishes a completion once the folders beside the typed path arrive; a listing for a path
// the user has since edited is dropped.
func (f *settingsForm) completeFrom(msg DirsListedMsg) {
	pf := f.pathField()
	v := f.values[pf]
	i := strings.LastIndex(v, "/")
	if f.pending == "" || v != f.pending || v[:i+1] != msg.Path {
		return
	}
	f.pending = ""
	if msg.Err != nil {
		f.hint = "cannot read " + msg.Path + ": " + msg.Err.Error()
		return
	}
	parent, base := v[:i+1], v[i+1:]
	var matches []string
	for _, n := range msg.Names {
		if hasPrefixFold(n, base) && (strings.HasPrefix(base, ".") || !strings.HasPrefix(n, ".")) {
			matches = append(matches, n)
		}
	}
	switch len(matches) {
	case 0:
		f.hint = "no folder starts with " + base
		if base == "" {
			f.hint = "no folders in " + parent
		}
	case 1:
		f.values[pf] = parent + matches[0] + "/"
		f.hint = ""
	default:
		if p := commonPrefixFold(matches); len([]rune(p)) > len([]rune(base)) {
			f.values[pf] = parent + p
		}
		var source, cands []string
		for _, n := range msg.Names {
			if strings.HasPrefix(base, ".") || !strings.HasPrefix(n, ".") {
				source = append(source, parent+n+"/")
			}
		}
		for _, n := range matches {
			cands = append(cands, parent+n+"/")
		}
		f.open(source, cands, parent)
	}
}

// resetCompletion closes the dropdown and forgets a pending listing.
func (f *settingsForm) resetCompletion() {
	f.cands, f.cand, f.source, f.dropBase, f.pending = nil, 0, nil, "", ""
}

// open shows cands in the dropdown; source holds every value typing may narrow back down to.
func (f *settingsForm) open(source, cands []string, base string) {
	f.source, f.cands, f.cand, f.dropBase, f.hint = source, cands, -1, base, ""
}

// refilter narrows the dropdown to what the edited value still matches, closing it when nothing does.
func (f *settingsForm) refilter() {
	f.pending = ""
	if f.source == nil {
		f.resetCompletion()
		return
	}
	v := f.values[f.focus]
	f.cands, f.cand = nil, -1
	for _, c := range f.source {
		if hasPrefixFold(c, v) {
			f.cands = append(f.cands, c)
		}
	}
	if len(f.cands) == 0 {
		f.resetCompletion()
	}
}

// highlight moves the dropdown's selection by d, wrapping, and shows it in the field.
func (f *settingsForm) highlight(d int) {
	n := len(f.cands)
	if f.cand < 0 && d < 0 {
		f.cand = 0
	}
	f.cand = (f.cand + d + n) % n
	f.values[f.focus] = f.cands[f.cand]
}

// pick puts match i in the field and closes the dropdown; a picked repository looks for its clone.
func (m Model) pick(i int) (tea.Model, tea.Cmd) {
	f := m.settings.form
	f.values[f.focus] = f.cands[i]
	repo := f.repo && f.focus == fieldRepo
	f.resetCompletion()
	if repo && f.values[fieldPath] == "" {
		return m, m.lookupClones()
	}
	return m, nil
}

// dropWindow is the slice of matches the dropdown shows, scrolled to keep the selection visible.
func (f *settingsForm) dropWindow() (int, int) {
	off := 0
	if f.cand >= dropRows {
		off = f.cand - dropRows + 1
	}
	return off, min(len(f.cands), off+dropRows)
}

// dropdownLines renders the matches under the focused field, without the prefix they all share.
func (f *settingsForm) dropdownLines() []string {
	off, end := f.dropWindow()
	var lines []string
	if off > 0 {
		lines = append(lines, "   "+dim.Render(fmt.Sprintf("↑ %d more", off)))
	}
	for i := off; i < end; i++ {
		label := strings.TrimPrefix(f.cands[i], f.dropBase)
		if i == f.cand {
			lines = append(lines, "   "+selected.Render("▸ "+label))
		} else {
			lines = append(lines, "     "+label)
		}
	}
	if more := len(f.cands) - end; more > 0 {
		lines = append(lines, "   "+dim.Render(fmt.Sprintf("↓ %d more", more)))
	}
	return append(lines, "   "+keyDim.Render(fmt.Sprintf("%d matches · ↑↓ choose · enter pick · esc close", len(f.cands))))
}

// dropdownRowAt maps a click on screen row y to the match it shows, or -1.
func (m Model) dropdownRowAt(y int) int {
	f := m.settings.form
	_, drop := f.lines()
	box := m.settingsModal()
	gap := max(m.settingsHeight(), lipgloss.Height(box)) - lipgloss.Height(box)
	// The body starts below the header and its two lines; the box's top border precedes its content.
	top := lineCount(m.header()) + 2 + gap - int(math.Round(float64(gap)/2)) + 1
	off, end := f.dropWindow()
	if i := off + y - top - drop; drop >= 0 && i >= off && i < end {
		return i
	}
	return -1
}
func hasPrefixFold(s, prefix string) bool {
	return strings.HasPrefix(strings.ToLower(s), strings.ToLower(prefix))
}

// commonPrefixFold returns the longest prefix all names share ignoring case, spelled as the first name.
func commonPrefixFold(names []string) string {
	first := []rune(names[0])
	n := len(first)
	for _, name := range names[1:] {
		r := []rune(strings.ToLower(name))
		k := 0
		for k < n && k < len(r) && strings.ToLower(string(first[k])) == string(r[k]) {
			k++
		}
		n = k
	}
	return string(first[:n])
}
