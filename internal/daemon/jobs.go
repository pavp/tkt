package daemon

import (
	"net/http"
	"time"
)

// jobEnd is how a Start or a task answered 202 is going: running, finished or failed with its message, for
// clients with no WebSocket, such as the Herdr desktop mod, which ask GET /v1/jobs/{id}.
type jobEnd struct {
	Kind    string `json:"kind"`
	Subject string `json:"subject"`
	State   string `json:"state"`
	Message string `json:"message,omitempty"`
	at      time.Time
}

// keepJobs is how long an ended job can still be asked for.
const keepJobs = 10 * time.Minute

// recordJob keeps job id's state, dropping jobs that ended long ago; callers hold s.mu.
func (s *Server) recordJob(id string, j jobEnd) {
	now := time.Now()
	if s.jobs == nil {
		s.jobs = map[string]jobEnd{}
	}
	for k, old := range s.jobs {
		if old.State != "running" && now.Sub(old.at) > keepJobs {
			delete(s.jobs, k)
		}
	}
	j.at = now
	s.jobs[id] = j
}

func (s *Server) jobState(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	s.mu.Lock()
	j, ok := s.jobs[id]
	s.mu.Unlock()
	if !ok {
		writeError(w, http.StatusNotFound, "not_found", "no job "+id+" is known: it ended long ago or never ran")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"id": id, "kind": j.Kind, "subject": j.Subject, "state": j.State, "message": j.Message})
}
