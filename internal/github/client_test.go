package github

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/pavp/tkt/internal/ticket"
)

func pr(repo string, num int, branch, state, review, checks string) string {
	rollup := "null"
	if checks != "" {
		rollup = `{"state":"` + checks + `"}`
	}
	rv := "null"
	if review != "" {
		rv = `"` + review + `"`
	}
	return `{"number":` + itoa(num) + `,"title":"PR ` + itoa(num) + `","url":"https://github.com/` + repo + `/pull/` + itoa(num) +
		`","state":"` + state + `","headRefName":"` + branch + `","headRefOid":"sha` + itoa(num) + `","reviewDecision":` + rv +
		`,"author":{"login":"alex"},"repository":{"nameWithOwner":"` + repo + `"},"commits":{"nodes":[{"commit":{"statusCheckRollup":` + rollup + `}}]}}`
}

func itoa(n int) string {
	b, _ := json.Marshal(n)
	return string(b)
}

type gqlCall struct {
	auth, query string
	vars        map[string]any
}

func gqlServer(t *testing.T, respond func(q string) (int, string)) (string, *http.Client, *[]gqlCall) {
	t.Helper()
	var calls []gqlCall
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/graphql" {
			http.NotFound(w, r)
			return
		}
		var body struct {
			Query     string         `json:"query"`
			Variables map[string]any `json:"variables"`
		}
		b, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(b, &body)
		calls = append(calls, gqlCall{auth: r.Header.Get("Authorization"), query: body.Query, vars: body.Variables})
		code, resp := respond(body.Query)
		w.WriteHeader(code)
		_, _ = io.WriteString(w, resp)
	}))
	t.Cleanup(srv.Close)
	return srv.URL, srv.Client(), &calls
}

func staticTokens(m map[string]string) TokenFunc {
	return func(_ context.Context, owner string) (string, error) {
		if t, ok := m[owner]; ok {
			return t, nil
		}
		return "", errors.New("no token for " + owner)
	}
}

func TestPullRequestsBatchesPerOwner(t *testing.T) {
	base, h, calls := gqlServer(t, func(q string) (int, string) {
		if strings.Contains(q, `owner: "example-org"`) {
			return 200, `{"data":{
			  "r0":{"open":{"nodes":[` + pr("example-org/api", 4, "feature/DEMO-1-x", "OPEN", "CHANGES_REQUESTED", "SUCCESS") + `]},"merged":{"nodes":[]}},
			  "r1":{"open":{"nodes":[` + pr("example-org/web", 9, "feature/DEMO-1-x", "OPEN", "", "FAILURE") + `]},
			        "merged":{"nodes":[` + pr("example-org/web", 7, "fix/DEMO-2", "MERGED", "APPROVED", "SUCCESS") + `]}}}}`
		}
		return 200, `{"data":{"r0":{"open":{"nodes":[` + pr("other-org/tool", 1, "chore/x", "OPEN", "REVIEW_REQUIRED", "PENDING") + `]},"merged":{"nodes":[]}}}}`
	})
	c := New(staticTokens(map[string]string{"example-org": "work", "other-org": "personal"})).WithEndpoints(base, h)
	got, err := c.PullRequests(context.Background(), []string{"example-org/api", "example-org/web", "other-org/tool"})
	if err != nil {
		t.Fatal(err)
	}
	if len(*calls) != 2 {
		t.Fatalf("calls = %d, want one per owner", len(*calls))
	}
	if (*calls)[0].auth != "Bearer work" || (*calls)[1].auth != "Bearer personal" {
		t.Errorf("auth = %q, %q", (*calls)[0].auth, (*calls)[1].auth)
	}
	want := []ticket.PullRequest{
		{Repo: "example-org/api", Number: 4, Title: "PR 4", URL: "https://github.com/example-org/api/pull/4", Branch: "feature/DEMO-1-x", State: ticket.PROpen, Checks: ticket.ChecksPass, Review: ticket.ReviewChangesRequested, Author: "alex", HeadSHA: "sha4"},
		{Repo: "example-org/web", Number: 9, Title: "PR 9", URL: "https://github.com/example-org/web/pull/9", Branch: "feature/DEMO-1-x", State: ticket.PROpen, Checks: ticket.ChecksFail, Author: "alex", HeadSHA: "sha9"},
		{Repo: "example-org/web", Number: 7, Title: "PR 7", URL: "https://github.com/example-org/web/pull/7", Branch: "fix/DEMO-2", State: ticket.PRMerged, Checks: ticket.ChecksPass, Review: ticket.ReviewApproved, Author: "alex", HeadSHA: "sha7"},
		{Repo: "other-org/tool", Number: 1, Title: "PR 1", URL: "https://github.com/other-org/tool/pull/1", Branch: "chore/x", State: ticket.PROpen, Checks: ticket.ChecksPending, Review: ticket.ReviewRequired, Author: "alex", HeadSHA: "sha1"},
	}
	if len(got) != len(want) {
		t.Fatalf("got %d PRs: %+v", len(got), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("PR %d = %+v\nwant %+v", i, got[i], want[i])
		}
	}
}

func TestPullRequestsErrors(t *testing.T) {
	base, h, _ := gqlServer(t, func(string) (int, string) { return 200, `{"data":{"r0":null}}` })
	c := New(staticTokens(map[string]string{"example-org": "t"})).WithEndpoints(base, h)
	if _, err := c.PullRequests(context.Background(), []string{"example-org/gone"}); err == nil || !strings.Contains(err.Error(), "example-org/gone not found") {
		t.Errorf("missing repo err = %v", err)
	}
	base, h, _ = gqlServer(t, func(string) (int, string) { return 401, `{"message":"Bad credentials"}` })
	c = New(staticTokens(map[string]string{"example-org": "t"})).WithEndpoints(base, h)
	if _, err := c.PullRequests(context.Background(), []string{"example-org/web"}); !errors.Is(err, ErrUnauthorized) {
		t.Errorf("401 err = %v", err)
	}
	base, h, _ = gqlServer(t, func(string) (int, string) { return 200, `{"errors":[{"message":"Something broke"}]}` })
	c = New(staticTokens(map[string]string{"example-org": "t"})).WithEndpoints(base, h)
	if _, err := c.PullRequests(context.Background(), []string{"example-org/web"}); err == nil || !strings.Contains(err.Error(), "Something broke") {
		t.Errorf("graphql err = %v", err)
	}
	c = New(staticTokens(nil)).WithEndpoints(base, h)
	if _, err := c.PullRequests(context.Background(), []string{"example-org/web"}); err == nil || !strings.Contains(err.Error(), "no token for example-org") {
		t.Errorf("token err = %v", err)
	}
}

func TestReviewRequests(t *testing.T) {
	base, h, calls := gqlServer(t, func(string) (int, string) {
		return 200, `{"data":{"search":{"nodes":[` + pr("example-org/web", 820, "feature/DEMO-9", "OPEN", "REVIEW_REQUIRED", "SUCCESS") + `,{}]}}}`
	})
	c := New(staticTokens(map[string]string{"example-org": "t"})).WithEndpoints(base, h)
	got, err := c.ReviewRequests(context.Background(), []string{"example-org/web", "example-org/api"}, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Number != 820 || got[0].Checks != ticket.ChecksPass {
		t.Errorf("got %+v", got)
	}
	if q := (*calls)[0].vars["q"]; q != "is:pr is:open archived:false user-review-requested:@me repo:example-org/web repo:example-org/api" {
		t.Errorf("direct search = %v", q)
	}
	if _, err := c.ReviewRequests(context.Background(), []string{"example-org/web"}, false); err != nil {
		t.Fatal(err)
	}
	if q := (*calls)[1].vars["q"]; q != "is:pr is:open archived:false review-requested:@me repo:example-org/web" {
		t.Errorf("team search = %v", q)
	}
}

func TestCachedTokensFetchesOncePerAccount(t *testing.T) {
	fetches := 0
	tf := CachedTokens(func(owner string) string {
		if owner == "example-org" || owner == "example-two" {
			return "work"
		}
		return ""
	}, func(_ context.Context, account string) (string, error) {
		fetches++
		return "tok-" + account, nil
	})
	for _, o := range []string{"example-org", "example-two", "example-org", "someone"} {
		if _, err := tf(context.Background(), o); err != nil {
			t.Fatal(err)
		}
	}
	if fetches != 2 {
		t.Errorf("fetches = %d, want 2 (work + active)", fetches)
	}
}
