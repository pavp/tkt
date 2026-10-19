package jira

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type recorded struct {
	method, path, query, auth string
	body                      map[string]any
}

func server(t *testing.T, routes map[string]string) (*Client, *[]recorded) {
	t.Helper()
	var calls []recorded
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rec := recorded{method: r.Method, path: r.URL.Path, query: r.URL.RawQuery, auth: r.Header.Get("Authorization")}
		if b, _ := io.ReadAll(r.Body); len(b) > 0 {
			_ = json.Unmarshal(b, &rec.body)
		}
		calls = append(calls, rec)
		key := r.Method + " " + r.URL.Path
		if tok, ok := rec.body["nextPageToken"].(string); ok {
			key += "#" + tok
		}
		body, ok := routes[key]
		if !ok {
			http.Error(w, `{"errorMessages":["no route `+key+`"]}`, http.StatusNotFound)
			return
		}
		if strings.HasPrefix(body, "STATUS ") {
			code := 0
			switch body {
			case "STATUS 401":
				code = http.StatusUnauthorized
			case "STATUS 400":
				code = http.StatusBadRequest
			}
			w.WriteHeader(code)
			_, _ = io.WriteString(w, `{"errorMessages":["Field 'x' does not exist"],"errors":{"jql":"bad"}}`)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, body)
	}))
	t.Cleanup(srv.Close)
	return New(srv.URL, "dev@example.com", "tok").WithHTTPClient(srv.Client()), &calls
}

const issueJSON = `{"key":"DEMO-12","fields":{
 "summary":"Filter accounts by date",
 "issuetype":{"name":"Story","subtask":false},
 "status":{"name":"In Progress","statusCategory":{"key":"indeterminate"}},
 "updated":"2026-10-18T09:30:00.000+0200",
 "description":{"type":"doc","version":1,"content":[]},
 "attachment":[{"filename":"mock.png","mimeType":"image/png","size":10,"content":"https://example.atlassian.net/att/1"}],
 "customfield_10042":{"type":"doc","version":1,"content":[]}}}`

func TestSearchPaginatesAndDecodes(t *testing.T) {
	c, calls := server(t, map[string]string{
		"POST /rest/api/3/search/jql":    `{"issues":[` + issueJSON + `],"nextPageToken":"p2","isLast":false}`,
		"POST /rest/api/3/search/jql#p2": `{"issues":[{"key":"DEMO-13","fields":{"summary":"Sub","issuetype":{"name":"Sub-task","subtask":true},"status":{"name":"To Do","statusCategory":{"key":"new"}},"parent":{"key":"DEMO-12"}}}],"isLast":true}`,
	})
	got, err := c.Search(context.Background(), MySprintJQL([]string{"DEMO", "OPS"}), ListFields)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d issues", len(got))
	}
	first := got[0]
	if first.Key != "DEMO-12" || first.Summary != "Filter accounts by date" || first.Type != "Story" || first.Status != "In Progress" || first.StatusCategory != "indeterminate" {
		t.Errorf("first = %+v", first)
	}
	if first.Updated.IsZero() {
		t.Error("updated not parsed")
	}
	if got[1].Parent != "DEMO-12" || !got[1].Subtask {
		t.Errorf("second = %+v", got[1])
	}
	body := (*calls)[0].body
	if body["jql"] != `assignee = currentUser() AND sprint in openSprints() AND project in ("DEMO", "OPS") ORDER BY updated DESC` {
		t.Errorf("jql = %v", body["jql"])
	}
	if !strings.HasPrefix((*calls)[0].auth, "Basic ") {
		t.Error("no basic auth")
	}
	if (*calls)[1].body["nextPageToken"] != "p2" {
		t.Error("second page not requested with its token")
	}
}

func TestIssueFetchesExtraFields(t *testing.T) {
	c, calls := server(t, map[string]string{"GET /rest/api/3/issue/DEMO-12": issueJSON})
	is, err := c.Issue(context.Background(), "DEMO-12", "customfield_10042")
	if err != nil {
		t.Fatal(err)
	}
	if len(is.Attachments) != 1 || is.Attachments[0].Filename != "mock.png" {
		t.Errorf("attachments = %+v", is.Attachments)
	}
	if _, ok := is.Fields["customfield_10042"]; !ok {
		t.Error("extra field missing")
	}
	if len(is.Description) == 0 {
		t.Error("description missing")
	}
	if !strings.Contains((*calls)[0].query, "customfield_10042") || !strings.Contains((*calls)[0].query, "description") {
		t.Errorf("query = %s", (*calls)[0].query)
	}
}

func TestErrors(t *testing.T) {
	c, _ := server(t, map[string]string{
		"GET /rest/api/3/myself":      "STATUS 401",
		"POST /rest/api/3/search/jql": "STATUS 400",
	})
	if _, err := c.Myself(context.Background()); !errors.Is(err, ErrUnauthorized) {
		t.Errorf("401 err = %v", err)
	}
	_, err := c.Search(context.Background(), "bad", ListFields)
	if err == nil || !strings.Contains(err.Error(), "400 Field 'x' does not exist") || !strings.Contains(err.Error(), "jql: bad") {
		t.Errorf("400 err = %v", err)
	}
}

func TestCommentsNewestFirst(t *testing.T) {
	c, calls := server(t, map[string]string{
		"GET /rest/api/3/issue/DEMO-12/comment": `{"comments":[{"author":{"displayName":"Alex"},"created":"2026-10-17T10:00:00.000+0000","body":{"type":"doc"}}]}`,
	})
	got, err := c.Comments(context.Background(), "DEMO-12", 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Author != "Alex" || got[0].Created.IsZero() {
		t.Errorf("comments = %+v", got)
	}
	if !strings.Contains((*calls)[0].query, "orderBy=-created") || !strings.Contains((*calls)[0].query, "maxResults=5") {
		t.Errorf("query = %s", (*calls)[0].query)
	}
	none, err := c.Comments(context.Background(), "DEMO-12", 0)
	if err != nil || none != nil {
		t.Errorf("zero comments = %v, %v", none, err)
	}
}

func TestStatusesDeduplicates(t *testing.T) {
	c, _ := server(t, map[string]string{
		"GET /rest/api/3/project/DEMO/statuses": `[{"statuses":[{"name":"To Do","statusCategory":{"key":"new"}},{"name":"Code Review","statusCategory":{"key":"indeterminate"}}]},{"statuses":[{"name":"To Do","statusCategory":{"key":"new"}}]}]`,
	})
	got, err := c.Statuses(context.Background(), "DEMO")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[1] != (Status{Name: "Code Review", Category: "indeterminate"}) {
		t.Errorf("statuses = %+v", got)
	}
}

func TestActiveSprints(t *testing.T) {
	c, calls := server(t, map[string]string{
		"GET /rest/agile/1.0/board":          `{"values":[{"id":7},{"id":8}]}`,
		"GET /rest/agile/1.0/board/7/sprint": `{"values":[{"name":"DEMO S42","endDate":"2026-10-23T17:00:00.000Z"}]}`,
		"GET /rest/agile/1.0/board/8/sprint": `{"values":[{"name":"DEMO S42","endDate":"2026-10-23T17:00:00.000Z"}]}`,
	})
	got, err := c.ActiveSprints(context.Background(), "DEMO")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Name != "DEMO S42" || got[0].Space != "DEMO" || got[0].End.IsZero() {
		t.Errorf("sprints = %+v", got)
	}
	if !strings.Contains((*calls)[0].query, "projectKeyOrId=DEMO") || !strings.Contains((*calls)[1].query, "state=active") {
		t.Errorf("queries = %s / %s", (*calls)[0].query, (*calls)[1].query)
	}
}

func TestProjectsAndMyself(t *testing.T) {
	c, _ := server(t, map[string]string{
		"GET /rest/api/3/project/search": `{"values":[{"key":"DEMO","name":"Demo"}]}`,
		"GET /rest/api/3/myself":         `{"accountId":"1","displayName":"Dev","emailAddress":"dev@example.com"}`,
	})
	ps, err := c.Projects(context.Background())
	if err != nil || len(ps) != 1 || ps[0].Key != "DEMO" {
		t.Errorf("projects = %+v, %v", ps, err)
	}
	u, err := c.Myself(context.Background())
	if err != nil || u.DisplayName != "Dev" {
		t.Errorf("myself = %+v, %v", u, err)
	}
	if c.BrowseURL("DEMO-1") != c.base+"/browse/DEMO-1" {
		t.Error("browse URL")
	}
}
