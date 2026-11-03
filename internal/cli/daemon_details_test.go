package cli

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/pavp/tkt/internal/daemon"
	"github.com/pavp/tkt/internal/ticket"
	"github.com/pavp/tkt/internal/tui"
	"github.com/pavp/tkt/internal/workspace"
)

// callDaemon serves one request through a daemon built from o, with the token the daemon made in dir.
func callDaemon(t *testing.T, o daemon.Options, method, path, body string) (int, string) {
	t.Helper()
	o.Dir, o.Interval = t.TempDir(), time.Hour
	s, err := daemon.New(o)
	if err != nil {
		t.Fatal(err)
	}
	token, err := daemon.Token(o.Dir)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	return rec.Code, rec.Body.String()
}

func TestDaemonBoardStateWritesTheFileTheBoardWrites(t *testing.T) {
	h := newHarness(t, baseConfig)
	a := h.app
	off := false
	b := &boardActions{a: a, ctx: context.Background()}
	if msg := b.SaveState(tui.BoardState{HiddenRepos: []string{"example-org/api"}, Collapsed: []string{"sprint:12"}, View: tui.ViewColumns, TeamPRs: &off})(); msg != nil {
		t.Fatalf("the board's save = %v", msg)
	}
	o := daemon.Options{Sync: a.daemonSync, SavedState: a.loadBoardState, SaveState: a.saveBoardState}
	code, body := callDaemon(t, o, http.MethodPatch, "/v1/board/state", `{"team":true,"hidden_boards":["Web","Core"],"view":"list"}`)
	if code != http.StatusOK {
		t.Fatalf("patch = %d %s", code, body)
	}
	file := filepath.Join(a.Env.DataDir(), "board-state.json")
	got, err := os.ReadFile(file)
	want := `{"hidden_repos":["example-org/api"],"hidden_boards":["Core","Web"],"collapsed":["sprint:12"],"view":"list","team":true,"team_prs":false}`
	if err != nil || string(got) != want {
		t.Errorf("board-state.json = %s, %v\nwant %s", got, err, want)
	}
	if info, err := os.Stat(file); err != nil || info.Mode().Perm() != 0o600 {
		t.Errorf("state file = %v, %v", info, err)
	}
	if entries, _ := os.ReadDir(a.Env.DataDir()); len(entries) != 1 {
		t.Errorf("the data directory should hold only the state file: %v", entries)
	}
	if st := a.loadBoardState(); a.boardView("") != tui.ViewList || !st.Team || st.TeamPRsShown() {
		t.Errorf("the board reopens with %+v", st)
	}
	if code, body := callDaemon(t, o, http.MethodGet, "/v1/board/state", ""); code != http.StatusOK ||
		body != `{"view":"list","team":true,"hidden_boards":["Core","Web"],"hidden_repos":["example-org/api"],"collapsed":["sprint:12"],"team_prs":false,"backlog_merged":false}`+"\n" {
		t.Errorf("state = %d %s", code, body)
	}
}

func TestDaemonContextIsWhatTheBoardCopies(t *testing.T) {
	h := showHarness(t)
	h.app.Env.GOOS = "darwin"
	h.run.out["pbcopy"] = ""
	c, err := h.app.LoadConfig()
	if err != nil {
		t.Fatal(err)
	}
	b := &boardActions{a: h.app, ctx: context.Background(), c: c}
	if msg := b.CopyContext("DEMO-1")(); msg != tui.StatusMsg("✓ copied DEMO-1's context as Markdown") {
		t.Fatalf("copy = %v", msg)
	}
	copied := h.run.stdin["pbcopy"]
	code, body := callDaemon(t, daemon.Options{Sync: h.app.daemonSync, Context: h.app.daemonContext}, http.MethodGet, "/v1/tickets/DEMO-1/context", "")
	var got struct{ Key, Text string }
	if err := json.Unmarshal([]byte(body), &got); err != nil || code != http.StatusOK || got.Key != "DEMO-1" || got.Text != copied || !strings.Contains(copied, "## Acceptance criteria") {
		t.Errorf("context = %d %s\ncopied %q", code, body, copied)
	}
}

func TestDaemonDetailCarriesTheDetailsPanel(t *testing.T) {
	h := showHarness(t)
	dir := t.TempDir()
	w := workspace.Work{Key: "DEMO-1", Repo: "example-org/web", Branch: "feature/DEMO-1-filter", Path: dir, Source: workspace.SourceAgent, LinkedAt: time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC)}
	if err := h.app.links().Add(w); err != nil {
		t.Fatal(err)
	}
	known := &ticket.Ticket{Key: "DEMO-1", PRs: []ticket.PullRequest{{Repo: "example-org/web", Number: 900, State: ticket.PROpen, Checks: ticket.ChecksFail, Yours: true}}}
	got, err := h.app.daemonDetail(context.Background(), "DEMO-1", known, "working")
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	var body map[string]json.RawMessage
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"ticket", "description", "acceptance_criteria", "attachments", "comments"} {
		if body[k] == nil {
			t.Errorf("the detail lost %s: %s", k, raw)
		}
	}
	if string(body["attention"]) != `["CI failing"]` || string(body["agent"]) != `"working"` {
		t.Errorf("attention %s, agent %s", body["attention"], body["agent"])
	}
	var work []workspace.Work
	if err := json.Unmarshal(body["work"], &work); err != nil || len(work) != 1 || work[0].Path != dir || work[0].Branch != w.Branch {
		t.Errorf("work = %s", body["work"])
	}
	quiet, _ := h.app.daemonDetail(context.Background(), "DEMO-1", &ticket.Ticket{Key: "DEMO-1"}, "")
	if raw, _ := json.Marshal(quiet); strings.Contains(string(raw), `"agent"`) || strings.Contains(string(raw), `"attention"`) {
		t.Errorf("a ticket needing nothing, without an agent: %s", raw)
	}
}
