package board

import (
	"slices"
	"strings"

	"github.com/pavp/tkt/internal/ticket"
)

// ViewState is what the board shows of a snapshot, as the user last left it.
type ViewState struct {
	// HiddenBoards lists the team boards whose tickets the board hides.
	HiddenBoards []string `json:"hidden_boards"`
	// Team shows the team's tickets beside the user's.
	Team bool `json:"team"`
}

// Row is one line of the board's list or one card of a column: a ticket, or a subtask under its parent.
type Row struct {
	Key    ticket.Key     `json:"key"`
	Parent ticket.Key     `json:"parent,omitempty"`
	Ticket *ticket.Ticket `json:"ticket,omitempty"`
	// Card is what the ticket's card says, set where the daemon serves rows to clients that draw cards.
	Card *Card `json:"card,omitempty"`
	// Mine marks one of the user's pull requests listed with its ticket, which starts no section.
	Mine bool `json:"-"`
}

// ShownTickets are the user's tickets the board filter leaves; team tickets show only while the team is on,
// after the user's own. A ticket on no team board always shows.
func ShownTickets(tickets []ticket.Ticket, hidden map[string]bool, team bool) []ticket.Ticket {
	var mine, theirs []ticket.Ticket
	for _, t := range tickets {
		switch {
		case t.Board != "" && hidden[t.Board]:
		case t.Team && team:
			theirs = append(theirs, t)
		case !t.Team:
			mine = append(mine, t)
		}
	}
	return append(mine, theirs...)
}

// BoardCounts counts, per team board, the tickets the board can show, the team's only while it is on; the
// board filter offers them.
func BoardCounts(tickets []ticket.Ticket, team bool) map[string]int {
	boards := map[string]int{}
	for _, t := range tickets {
		if t.Board != "" && (!t.Team || team) {
			boards[t.Board]++
		}
	}
	return boards
}

// Columns places each ticket in its column and each subtask in its own, done ones included as Jira does:
// right after its parent when they share a column, else after that column's tickets.
func Columns(ts []ticket.Ticket) [][]Row {
	cols := make([][]Row, len(ticket.Columns))
	for ci, c := range ticket.Columns {
		var elsewhere []Row
		for i := range ts {
			t := ts[i]
			if t.Category == c {
				cols[ci] = append(cols[ci], Row{Key: t.Key, Ticket: &t})
			}
			for j := range t.Subtasks {
				st := t.Subtasks[j]
				if st.Category != c {
					continue
				}
				r := Row{Key: st.Key, Ticket: &st, Parent: t.Key}
				if t.Category == c {
					cols[ci] = append(cols[ci], r)
				} else {
					elsewhere = append(elsewhere, r)
				}
			}
		}
		cols[ci] = append(cols[ci], elsewhere...)
	}
	return cols
}

// Points adds up the story points of a column's cards.
func Points(rows []Row) float64 {
	points := 0.0
	for _, r := range rows {
		if r.Ticket != nil {
			points += r.Ticket.Points
		}
	}
	return roundPoints(points)
}

// ListRows orders the list: the user's tickets sorted, then the team's sorted and grouped by board, each
// ticket followed by its subtasks.
func ListRows(ts []ticket.Ticket) []Row {
	var out []Row
	team := slices.IndexFunc(ts, func(t ticket.Ticket) bool { return t.Team })
	if team < 0 {
		team = len(ts)
	}
	theirs := Sorted(ts[team:])
	slices.SortStableFunc(theirs, func(a, b ticket.Ticket) int { return strings.Compare(a.Board, b.Board) })
	for _, t := range append(Sorted(ts[:team]), theirs...) {
		t := t
		out = append(out, Row{Key: t.Key, Ticket: &t})
		for j := range t.Subtasks {
			st := t.Subtasks[j]
			out = append(out, Row{Key: st.Key, Ticket: &st, Parent: t.Key})
		}
	}
	return out
}

// SectionAt names the section the list's row i opens, such as "My tickets" or "Team · Web", with how many
// tickets it holds; ok is false when row i opens none.
func SectionAt(rows []Row, i int) (title string, n int, ok bool) {
	r := rows[i]
	switch {
	case r.Ticket != nil && r.Ticket.Team && !r.Mine && (i == 0 || rows[i-1].Ticket == nil || !rows[i-1].Ticket.Team || rows[i-1].Ticket.Board != r.Ticket.Board):
		for _, o := range rows[i:] {
			if o.Ticket != nil && o.Ticket.Team && o.Ticket.Board == r.Ticket.Board {
				n++
			}
		}
		title = "Team"
		if r.Ticket.Board != "" {
			title += " · " + r.Ticket.Board
		}
		return title, n, true
	case i == 0 && r.Ticket != nil:
		for _, o := range rows {
			if o.Ticket != nil && !o.Ticket.Team && !o.Mine && o.Parent == "" {
				n++
			}
		}
		return "My tickets", n, true
	}
	return "", 0, false
}
