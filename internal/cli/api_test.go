package cli

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestAPIAnswersWhatTheDaemonAnswers(t *testing.T) {
	tests := []struct {
		name   string
		args   []string
		status float64
		field  string
	}{
		{"a read", []string{"api", "GET", "/v1/tickets"}, 200, "tickets"},
		{"a refusal comes back as the daemon's answer", []string{"api", "GET", "/v1/board/state"}, 404, "code"},
		{"a request with a body", []string{"api", "POST", "/v1/sync", "--body", `{}`}, 202, "ok"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h, f := startHarness(t)
			f.board = daemonBoard()
			if code := h.exec(tt.args...); code != 0 {
				t.Fatalf("exit %d: %s", code, h.errOut)
			}
			var got struct {
				Status float64        `json:"status"`
				Body   map[string]any `json:"body"`
			}
			if err := json.Unmarshal(h.out.Bytes(), &got); err != nil {
				t.Fatalf("output %q: %v", h.out, err)
			}
			if got.Status != tt.status || got.Body[tt.field] == nil {
				t.Errorf("got %+v, want status %v with %s", got, tt.status, tt.field)
			}
		})
	}
}

func TestAPIRefusesWhatIsNotADaemonRoute(t *testing.T) {
	for _, args := range [][]string{{"api", "GET", "https://example.com/v1/board"}, {"api", "FETCH", "/v1/board"}, {"api", "GET", "/etc/passwd"}, {"api", "GET"}} {
		h, _ := startHarness(t)
		if code := h.exec(args...); code == 0 || h.out.Len() != 0 {
			t.Errorf("%v: exit %d, output %q, want a refusal", args, code, h.out)
		}
		if !strings.Contains(h.errOut.String(), "tkt") {
			t.Errorf("%v: stderr %q", args, h.errOut)
		}
	}
}
