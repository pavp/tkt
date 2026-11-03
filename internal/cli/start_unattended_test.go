package cli

import (
	"context"
	"errors"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/pavp/tkt/internal/daemon"
	"github.com/pavp/tkt/internal/herdrport"
)

// daemonDesktop is the harness of a daemon that reaches both Herdrs, Herdr desktop faked by d, and was started from
// a Herdr 0.9.5 terminal whose environment it inherited.
func daemonDesktop(t *testing.T) (*harness, *desktopHerdr) {
	t.Helper()
	h, d := desktopHarness(t)
	delete(h.env, "HC_TERMINAL_ID")
	h.env["HERDR_ENV"], h.env["HERDR_PANE_ID"] = "1", "w1:p1"
	h.app.Stdin = strings.NewReader("1\n1\n1\ny\n")
	h.app.HerdrRunning = func(context.Context) (bool, bool) { return true, true }
	h.app.herdrOf = func(f herdrFlavor) HerdrAPI {
		if f == herdrDesktop {
			return d
		}
		return failingHerdr{errors.New("Herdr 0.9.5 must not be used")}
	}
	return h, d
}

func TestAnUnattendedStartNamesTheAnswerItNeeds(t *testing.T) {
	no := false
	tests := []struct {
		name  string
		req   daemon.StartRequest
		setup func(h *harness, d *desktopHerdr)
		need  string
	}{
		{"several worktrees and no agent in one", daemon.StartRequest{Agent: "claude"}, func(h *harness, d *desktopHerdr) {
			h.clone(t, "web")
			h.run.out["git worktree list --porcelain"] = "worktree /src/web-DEMO-7-a\nHEAD a\nbranch refs/heads/feature/DEMO-7-a\n\n" +
				"worktree /src/web-DEMO-7-b\nHEAD b\nbranch refs/heads/feature/DEMO-7-b\n"
		}, "worktree"},
		{"several repositories", daemon.StartRequest{Agent: "claude"}, func(*harness, *desktopHerdr) {}, "repos"},
		{"a repository not cloned", daemon.StartRequest{Repos: []string{"web"}, Agent: "claude"}, func(*harness, *desktopHerdr) {}, "clone"},
		{"several agents", daemon.StartRequest{Repos: []string{"web"}, Clone: &no}, func(h *harness, _ *desktopHerdr) { h.clone(t, "web") }, "agent"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h, d := daemonDesktop(t)
			tt.setup(h, d)
			tt.req.Herdr = "desktop"
			settled := false
			err := h.app.daemonStart(context.Background(), "DEMO-7", tt.req, func() { settled = true })
			if need := (daemon.NeedsAnswerError{}); !errors.As(err, &need) || need.Need != tt.need {
				t.Fatalf("daemonStart = %v, want it to need %s", err, tt.need)
			}
			if settled || len(d.calls) > 0 {
				t.Errorf("settled %v, Herdr calls %q: nothing may start before every answer is in", settled, d.calls)
			}
			if strings.Contains(h.errOut.String(), "1)") || strings.Contains(h.errOut.String(), "?") {
				t.Errorf("an unattended start asked on the terminal:\n%s", h.errOut)
			}
		})
	}
}

func TestAnUnattendedStartTakesTheCloneAnswer(t *testing.T) {
	yes, no := true, false
	tests := []struct {
		name    string
		clone   *bool
		wantErr string
	}{
		{"clone", &yes, ""},
		{"do not clone", &no, "no repository to start in"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h, _ := daemonDesktop(t)
			dest := filepath.Join(h.home, "src", "web")
			h.run.out["gh repo clone example-org/web "+dest] = ""
			err := h.app.daemonStart(context.Background(), "DEMO-7", daemon.StartRequest{Herdr: "desktop", Repos: []string{"web"}, Agent: "claude", Clone: tt.clone}, func() {})
			cloned := strings.Contains(strings.Join(h.run.calls, "\n"), "gh repo clone")
			if cloned != (tt.clone == &yes) {
				t.Errorf("cloned = %v", cloned)
			}
			if tt.wantErr == "" && err != nil && strings.Contains(err.Error(), "not cloned") || tt.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tt.wantErr)) {
				t.Errorf("daemonStart = %v, want %q", err, tt.wantErr)
			}
		})
	}
}

func TestAnUnattendedStartOpensTheAgentInTheHerdrNamed(t *testing.T) {
	h, d := daemonDesktop(t)
	web := h.clone(t, "web")
	settledAt := -1
	err := h.app.daemonStart(context.Background(), "DEMO-7", daemon.StartRequest{Herdr: "desktop", Repos: []string{"web"}, Agent: "claude"}, func() { settledAt = len(d.calls) })
	if err != nil {
		t.Fatalf("daemonStart: %v\n%s", err, h.errOut)
	}
	if settledAt != 0 {
		t.Errorf("settled after %d Herdr calls, want before the first", settledAt)
	}
	if len(d.calls) == 0 || d.calls[0] != `workspace "" `+web || !strings.Contains(strings.Join(d.calls, "\n"), "start claude t1") {
		t.Errorf("Herdr desktop calls = %q", d.calls)
	}
	if strings.Contains(strings.Join(d.calls, "\n"), "w1:p1") {
		t.Errorf("the agent opened beside the pane the daemon's environment names: %q", d.calls)
	}
}

func TestAnUnattendedStartFocusesARunningAgentWithoutSettling(t *testing.T) {
	h, d := daemonDesktop(t)
	h.clone(t, "web")
	h.run.out["git worktree list --porcelain"] = "worktree /src/web-DEMO-7-a\nHEAD a\nbranch refs/heads/feature/DEMO-7-a\n"
	d.wss = []herdrport.Workspace{{ID: "sp3", Label: "web-DEMO-7-a", Folder: "/src/web-DEMO-7-a"}}
	settled := false
	if err := h.app.daemonStart(context.Background(), "DEMO-7", daemon.StartRequest{Herdr: "desktop"}, func() { settled = true }); err != nil {
		t.Fatal(err)
	}
	if want := []string{"focus workspace sp3"}; !reflect.DeepEqual(d.calls, want) || settled {
		t.Errorf("calls %q settled %v, want %q unsettled", d.calls, settled, want)
	}
}

func TestAnUnattendedStartNeedsTheHerdrRunning(t *testing.T) {
	h, _ := daemonDesktop(t)
	h.app.HerdrRunning = func(context.Context) (bool, bool) { return true, false }
	err := h.app.daemonStart(context.Background(), "DEMO-7", daemon.StartRequest{Herdr: "desktop", Repos: []string{"web"}, Agent: "claude"}, func() {})
	if !errors.Is(err, daemon.ErrHerdrUnavailable) || !strings.Contains(err.Error(), "Herdr desktop is not running") {
		t.Errorf("daemonStart = %v, want Herdr desktop unavailable", err)
	}
	if _, err := h.app.daemonStartChoices(context.Background(), "DEMO-7", "desktop"); !errors.Is(err, daemon.ErrHerdrUnavailable) {
		t.Errorf("daemonStartChoices = %v, want Herdr desktop unavailable", err)
	}
}

func TestDaemonStartChoicesAreTheModalsWithTheNamedHerdrsAgents(t *testing.T) {
	h, _ := daemonDesktop(t)
	for _, k := range desktopKinds {
		h.run.missing[k] = k != "claude" && k != "opencode"
	}
	st := h.app.loadState()
	st.LastRepos["DEMO"], st.LastAgent = []string{"example-org/api"}, "opencode"
	h.app.saveState(st)
	got, err := h.app.daemonStartChoices(context.Background(), "DEMO-7", "desktop")
	want := daemon.StartChoices{Opens: true, Repos: []daemon.StartRepo{{Name: "example-org/web"}, {Name: "example-org/api", LastUsed: true, Checked: true}},
		Agents: []string{"claude", "opencode"}, LastAgent: "opencode"}
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Errorf("daemonStartChoices = %+v, %v\nwant %+v", got, err, want)
	}
}

func TestTheTerminalStartStillAsks(t *testing.T) {
	tests := []struct {
		name, stdin string
		args        []string
		asked       string
		wantCode    int
	}{
		{"the repositories", "2\n", []string{"herdr", "start", "DEMO-7", "--agent", "claude"}, "Start in which repositories? (e.g. 1,2) [1] ", 0},
		{"whether to clone", "n\n", []string{"herdr", "start", "DEMO-7", "--repo", "web", "--agent", "claude"}, "example-org/web is not cloned locally. Clone it into ", 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h, _ := desktopHarness(t)
			h.clone(t, "api")
			h.app.Stdin = strings.NewReader(tt.stdin)
			if code := h.exec(tt.args...); code != tt.wantCode || !strings.Contains(h.errOut.String(), tt.asked) {
				t.Errorf("exit %d, want %d; stderr:\n%s\nwant it to ask %q", code, tt.wantCode, h.errOut, tt.asked)
			}
		})
	}
}
