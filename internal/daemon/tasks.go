package daemon

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strconv"

	"github.com/pavp/tkt/internal/ticket"
)

// Task is work the board hands to an agent: a ticket's feedback (Key, with the board's copy in Known when it holds
// one), or a review of a pull request the board shows (Review).
type Task struct {
	Key    ticket.Key
	Known  *ticket.Ticket
	Review *ticket.PullRequest
}

// TaskAgentsFunc lists the agents open in the Herdr named herdr that task can go to, with the kinds a new agent can be.
type TaskAgentsFunc func(ctx context.Context, task Task, herdr string) (TaskAgents, error)

// TaskFunc hands task to the agent req names. It calls settled once it asks nothing more and only cloning or opening
// an agent remains, which can take minutes.
type TaskFunc func(ctx context.Context, task Task, req TaskRequest, settled func()) error

// TaskAgents is what the board's "Send … to" menus offer: the open agents, the free ones in the task's repositories
// first, and the kinds of a new agent.
type TaskAgents struct {
	Herdr     string      `json:"herdr"`
	Phrase    string      `json:"phrase"`
	Agents    []OpenAgent `json:"agents"`
	Kinds     []string    `json:"kinds"`
	LastAgent string      `json:"last_agent,omitempty"`
	// KindsError says why no new agent can be offered.
	KindsError string `json:"kinds_error,omitempty"`
}

type OpenAgent struct {
	Name      string     `json:"name"`
	Pane      string     `json:"pane"`
	Status    string     `json:"status"`
	Folder    string     `json:"folder"`
	Workspace string     `json:"workspace,omitempty"`
	Repo      string     `json:"repo,omitempty"`
	Branch    string     `json:"branch,omitempty"`
	Ticket    ticket.Key `json:"ticket,omitempty"`
	SameRepo  bool       `json:"same_repo"`
}

// TaskReview names a pull request the board shows, to review or, as Feedback, to send its own feedback.
type TaskReview struct {
	Repo   string `json:"repo"`
	Number int    `json:"number"`
}

// TaskRequest hands a ticket's feedback, one of the user's pull requests' feedback or a review to an agent: "own" is the
// ticket's own agent, "open" the open agent in Pane, "new" an agent where the work belongs, of kind Agent; Clone nil
// means the request did not answer it.
type TaskRequest struct {
	Herdr  string      `json:"herdr"`
	Ticket ticket.Key  `json:"ticket,omitempty"`
	Review *TaskReview `json:"review,omitempty"`
	// Feedback is a pull request of the user's whose ticket is not theirs: its feedback alone goes, under the key it names.
	Feedback *TaskReview `json:"feedback,omitempty"`
	To       string      `json:"to"`
	Pane     string      `json:"pane,omitempty"`
	Agent    string      `json:"agent,omitempty"`
	Clone    *bool       `json:"clone,omitempty"`
}

// subject names the task in its job and events: the ticket key, or "owner/name#N" for a review.
func (t Task) subject() string {
	if t.Review != nil {
		return fmt.Sprintf("%s#%d", t.Review.Repo, t.Review.Number)
	}
	return string(t.Key)
}

// boardTask finds the task a request names: a ticket, with the board's copy when the board holds it, or a pull request
// the board shows, since a review or feedback prompt is built from the board's own copy rather than from the caller's.
func (s *Server) boardTask(w http.ResponseWriter, key ticket.Key, review, feedback *TaskReview) (Task, bool) {
	s.mu.Lock()
	snap, have := s.snap, s.have
	s.mu.Unlock()
	named := cmp.Or(review, feedback)
	if named == nil {
		task := Task{Key: key}
		if t, ok := snap.Find(key); have && ok {
			task.Known = &t
		}
		return task, true
	}
	if !have {
		writeError(w, http.StatusServiceUnavailable, "not_synced", "the daemon has no board yet")
		return Task{}, false
	}
	prs := append(append([]ticket.PullRequest{}, snap.Reviews...), snap.Others...)
	for _, t := range snap.Tickets {
		prs = append(prs, t.PRs...)
	}
	i := slices.IndexFunc(prs, func(pr ticket.PullRequest) bool { return pr.Repo == named.Repo && pr.Number == named.Number })
	if i < 0 {
		writeError(w, http.StatusNotFound, "not_found", fmt.Sprintf("pull request %s#%d is not on the board", named.Repo, named.Number))
		return Task{}, false
	}
	pr := prs[i]
	if review != nil {
		return Task{Review: &pr}, true
	}
	if !pr.Yours {
		writeError(w, http.StatusBadRequest, "bad_request", fmt.Sprintf("%s#%d is not yours: only your pull requests' feedback goes to an agent", pr.Repo, pr.Number))
		return Task{}, false
	}
	t, ok := ticket.FeedbackTicket(pr)
	if !ok {
		writeError(w, http.StatusBadRequest, "bad_request", fmt.Sprintf("%s#%d names no ticket in its title or branch", pr.Repo, pr.Number))
		return Task{}, false
	}
	return Task{Key: t.Key, Known: &t}, true
}

// validKey answers 400 when key is not a ticket key.
func validKey(w http.ResponseWriter, key ticket.Key) bool {
	if keys := ticket.KeysIn(string(key)); len(keys) != 1 || keys[0] != key {
		writeError(w, http.StatusBadRequest, "bad_key", fmt.Sprintf("%q is not a ticket key like DEMO-123", key))
		return false
	}
	return true
}

// validTask answers 400 unless a request names exactly one task: a ticket, a pull request to review, or one whose
// feedback goes.
func validTask(w http.ResponseWriter, key ticket.Key, review, feedback *TaskReview) bool {
	named := 0
	for _, set := range []bool{key != "", review != nil, feedback != nil} {
		if set {
			named++
		}
	}
	pr := cmp.Or(review, feedback)
	switch {
	case named != 1:
		writeError(w, http.StatusBadRequest, "bad_request", "name one ticket or one pull request")
		return false
	case pr != nil && (pr.Repo == "" || pr.Number <= 0):
		writeError(w, http.StatusBadRequest, "bad_request", "a pull request needs its repository and number")
		return false
	case pr == nil:
		return validKey(w, key)
	}
	return true
}

func (s *Server) taskAgents(w http.ResponseWriter, r *http.Request) {
	if s.opts.TaskAgents == nil {
		writeError(w, http.StatusNotFound, "not_found", "this daemon hands no tasks to agents")
		return
	}
	q := r.URL.Query()
	herdr := q.Get("herdr")
	if !validHerdr(w, herdr) {
		return
	}
	var feedback bool
	if !queryBool(w, q, "feedback", &feedback) {
		return
	}
	var review, fb *TaskReview
	if q.Has("repo") || q.Has("number") {
		n, err := strconv.Atoi(q.Get("number"))
		if err != nil {
			writeError(w, http.StatusBadRequest, "bad_request", fmt.Sprintf("number must be a pull request number, not %q", q.Get("number")))
			return
		}
		review = &TaskReview{Repo: q.Get("repo"), Number: n}
	}
	if feedback {
		if review == nil {
			writeError(w, http.StatusBadRequest, "bad_request", "feedback needs the pull request's repo and number")
			return
		}
		review, fb = nil, review
	}
	key := ticket.Key(q.Get("ticket"))
	if !validTask(w, key, review, fb) {
		return
	}
	task, ok := s.boardTask(w, key, review, fb)
	if !ok {
		return
	}
	out, err := s.opts.TaskAgents(r.Context(), task, herdr)
	if err != nil {
		writeJobError(w, "task", err)
		return
	}
	out.Herdr = herdr
	out.Agents, out.Kinds = orNoneOf(out.Agents), orNoneOf(out.Kinds)
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) task(w http.ResponseWriter, r *http.Request) {
	if s.opts.Task == nil {
		writeError(w, http.StatusNotFound, "not_found", "this daemon hands no tasks to agents")
		return
	}
	var req TaskRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "the body is not a task request: "+err.Error())
		return
	}
	if !validHerdr(w, req.Herdr) || !validTask(w, req.Ticket, req.Review, req.Feedback) || !validDestination(w, req) {
		return
	}
	task, ok := s.boardTask(w, req.Ticket, req.Review, req.Feedback)
	if !ok {
		return
	}
	s.runJob(w, r, job{kind: "task", field: "task", subject: task.subject()}, func(ctx context.Context, settled func()) error {
		return s.opts.Task(ctx, task, req, settled)
	})
}

// validDestination answers 400 unless the request's answers fit where it sends the task.
func validDestination(w http.ResponseWriter, req TaskRequest) bool {
	bad := ""
	switch req.To {
	case "own":
		if req.Review != nil {
			bad = "a review has no agent of its own: send it to an open agent or a new one"
		}
	case "open":
		if req.Pane == "" {
			bad = "name the pane of the open agent"
		}
	case "new":
	default:
		bad = fmt.Sprintf("to must be own, open or new, not %q", req.To)
	}
	switch {
	case bad != "":
	case req.Pane != "" && req.To != "open":
		bad = "a pane goes only with an open agent"
	case (req.Agent != "" || req.Clone != nil) && req.To != "new":
		bad = "an agent kind and a clone go only with a new agent"
	}
	if bad != "" {
		writeError(w, http.StatusBadRequest, "bad_request", bad)
		return false
	}
	return true
}
