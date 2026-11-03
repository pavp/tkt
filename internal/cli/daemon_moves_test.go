package cli

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/pavp/tkt/internal/daemon"
	"github.com/pavp/tkt/internal/jira"
)

func TestDaemonMovesAndAssignsThroughJira(t *testing.T) {
	h, _ := listHarness(t)
	var mu sync.Mutex
	var got []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		mu.Lock()
		got = append(got, strings.TrimSpace(r.Method+" "+r.URL.RequestURI()+" "+string(body)))
		mu.Unlock()
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/rest/api/3/issue/DEMO-123/transitions":
			_, _ = io.WriteString(w, `{"transitions":[{"id":"51","name":"Block","to":{"name":"Blocked"},"fields":{"customfield_10300":{"required":true,"name":"Reason","schema":{"type":"option"},"allowedValues":[{"id":"9001","value":"Waiting on a decision"}]}}}]}`)
		case r.URL.Path == "/rest/api/3/myself":
			_, _ = io.WriteString(w, `{"accountId":"acc-me","displayName":"Ada Lovelace"}`)
		case r.URL.Path == "/rest/api/3/user/assignable/search":
			_, _ = io.WriteString(w, `[{"accountId":"acc-ada","displayName":"Ada Lovelace","accountType":"atlassian"},{"accountId":"acc-bot","displayName":"Ada Bot","accountType":"app"}]`)
		default:
			w.WriteHeader(http.StatusNoContent)
		}
	}))
	defer srv.Close()
	h.app.NewJira = func(_, email, token string) JiraAPI { return jira.New(srv.URL, email, token) }
	a := h.app
	dir := t.TempDir()
	s, err := daemon.New(daemon.Options{Dir: dir, Sync: a.daemonSync, Interval: time.Hour,
		Transitions: a.daemonTransitions, Transition: a.daemonTransition, Users: a.daemonUsers, Assign: a.daemonAssign})
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

	steps := []struct {
		method, path, body string
		code               int
		jira               []string
	}{
		{http.MethodPost, "/v1/tickets/DEMO-123/transitions", `{"id":"51","values":{"customfield_10300":"9001"},"comment":"Waiting for keys"}`, http.StatusOK, []string{
			"GET /rest/api/3/issue/DEMO-123/transitions?expand=transitions.fields",
			`POST /rest/api/3/issue/DEMO-123/transitions {"fields":{"customfield_10300":{"id":"9001"}},"transition":{"id":"51"},"update":{"comment":[{"add":{"body":{"content":[{"content":[{"text":"Waiting for keys","type":"text"}],"type":"paragraph"}],"type":"doc","version":1}}}]}}`,
		}},
		{http.MethodGet, "/v1/tickets/DEMO-123/users?query=ada", "", http.StatusOK, []string{
			"GET /rest/api/3/user/assignable/search?issueKey=DEMO-123&maxResults=20&query=ada",
		}},
		{http.MethodPut, "/v1/tickets/DEMO-123/assignee", `{"to":"me"}`, http.StatusOK, []string{
			"GET /rest/api/3/myself",
			`PUT /rest/api/3/issue/DEMO-123/assignee {"accountId":"acc-me"}`,
		}},
		{http.MethodPut, "/v1/tickets/DEMO-123/assignee", `{"to":"nobody"}`, http.StatusOK, []string{
			`PUT /rest/api/3/issue/DEMO-123/assignee {"accountId":null}`,
		}},
	}
	for _, st := range steps {
		mu.Lock()
		got = nil
		mu.Unlock()
		code, body := call(st.method, st.path, st.body)
		mu.Lock()
		if code != st.code || strings.Join(got, "\n") != strings.Join(st.jira, "\n") {
			t.Errorf("%s %s = %d %s\nJira got:\n%s\nwant:\n%s", st.method, st.path, code, body, strings.Join(got, "\n"), strings.Join(st.jira, "\n"))
		}
		mu.Unlock()
		if st.method == http.MethodGet && !strings.Contains(body, `"users":[{"id":"acc-ada","name":"Ada Lovelace"}]`) {
			t.Errorf("users = %s, want only Ada Lovelace, apps left out", body)
		}
	}
}
