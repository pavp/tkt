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

// runArg is an argument of an agent's run: plain words, flags and values, nothing a shell would read as code.
var runArg = regexp.MustCompile(`^[A-Za-z0-9._,:=@/+-]{1,128}$`)

// runnable reports whether run starts a program by a bare name with plain arguments, so it reaches herdr-core as an
// argv with no path or shell syntax; the caller also requires that program to be one of the listed agents. Neither
// limits what a holder of the daemon's token, who is already the user, could run.
func runnable(run []string) bool {
	if len(run) == 0 || len(run) > 8 || !agentKind.MatchString(run[0]) {
		return false
	}
	for _, a := range run[1:] {
		if !runArg.MatchString(a) {
			return false
		}
	}
	return true
}

func (s *Server) saveAgentKinds(w http.ResponseWriter, r *http.Request) {
	if s.opts.SaveAgentKinds == nil {
		writeError(w, http.StatusNotFound, "not_found", "this daemon keeps no agent list")
		return
	}
	var req struct {
		Agents *[]string           `json:"agents"`
		Runs   map[string][]string `json:"runs"`
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
	runs := map[string][]string{}
	for k, run := range req.Runs {
		if !slices.Contains(kinds, k) || !runnable(run) || !slices.Contains(kinds, run[0]) {
			writeError(w, http.StatusBadRequest, "bad_request", fmt.Sprintf("%q's run must start one of the listed agents, with plain arguments", k))
			return
		}
		runs[k] = run
	}
	if err := s.opts.SaveAgentKinds(kinds, runs); err != nil {
		writeError(w, http.StatusInternalServerError, "agents_failed", "could not keep the agents: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"agents": kinds})
}
