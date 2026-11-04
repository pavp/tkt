package daemon

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"net/http"
	"slices"

	"github.com/pavp/tkt/internal/board"
)

// stateView is the board state as GET and PATCH /v1/board/state show it: every field, each in effect.
type stateView struct {
	View          string   `json:"view"`
	Team          bool     `json:"team"`
	HiddenBoards  []string `json:"hidden_boards"`
	HiddenRepos   []string `json:"hidden_repos"`
	Collapsed     []string `json:"collapsed"`
	TeamPRs       bool     `json:"team_prs"`
	BacklogMerged bool     `json:"backlog_merged"`
}

func viewOf(st board.SavedState) stateView {
	view := board.ViewColumns
	if st.View == board.ViewList {
		view = board.ViewList
	}
	orEmpty := func(s []string) []string {
		if s == nil {
			return []string{}
		}
		return s
	}
	return stateView{View: view, Team: st.Team, HiddenBoards: orEmpty(st.HiddenBoards), HiddenRepos: orEmpty(st.HiddenRepos), Collapsed: orEmpty(st.Collapsed),
		TeamPRs: st.TeamPRsShown(), BacklogMerged: st.BacklogMerged}
}

func (s *Server) boardState(w http.ResponseWriter, _ *http.Request) {
	if s.opts.SavedState == nil || s.opts.SaveState == nil {
		writeError(w, http.StatusNotFound, "not_found", "this daemon keeps no board state")
		return
	}
	writeJSON(w, http.StatusOK, viewOf(s.opts.SavedState()))
}

func (s *Server) patchBoardState(w http.ResponseWriter, r *http.Request) {
	if s.opts.SavedState == nil || s.opts.SaveState == nil {
		writeError(w, http.StatusNotFound, "not_found", "this daemon keeps no board state")
		return
	}
	var patch map[string]json.RawMessage
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16))
	err := dec.Decode(&patch)
	if err == nil && dec.Decode(&struct{}{}) != io.EOF {
		err = errors.New("more than one JSON value")
	}
	if err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "the body is not a board state: "+err.Error())
		return
	}
	s.stateMu.Lock()
	defer s.stateMu.Unlock()
	st := s.opts.SavedState()
	for _, k := range slices.Sorted(maps.Keys(patch)) {
		if err := applyState(&st, k, patch[k]); err != nil {
			writeError(w, http.StatusBadRequest, "bad_request", err.Error())
			return
		}
	}
	if err := s.opts.SaveState(st); err != nil {
		writeError(w, http.StatusInternalServerError, "state_failed", "could not remember the board's filters: "+err.Error())
		return
	}
	v := viewOf(st)
	s.noteFile(s.opts.StateFile)
	s.mu.Lock()
	s.publish("state.updated", v)
	s.mu.Unlock()
	writeJSON(w, http.StatusOK, v)
}

// applyState sets one field the TUI remembers, as the TUI writes it: lists sorted, without repeats, absent when empty.
func applyState(st *board.SavedState, key string, raw json.RawMessage) error {
	flag := func(to *bool) error {
		if string(raw) == "null" || json.Unmarshal(raw, to) != nil {
			return fmt.Errorf("%s must be true or false", key)
		}
		return nil
	}
	names := func(to *[]string) error {
		var list []string
		if string(raw) == "null" || json.Unmarshal(raw, &list) != nil || slices.Contains(list, "") {
			return fmt.Errorf("%s must be a list of names", key)
		}
		slices.Sort(list)
		if list = slices.Compact(list); len(list) == 0 {
			list = nil
		}
		*to = list
		return nil
	}
	switch key {
	case "view":
		var v string
		if json.Unmarshal(raw, &v) != nil || (v != board.ViewColumns && v != board.ViewList) {
			return fmt.Errorf("view must be columns or list, not %s", raw)
		}
		st.View = v
	case "team":
		return flag(&st.Team)
	case "team_prs":
		var on bool
		if err := flag(&on); err != nil {
			return err
		}
		st.TeamPRs = &on
	case "backlog_merged":
		return flag(&st.BacklogMerged)
	case "hidden_boards":
		return names(&st.HiddenBoards)
	case "hidden_repos":
		return names(&st.HiddenRepos)
	case "collapsed":
		return names(&st.Collapsed)
	default:
		return fmt.Errorf("unknown field %q", key)
	}
	return nil
}
