package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"

	"github.com/pavp/tkt/internal/board"
	"github.com/pavp/tkt/internal/ticket"
)

// TransitionsFunc lists where Jira lets key move, with the form fields the board fills.
type TransitionsFunc func(ctx context.Context, key ticket.Key) ([]board.Transition, error)

// TransitionFunc moves key through the transition id, writing values and comment.
type TransitionFunc func(ctx context.Context, key ticket.Key, id string, values []board.Value, comment string) error

// UsersFunc lists who key can be assigned to whose name matches query.
type UsersFunc func(ctx context.Context, key ticket.Key, query string) ([]board.User, error)

// AssignFunc gives key to the user (me), to accountID, or to nobody when neither is given.
type AssignFunc func(ctx context.Context, key ticket.Key, me bool, accountID string) error

// maxUsers bounds a user search, as Jira's own answer is.
const maxUsers = 20

type MovesView struct {
	Key ticket.Key `json:"key"`
	// URL opens the ticket in Jira, for the moves whose form only Jira fills.
	URL   string       `json:"url"`
	Moves []board.Move `json:"moves"`
}

type MoveRequest struct {
	ID string `json:"id"`
	// Values maps a form field's ID to the option or user account ID chosen for it.
	Values  map[string]string `json:"values,omitempty"`
	Comment string            `json:"comment,omitempty"`
}

// AssignRequest gives a ticket to "me", to the "user" with AccountID, or to "nobody".
type AssignRequest struct {
	To        string `json:"to"`
	AccountID string `json:"account_id,omitempty"`
}

// writeJiraError answers a Jira call that failed: its form's messages when Jira rejected the values, not found when
// it does not know the ticket.
func writeJiraError(w http.ResponseWriter, err error) {
	var rej interface {
		Rejection() (int, []string, map[string]string)
	}
	if errors.As(err, &rej) {
		status, msgs, fields := rej.Rejection()
		switch status {
		case http.StatusNotFound:
			writeError(w, http.StatusNotFound, "not_found", err.Error())
			return
		case http.StatusBadRequest, http.StatusUnprocessableEntity:
			writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"type": "error", "code": "jira_rejected", "message": err.Error(),
				"messages": orNoneOf(msgs), "fields": orNoMessages(fields)})
			return
		}
	}
	writeError(w, http.StatusBadGateway, "jira_failed", err.Error())
}

func orNoMessages(m map[string]string) map[string]string {
	if m == nil {
		return map[string]string{}
	}
	return m
}

// ticketMoves is where key can move, as the board's Move menu offers it.
func (s *Server) ticketMoves(ctx context.Context, key ticket.Key) (MovesView, error) {
	s.mu.Lock()
	var url string
	if t, ok := s.snap.Find(key); ok {
		url = t.URL
	}
	s.mu.Unlock()
	trs, err := s.opts.Transitions(ctx, key)
	if err != nil {
		return MovesView{}, err
	}
	view := MovesView{Key: key, URL: url, Moves: orNoneOf(board.Moves(trs, url))}
	for i := range view.Moves {
		view.Moves[i].Fields = orNoneOf(view.Moves[i].Fields)
	}
	return view, nil
}

func (s *Server) moves(w http.ResponseWriter, r *http.Request) {
	if s.opts.Transitions == nil {
		writeError(w, http.StatusNotFound, "not_found", "this daemon moves no tickets")
		return
	}
	key, ok := pathKey(w, r)
	if !ok {
		return
	}
	view, err := s.ticketMoves(r.Context(), key)
	if err != nil {
		writeJiraError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, view)
}

func (s *Server) move(w http.ResponseWriter, r *http.Request) {
	if s.opts.Transitions == nil || s.opts.Transition == nil {
		writeError(w, http.StatusNotFound, "not_found", "this daemon moves no tickets")
		return
	}
	key, ok := pathKey(w, r)
	if !ok {
		return
	}
	var req MoveRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&req); err != nil || req.ID == "" {
		writeError(w, http.StatusBadRequest, "bad_request", "the body is not a move: give the transition's id")
		return
	}
	view, err := s.ticketMoves(r.Context(), key)
	if err != nil {
		writeJiraError(w, err)
		return
	}
	i := slices.IndexFunc(view.Moves, func(m board.Move) bool { return m.ID == req.ID })
	if i < 0 {
		writeError(w, http.StatusConflict, "transition_unavailable", fmt.Sprintf("Jira offers no transition %s for %s now", req.ID, key))
		return
	}
	mv := view.Moves[i]
	if mv.InJira {
		writeJSON(w, http.StatusConflict, map[string]string{"type": "error", "code": "needs_jira_form", "url": view.URL,
			"message": "moving " + string(key) + " to " + mv.To + " needs Jira's own form"})
		return
	}
	for id, value := range req.Values {
		f := slices.IndexFunc(mv.Fields, func(f board.Field) bool { return f.ID == id })
		if f < 0 {
			writeError(w, http.StatusBadRequest, "bad_request", fmt.Sprintf("the move to %s has no field %s", mv.To, id))
			return
		}
		if field := mv.Fields[f]; field.Kind == board.KindOption && !slices.ContainsFunc(field.Options, func(o board.Option) bool { return o.ID == value }) {
			writeError(w, http.StatusBadRequest, "bad_request", fmt.Sprintf("%s has no option %s", field.Name, value))
			return
		}
	}
	values, missing := board.FormValues(mv.Fields, req.Values)
	if missing != nil {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"type": "error", "code": "missing_field", "field": missing.ID,
			"message": "choose " + missing.Name + " first"})
		return
	}
	if err := s.opts.Transition(r.Context(), key, mv.ID, values, strings.TrimSpace(req.Comment)); err != nil {
		writeJiraError(w, err)
		return
	}
	s.syncSoon()
	writeJSON(w, http.StatusOK, map[string]string{"key": string(key), "to": mv.To})
}

func (s *Server) users(w http.ResponseWriter, r *http.Request) {
	if s.opts.Users == nil {
		writeError(w, http.StatusNotFound, "not_found", "this daemon assigns no tickets")
		return
	}
	key, ok := pathKey(w, r)
	if !ok {
		return
	}
	query := r.URL.Query().Get("query")
	users := []board.User{}
	if len([]rune(strings.TrimSpace(query))) >= 2 {
		found, err := s.opts.Users(r.Context(), key, query)
		if err != nil {
			writeJiraError(w, err)
			return
		}
		users = orNoneOf(found[:min(len(found), maxUsers)])
	}
	writeJSON(w, http.StatusOK, map[string]any{"key": key, "query": query, "users": users})
}

func (s *Server) assign(w http.ResponseWriter, r *http.Request) {
	if s.opts.Assign == nil {
		writeError(w, http.StatusNotFound, "not_found", "this daemon assigns no tickets")
		return
	}
	key, ok := pathKey(w, r)
	if !ok {
		return
	}
	var req AssignRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "the body is not an assignment: "+err.Error())
		return
	}
	if (req.To == "user") != (req.AccountID != "") || !slices.Contains([]string{"me", "user", "nobody"}, req.To) {
		writeError(w, http.StatusBadRequest, "bad_request", `give "to": "me", "nobody", or "user" with its "account_id"`)
		return
	}
	if err := s.opts.Assign(r.Context(), key, req.To == "me", req.AccountID); err != nil {
		writeJiraError(w, err)
		return
	}
	s.markBacklogStale()
	s.syncSoon()
	writeJSON(w, http.StatusOK, map[string]string{"key": string(key), "to": req.To, "account_id": req.AccountID})
}

func (s *Server) markBacklogStale() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.staleBacklog = true
}

func (s *Server) takeStaleBacklog() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	stale := s.staleBacklog
	s.staleBacklog = false
	return stale
}
