package board

import (
	"cmp"
	"slices"
	"strings"

	"github.com/pavp/tkt/internal/config"
	"github.com/pavp/tkt/internal/ticket"
)

// Status is one of a space's statuses with its Jira category ("new", "indeterminate" or "done").
type Status struct {
	Name     string
	Category string
}

// StatusPlace is the column and mark a status gets, and whether each is the automatic one.
func StatusPlace(sp config.Space, st Status) (col ticket.Category, colAuto bool, mark ticket.Mark, markAuto bool) {
	overrides, marks := sp.Categories(), sp.Marks()
	col = ticket.Column(st.Name, st.Category, overrides)
	_, moved := overrides[st.Name]
	_, marked := marks[st.Name]
	return col, !moved || col == ticket.Column(st.Name, st.Category, nil), ticket.MarkFor(st.Name, st.Category, overrides, marks), !marked
}

// SetStatusColumn puts a status in col; its automatic column is not saved.
func SetStatusColumn(sp *config.Space, st Status, col ticket.Category) {
	if col == ticket.Column(st.Name, st.Category, nil) {
		delete(sp.StatusOverrides, st.Name)
		return
	}
	if sp.StatusOverrides == nil {
		sp.StatusOverrides = map[string]string{}
	}
	sp.StatusOverrides[st.Name] = string(col)
}

// SetStatusMark gives a status mark; its automatic mark is not saved.
func SetStatusMark(sp *config.Space, st Status, mark ticket.Mark) {
	if mark == ticket.MarkFor(st.Name, st.Category, sp.Categories(), nil) {
		delete(sp.StatusMarks, st.Name)
		return
	}
	if sp.StatusMarks == nil {
		sp.StatusMarks = map[string]string{}
	}
	sp.StatusMarks[st.Name] = cmp.Or(string(mark), "none")
}

// ResetStatus brings a status back to its automatic column and mark.
func ResetStatus(sp *config.Space, name string) {
	delete(sp.StatusOverrides, name)
	delete(sp.StatusMarks, name)
}

// InferredRepos are the repositories inferred from a space's tickets that Settings offers to pin or ignore: those
// neither configured nor ignored.
func InferredRepos(sp config.Space, inferred []string) []string {
	var out []string
	for _, name := range inferred {
		if !sp.Ignores(name) && !slices.ContainsFunc(sp.Repos, func(r config.Repo) bool { return strings.EqualFold(r.Name, name) }) {
			out = append(out, name)
		}
	}
	return out
}
