package daemon

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/pavp/tkt/internal/ticket"
)

func TestAJobsEndCanBeAskedFor(t *testing.T) {
	for _, tt := range []struct {
		name, state, message string
		err                  error
	}{
		{"it finished", "finished", "", nil},
		{"it failed", "failed", "the agent did not start", errors.New("the agent did not start")},
	} {
		t.Run(tt.name, func(t *testing.T) {
			release := make(chan struct{})
			s := startServer(t, nil, func(_ context.Context, _ ticket.Key, _ StartRequest, settled func()) error {
				settled()
				<-release
				return tt.err
			})
			_, body := post(t, s.Handler(), "/v1/tickets/DEMO-7/start", s.token, "application/json", "", `{"herdr":"desktop","agent":"claude"}`)
			id, _ := body["id"].(string)
			code, job := get(t, s.Handler(), http.MethodGet, "/v1/jobs/"+id, s.token)
			if code != http.StatusOK || job["state"] != "running" || job["kind"] != "start" || job["subject"] != "DEMO-7" {
				t.Fatalf("while running: %d %v", code, job)
			}
			close(release)
			deadline := time.Now().Add(3 * time.Second)
			for time.Now().Before(deadline) {
				if _, job = get(t, s.Handler(), http.MethodGet, "/v1/jobs/"+id, s.token); job["state"] != "running" {
					break
				}
				time.Sleep(10 * time.Millisecond)
			}
			if job["state"] != tt.state || (tt.message != "" && job["message"] != tt.message) {
				t.Errorf("after it ended: %v, want %s %q", job, tt.state, tt.message)
			}
			if code, _ := get(t, s.Handler(), http.MethodGet, "/v1/jobs/unknown", s.token); code != http.StatusNotFound {
				t.Errorf("an unknown job = %d, want 404", code)
			}
		})
	}
}
