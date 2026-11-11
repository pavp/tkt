package cli

import (
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// headlessStart fakes the one-shot Claude of a Start of DEMO-7 in web answering out, and the worktree it names.
func headlessStart(t *testing.T, out, branch, top string) (*harness, *desktopHerdr, string) {
	t.Helper()
	h, d := desktopHarness(t)
	web := h.clone(t, "web")
	call := strings.Join(append([]string{"claude"}, headlessArgs("DEMO-7", "Export users", web, h.home)...), " ")
	h.run.out[call] = out
	if branch != "" {
		h.inCheckout(branch, top, "git@github.com:example-org/web.git")
	}
	return h, d, call
}

func TestHerdrStartOnDesktopWithClaudeCreatesTheWorktreeHeadlessAndOpensOneSpace(t *testing.T) {
	wt := "/home/u/.herdr/worktrees/web/feature-DEMO-7-export"
	h, d, call := headlessStart(t, `{"type":"result","is_error":false,"structured_output":{"branch":"feature/DEMO-7-export","worktree":"`+wt+`"}}`, "feature/DEMO-7-export", wt)
	if code := h.exec("herdr", "start", "DEMO-7", "--repo", "web", "--agent", "claude"); code != 0 {
		t.Fatalf("exit %d: %s", code, h.errOut)
	}
	for _, s := range []string{"-p ", "--model haiku", "--json-schema", "--allowedTools Bash(git:*) Read Grep Glob", "--no-session-persistence", "must contain DEMO-7", filepath.Join(h.home, ".herdr", "worktrees", "web")} {
		if !strings.Contains(call, s) {
			t.Errorf("claude call missing %q: %s", s, call)
		}
	}
	want := []string{`space "DEMO-7 · feature-DEMO-7-export" ` + wt + ` claude`, "mark t20 DEMO-7", "focus workspace sp20", "focus t20"}
	if !reflect.DeepEqual(d.calls, want) {
		t.Errorf("calls = %q\nwant    %q (no setup agent, one space with the ticket's agent)", d.calls, want)
	}
	if len(d.texts) != 1 || !strings.Contains(d.texts[0], "Work on Jira ticket DEMO-7: Export users") {
		t.Errorf("task = %q", d.texts)
	}
	if links, _ := h.app.links().Load(); len(links) != 1 || links[0].Path != wt || links[0].Branch != "feature/DEMO-7-export" || links[0].Key != "DEMO-7" {
		t.Errorf("links = %+v", links)
	}
	if hos := h.app.loadState().Handoffs; len(hos) != 0 {
		t.Errorf("no setup agent, so no handoff: %+v", hos)
	}
}

func TestHerdrStartOnDesktopWithClaudeOpensNothingWhenTheWorktreeIsWrong(t *testing.T) {
	tests := []struct {
		name, out, branch, top, wantErr string
	}{
		{"claude failed", `{"type":"result","is_error":true,"result":"git worktree add: fatal: invalid reference"}`, "", "", "invalid reference"},
		{"a branch without the key", `{"is_error":false,"structured_output":{"branch":"feature/export","worktree":"/w/web-export"}}`, "feature/export", "/w/web-export", `does not contain DEMO-7`},
		{"the main checkout switched", `{"is_error":false,"structured_output":{"branch":"feature/DEMO-7-export","worktree":"MAIN"}}`, "feature/DEMO-7-export", "MAIN", "instead of creating a worktree"},
		{"another branch than answered", `{"is_error":false,"structured_output":{"branch":"feature/DEMO-7-export","worktree":"/w/web-DEMO-7"}}`, "main", "/w/web-DEMO-7", "is on main"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h, d := desktopHarness(t)
			web := h.clone(t, "web")
			out := strings.ReplaceAll(tt.out, "MAIN", web)
			h.run.out[strings.Join(append([]string{"claude"}, headlessArgs("DEMO-7", "Export users", web, h.home)...), " ")] = out
			if tt.branch != "" {
				h.inCheckout(tt.branch, strings.ReplaceAll(tt.top, "MAIN", web), "git@github.com:example-org/web.git")
			}
			if code := h.exec("herdr", "start", "DEMO-7", "--repo", "web", "--agent", "claude"); code == 0 {
				t.Fatal("exit 0, want the Start to fail")
			}
			if !strings.Contains(h.errOut.String(), "could not create the worktree of DEMO-7") || !strings.Contains(h.errOut.String(), tt.wantErr) {
				t.Errorf("stderr = %q, want %q", h.errOut, tt.wantErr)
			}
			if len(d.calls) != 0 {
				t.Errorf("calls = %q, want nothing opened", d.calls)
			}
			if links, _ := h.app.links().Load(); len(links) != 0 {
				t.Errorf("links = %+v, want nothing registered", links)
			}
		})
	}
}
