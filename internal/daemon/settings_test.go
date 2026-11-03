package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/pavp/tkt/internal/board"
	"github.com/pavp/tkt/internal/config"
)

// fakeSettings keeps config.toml in memory and Jira's answers for Settings, counting saves and Jira calls.
type fakeSettings struct {
	cfg       config.Config
	saves     int
	jiraCalls int
	jiraErr   error
}

func (f *fakeSettings) load() (config.Config, error) { return f.cfg.Clone(), nil }

func (f *fakeSettings) save(c config.Config) (config.Config, error) {
	f.cfg, f.saves = c.Clone(), f.saves+1
	return c, nil
}

func (f *fakeSettings) statuses(_ context.Context, space string) ([]board.Status, error) {
	f.jiraCalls++
	if space != "DEMO" {
		return nil, nil
	}
	return []board.Status{{Name: "To Do", Category: "new"}, {Name: "In Progress", Category: "indeterminate"}, {Name: "Testing", Category: "indeterminate"},
		{Name: "Blocked", Category: "indeterminate"}, {Name: "Done", Category: "done"}}, f.jiraErr
}

func (f *fakeSettings) boards(_ context.Context, space string) ([]config.TeamBoard, error) {
	f.jiraCalls++
	return []config.TeamBoard{{ID: 7, Name: "Web"}, {ID: 9, Name: "Core"}, {ID: 11, Name: "Mobile"}}, f.jiraErr
}

func (f *fakeSettings) fields(context.Context) ([]string, error) {
	f.jiraCalls++
	return []string{"Product", "Sprint", "Story Points"}, f.jiraErr
}

func settingsConfig() config.Config {
	return config.Config{
		RepoRoots: []string{"~/src"},
		Jira:      config.Jira{Site: "example.atlassian.net", Email: "dev@example.com", LabelField: "Product"},
		GitHub:    config.GitHub{Accounts: map[string]string{"example-org": "dev-work"}},
		Context:   config.Context{Comments: 5},
		Spaces: []config.Space{
			{Key: "DEMO", Name: "Example Product", StatusOverrides: map[string]string{"Testing": "review"}, StatusMarks: map[string]string{"Blocked": "none"},
				Repos: []config.Repo{{Name: "example-org/web", Path: "~/src/web", Base: "main"}}, Ignored: []string{"example-org/old"}, TeamBoards: []config.TeamBoard{{ID: 7, Name: "Web"}}},
			{Key: "OPS"},
		},
	}
}

func settingsServer(t *testing.T, f *fakeSettings) *Server {
	t.Helper()
	seed := board.Snapshot{FetchedAt: fetched, Repos: map[string][]string{"DEMO": {"example-org/web", "example-org/api", "example-org/old"}}}
	s, err := New(Options{Dir: t.TempDir(), Version: "1.2.3", Sync: (&fakeSync{}).sync, Interval: time.Hour, Seed: &seed,
		Settings: f.load, SaveSettings: f.save, JiraStatuses: f.statuses, JiraBoards: f.boards, JiraFields: f.fields})
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func patchSettings(s *Server, body, contentType, origin string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPatch, "/v1/settings", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+s.token)
	req.Header.Set("Content-Type", contentType)
	if origin != "" {
		req.Header.Set("Origin", origin)
	}
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	return rec
}

func TestContractSettings(t *testing.T) {
	tests := []struct{ name, path, fixture string }{
		{"the five sections", "/v1/settings", "settings"},
		{"a space's statuses", "/v1/settings/statuses?space=DEMO", "settings-statuses"},
		{"a space's scrum boards", "/v1/settings/boards?space=DEMO", "settings-boards"},
		{"the site's fields", "/v1/settings/fields", "settings-fields"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := settingsServer(t, &fakeSettings{cfg: settingsConfig()})
			assertContract(t, s.Handler(), tt.path, s.token, tt.fixture)
		})
	}
}

func TestSettingsLookupsRefuseAnUnknownSpace(t *testing.T) {
	for _, path := range []string{"/v1/settings/statuses", "/v1/settings/statuses?space=NOPE", "/v1/settings/boards?space=demo"} {
		f := &fakeSettings{cfg: settingsConfig()}
		s := settingsServer(t, f)
		if code, body := get(t, s.Handler(), http.MethodGet, path, s.token); code != http.StatusBadRequest || body["code"] != "bad_request" || f.jiraCalls != 0 {
			t.Errorf("GET %s = %d %v after %d Jira calls", path, code, body, f.jiraCalls)
		}
	}
	f := &fakeSettings{cfg: settingsConfig(), jiraErr: errors.New("jira: 503 Service Unavailable")}
	s := settingsServer(t, f)
	if code, body := get(t, s.Handler(), http.MethodGet, "/v1/settings/fields", s.token); code != http.StatusBadGateway || body["code"] != "jira_failed" {
		t.Errorf("fields with Jira down = %d %v", code, body)
	}
}

func TestPatchSettingsChangesOnlyWhatIsSent(t *testing.T) {
	f := &fakeSettings{cfg: settingsConfig()}
	s := settingsServer(t, f)
	rec := patchSettings(s, `{"label_field":" Team ","repo_roots":["~/src"," ~/work "],"spaces":{"DEMO":{
		"repos":[{"repo":"example-org/web","path":" ~/code/web "},{"repo":" example-org/api "}],
		"ignore":["example-org/api"],
		"team_boards":[9,7],
		"statuses":{"Testing":{"column":"in_progress"},"To Do":{"column":"review","mark":"star"},"Blocked":null}}}}`, "application/json", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("patch = %d %s", rec.Code, rec.Body)
	}
	want := settingsConfig()
	want.Jira.LabelField = "Team"
	want.RepoRoots = []string{"~/src", "~/work"}
	sp := &want.Spaces[0]
	sp.Repos = []config.Repo{{Name: "example-org/web", Path: "~/code/web", Base: "main"}, {Name: "example-org/api"}}
	sp.Ignored = []string{"example-org/old", "example-org/api"}
	sp.TeamBoards = []config.TeamBoard{{ID: 9, Name: "Core"}, {ID: 7, Name: "Web"}}
	sp.StatusOverrides = map[string]string{"To Do": "review"}
	sp.StatusMarks = map[string]string{"To Do": "star"}
	if !reflect.DeepEqual(f.cfg, want) || f.saves != 1 {
		t.Errorf("saved %d times:\n%+v\nwant\n%+v", f.saves, f.cfg, want)
	}
	var got SettingsView
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil || got.LabelField != "Team" || len(got.Spaces[0].TeamBoards) != 2 || len(got.Spaces[0].Inferred) != 0 {
		t.Errorf("answer = %s (%v)", rec.Body, err)
	}
	select {
	case <-s.syncNow:
	default:
		t.Error("a saved change asked for no sync")
	}

	f.jiraCalls = 0
	if rec := patchSettings(s, `{"points_field":"Story Points","spaces":{"DEMO":{"team_boards":[7]}}}`, "application/json", ""); rec.Code != http.StatusOK || f.cfg.Jira.PointsField != "Story Points" ||
		!reflect.DeepEqual(f.cfg.Spaces[0].TeamBoards, []config.TeamBoard{{ID: 7, Name: "Web"}}) || f.jiraCalls != 0 {
		t.Errorf("removing a board = %d %s after %d Jira calls, saved %+v", rec.Code, rec.Body, f.jiraCalls, f.cfg.Spaces[0].TeamBoards)
	}
}

func TestPatchSettingsRefusesWithoutWriting(t *testing.T) {
	tests := []struct {
		name, body, contentType, origin string
		code                            int
		message                         string
	}{
		{"an unknown field", `{"jira":{"site":"x"}}`, "application/json", "", http.StatusBadRequest, `unknown field "jira"`},
		{"a secret", `{"token":"abc"}`, "application/json", "", http.StatusBadRequest, `unknown field "token"`},
		{"an unknown space", `{"spaces":{"NOPE":{"repos":[]}}}`, "application/json", "", http.StatusBadRequest, `unknown space "NOPE"`},
		{"an unknown space field", `{"spaces":{"DEMO":{"name":"Other"}}}`, "application/json", "", http.StatusBadRequest, `unknown field "name"`},
		{"an unknown repository field", `{"spaces":{"DEMO":{"repos":[{"repo":"example-org/web","base":"dev"}]}}}`, "application/json", "", http.StatusBadRequest, `unknown field "base"`},
		{"a null", `{"repo_roots":null}`, "application/json", "", http.StatusBadRequest, "repo_roots must not be null"},
		{"a null repository list", `{"spaces":{"DEMO":{"repos":null}}}`, "application/json", "", http.StatusBadRequest, "spaces.DEMO.repos must not be null"},
		{"an empty folder", `{"repo_roots":["~/src","  "]}`, "application/json", "", http.StatusBadRequest, "Folder is required"},
		{"a repository without a name", `{"spaces":{"DEMO":{"repos":[{"path":"~/src/web"}]}}}`, "application/json", "", http.StatusBadRequest, "Repository (owner/name) is required"},
		{"a repository not owner/name", `{"spaces":{"DEMO":{"repos":[{"repo":"web"}]}}}`, "application/json", "", http.StatusBadRequest, `repo "web" must look like owner/name`},
		{"a board the space lacks", `{"spaces":{"DEMO":{"team_boards":[7,99]}}}`, "application/json", "", http.StatusBadRequest, "no scrum board of DEMO has the id 99"},
		{"a board twice", `{"spaces":{"DEMO":{"team_boards":[9,9]}}}`, "application/json", "", http.StatusBadRequest, "Core is already a team board of DEMO"},
		{"an unknown status", `{"spaces":{"DEMO":{"statuses":{"Shipped":{"column":"done"}}}}}`, "application/json", "", http.StatusBadRequest, `DEMO has no status "Shipped"`},
		{"an unknown column", `{"spaces":{"DEMO":{"statuses":{"To Do":{"column":"later"}}}}}`, "application/json", "", http.StatusBadRequest, `unknown column "later" (use todo, in_progress, review or done)`},
		{"an unknown mark", `{"spaces":{"DEMO":{"statuses":{"To Do":{"mark":"heart"}}}}}`, "application/json", "", http.StatusBadRequest, `unknown mark "heart"`},
		{"an empty status change", `{"spaces":{"DEMO":{"statuses":{"To Do":{}}}}}`, "application/json", "", http.StatusBadRequest, `"To Do" needs a column, a mark, or null`},
		{"ignoring a configured repository", `{"spaces":{"DEMO":{"ignore":["example-org/web"]}}}`, "application/json", "", http.StatusBadRequest, "example-org/web is not a repository inferred for DEMO"},
		{"a wrong type", `{"label_field":3}`, "application/json", "", http.StatusBadRequest, "the body is not a settings change"},
		{"a second value", `{"label_field":"A"}{"label_field":"B"}`, "application/json", "", http.StatusBadRequest, "the body is not a settings change"},
		{"not JSON", `{"label_field":"A"}`, "text/plain", "", http.StatusUnsupportedMediaType, "send the request as application/json"},
		{"another origin", `{"label_field":"A"}`, "application/json", "http://evil.example", http.StatusForbidden, "requests from another origin are refused"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := &fakeSettings{cfg: settingsConfig()}
			s := settingsServer(t, f)
			rec := patchSettings(s, tt.body, tt.contentType, tt.origin)
			var body struct{ Code, Message string }
			_ = json.Unmarshal(rec.Body.Bytes(), &body)
			if rec.Code != tt.code || !strings.Contains(body.Message, tt.message) {
				t.Errorf("patch = %d %s, want %d %q", rec.Code, rec.Body, tt.code, tt.message)
			}
			if f.saves != 0 || !reflect.DeepEqual(f.cfg, settingsConfig()) {
				t.Errorf("a refused patch saved %d times: %+v", f.saves, f.cfg)
			}
		})
	}
}

func TestPatchSettingsTellsTheOtherClients(t *testing.T) {
	f := &fakeSettings{cfg: settingsConfig()}
	d, err := New(Options{Dir: t.TempDir(), Version: "1.2.3", Sync: (&scriptedSync{}).sync, Interval: time.Hour, Seed: &board.Snapshot{FetchedAt: fetched}, Settings: f.load, SaveSettings: f.save})
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(d.Handler())
	t.Cleanup(srv.Close)
	c := joined(t, d, srv)
	next(t, c)
	if rec := patchSettings(d, `{"points_field":"Story Points"}`, "application/json", ""); rec.Code != http.StatusOK {
		t.Fatalf("patch = %d %s", rec.Code, rec.Body)
	}
	if e := next(t, c); e.Type != "settings.updated" || !strings.Contains(string(e.Data), `"points_field":"Story Points"`) {
		t.Errorf("event = %s %s", e.Type, e.Data)
	}
}

func TestSettingsRoutes(t *testing.T) {
	s := newServer(t, &fakeSync{}, nil)
	for _, path := range []string{"/v1/settings", "/v1/settings/statuses?space=DEMO", "/v1/settings/boards?space=DEMO", "/v1/settings/fields"} {
		if code, body := get(t, s.Handler(), http.MethodGet, path, s.token); code != http.StatusNotFound || body["code"] != "not_found" {
			t.Errorf("GET %s without settings = %d %v", path, code, body)
		}
	}
	if rec := patchSettings(s, `{"label_field":"A"}`, "application/json", ""); rec.Code != http.StatusNotFound {
		t.Errorf("patch without settings = %d %s", rec.Code, rec.Body)
	}
	f := &fakeSettings{cfg: settingsConfig()}
	s, err := New(Options{Dir: t.TempDir(), Version: "1.2.3", Sync: (&fakeSync{}).sync, Interval: time.Hour, Settings: f.load, SaveSettings: f.save})
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/v1/settings/statuses?space=DEMO", "/v1/settings/boards?space=DEMO", "/v1/settings/fields"} {
		if code, body := get(t, s.Handler(), http.MethodGet, path, s.token); code != http.StatusNotFound || body["code"] != "not_found" {
			t.Errorf("GET %s without Jira lookups = %d %v", path, code, body)
		}
	}
	s = settingsServer(t, &fakeSettings{cfg: settingsConfig()})
	if code, _ := get(t, s.Handler(), http.MethodGet, "/v1/settings", ""); code != http.StatusUnauthorized {
		t.Errorf("settings without the token = %d", code)
	}
}
