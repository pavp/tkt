package board

import (
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/pavp/tkt/internal/ticket"
)

// BacklogGroupKey is the Backlog's sprintless group, folded until the user opens it, as it is usually the longest.
const BacklogGroupKey = "backlog:"

// BacklogGroup is one group of the Backlog: a sprint of one board, a sprint name across boards while merged, or
// the tickets in no sprint.
type BacklogGroup struct {
	Group, Title, Board, Dates string
	Sprints                    []int
	Tickets                    []ticket.Ticket
}

// BacklogGroups lists the backlog's active sprints, then its future sprints soonest first, one group per sprint
// and board, or per sprint name while merged, then its tickets in no sprint. Hidden boards' sprints and tickets
// are left out.
func BacklogGroups(bl Backlog, hidden map[string]bool, merged bool) []BacklogGroup {
	var out []BacklogGroup
	add := func(g BacklogGroup) {
		g.Tickets = slices.DeleteFunc(slices.Clone(g.Tickets), func(t ticket.Ticket) bool { return t.Board != "" && hidden[t.Board] })
		out = append(out, g)
	}
	type sprintGroup struct {
		key, title, board string
		start, end        time.Time
		active            bool
		ids               []int
		tickets           []ticket.Ticket
	}
	// Sprints come active first, then soonest first, so a group's first sprint places it.
	var groups []*sprintGroup
	byKey := map[string]*sprintGroup{}
	for _, sp := range bl.Sprints {
		if hidden[sp.BoardName] {
			continue
		}
		key, boardName := fmt.Sprintf("sprint:%d", sp.ID), sp.BoardName
		if merged {
			key, boardName = "sprint:"+sp.Name, ""
		}
		g, ok := byKey[key]
		if !ok {
			g = &sprintGroup{key: key, title: sp.Name, board: boardName}
			byKey[key] = g
			groups = append(groups, g)
		}
		if !sp.Start.IsZero() && (g.start.IsZero() || sp.Start.Before(g.start)) {
			g.start = sp.Start
		}
		if sp.End.After(g.end) {
			g.end = sp.End
		}
		g.active = g.active || sp.State == "active"
		g.ids = append(g.ids, sp.ID)
		g.tickets = append(g.tickets, sp.Tickets...)
	}
	for _, g := range groups {
		var dates []string
		switch {
		case !g.start.IsZero() && !g.end.IsZero():
			dates = append(dates, g.start.Format("Jan 2")+" – "+g.end.Format("Jan 2"))
		case !g.start.IsZero():
			dates = append(dates, "from "+g.start.Format("Jan 2"))
		}
		if g.active {
			dates = append(dates, "active")
		}
		add(BacklogGroup{Group: g.key, Title: g.title, Board: g.board, Dates: strings.Join(dates, " · "), Sprints: g.ids, Tickets: g.tickets})
	}
	add(BacklogGroup{Group: BacklogGroupKey, Title: "Backlog", Tickets: bl.Backlog})
	return out
}

// Tickets are every ticket of the backlog, sprints first.
func (b Backlog) Tickets() []ticket.Ticket {
	var out []ticket.Ticket
	for _, sp := range b.Sprints {
		out = append(out, sp.Tickets...)
	}
	return append(out, b.Backlog...)
}

// BoardCounts counts the backlog's tickets per team board, which the Boards filter offers in the Backlog.
func (b Backlog) BoardCounts() map[string]int {
	boards := map[string]int{}
	for _, t := range b.Tickets() {
		boards[t.Board]++
	}
	return boards
}
