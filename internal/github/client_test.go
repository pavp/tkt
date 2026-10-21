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
		`,"author":{"login":"alex"},"repository":{"nameWithOwner":"` + repo + `"},"commits":{"nodes":[{"commit":{"statusCheckRollup":` + rollup + `}}]},` +
		`"latestOpinionatedReviews":{"nodes":[]}}`
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

func TestPullRequestsCountApprovals(t *testing.T) {
	reviews := `"latestOpinionatedReviews":{"nodes":[{"state":"APPROVED"},{"state":"CHANGES_REQUESTED"},{"state":"APPROVED"}]}`
	node := strings.Replace(pr("example-org/web", 9, "feature/DEMO-1-x", "OPEN", "REVIEW_REQUIRED", "SUCCESS"), `"latestOpinionatedReviews":{"nodes":[]}`, reviews, 1)
	base, h, calls := gqlServer(t, func(string) (int, string) {
		return 200, `{"data":{"r0":{"open":{"nodes":[` + node + `]},"merged":{"nodes":[]}}}}`
	})
	got, err := New(staticTokens(map[string]string{"example-org": "work"})).WithEndpoints(base, h).PullRequests(context.Background(), []string{"example-org/web"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Approvals != 2 {
		t.Errorf("PRs = %+v, want 2 approvals", got)
	}
	if !strings.Contains((*calls)[0].query, "latestOpinionatedReviews") {
		t.Errorf("query does not ask for reviews:\n%s", (*calls)[0].query)
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
	bot := strings.Replace(pr("example-org/web", 821, "dependabot/npm/x", "OPEN", "", ""), `{"login":"alex"}`, `{"login":"dependabot","__typename":"Bot"}`, 1)
	base, h, calls := gqlServer(t, func(string) (int, string) {
		return 200, `{"data":{"search":{"nodes":[` + pr("example-org/web", 820, "feature/DEMO-9", "OPEN", "REVIEW_REQUIRED", "SUCCESS") + `,` + bot + `,{}]}}}`
	})
	c := New(staticTokens(map[string]string{"example-org": "t"})).WithEndpoints(base, h)
	got, err := c.ReviewRequests(context.Background(), []string{"example-org/web", "example-org/api"}, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Number != 820 || got[0].Checks != ticket.ChecksPass || got[0].AuthorBot {
		t.Fatalf("got %+v", got)
	}
	if !got[1].AuthorBot || got[1].Author != "dependabot" {
		t.Errorf("bot author not marked: %+v", got[1])
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
	tf := CachedTokens(func(_ context.Context, owner string) string {
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

func TestPullRequestsForKeysBatchesFiveKeys(t *testing.T) {
	base, h, calls := gqlServer(t, func(string) (int, string) {
		return 200, `{"data":{"search":{"nodes":[` + pr("example-org/web", 569, "x", "MERGED", "", "SUCCESS") + `]}}}`
	})
	c := New(staticTokens(map[string]string{"example-org": "t"})).WithEndpoints(base, h)
	keys := []string{"DEMO-1", "DEMO-2", "DEMO-3", "DEMO-4", "DEMO-5", "DEMO-6"}
	got, err := c.PullRequestsForKeys(context.Background(), []string{"example-org/web", "example-org/api"}, keys)
	if err != nil {
		t.Fatal(err)
	}
	if len(*calls) != 2 || len(got) != 2 {
		t.Fatalf("calls = %d, prs = %d", len(*calls), len(got))
	}
	if q := (*calls)[0].vars["q"]; q != "is:pr repo:example-org/web repo:example-org/api DEMO-1 OR DEMO-2 OR DEMO-3 OR DEMO-4 OR DEMO-5" {
		t.Errorf("first query = %v", q)
	}
	if q := (*calls)[1].vars["q"]; q != "is:pr repo:example-org/web repo:example-org/api DEMO-6" {
		t.Errorf("second query = %v", q)
	}
}

func TestCachedTokensResolvesEachOwnerOnce(t *testing.T) {
	lookups := 0
	tf := CachedTokens(func(context.Context, string) string {
		lookups++
		return ""
	}, func(context.Context, string) (string, error) { return "tok", nil })
	for i := 0; i < 10; i++ {
		if _, err := tf(context.Background(), "example-org"); err != nil {
			t.Fatal(err)
		}
	}
	if lookups != 1 {
		t.Errorf("account resolved %d times, want once per owner", lookups)
	}
}

func TestReposListsAnOwnersRepositories(t *testing.T) {
	page := 0
	base, h, calls := gqlServer(t, func(q string) (int, string) {
		page++
		if page == 1 {
			return 200, `{"data":{"repositoryOwner":{"repositories":{"nodes":[{"nameWithOwner":"example-org/web"}],"pageInfo":{"hasNextPage":true,"endCursor":"c1"}}}}}`
		}
		return 200, `{"data":{"repositoryOwner":{"repositories":{"nodes":[{"nameWithOwner":"example-org/api"}],"pageInfo":{"hasNextPage":false,"endCursor":"c2"}}}}}`
	})
	c := New(staticTokens(map[string]string{"example-org": "work"})).WithEndpoints(base, h)
	got, err := c.Repos(context.Background(), "example-org")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(got, ",") != "example-org/web,example-org/api" {
		t.Errorf("repos = %v", got)
	}
	if len(*calls) != 2 || (*calls)[0].auth != "Bearer work" || (*calls)[0].vars["owner"] != "example-org" || (*calls)[1].vars["after"] != "c1" {
		t.Errorf("calls = %+v", *calls)
	}
}

func TestReposUnknownOwner(t *testing.T) {
	base, h, _ := gqlServer(t, func(string) (int, string) { return 200, `{"data":{"repositoryOwner":null}}` })
	c := New(staticTokens(map[string]string{"nobody": "t"})).WithEndpoints(base, h)
	if _, err := c.Repos(context.Background(), "nobody"); err == nil || err.Error() != "no GitHub organization or user named nobody" {
		t.Errorf("err = %v", err)
	}
}
