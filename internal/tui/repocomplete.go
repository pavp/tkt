package tui

import (
	"slices"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

// ReposListedMsg carries owner's GitHub repositories, as "owner/name", listed by ListRepos.
type ReposListedMsg struct {
	Owner string
	Names []string
	Err   error
}

// completeRepo runs Tab in the repository field: an empty or partial owner completes from the owners
// tkt knows, and "owner/…" completes from that owner's repositories on GitHub.
func (m Model) completeRepo() (tea.Model, tea.Cmd) {
	f := m.settings.form
	if len(f.cands) > 0 {
		f.cand = (f.cand + 1) % len(f.cands)
		f.values[fieldRepo] = f.cands[f.cand]
		return m, nil
	}
	v := strings.TrimSpace(f.values[fieldRepo])
	if owner, _, ok := strings.Cut(v, "/"); ok {
		f.pending = v
		return m, m.actions.ListRepos(owner)
	}
	var owners []string
	for _, o := range m.settings.knownOwners() {
		if hasPrefixFold(o, v) {
			owners = append(owners, o+"/")
		}
	}
	switch len(owners) {
	case 0:
		f.hint = "type owner/ to list its repositories"
		if v != "" {
			f.hint = "no known owner starts with " + v + " · " + f.hint
		}
	case 1:
		f.values[fieldRepo], f.hint = owners[0], ""
	default:
		f.open(owners, owners, "")
		f.cand = 0
		f.values[fieldRepo] = owners[0]
	}
	return m, nil
}

// knownOwners lists the owners of the configured and inferred repositories, ignored ones left out.
func (s *settingsView) knownOwners() []string {
	var owners []string
	add := func(repo string) {
		if o, _, ok := strings.Cut(repo, "/"); ok && !slices.ContainsFunc(owners, func(x string) bool { return strings.EqualFold(x, o) }) {
			owners = append(owners, o)
		}
	}
	for _, sp := range s.cfg.Spaces {
		for _, r := range sp.Repos {
			add(r.Name)
		}
		for _, r := range s.inferred[sp.Key] {
			if !sp.Ignores(r) {
				add(r)
			}
		}
	}
	slices.Sort(owners)
	return owners
}

// completeRepoFrom finishes a repository completion once owner's list arrives, and reports whether it
// settled on a single repository.
func (f *settingsForm) completeRepoFrom(msg ReposListedMsg) bool {
	v := f.values[fieldRepo]
	owner, name, _ := strings.Cut(v, "/")
	if f.pending == "" || v != f.pending || !strings.EqualFold(owner, msg.Owner) {
		return false
	}
	f.pending = ""
	if msg.Err != nil {
		f.hint = "cannot list " + owner + "'s repositories: " + msg.Err.Error()
		return false
	}
	var matches []string
	for _, n := range msg.Names {
		if hasPrefixFold(n, v) {
			matches = append(matches, n)
		}
	}
	switch len(matches) {
	case 0:
		f.hint = "no repository of " + owner + " starts with " + name
		return false
	case 1:
		f.values[fieldRepo], f.hint = matches[0], ""
		return true
	}
	if p := commonPrefixFold(matches); len([]rune(p)) > len([]rune(v)) {
		f.values[fieldRepo] = p
	}
	f.open(msg.Names, matches, owner+"/")
	return false
}
