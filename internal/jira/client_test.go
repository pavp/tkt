package jira

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type recorded struct {
	method, path, query, auth string
	body                      map[string]any
}

func server(t *testing.T, routes map[string]string) (*Client, *[]recorded) {
	t.Helper()
	var calls []recorded
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, ok := routes["GET /rest/api/3/field"]; !ok && r.Method == "GET" && r.URL.Path == "/rest/api/3/field" {
			_, _ = io.WriteString(w, "[]")
			return
		}
		rec := recorded{method: r.Method, path: r.URL.Path, query: r.URL.RawQuery, auth: r.Header.Get("Authorization")}
		if b, _ := io.ReadAll(r.Body); len(b) > 0 {
			_ = json.Unmarshal(b, &rec.body)
		}
		calls = append(calls, rec)
		key := r.Method + " " + r.URL.Path
		if at := r.URL.Query().Get("startAt"); at != "" && at != "0" {
			key += "#" + at
		}
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
 "assignee":{"displayName":"Ana López","accountId":"a1"},
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
	if first.Assignee != "Ana López" || got[1].Assignee != "" {
		t.Errorf("assignees = %q, %q", first.Assignee, got[1].Assignee)
	}
	if fields, _ := (*calls)[0].body["fields"].([]any); !slices.Contains(fields, any("assignee")) {
		t.Errorf("the search should ask for the assignee: %v", fields)
	}
	if got[1].Parent != "DEMO-12" || !got[1].Subtask {
		t.Errorf("second = %+v", got[1])
	}
	body := (*calls)[0].body
	if body["jql"] != `sprint in openSprints() AND project in ("DEMO", "OPS") AND (assignee = currentUser() OR (statusCategory = Done AND assignee WAS currentUser())) ORDER BY updated DESC` {
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

func TestFieldIDFindsAFieldByNameOnce(t *testing.T) {
	c, calls := server(t, map[string]string{
		"GET /rest/api/3/field":       `[{"id":"summary","name":"Summary","schema":{}},{"id":"customfield_10020","name":"Sprint","schema":{"custom":"com.pyxis.greenhopper.jira:gh-sprint"}},{"id":"customfield_10136","name":"Product","schema":{}}]`,
		"POST /rest/api/3/search/jql": `{"issues":[],"isLast":true}`,
	})
	ctx := context.Background()
	for _, name := range []string{"Product", "product"} {
		if id, err := c.FieldID(ctx, name); err != nil || id != "customfield_10136" {
			t.Errorf("FieldID(%q) = %q, %v", name, id, err)
		}
	}
	if _, err := c.FieldID(ctx, "Team"); !errors.Is(err, ErrFieldNotFound) {
		t.Errorf("missing field: %v", err)
	}
	if _, err := c.Search(ctx, "x", ListFields); err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, call := range *calls {
		if call.path == "/rest/api/3/field" {
			n++
		}
	}
	if n != 1 {
		t.Errorf("field list read %d times, want once", n)
	}
}

func TestFieldNamesAreSortedAndUnique(t *testing.T) {
	c, _ := server(t, map[string]string{
		"GET /rest/api/3/field": `[{"id":"summary","name":"Summary"},{"id":"customfield_1","name":"Product"},{"id":"customfield_2","name":"Product"},{"id":"customfield_3","name":"Cloud Product"},{"id":"x","name":""}]`,
	})
	got, err := c.FieldNames(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(got, ",") != "Cloud Product,Product,Summary" {
		t.Errorf("FieldNames = %v", got)
	}
}

func TestFieldText(t *testing.T) {
	tests := []struct {
		raw, want string
	}{
		{`{"self":"x","value":"User Access Center","id":"10330"}`, "User Access Center"},
		{`"Payments"`, "Payments"},
		{`[{"value":"Web"},{"value":"API"}]`, "Web, API"},
		{`{"name":"Team Orion"}`, "Team Orion"},
		{`{"displayName":"Alex"}`, "Alex"},
		{`null`, ""},
		{``, ""},
		{`42`, ""},
	}
	for _, tt := range tests {
		if got := FieldText(json.RawMessage(tt.raw)); got != tt.want {
			t.Errorf("FieldText(%s) = %q, want %q", tt.raw, got, tt.want)
		}
	}
}

func TestSprintFieldIsDiscoveredAndParsed(t *testing.T) {
	c, calls := server(t, map[string]string{
		"GET /rest/api/3/field":       `[{"id":"summary","schema":{}},{"id":"customfield_10020","schema":{"custom":"com.pyxis.greenhopper.jira:gh-sprint"}}]`,
		"POST /rest/api/3/search/jql": `{"issues":[{"key":"DEMO-1","fields":{"summary":"x","customfield_10020":[{"name":"DEMO S41","state":"closed","endDate":"2026-10-09T17:00:00.000Z"},{"id":42,"name":"DEMO S42","state":"active","endDate":"2026-10-23T17:00:00.000Z"}]}}],"isLast":true}`,
	})
	got, err := c.Search(context.Background(), "x", ListFields)
	if err != nil {
		t.Fatal(err)
	}
	if len(got[0].Sprints) != 2 || got[0].Sprints[1] != (Sprint{ID: 42, Space: "DEMO", Name: "DEMO S42", State: "active", End: time.Date(2026, 10, 23, 17, 0, 0, 0, time.UTC)}) {
		t.Errorf("sprints = %+v", got[0].Sprints)
	}
	fields := fmt.Sprint((*calls)[1].body["fields"])
	if !strings.Contains(fields, "customfield_10020") || strings.Contains(fields, " sprint") {
		t.Errorf("fields sent = %s", fields)
	}
	if _, err := c.Search(context.Background(), "x", ListFields); err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, call := range *calls {
		if call.path == "/rest/api/3/field" {
			n++
		}
	}
	if n != 1 {
		t.Errorf("field list fetched %d times, want once per client", n)
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

func TestDownload(t *testing.T) {
	c, calls := server(t, map[string]string{"GET /att/1": "PNGDATA"})
	var buf strings.Builder
	if err := c.Download(context.Background(), c.base+"/att/1", &buf, 100); err != nil {
		t.Fatal(err)
	}
	if buf.String() != "PNGDATA" || !strings.HasPrefix((*calls)[0].auth, "Basic ") {
		t.Errorf("got %q, auth %q", buf.String(), (*calls)[0].auth)
	}
	if err := c.Download(context.Background(), "https://evil.example.com/att/1", &buf, 100); err == nil || !strings.Contains(err.Error(), "outside") {
		t.Errorf("foreign host err = %v", err)
	}
	if len(*calls) != 1 {
		t.Error("credentials sent to a foreign host")
	}
	if err := c.Download(context.Background(), c.base+"/att/1", &buf, 3); err == nil || !strings.Contains(err.Error(), "larger than 3 bytes") {
		t.Errorf("size limit err = %v", err)
	}
}

func TestDevPullRequests(t *testing.T) {
	c, calls := server(t, map[string]string{
		"GET /rest/dev-status/latest/issue/summary": `{"summary":{"pullrequest":{"overall":{"count":2},"byInstanceType":{"oAuth-example.github":{"count":2}}}}}`,
		"GET /rest/dev-status/latest/issue/detail": `{"detail":[{"pullRequests":[
			{"id":"#593","name":"fix: DEMO-1 widths","url":"https://github.com/example-org/web/pull/593","status":"MERGED","repositoryName":"example-org/web","source":{"branch":"fix/demo-1-widths"}}]}]}`,
	})
	got, err := c.DevPullRequests(context.Background(), "101")
	if err != nil {
		t.Fatal(err)
	}
	want := []DevPullRequest{{Repo: "example-org/web", Number: 593, Title: "fix: DEMO-1 widths", URL: "https://github.com/example-org/web/pull/593", Branch: "fix/demo-1-widths", Status: "MERGED"}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %+v", got)
	}
	if _, err := c.DevPullRequests(context.Background(), "102"); err != nil {
		t.Fatal(err)
	}
	var paths []string
	for _, r := range *calls {
		paths = append(paths, r.path+"?"+r.query)
	}
	wantPaths := []string{
		"/rest/dev-status/latest/issue/summary?issueId=101",
		"/rest/dev-status/latest/issue/detail?applicationType=oAuth-example.github&dataType=pullrequest&issueId=101",
		"/rest/dev-status/latest/issue/detail?applicationType=oAuth-example.github&dataType=pullrequest&issueId=102",
	}
	if !reflect.DeepEqual(paths, wantPaths) {
		t.Errorf("calls = %v", paths)
	}
}

func TestDevPullRequestsSkipsDetailWithoutPRs(t *testing.T) {
	c, calls := server(t, map[string]string{
		"GET /rest/dev-status/latest/issue/summary": `{"summary":{"pullrequest":{"overall":{"count":0},"byInstanceType":{}}}}`,
	})
	got, err := c.DevPullRequests(context.Background(), "101")
	if err != nil || got != nil || len(*calls) != 1 {
		t.Errorf("got %v, %v after %d calls", got, err, len(*calls))
	}
}

func TestDevPullRequestsIsSafeToCallConcurrently(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/summary") {
			_, _ = io.WriteString(w, `{"summary":{"pullrequest":{"overall":{"count":1},"byInstanceType":{"oAuth-example.github":{"count":1}}}}}`)
			return
		}
		_, _ = io.WriteString(w, `{"detail":[{"pullRequests":[{"id":"#1","url":"https://github.com/example-org/web/pull/1","status":"OPEN","repositoryName":"example-org/web"}]}]}`)
	}))
	t.Cleanup(srv.Close)
	c := New(srv.URL, "dev@example.com", "t").WithHTTPClient(srv.Client())
	var wg sync.WaitGroup
	errs := make([]error, 8)
	for i := range errs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, errs[i] = c.DevPullRequests(context.Background(), fmt.Sprint(100+i))
		}()
	}
	wg.Wait()
	for _, err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
}

func TestRequestsRetryWhenJiraThrottles(t *testing.T) {
	cases := []struct {
		name      string
		throttled int
		wantErr   bool
		wantCalls int
	}{
		{"once", 1, false, 2},
		{"until giving up", 10, true, 3},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var calls atomic.Int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if int(calls.Add(1)) <= tc.throttled {
					w.Header().Set("Retry-After", "0")
					w.WriteHeader(http.StatusTooManyRequests)
					return
				}
				_, _ = io.WriteString(w, `{"summary":{"pullrequest":{"overall":{"count":0}}}}`)
			}))
			t.Cleanup(srv.Close)
			c := New(srv.URL, "dev@example.com", "t").WithHTTPClient(srv.Client())
			_, err := c.DevPullRequests(context.Background(), "101")
			if (err != nil) != tc.wantErr || int(calls.Load()) != tc.wantCalls {
				t.Errorf("err %v after %d calls", err, calls.Load())
			}
			if tc.wantErr && !strings.Contains(err.Error(), "429") {
				t.Errorf("err = %v", err)
			}
		})
	}
}

func TestTeamSprintJQL(t *testing.T) {
	want := `sprint in (42, 43) AND project in ("DEMO", "OPS") AND (assignee != currentUser() OR assignee IS EMPTY) AND NOT (statusCategory = Done AND assignee WAS currentUser()) ORDER BY updated DESC`
	if got := TeamSprintJQL([]string{"DEMO", "OPS"}, []int{42, 43}); got != want {
		t.Errorf("jql = %s", got)
	}
}

func TestBoardsListsASpacesScrumBoards(t *testing.T) {
	c, calls := server(t, map[string]string{
		"GET /rest/agile/1.0/board":   `{"isLast":false,"values":[{"id":418,"name":"User Access Center","type":"scrum"},{"id":12,"name":"Ops kanban","type":"kanban"}]}`,
		"GET /rest/agile/1.0/board#2": `{"isLast":true,"values":[{"id":5205,"name":"Authorization Center","type":"scrum"}]}`,
	})
	got, err := c.Boards(context.Background(), "DEMO")
	if err != nil {
		t.Fatal(err)
	}
	want := []Board{{ID: 418, Name: "User Access Center"}, {ID: 5205, Name: "Authorization Center"}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("boards = %+v", got)
	}
	if q := (*calls)[0].query; !strings.Contains(q, "projectKeyOrId=DEMO") || !strings.Contains(q, "type=scrum") {
		t.Errorf("query = %s", q)
	}
}

func TestActiveSprints(t *testing.T) {
	c, calls := server(t, map[string]string{
		"GET /rest/agile/1.0/board/5205/sprint": `{"isLast":true,"values":[{"id":11807,"state":"active"}]}`,
	})
	got, err := c.ActiveSprints(context.Background(), 5205)
	if err != nil || !reflect.DeepEqual(got, []int{11807}) {
		t.Errorf("sprints = %v, %v", got, err)
	}
	if !strings.Contains((*calls)[0].query, "state=active") {
		t.Errorf("query = %s", (*calls)[0].query)
	}
}

func TestTransitionsDescribeTheirForms(t *testing.T) {
	c, calls := server(t, map[string]string{
		"GET /rest/api/3/issue/DEMO-12/transitions": `{"transitions":[
 {"id":"31","name":"Commit Review","hasScreen":false,"to":{"name":"In Review"}},
 {"id":"41","name":"In progress","hasScreen":true,"to":{"name":"In Progress"},"fields":{
  "customfield_10028":{"required":false,"name":"Story Points","schema":{"type":"number","custom":"com.atlassian.jira.plugin.system.customfieldtypes:float"}},
  "customfield_10020":{"required":false,"name":"Sprint","schema":{"type":"array","custom":"com.pyxis.greenhopper.jira:gh-sprint"}}}},
 {"id":"42","name":"Estimate","hasScreen":true,"to":{"name":"Estimated"},"fields":{"customfield_10028":{"required":true,"hasDefaultValue":false,"name":"Story Points"}}},
 {"id":"51","name":"Blocked","hasScreen":true,"to":{"name":"Blocked"},"fields":{
  "customfield_10300":{"required":false,"name":"Reason","schema":{"type":"option","custom":"com.atlassian.jira.plugin.system.customfieldtypes:select"},"allowedValues":[{"id":"9001","value":"Waiting on a decision"},{"id":"9002","value":"No access"}]},
  "customfield_10301":{"required":true,"name":"Owner","schema":{"type":"user"}},
  "issuelinks":{"required":false,"name":"Linked issues","schema":{"type":"array","items":"issuelinks"}}}},
 {"id":"61","name":"Ceased","hasScreen":true,"to":{"name":"Ceased"}},
 {"id":"71","name":"Link","hasScreen":true,"to":{"name":"Linked"},"fields":{"issuelinks":{"required":true,"name":"Linked issues","schema":{"type":"array","items":"issuelinks"}}}}]}`,
	})
	got, err := c.Transitions(context.Background(), "DEMO-12")
	if err != nil {
		t.Fatal(err)
	}
	want := []Transition{
		{ID: "31", Name: "Commit Review", To: "In Review"},
		{ID: "41", Name: "In progress", To: "In Progress"},
		{ID: "42", Name: "Estimate", To: "Estimated", NeedsForm: true},
		{ID: "51", Name: "Blocked", To: "Blocked", Fields: []Field{
			{ID: "customfield_10301", Name: "Owner", Kind: KindUser, Required: true},
			{ID: "customfield_10300", Name: "Reason", Kind: KindOption, Options: []Option{{ID: "9001", Value: "Waiting on a decision"}, {ID: "9002", Value: "No access"}}},
		}},
		{ID: "61", Name: "Ceased", To: "Ceased"},
		{ID: "71", Name: "Link", To: "Linked", NeedsForm: true},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("transitions = %+v", got)
	}
	if !strings.Contains((*calls)[0].query, "expand=transitions.fields") {
		t.Errorf("query = %s", (*calls)[0].query)
	}
}

func TestTransitionPostsTheTransitionID(t *testing.T) {
	c, calls := server(t, map[string]string{"POST /rest/api/3/issue/DEMO-12/transitions": ""})
	if err := c.Transition(context.Background(), "DEMO-12", "31", nil); err != nil {
		t.Fatal(err)
	}
	if got := (*calls)[0].body; !reflect.DeepEqual(got, map[string]any{"transition": map[string]any{"id": "31"}}) {
		t.Errorf("body = %v", got)
	}
	values := []Value{{Field: "customfield_10300", Kind: KindOption, ID: "9001"}, {Field: "customfield_10301", Kind: KindUser, ID: "acc-7"}}
	if err := c.Transition(context.Background(), "DEMO-12", "51", values); err != nil {
		t.Fatal(err)
	}
	want := map[string]any{"transition": map[string]any{"id": "51"}, "fields": map[string]any{
		"customfield_10300": map[string]any{"id": "9001"},
		"customfield_10301": map[string]any{"accountId": "acc-7"},
	}}
	if got := (*calls)[1].body; !reflect.DeepEqual(got, want) {
		t.Errorf("body with values = %v", got)
	}
}
