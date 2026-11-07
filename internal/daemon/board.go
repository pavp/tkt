package daemon

import (
	"fmt"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/pavp/tkt/internal/board"
	"github.com/pavp/tkt/internal/ticket"
)

// BoardView is the Sprint screen as the board lays it out: its columns and its list's sections.
type BoardView struct {
	FetchedAt time.Time `json:"fetched_at"`
	Partial   bool      `json:"partial,omitempty"`
	Syncing   bool      `json:"syncing,omitempty"`
	SyncError string    `json:"sync_error,omitempty"`
	// SyncLabel says how fresh the board is, as the TUI's status line does.
	SyncLabel string `json:"sync_label"`
	board.ViewState
	// Sprints are the open sprints as the TUI's context line words them: "DEMO · Sprint 12 · 3d left".
	Sprints []string `json:"sprints"`
	// Reviews are the review requests the strip above the Sprint shows, each with the PRs tab's row for its menu.
	Reviews    []ReviewChip      `json:"reviews"`
	Boards     []BoardFilter     `json:"boards"`
	Columns    []BoardColumn     `json:"columns"`
	Sections   []BoardSection    `json:"sections"`
	Agents     map[string]string `json:"agents,omitempty"`
	LabelField string            `json:"label_field,omitempty"`
	Warnings   []string          `json:"warnings,omitempty"`
}

// ReviewChip is one review request of the Sprint's strip: "#51 · DEMO-142" (the branch's ticket key, when it has one).
type ReviewChip struct {
	Label string `json:"label"`
	Row   PRRow  `json:"row"`
}

// BoardFilter is one team board the Boards filter offers, with the tickets it holds.
type BoardFilter struct {
	Name    string `json:"name"`
	Tickets int    `json:"tickets"`
	Hidden  bool   `json:"hidden"`
}

type BoardColumn struct {
	Category ticket.Category `json:"category"`
	Title    string          `json:"title"`
	Points   float64         `json:"points"`
	Rows     []board.Row     `json:"rows"`
}

type BoardSection struct {
	Title string      `json:"title"`
	Count int         `json:"count"`
	Rows  []board.Row `json:"rows"`
}

func (s *Server) board(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	snap, have, syncing, syncErr := s.snap, s.have, s.syncing, s.syncErr
	s.mu.Unlock()
	if !have {
		writeError(w, http.StatusServiceUnavailable, "not_synced", "the first sync has not finished")
		return
	}
	var state board.ViewState
	if s.opts.State != nil {
		state = s.opts.State()
	}
	q := r.URL.Query()
	if !queryBool(w, q, "team", &state.Team) {
		return
	}
	queryList(q, "hidden_boards", &state.HiddenBoards)
	agents := snap.Agents
	if h := q.Get("herdr"); h != "" {
		if h != "desktop" && h != "classic" {
			writeError(w, http.StatusBadRequest, "bad_request", "herdr must be desktop or classic")
			return
		}
		agents = orNoAgents(snap.AgentsBy[h])
	}
	hidden := setOf(state.HiddenBoards)
	view := BoardView{FetchedAt: snap.FetchedAt, Partial: snap.Partial, Syncing: syncing, SyncError: syncErr, ViewState: state,
		Sprints: []string{}, Reviews: []ReviewChip{}, Boards: []BoardFilter{}, Columns: []BoardColumn{}, Sections: []BoardSection{}, Agents: agents, LabelField: snap.LabelField, Warnings: snap.Warnings}
	view.HiddenBoards = orNone(view.HiddenBoards)
	for _, pr := range board.Reviews(snap, board.PRFilter{}) {
		label := fmt.Sprintf("#%d", pr.Number)
		if keys := ticket.BranchKeys(pr.Branch); len(keys) > 0 {
			label += " · " + string(keys[0])
		}
		row := board.PRRow{PR: &pr}
		view.Reviews = append(view.Reviews, ReviewChip{Label: label, Row: PRRow{Key: fmt.Sprintf("#%d", pr.Number), PR: &pr, Review: true, Status: row.Status(),
			Approvals: pr.ApprovalsText(), NeedsYou: board.NeedsYou(pr), Attention: reasons(pr.Attention())}})
	}
	for _, sp := range snap.Sprints {
		view.Sprints = append(view.Sprints, sp.Space+" · "+board.SprintLabel(sp, snap.FetchedAt))
	}
	snap = board.ForTeam(snap, state.Team)
	for name, n := range board.BoardCounts(snap.Tickets, state.Team) {
		view.Boards = append(view.Boards, BoardFilter{Name: name, Tickets: n, Hidden: hidden[name]})
	}
	slices.SortFunc(view.Boards, func(a, b BoardFilter) int { return strings.Compare(a.Name, b.Name) })
	shown := board.ShownTickets(snap.Tickets, hidden, state.Team)
	for ci, rows := range board.Columns(shown) {
		c := ticket.Columns[ci]
		view.Columns = append(view.Columns, BoardColumn{Category: c, Title: c.Title(), Points: board.Points(rows), Rows: orEmpty(rows)})
	}
	rows := board.ListRows(shown)
	for i := range rows {
		if title, n, ok := board.SectionAt(rows, i); ok {
			view.Sections = append(view.Sections, BoardSection{Title: title, Count: n})
		}
		if last := len(view.Sections) - 1; last >= 0 {
			view.Sections[last].Rows = append(view.Sections[last].Rows, rows[i])
		}
	}
	for i := range view.Columns {
		withCards(view.Columns[i].Rows, agents)
	}
	for i := range view.Sections {
		withCards(view.Sections[i].Rows, agents)
	}
	view.SyncLabel = board.SyncLabel(board.SyncState{Loading: syncing, Partial: snap.Partial, Failed: syncErr != "", FetchedAt: snap.FetchedAt}, s.now())
	writeJSON(w, http.StatusOK, view)
}

// withCards adds each row's card; the rows are the view's own copies, so the snapshot keeps none.
func withCards(rows []board.Row, agents map[string]string) {
	for i := range rows {
		if rows[i].Ticket != nil {
			c := board.CardOf(*rows[i].Ticket, agents[string(rows[i].Key)] != "")
			rows[i].Card = &c
		}
	}
}

func orEmpty(rows []board.Row) []board.Row {
	if rows == nil {
		return []board.Row{}
	}
	return rows
}

func orNoAgents(m map[string]string) map[string]string {
	if m == nil {
		return map[string]string{}
	}
	return m
}

func (s *Server) now() time.Time {
	if s.opts.Now != nil {
		return s.opts.Now()
	}
	return time.Now()
}
