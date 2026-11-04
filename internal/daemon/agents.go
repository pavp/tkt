package daemon

import (
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"slices"
)

// agentKind is a program name as Herdr desktop's knownAgents keys it: no path, flag or shell syntax, since
// tkt starts it as a program.
var agentKind = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,39}$`)

func (s *Server) saveAgentKinds(w http.ResponseWriter, r *http.Request) {
	if s.opts.SaveAgentKinds == nil {
		writeError(w, http.StatusNotFound, "not_found", "this daemon keeps no agent list")
		return
	}
	var req struct {
		Agents *[]string `json:"agents"`
	}
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil || req.Agents == nil || len(*req.Agents) > 64 {
		writeError(w, http.StatusBadRequest, "bad_request", `send {"agents": ["claude", …]}, at most 64`)
		return
	}
	kinds := []string{}
	for _, k := range *req.Agents {
		if !agentKind.MatchString(k) {
			writeError(w, http.StatusBadRequest, "bad_request", fmt.Sprintf("%q is not an agent's name", k))
			return
		}
		if !slices.Contains(kinds, k) {
			kinds = append(kinds, k)
		}
	}
	if err := s.opts.SaveAgentKinds(kinds); err != nil {
		writeError(w, http.StatusInternalServerError, "agents_failed", "could not keep the agents: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"agents": kinds})
}
