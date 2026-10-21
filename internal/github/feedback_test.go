package github

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
)

func restServer(t *testing.T, routes map[string]string) (string, *http.Client, *[]string) {
	t.Helper()
	var hits []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits = append(hits, r.Method+" "+r.URL.Path+" "+r.Header.Get("Authorization"))
		if r.URL.Path == "/graphql" {
			_, _ = io.WriteString(w, routes["graphql"])
			return
		}
		body, ok := routes[r.URL.Path]
		if !ok {
			http.NotFound(w, r)
			return
		}
		_, _ = io.WriteString(w, body)
	}))
	t.Cleanup(srv.Close)
	return srv.URL, srv.Client(), &hits
}

func TestFailedChecksWithLogTail(t *testing.T) {
	longLog := strings.Repeat("noise line\n", 1000) + "--- FAIL: TestRange (0.01s)\n    expected ?from=2026-09-01\n"
	base, h, hits := restServer(t, map[string]string{
		"/repos/example-org/web/commits/abc/check-runs": `{"check_runs":[
			{"id":11,"name":"unit-tests","conclusion":"failure","html_url":"https://github.com/example-org/web/runs/11","app":{"slug":"github-actions"},"output":{"title":"1 failing","summary":""}},
			{"id":12,"name":"lint","conclusion":"success","app":{"slug":"github-actions"}},
			{"id":13,"name":"external-ci","conclusion":"timed_out","details_url":"https://ci.example.com/13","app":{"slug":"other"}}]}`,
		"/repos/example-org/web/actions/jobs/11/logs": longLog,
	})
	c := New(staticTokens(map[string]string{"example-org": "tok"})).WithEndpoints(base, h)
	got, err := c.FailedChecks(context.Background(), "example-org/web", "abc")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("failed checks = %+v", got)
	}
	if got[0].Name != "unit-tests" || got[0].Summary != "1 failing" || got[0].URL != "https://github.com/example-org/web/runs/11" {
		t.Errorf("first = %+v", got[0])
	}
	if !strings.HasSuffix(got[0].LogTail, "expected ?from=2026-09-01") || len(got[0].LogTail) > logTailBytes {
		t.Errorf("log tail (%d bytes) = ...%q", len(got[0].LogTail), got[0].LogTail[len(got[0].LogTail)-40:])
	}
	if strings.HasPrefix(got[0].LogTail, "oise") {
		t.Error("tail starts mid-line")
	}
	if got[1].Name != "external-ci" || got[1].URL != "https://ci.example.com/13" || got[1].LogTail != "" {
		t.Errorf("second = %+v", got[1])
	}
	for _, hit := range *hits {
		if !strings.HasSuffix(hit, "Bearer tok") {
			t.Errorf("unauthenticated request: %s", hit)
		}
	}
}

func TestFailedChecksErrors(t *testing.T) {
	base, h, _ := restServer(t, map[string]string{})
	c := New(staticTokens(map[string]string{"example-org": "tok"})).WithEndpoints(base, h)
	if _, err := c.FailedChecks(context.Background(), "example-org/web", "nope"); err == nil || !strings.Contains(err.Error(), "404") {
		t.Errorf("err = %v", err)
	}
}

func TestChangeRequests(t *testing.T) {
	base, h, _ := restServer(t, map[string]string{
		"graphql": `{"data":{"repository":{"pullRequest":{"reviews":{"nodes":[
			{"author":{"login":"alex"},"body":"Please cover the expired-token path.","comments":{"nodes":[{"path":"src/auth.ts","line":42,"body":"Missing test"}]}}]}}}}}`,
	})
	c := New(staticTokens(map[string]string{"example-org": "tok"})).WithEndpoints(base, h)
	got, err := c.ChangeRequests(context.Background(), "example-org/web", 45)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Author != "alex" || len(got[0].Comments) != 1 || got[0].Comments[0].Line != 42 {
		t.Errorf("got %+v", got)
	}
	base, h, _ = restServer(t, map[string]string{"graphql": `{"data":{"repository":{"pullRequest":null}}}`})
	c = New(staticTokens(map[string]string{"example-org": "tok"})).WithEndpoints(base, h)
	if _, err := c.ChangeRequests(context.Background(), "example-org/web", 1); err == nil || !strings.Contains(err.Error(), "not found") {
		t.Errorf("err = %v", err)
	}
}

func TestUnresolvedThreads(t *testing.T) {
	base, h, calls := gqlServer(t, func(string) (int, string) {
		return 200, `{"data":{"repository":{"pullRequest":{"reviewThreads":{"nodes":[
		  {"isResolved":false,"path":"web/form.go","line":7,"comments":{"nodes":[{"author":{"login":"sam"},"body":"Rename this"},{"author":{"login":"alex"},"body":"Why?"}]}},
		  {"isResolved":true,"path":"web/old.go","line":3,"comments":{"nodes":[{"author":{"login":"sam"},"body":"Done"}]}}]}}}}}`
	})
	got, err := New(staticTokens(map[string]string{"example-org": "work"})).WithEndpoints(base, h).UnresolvedThreads(context.Background(), "example-org/web", 40)
	if err != nil {
		t.Fatal(err)
	}
	want := []ReviewThread{{Path: "web/form.go", Line: 7, Comments: []ThreadComment{{Author: "sam", Body: "Rename this"}, {Author: "alex", Body: "Why?"}}}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("threads = %+v", got)
	}
	if v := (*calls)[0].vars; v["owner"] != "example-org" || v["name"] != "web" || v["number"] != float64(40) {
		t.Errorf("vars = %v", v)
	}
}
