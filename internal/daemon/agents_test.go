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
				SaveAgentKinds: func(kinds []string, _ map[string][]string) error { saved = kinds; return nil }})
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

func TestTheModSavesHowHerdrStartsEachAgent(t *testing.T) {
	tests := []struct {
		name, body string
		code       int
		runs       map[string][]string
	}{
		{"an agent Herdr starts through another program", `{"agents":["claude","gentle-shell"],"runs":{"gentle-shell":["gentle-shell","--agent","pi"]}}`, http.StatusOK,
			map[string][]string{"gentle-shell": {"gentle-shell", "--agent", "pi"}}},
		{"an agent overridden to another listed agent", `{"agents":["pi","gentle-shell"],"runs":{"pi":["gentle-shell"]}}`, http.StatusOK, map[string][]string{"pi": {"gentle-shell"}}},
		{"a run starting a program that is not a listed agent", `{"agents":["pi"],"runs":{"pi":["gentle-shell"]}}`, http.StatusBadRequest, nil},
		{"a launcher dressed as an agent", `{"agents":["claude"],"runs":{"claude":["sh","-c","id"]}}`, http.StatusBadRequest, nil},
		{"no runs", `{"agents":["claude"]}`, http.StatusOK, map[string][]string{}},
		{"a run for an agent not listed", `{"agents":["claude"],"runs":{"pi":["pi"]}}`, http.StatusBadRequest, nil},
		{"a run naming a path", `{"agents":["pi"],"runs":{"pi":["/tmp/evil"]}}`, http.StatusBadRequest, nil},
		{"a run with a shell command", `{"agents":["pi"],"runs":{"pi":["sh","-c","rm -rf ~"]}}`, http.StatusBadRequest, nil},
		{"an empty run", `{"agents":["pi"],"runs":{"pi":[]}}`, http.StatusBadRequest, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var runs map[string][]string
			s, err := New(Options{Dir: t.TempDir(), Version: "1.2.3", Sync: (&fakeSync{}).sync, Interval: time.Hour,
				SaveAgentKinds: func(_ []string, r map[string][]string) error { runs = r; return nil }})
			if err != nil {
				t.Fatal(err)
			}
			code, body := post(t, s.Handler(), "/v1/herdr/agents", s.token, "application/json", "", tt.body)
			if code != tt.code || !reflect.DeepEqual(runs, tt.runs) {
				t.Errorf("POST = %d %v, runs %q; want %d, %q", code, body, runs, tt.code, tt.runs)
			}
		})
	}
}
