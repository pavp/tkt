package tui

import tea "github.com/charmbracelet/bubbletea"

// JiraFieldsListedMsg carries the names of the Jira site's fields, listed by JiraFields.
type JiraFieldsListedMsg struct {
	Names []string
	Err   error
}

// completeJiraField runs Tab in the ticket label field, listing the site's fields the first time.
func (m Model) completeJiraField() (tea.Model, tea.Cmd) {
	f := m.settings.form
	if len(f.cands) > 0 {
		f.cand = (f.cand + 1) % len(f.cands)
		f.values[0] = f.cands[f.cand]
		return m, nil
	}
	f.pending = f.values[0]
	return m, m.actions.JiraFields()
}

// completeJiraFieldFrom finishes a completion once the site's fields arrive.
func (f *settingsForm) completeJiraFieldFrom(msg JiraFieldsListedMsg) {
	v := f.values[0]
	if !f.jiraField || v != f.pending {
		return
	}
	f.pending = ""
	if msg.Err != nil {
		f.hint = "cannot list your Jira fields: " + msg.Err.Error()
		return
	}
	var matches []string
	for _, n := range msg.Names {
		if hasPrefixFold(n, v) {
			matches = append(matches, n)
		}
	}
	switch len(matches) {
	case 0:
		f.hint = "no Jira field starts with " + v
	case 1:
		f.values[0], f.hint = matches[0], ""
	default:
		f.open(msg.Names, matches, "")
	}
}
