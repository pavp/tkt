package github

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/pavp/tkt/internal/ticket"
)

// ErrUnauthorized means GitHub rejected the gh token.
var ErrUnauthorized = errors.New("github rejected the token: run `gh auth login`")

// TokenFunc returns the token that can read repositories of owner.
type TokenFunc func(ctx context.Context, owner string) (string, error)

type Client struct {
	graphql string
	rest    string
	token   TokenFunc
	http    *http.Client
}

func New(token TokenFunc) *Client {
	return &Client{
		graphql: "https://api.github.com/graphql",
		rest:    "https://api.github.com",
		token:   token,
		http:    &http.Client{Timeout: 30 * time.Second},
	}
}

// WithEndpoints points the client at another server, mainly for tests.
func (c *Client) WithEndpoints(base string, h *http.Client) *Client {
	c.graphql = strings.TrimRight(base, "/") + "/graphql"
	c.rest = strings.TrimRight(base, "/")
	c.http = h
	return c
}

// CachedTokens memoizes gh tokens per account for the life of one command.
func CachedTokens(accountFor func(ctx context.Context, owner string) string, fetch func(ctx context.Context, account string) (string, error)) TokenFunc {
	var mu sync.Mutex
	cache := map[string]string{}
	return func(ctx context.Context, owner string) (string, error) {
		mu.Lock()
		defer mu.Unlock()
		account := accountFor(ctx, owner)
		if t, ok := cache[account]; ok {
			return t, nil
		}
		t, err := fetch(ctx, account)
		if err != nil {
			return "", err
		}
		cache[account] = t
		return t, nil
	}
}

func (c *Client) query(ctx context.Context, owner, q string, vars map[string]any, out any) error {
	tok, err := c.token(ctx, owner)
	if err != nil {
		return err
	}
	body, err := json.Marshal(map[string]any{"query": q, "variables": vars})
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.graphql, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+tok)
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("github graphql: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusUnauthorized {
		return ErrUnauthorized
	}
	if resp.StatusCode >= 300 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("github graphql: %d %s", resp.StatusCode, strings.TrimSpace(string(b)))
	}
	var env struct {
		Data   json.RawMessage `json:"data"`
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&env); err != nil {
		return fmt.Errorf("github graphql: decode: %w", err)
	}
	if len(env.Errors) > 0 && len(env.Data) == 0 {
		msgs := make([]string, len(env.Errors))
		for i, e := range env.Errors {
			msgs[i] = e.Message
		}
		return fmt.Errorf("github graphql: %s", strings.Join(msgs, "; "))
	}
	return json.Unmarshal(env.Data, out)
}

const prFields = `number title url state isDraft headRefName headRefOid reviewDecision
  author { login }
  repository { nameWithOwner }
  commits(last: 1) { nodes { commit { statusCheckRollup { state } } } }`

type prNode struct {
	Number         int    `json:"number"`
	Title          string `json:"title"`
	URL            string `json:"url"`
	State          string `json:"state"`
	HeadRefName    string `json:"headRefName"`
	HeadRefOid     string `json:"headRefOid"`
	ReviewDecision string `json:"reviewDecision"`
	Author         *struct {
		Login string `json:"login"`
	} `json:"author"`
	Repository struct {
		NameWithOwner string `json:"nameWithOwner"`
	} `json:"repository"`
	Commits struct {
		Nodes []struct {
			Commit struct {
				StatusCheckRollup *struct {
					State string `json:"state"`
				} `json:"statusCheckRollup"`
			} `json:"commit"`
		} `json:"nodes"`
	} `json:"commits"`
}

func (n prNode) toPR() ticket.PullRequest {
	pr := ticket.PullRequest{
		Repo:    n.Repository.NameWithOwner,
		Number:  n.Number,
		Title:   n.Title,
		URL:     n.URL,
		Branch:  n.HeadRefName,
		HeadSHA: n.HeadRefOid,
	}
	switch n.State {
	case "MERGED":
		pr.State = ticket.PRMerged
	case "CLOSED":
		pr.State = ticket.PRClosed
	default:
		pr.State = ticket.PROpen
	}
	switch n.ReviewDecision {
	case "APPROVED":
		pr.Review = ticket.ReviewApproved
	case "CHANGES_REQUESTED":
		pr.Review = ticket.ReviewChangesRequested
	case "REVIEW_REQUIRED":
		pr.Review = ticket.ReviewRequired
	}
	if n.Author != nil {
		pr.Author = n.Author.Login
	}
	if len(n.Commits.Nodes) > 0 && n.Commits.Nodes[0].Commit.StatusCheckRollup != nil {
		switch n.Commits.Nodes[0].Commit.StatusCheckRollup.State {
		case "SUCCESS":
			pr.Checks = ticket.ChecksPass
		case "FAILURE", "ERROR":
			pr.Checks = ticket.ChecksFail
		case "PENDING", "EXPECTED":
			pr.Checks = ticket.ChecksPending
		}
	}
	return pr
}

// PullRequests returns open and recently merged PRs of repos ("owner/name"), batching one GraphQL query
// per owner because each owner may need a different gh account.
func (c *Client) PullRequests(ctx context.Context, repos []string) ([]ticket.PullRequest, error) {
	byOwner := map[string][]string{}
	for _, r := range repos {
		owner, _, _ := strings.Cut(r, "/")
		byOwner[owner] = append(byOwner[owner], r)
	}
	owners := make([]string, 0, len(byOwner))
	for o := range byOwner {
		owners = append(owners, o)
	}
	sort.Strings(owners)
	var out []ticket.PullRequest
	for _, owner := range owners {
		var b strings.Builder
		b.WriteString("query {\n")
		for i, r := range byOwner[owner] {
			o, name, _ := strings.Cut(r, "/")
			fmt.Fprintf(&b, "  r%d: repository(owner: %q, name: %q) {\n", i, o, name)
			fmt.Fprintf(&b, "    open: pullRequests(states: OPEN, first: 50, orderBy: {field: UPDATED_AT, direction: DESC}) { nodes { %s } }\n", prFields)
			fmt.Fprintf(&b, "    merged: pullRequests(states: MERGED, first: 20, orderBy: {field: UPDATED_AT, direction: DESC}) { nodes { %s } }\n", prFields)
			b.WriteString("  }\n")
		}
		b.WriteString("}")
		var data map[string]*struct {
			Open   struct{ Nodes []prNode } `json:"open"`
			Merged struct{ Nodes []prNode } `json:"merged"`
		}
		if err := c.query(ctx, owner, b.String(), nil, &data); err != nil {
			return nil, fmt.Errorf("pull requests of %s: %w", owner, err)
		}
		for i, r := range byOwner[owner] {
			repo := data[fmt.Sprintf("r%d", i)]
			if repo == nil {
				return nil, fmt.Errorf("repository %s not found or not visible to the gh account", r)
			}
			for _, n := range append(repo.Open.Nodes, repo.Merged.Nodes...) {
				out = append(out, n.toPR())
			}
		}
	}
	return out, nil
}

// ReviewRequests returns open PRs in repos ("owner/name") that request the token user's review. direct
// keeps only requests addressed to the user; otherwise requests to the user's teams count too.
func (c *Client) ReviewRequests(ctx context.Context, repos []string, direct bool) ([]ticket.PullRequest, error) {
	byOwner := map[string][]string{}
	for _, r := range repos {
		owner, _, _ := strings.Cut(r, "/")
		byOwner[owner] = append(byOwner[owner], r)
	}
	owners := make([]string, 0, len(byOwner))
	for o := range byOwner {
		owners = append(owners, o)
	}
	sort.Strings(owners)
	qualifier := "review-requested:@me"
	if direct {
		qualifier = "user-review-requested:@me"
	}
	q := `query($q: String!) { search(query: $q, type: ISSUE, first: 50) { nodes { ... on PullRequest { ` + prFields + ` } } } }`
	var out []ticket.PullRequest
	for _, owner := range owners {
		terms := []string{"is:pr", "is:open", "archived:false", qualifier}
		for _, r := range byOwner[owner] {
			terms = append(terms, "repo:"+r)
		}
		var data struct {
			Search struct {
				Nodes []prNode `json:"nodes"`
			} `json:"search"`
		}
		if err := c.query(ctx, owner, q, map[string]any{"q": strings.Join(terms, " ")}, &data); err != nil {
			return nil, fmt.Errorf("review requests in %s: %w", owner, err)
		}
		for _, n := range data.Search.Nodes {
			if n.Number != 0 {
				out = append(out, n.toPR())
			}
		}
	}
	return out, nil
}

// keysPerSearch stays within GitHub search's limit of five OR operators per query.
const keysPerSearch = 5

// PullRequestsForKeys finds PRs mentioning any of keys in repos, open or finished, however old: the
// recent-PR listing misses tickets whose PRs merged long ago.
func (c *Client) PullRequestsForKeys(ctx context.Context, repos, keys []string) ([]ticket.PullRequest, error) {
	byOwner := map[string][]string{}
	for _, r := range repos {
		owner, _, _ := strings.Cut(r, "/")
		byOwner[owner] = append(byOwner[owner], r)
	}
	owners := make([]string, 0, len(byOwner))
	for o := range byOwner {
		owners = append(owners, o)
	}
	sort.Strings(owners)
	q := `query($q: String!) { search(query: $q, type: ISSUE, first: 50) { nodes { ... on PullRequest { ` + prFields + ` } } } }`
	var out []ticket.PullRequest
	for _, owner := range owners {
		for start := 0; start < len(keys); start += keysPerSearch {
			batch := keys[start:min(start+keysPerSearch, len(keys))]
			terms := []string{"is:pr"}
			for _, r := range byOwner[owner] {
				terms = append(terms, "repo:"+r)
			}
			terms = append(terms, strings.Join(batch, " OR "))
			var data struct {
				Search struct {
					Nodes []prNode `json:"nodes"`
				} `json:"search"`
			}
			if err := c.query(ctx, owner, q, map[string]any{"q": strings.Join(terms, " ")}, &data); err != nil {
				return nil, fmt.Errorf("pull requests for %s: %w", strings.Join(batch, ", "), err)
			}
			for _, n := range data.Search.Nodes {
				if n.Number != 0 {
					out = append(out, n.toPR())
				}
			}
		}
	}
	return out, nil
}
