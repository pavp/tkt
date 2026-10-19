package github

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strings"

	"github.com/pavp/tkt/internal/execx"
)

var accountLine = regexp.MustCompile(`Logged in to github\.com account (\S+)`)

// LoggedInAccounts lists gh's github.com accounts, the active one first. When any account has a problem
// gh exits non-zero and prints the whole status to stderr, which the runner folds into its error.
func LoggedInAccounts(ctx context.Context, run execx.Runner) []string {
	out, err := run.Run(ctx, "", "gh", []string{"auth", "status", "--hostname", "github.com"}, nil)
	text := string(out)
	if err != nil {
		text += "\n" + err.Error()
	}
	var accounts []string
	active := -1
	for _, line := range strings.Split(text, "\n") {
		if m := accountLine.FindStringSubmatch(line); m != nil {
			accounts = append(accounts, m[1])
			continue
		}
		if strings.Contains(line, "Active account: true") && len(accounts) > 0 {
			active = len(accounts) - 1
		}
	}
	if active > 0 {
		a := accounts[active]
		accounts = append([]string{a}, append(accounts[:active:active], accounts[active+1:]...)...)
	}
	return accounts
}

// OrgMember reports whether the token's user is an active member of org.
func (c *Client) OrgMember(ctx context.Context, token, org string) (bool, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.rest+"/user/memberships/orgs/"+url.PathEscape(org), nil)
	if err != nil {
		return false, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/vnd.github+json")
	resp, err := c.http.Do(req)
	if err != nil {
		return false, fmt.Errorf("github membership of %s: %w", org, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusForbidden {
		return false, nil
	}
	if resp.StatusCode >= 300 {
		return false, fmt.Errorf("github membership of %s: %d", org, resp.StatusCode)
	}
	var m struct {
		State string `json:"state"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&m); err != nil {
		return false, fmt.Errorf("github membership of %s: %w", org, err)
	}
	return m.State == "active", nil
}

// DetectAccount returns the first account that can read owner's repositories: the account named like
// owner, or one that is a member of the owner organization; empty when none is.
func DetectAccount(ctx context.Context, accounts []string, owner string, tokenFor func(ctx context.Context, account string) (string, error), member func(ctx context.Context, token, org string) (bool, error)) string {
	for _, a := range accounts {
		if strings.EqualFold(a, owner) {
			return a
		}
	}
	for _, a := range accounts {
		tok, err := tokenFor(ctx, a)
		if err != nil {
			continue
		}
		if ok, err := member(ctx, tok, owner); err == nil && ok {
			return a
		}
	}
	return ""
}
