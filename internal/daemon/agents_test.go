package daemon

import (
	"net/http"
	"reflect"
	"testing"
	"time"
)

func TestTheModSavesHerdrDesktopsAgents(t *testing.T) {
	tests := []struct {
		name, body string
		code       int
		saved      []string
	}{
		{"the installed agents Herdr knows", `{"agents":["claude","codex","claude"]}`, http.StatusOK, []string{"claude", "codex"}},
		{"none", `{"agents":[]}`, http.StatusOK, []string{}},
		{"a path is not an agent", `{"agents":["/bin/sh"]}`, http.StatusBadRequest, nil},
		{"a flag is not an agent", `{"agents":["--help"]}`, http.StatusBadRequest, nil},
		{"a command line is not an agent", `{"agents":["claude; rm -rf ~"]}`, http.StatusBadRequest, nil},
		{"no list", `{}`, http.StatusBadRequest, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var saved []string
			s, err := New(Options{Dir: t.TempDir(), Version: "1.2.3", Sync: (&fakeSync{}).sync, Interval: time.Hour,
				SaveAgentKinds: func(kinds []string) error { saved = kinds; return nil }})
			if err != nil {
				t.Fatal(err)
			}
			code, body := post(t, s.Handler(), "/v1/herdr/agents", s.token, "application/json", "", tt.body)
			if code != tt.code || !reflect.DeepEqual(saved, tt.saved) {
				t.Errorf("POST = %d %v, saved %q; want %d, %q", code, body, saved, tt.code, tt.saved)
			}
		})
	}
}
