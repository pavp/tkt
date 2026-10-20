package tui

import (
	"fmt"
	"slices"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
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

const maxShownMatches = 6

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
		f.cands, f.cand = known, 0
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
		f.cands, f.cand = nil, -1
		for _, n := range matches {
			f.cands = append(f.cands, parent+n+"/")
		}
		shown := matches[:min(len(matches), maxShownMatches)]
		f.hint = strings.Join(shown, " · ")
		if more := len(matches) - len(shown); more > 0 {
			f.hint += fmt.Sprintf(" · +%d more", more)
		}
		f.hint += " · tab cycles"
	}
}

// resetCompletion forgets matches and pending listings once the user edits the path by hand.
func (f *settingsForm) resetCompletion() {
	f.cands, f.cand, f.pending = nil, 0, ""
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
