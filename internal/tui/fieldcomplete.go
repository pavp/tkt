package tui

import (
	"fmt"
	"slices"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/pavp/tkt/internal/config"
)

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

// JiraBoardsListedMsg carries a space's scrum boards, listed by JiraBoards.
type JiraBoardsListedMsg struct {
	Space  string
	Boards []config.TeamBoard
	Err    error
}

// teamBoardForm adds a team board to a space; it asks for the space's boards at once, so tab and enter
// can match against them.
func (m Model) teamBoardForm(space int) (tea.Model, tea.Cmd) {
	key := m.settings.cfg.Spaces[space].Key
	m.settings.form = &settingsForm{title: "Add a team board to " + key, labels: []string{"Board (its active sprints join the Team mode)"},
		values: []string{""}, teamBoard: true, space: space, hint: "loading the boards of " + key + "…"}
	return m, m.actions.JiraBoards(key)
}

// completeTeamBoard runs Tab in the team board field: it cycles the dropdown, or opens it on the names
// starting with what was typed once the boards are in.
func (m Model) completeTeamBoard() (tea.Model, tea.Cmd) {
	f := m.settings.form
	if len(f.cands) > 0 {
		f.cand = (f.cand + 1) % len(f.cands)
		f.values[0] = f.cands[f.cand]
		return m, nil
	}
	if !f.boardsIn {
		f.pending = f.values[0]
		return m, nil
	}
	f.completeTeamBoardNames()
	return m, nil
}

func (f *settingsForm) completeTeamBoardNames() {
	v := f.values[0]
	names := make([]string, len(f.boards))
	for i, b := range f.boards {
		names[i] = b.Name
	}
	var matches []string
	for _, n := range names {
		if hasPrefixFold(n, v) {
			matches = append(matches, n)
		}
	}
	switch len(matches) {
	case 0:
		f.hint = "no board starts with " + v
	case 1:
		f.values[0], f.hint = matches[0], ""
	default:
		f.open(names, matches, "")
	}
}

// teamBoardsListed keeps the space's boards in the form, finishing a completion asked for meanwhile.
func (f *settingsForm) teamBoardsListed(msg JiraBoardsListedMsg, key string) {
	if !f.teamBoard || msg.Space != key {
		return
	}
	if msg.Err != nil {
		f.hint = "cannot list the boards of " + key + ": " + msg.Err.Error()
		return
	}
	f.boards, f.boardsIn, f.hint = msg.Boards, true, fmt.Sprintf("%d scrum boards · tab completes", len(msg.Boards))
	if f.pending != "" && f.pending == f.values[0] {
		f.pending = ""
		f.completeTeamBoardNames()
	}
}

// pickTeamBoard resolves the typed name to one of the space's boards, or says why it cannot.
func (f *settingsForm) pickTeamBoard(sp config.Space) (config.TeamBoard, string) {
	if !f.boardsIn {
		return config.TeamBoard{}, "the boards of " + sp.Key + " are still loading"
	}
	b, n := config.MatchBoard(f.boards, f.values[0])
	switch {
	case n == 0:
		return b, fmt.Sprintf("no scrum board of %s is called %q", sp.Key, f.values[0])
	case n > 1:
		return b, fmt.Sprintf("%q fits several boards of %s: type more of its name", f.values[0], sp.Key)
	case slices.ContainsFunc(sp.TeamBoards, func(t config.TeamBoard) bool { return t.ID == b.ID }):
		return b, fmt.Sprintf("%s is already a team board of %s", b.Name, sp.Key)
	}
	return b, ""
}
