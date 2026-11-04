package board

import (
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/pavp/tkt/internal/ticket"
)

// Card is what a ticket's card says besides the ticket itself, as the TUI words it, for clients that draw cards.
type Card struct {
	Attention []string `json:"attention,omitempty"`
	Points    string   `json:"points,omitempty"`
	Who       string   `json:"who"`
	Progress  string   `json:"progress,omitempty"`
	// TypeKind is the Jira type as bug, epic, subtask, story, task or other, for its icon.
	TypeKind string `json:"type_kind,omitempty"`
	// Details and Hint are the preview's second line for the ticket when it is selected.
	Details []string `json:"details,omitempty"`
	Hint    string   `json:"hint,omitempty"`
	// Finished words the merged and closed pull requests: "#12 merged" for one, else "7 merged · 1 closed".
	Finished string `json:"finished,omitempty"`
}

// CardOf is t's card; hasAgent says an agent works on it in the Herdr the card is drawn for.
func CardOf(t ticket.Ticket, hasAgent bool) Card {
	c := Card{Points: PointsText(t.Points), Who: ShortName(t.Assignee), TypeKind: TypeKind(t.Type), Details: PreviewParts(t), Hint: ActionHint(t, hasAgent)}
	for _, r := range t.Attention() {
		c.Attention = append(c.Attention, string(r))
	}
	if len(t.Subtasks) > 0 {
		c.Progress = SubtaskProgress(t)
	}
	c.Finished = finishedPRs(t.PRs)
	return c
}

func finishedPRs(prs []ticket.PullRequest) string {
	_, merged, closed := ticket.SplitPRs(prs)
	if merged+closed != 1 {
		return ticket.FinishedSummary(merged, closed)
	}
	for _, pr := range prs {
		switch pr.State {
		case ticket.PRMerged:
			return fmt.Sprintf("#%d merged", pr.Number)
		case ticket.PRClosed:
			return fmt.Sprintf("#%d closed", pr.Number)
		}
	}
	return ""
}

func PointsText(p float64) string {
	if p == 0 {
		return ""
	}
	return strconv.FormatFloat(p, 'f', -1, 64) + "pt"
}

// SubtaskProgress words a parent's subtasks, "1/3 subtasks · 1 to do": how many are done and how many have
// not started though their parent has.
func SubtaskProgress(t ticket.Ticket) string {
	done, todo := 0, 0
	for _, st := range t.Subtasks {
		switch {
		case st.Category == ticket.Done:
			done++
		case st.Category == ticket.ToDo && t.Category != ticket.ToDo:
			todo++
		}
	}
	out := fmt.Sprintf("%d/%d subtasks", done, len(t.Subtasks))
	if todo > 0 {
		out += fmt.Sprintf(" · %d to do", todo)
	}
	return out
}

// ShortName is "Ada L." for "Ada Lovelace", the first name alone for one word, "unassigned" for none.
func ShortName(name string) string {
	parts := strings.Fields(name)
	switch len(parts) {
	case 0:
		return "unassigned"
	case 1:
		return parts[0]
	}
	last := []rune(parts[len(parts)-1])
	return parts[0] + " " + string(last[0]) + "."
}

// typeKinds are matched in order against the lowercased Jira type name, in English or Spanish, so
// "Subtarea" is a subtask before it is a task.
var typeKinds = []struct {
	words []string
	kind  string
}{
	{[]string{"bug", "error", "defect"}, "bug"},
	{[]string{"epic", "épica"}, "epic"},
	{[]string{"sub"}, "subtask"},
	{[]string{"story", "historia"}, "story"},
	{[]string{"task", "tarea"}, "task"},
}

// TypeKind sorts a Jira type into the kinds the board draws an icon for; empty without a type.
func TypeKind(typ string) string {
	if typ == "" {
		return ""
	}
	l := strings.ToLower(typ)
	for _, k := range typeKinds {
		if slices.ContainsFunc(k.words, func(w string) bool { return strings.Contains(l, w) }) {
			return k.kind
		}
	}
	return "other"
}
