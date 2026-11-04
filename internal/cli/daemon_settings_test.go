package cli

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/pavp/tkt/internal/board"
	"github.com/pavp/tkt/internal/config"
	"github.com/pavp/tkt/internal/daemon"
	"github.com/pavp/tkt/internal/jira"
	"github.com/pavp/tkt/internal/ticket"
)

const settingsTOML = baseConfig + `path = "~/src/web"

[[spaces]]
key = "OPS"
`

// settingsJira answers the Jira lookups Settings makes, with fictitious data.
func settingsJira(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/rest/api/3/project/DEMO/statuses":
			_, _ = io.WriteString(w, `[{"statuses":[{"name":"To Do","statusCategory":{"key":"new"}},{"name":"Testing","statusCategory":{"key":"indeterminate"}}]}]`)
		case r.URL.Path == "/rest/agile/1.0/board":
			_, _ = io.WriteString(w, `{"isLast":true,"values":[{"id":7,"name":"Web","type":"scrum"},{"id":9,"name":"Core","type":"scrum"}]}`)
		case r.URL.Path == "/rest/api/3/field":
			_, _ = io.WriteString(w, `[{"id":"customfield_10010","name":"Product"},{"id":"customfield_10016","name":"Story Points"}]`)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func settingsHarness(t *testing.T) *harness {
	t.Helper()
	h := newHarness(t, settingsTOML)
	h.env["XDG_DATA_HOME"] = h.home + "/data"
	h.creds["jira:example.atlassian.net:dev@example.com"] = "tok"
	srv := settingsJira(t)
	h.app.NewJira = func(_, email, token string) JiraAPI { return jira.New(srv.URL, email, token) }
	return h
}

func TestDaemonSettingsWriteWhatTheTUIsSettingsWrite(t *testing.T) {
	tui := settingsHarness(t)
	c, err := tui.app.LoadConfig()
	if err != nil {
		t.Fatal(err)
	}
	b := &boardActions{a: tui.app, c: c, ctx: context.Background()}
	edit := b.Config()
	edit.RepoRoots = append(edit.RepoRoots, "~/work")
	edit.Jira.LabelField = "Product"
	sp := &edit.Spaces[0]
	sp.Repos = append(sp.Repos, config.Repo{Name: "example-org/api", Path: "~/work/api"})
	sp.TeamBoards = append(sp.TeamBoards, config.TeamBoard{ID: 9, Name: "Core"})
	board.SetStatusColumn(sp, board.Status{Name: "Testing", Category: "indeterminate"}, ticket.InReview)
	board.SetStatusMark(sp, board.Status{Name: "To Do", Category: "new"}, ticket.MarkStar)
	if msg := b.SaveConfig(b.Config(), edit)(); msg == nil {
		t.Fatal("the TUI's save answered nothing")
	}
	want, err := os.ReadFile(tui.app.ConfigPath())
	if err != nil {
		t.Fatal(err)
	}

	h := settingsHarness(t)
	a := h.app
	dir := t.TempDir()
	s, err := daemon.New(daemon.Options{Dir: dir, Sync: a.daemonSync, Interval: time.Hour,
		Settings: a.savedConfig, SaveSettings: a.saveConfig, JiraStatuses: a.daemonStatuses, JiraBoards: a.daemonBoards, JiraFields: a.daemonFields})
	if err != nil {
		t.Fatal(err)
	}
	token, err := daemon.Token(dir)
	if err != nil {
		t.Fatal(err)
	}
	call := func(method, path, body string) (int, string) {
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		s.Handler().ServeHTTP(rec, req)
		return rec.Code, rec.Body.String()
	}
	if code, body := call(http.MethodPatch, "/v1/settings", `{"repo_roots":["~/src","~/work"],"label_field":"Product","spaces":{"DEMO":{
		"repos":[{"repo":"example-org/web","path":"~/src/web"},{"repo":"example-org/api","path":"~/work/api"}],
		"team_boards":[9],"statuses":{"Testing":{"column":"review"},"To Do":{"mark":"star"}}}}}`); code != http.StatusOK {
		t.Fatalf("patch = %d %s", code, body)
	}
	got, err := os.ReadFile(a.ConfigPath())
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(want) {
		t.Errorf("the API wrote:\n%s\nthe TUI writes:\n%s", got, want)
	}
	if fi, err := os.Stat(a.ConfigPath()); err != nil || fi.Mode().Perm() != 0o600 {
		t.Errorf("config.toml mode = %v (%v), want 0600", fi.Mode().Perm(), err)
	}

	lookups := []struct{ path, want string }{
		{"/v1/settings/statuses?space=DEMO", `{"name":"Testing","category":"indeterminate","column":"review","column_auto":false,"mark":"diamond","mark_auto":true}`},
		{"/v1/settings/boards?space=DEMO", `"boards":[{"id":7,"name":"Web"},{"id":9,"name":"Core"}]`},
		{"/v1/settings/fields", `{"fields":["Product","Story Points"]}`},
		{"/v1/settings", `"team_boards":[{"id":9,"name":"Core"}]`},
	}
	for _, l := range lookups {
		if code, body := call(http.MethodGet, l.path, ""); code != http.StatusOK || !strings.Contains(body, l.want) {
			t.Errorf("GET %s = %d %s, want %s", l.path, code, body, l.want)
		}
	}
	if code, body := call(http.MethodGet, "/v1/settings", ""); strings.Contains(body, "tok") || strings.Contains(body, "dev@example.com") || code != http.StatusOK {
		t.Errorf("settings show the account: %d %s", code, body)
	}
}
