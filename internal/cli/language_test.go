package cli

import (
	"os"
	"strings"
	"testing"

	"github.com/pavp/tkt/internal/ticket"
	"github.com/pavp/tkt/internal/tui"
)

const spanishLine = "Talk to the user in Spanish."

// speak sets the language agents talk to the user in, a top-level key that must precede the config's tables.
func speak(t *testing.T, h *harness, language string) {
	t.Helper()
	old, err := os.ReadFile(h.app.ConfigPath())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(h.app.ConfigPath(), append([]byte("language = \""+language+"\"\n"), old...), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestAgentPromptsCarryTheConfiguredLanguage(t *testing.T) {
	tests := []struct {
		language string
		want     bool
	}{{"", false}, {"en", false}, {"es", true}}
	for _, tt := range tests {
		t.Run("language "+tt.language, func(t *testing.T) {
			b, _, f := agentsHarness(t)
			b.c.Language = tt.language
			pr := ticket.PullRequest{Repo: "example-org/web", Number: 820, Title: "Add pagination", URL: "https://github.com/example-org/web/pull/820"}
			b.SendTask(tui.AgentTask{Review: &pr}, "w3:p1")()
			text, _ := f.Last("agent.prompt")["text"].(string)
			if !strings.Contains(text, "Review pull request") || strings.Contains(text, spanishLine) != tt.want {
				t.Errorf("review prompt, want Spanish line %v:\n%s", tt.want, text)
			}
		})
	}
}

func TestHeadlessStartTellsTheAgentToTalkInSpanish(t *testing.T) {
	wt := "/home/u/.herdr/worktrees/web/feature-DEMO-7-export"
	h, d, _ := headlessStart(t, `{"type":"result","is_error":false,"structured_output":{"branch":"feature/DEMO-7-export","worktree":"`+wt+`"}}`, "feature/DEMO-7-export", wt)
	speak(t, h, "es")
	if code := h.exec("herdr", "start", "DEMO-7", "--repo", "web", "--agent", "claude"); code != 0 {
		t.Fatalf("exit %d: %s", code, h.errOut)
	}
	if len(d.texts) != 1 || !strings.Contains(d.texts[0], "Work on Jira ticket DEMO-7") || !strings.HasSuffix(d.texts[0], spanishLine+" Code, commits, pull requests and other files follow this repository's own conventions.") {
		t.Errorf("task = %q", d.texts)
	}
}

func TestStartSetupAndFixPromptsTalkInSpanish(t *testing.T) {
	t.Run("start copies it", func(t *testing.T) {
		h, _ := workHarness(t)
		speak(t, h, "es")
		h.clone(t, "web")
		h.run.out["wl-copy"] = ""
		if code := h.exec("start", "DEMO-7", "--repo", "web"); code != 0 {
			t.Fatalf("exit %d: %s", code, h.errOut)
		}
		if copied := h.run.stdin["wl-copy"]; !strings.Contains(copied, "Work on Jira ticket DEMO-7") || !strings.Contains(copied, spanishLine) {
			t.Errorf("copied %q", copied)
		}
	})
	t.Run("the setup agent gets it", func(t *testing.T) {
		h, f, _ := herdrHarness(t)
		speak(t, h, "es")
		h.clone(t, "web")
		f.Results["workspace.create"] = `{"type":"workspace_created","workspace":{"workspace_id":"w9","label":"x"},"root_pane":{"pane_id":"w9:p1"},"tab":{}}`
		f.Results["pane.split"] = `{"type":"pane_info","pane":{"pane_id":"w9:p2","workspace_id":"w9"}}`
		if code := h.exec("herdr", "start", "DEMO-7", "--repo", "web"); code != 0 {
			t.Fatalf("exit %d: %s", code, h.errOut)
		}
		if text, _ := f.Last("agent.prompt")["text"].(string); !strings.Contains(text, "Prepare Jira ticket DEMO-7") || !strings.Contains(text, spanishLine) {
			t.Errorf("setup prompt = %q", text)
		}
	})
	t.Run("fix prints it", func(t *testing.T) {
		h, _, _ := fixHarness(t)
		speak(t, h, "es")
		if code := h.exec("fix", "DEMO-1"); code != 0 {
			t.Fatalf("exit %d: %s", code, h.errOut)
		}
		if out := h.out.String(); !strings.Contains(out, "Feedback on Jira ticket DEMO-1") || !strings.Contains(out, spanishLine) {
			t.Errorf("fix = %q", out)
		}
	})
}
