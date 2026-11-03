package daemon

import (
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/pavp/tkt/internal/board"
	"github.com/pavp/tkt/internal/ticket"
)

// PRsView is the PRs tab as the board lays it out: the user's pull requests, then the review requests, or every
// open pull request while team_prs is on, in groups by repository.
type PRsView struct {
	FetchedAt   time.Time `json:"fetched_at"`
	Partial     bool      `json:"partial,omitempty"`
	Syncing     bool      `json:"syncing,omitempty"`
	SyncError   string    `json:"sync_error,omitempty"`
	HiddenRepos []string  `json:"hidden_repos"`
	Collapsed   []string  `json:"collapsed"`
	TeamPRs     bool      `json:"team_prs"`
	Bots        bool      `json:"bots"`
	// HiddenBots counts the pull requests opened by bots, which bots=true shows.
	HiddenBots int          `json:"hidden_bots"`
	Repos      []RepoFilter `json:"repos"`
	Sections   []PRSection  `json:"sections"`
	Warnings   []string     `json:"warnings,omitempty"`
}

// RepoFilter is one repository the Repos filter offers, with its open pull requests and review requests.
type RepoFilter struct {
	Name   string `json:"name"`
	PRs    int    `json:"prs"`
	Hidden bool   `json:"hidden"`
}

type PRSection struct {
	Group     string  `json:"group"`
	Title     string  `json:"title"`
	Count     int     `json:"count"`
	Needs     int     `json:"needs"`
	Collapsed bool    `json:"collapsed"`
	Rows      []PRRow `json:"rows"`
}

// PRRow is one pull request: the user's (with its ticket when it has one), a review request, or someone else's.
type PRRow struct {
	Key       string              `json:"key"`
	PR        *ticket.PullRequest `json:"pr"`
	Ticket    *ticket.Ticket      `json:"ticket,omitempty"`
	Other     bool                `json:"other,omitempty"`
	Review    bool                `json:"review,omitempty"`
	Status    string              `json:"status"`
	Approvals string              `json:"approvals,omitempty"`
	NeedsYou  bool                `json:"needs_you,omitempty"`
}

// BacklogView is the Backlog as the board lays it out: its sprints, then the tickets in no sprint.
type BacklogView struct {
	FetchedAt time.Time `json:"fetched_at"`
	// Error says why the backlog could not be refreshed; the backlog is then the last copy.
	Error        string           `json:"error,omitempty"`
	HiddenBoards []string         `json:"hidden_boards"`
	Merged       bool             `json:"merged"`
	Collapsed    []string         `json:"collapsed"`
	Boards       []BoardFilter    `json:"boards"`
	Groups       []BacklogSection `json:"groups"`
	LabelField   string           `json:"label_field,omitempty"`
	Me           string           `json:"me,omitempty"`
	Warnings     []string         `json:"warnings,omitempty"`
}

type BacklogSection struct {
	Group     string         `json:"group"`
	Title     string         `json:"title"`
	Board     string         `json:"board,omitempty"`
	Dates     string         `json:"dates,omitempty"`
	Sprints   []int          `json:"sprints,omitempty"`
	Count     int            `json:"count"`
	Collapsed bool           `json:"collapsed"`
	Points    *BacklogPoints `json:"points,omitempty"`
	Rows      []board.Row    `json:"rows"`
}

// BacklogPoints are a group's story points by stage, set only when the site has a story points field.
type BacklogPoints struct {
	ToDo        float64 `json:"todo"`
	Doing       float64 `json:"doing"`
	Done        float64 `json:"done"`
	Unestimated int     `json:"unestimated"`
}

func (s *Server) prs(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	snap, have, syncing, syncErr := s.snap, s.have, s.syncing, s.syncErr
	s.mu.Unlock()
	if !have {
		writeError(w, http.StatusServiceUnavailable, "not_synced", "the first sync has not finished")
		return
	}
	lists := s.lists()
	q := r.URL.Query()
	var bots bool
	if !queryBool(w, q, "team_prs", &lists.TeamPRs) || !queryBool(w, q, "bots", &bots) {
		return
	}
	queryList(q, "hidden_repos", &lists.HiddenRepos)
	f := board.PRFilter{HiddenRepos: setOf(lists.HiddenRepos), Team: lists.TeamPRs, Bots: bots}
	collapsed := setOf(lists.Collapsed)
	view := PRsView{FetchedAt: snap.FetchedAt, Partial: snap.Partial, Syncing: syncing, SyncError: syncErr,
		HiddenRepos: orNone(lists.HiddenRepos), Collapsed: orNone(lists.Collapsed), TeamPRs: lists.TeamPRs, Bots: bots,
		HiddenBots: board.HiddenBots(snap), Repos: []RepoFilter{}, Sections: []PRSection{}, Warnings: snap.Warnings}
	var configured []string
	if s.opts.Repos != nil {
		configured = s.opts.Repos()
	}
	for name, n := range board.Repos(snap, configured) {
		view.Repos = append(view.Repos, RepoFilter{Name: name, PRs: n, Hidden: f.HiddenRepos[name]})
	}
	slices.SortFunc(view.Repos, func(a, b RepoFilter) int { return strings.Compare(a.Name, b.Name) })
	for _, g := range board.PRGroups(snap, f) {
		sec := PRSection{Group: g.Group, Title: g.Title, Count: len(g.Rows), Needs: g.Needs, Collapsed: board.Folded(collapsed, g.Group), Rows: []PRRow{}}
		for _, row := range g.Rows {
			sec.Rows = append(sec.Rows, PRRow{Key: fmt.Sprintf("#%d", row.PR.Number), PR: row.PR, Ticket: row.Ticket, Other: row.Other, Review: row.Review(),
				Status: row.Status(), Approvals: row.PR.ApprovalsText(), NeedsYou: board.NeedsYou(*row.PR)})
		}
		view.Sections = append(view.Sections, sec)
	}
	writeJSON(w, http.StatusOK, view)
}

func (s *Server) backlog(w http.ResponseWriter, r *http.Request) {
	if s.opts.Backlog == nil {
		writeError(w, http.StatusNotFound, "not_found", "this daemon serves no backlog")
		return
	}
	var state board.ViewState
	if s.opts.State != nil {
		state = s.opts.State()
	}
	lists := s.lists()
	q := r.URL.Query()
	var refresh bool
	if !queryBool(w, q, "merged", &lists.BacklogMerged) || !queryBool(w, q, "refresh", &refresh) {
		return
	}
	queryList(q, "hidden_boards", &state.HiddenBoards)
	// One load at a time, so requests arriving together share the copy the first one caches instead of each asking Jira.
	s.backlogMu.Lock()
	stale := s.takeStaleBacklog()
	bl, err := s.opts.Backlog(r.Context(), refresh || stale)
	if err != nil && stale {
		s.markBacklogStale()
	}
	s.backlogMu.Unlock()
	if err != nil && bl.FetchedAt.IsZero() {
		writeError(w, http.StatusBadGateway, "backlog_failed", err.Error())
		return
	}
	hidden, collapsed := setOf(state.HiddenBoards), setOf(lists.Collapsed)
	view := BacklogView{FetchedAt: bl.FetchedAt, HiddenBoards: orNone(state.HiddenBoards), Merged: lists.BacklogMerged, Collapsed: orNone(lists.Collapsed),
		Boards: []BoardFilter{}, Groups: []BacklogSection{}, LabelField: bl.LabelField, Me: bl.Me, Warnings: bl.Warnings}
	if err != nil {
		view.Error = err.Error()
	}
	for name, n := range bl.BoardCounts() {
		view.Boards = append(view.Boards, BoardFilter{Name: name, Tickets: n, Hidden: hidden[name]})
	}
	slices.SortFunc(view.Boards, func(a, b BoardFilter) int { return strings.Compare(a.Name, b.Name) })
	for _, g := range board.BacklogGroups(bl, hidden, lists.BacklogMerged) {
		sec := BacklogSection{Group: g.Group, Title: g.Title, Board: g.Board, Dates: g.Dates, Sprints: g.Sprints, Count: len(g.Tickets), Collapsed: board.Folded(collapsed, g.Group), Rows: []board.Row{}}
		if bl.HasPoints {
			p := BacklogPoints{}
			p.ToDo, p.Doing, p.Done, p.Unestimated = board.PointsByStage(g.Tickets)
			sec.Points = &p
		}
		for _, t := range g.Tickets {
			sec.Rows = append(sec.Rows, board.Row{Key: t.Key, Ticket: &t})
		}
		view.Groups = append(view.Groups, sec)
	}
	writeJSON(w, http.StatusOK, view)
}

func (s *Server) lists() board.ListState {
	if s.opts.Lists == nil {
		return board.ListState{}
	}
	return s.opts.Lists()
}

// queryBool overrides into with the query's name when it is given; any value but true or false answers 400 and
// returns false.
func queryBool(w http.ResponseWriter, q url.Values, name string, into *bool) bool {
	vs, ok := q[name]
	if !ok {
		return true
	}
	switch vs[0] {
	case "true":
		*into = true
	case "false":
		*into = false
	default:
		writeError(w, http.StatusBadRequest, "bad_request", fmt.Sprintf("%s must be true or false, not %q", name, vs[0]))
		return false
	}
	return true
}

// queryList overrides into with the query's repeated name when it is given; an empty value means none.
func queryList(q url.Values, name string, into *[]string) {
	if vs, ok := q[name]; ok {
		*into = slices.DeleteFunc(slices.Clone(vs), func(v string) bool { return v == "" })
	}
}

func setOf(vs []string) map[string]bool {
	set := map[string]bool{}
	for _, v := range vs {
		set[v] = true
	}
	return set
}

func orNone(vs []string) []string {
	if vs == nil {
		return []string{}
	}
	return vs
}
