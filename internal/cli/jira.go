package cli

import (
	"context"
	"errors"
	"io"
	"strings"

	"github.com/pavp/tkt/internal/config"
	"github.com/pavp/tkt/internal/credstore"
	"github.com/pavp/tkt/internal/github"
	"github.com/pavp/tkt/internal/jira"
	"github.com/pavp/tkt/internal/ticket"
)

// JiraAPI is the read-only Jira surface commands use.
type JiraAPI interface {
	Myself(ctx context.Context) (jira.User, error)
	Search(ctx context.Context, jql string, fields []string) ([]jira.Issue, error)
	Issue(ctx context.Context, key string, extraFields ...string) (jira.Issue, error)
	Comments(ctx context.Context, key string, max int) ([]jira.Comment, error)
	Statuses(ctx context.Context, project string) ([]jira.Status, error)
	Projects(ctx context.Context) ([]jira.Project, error)
	BrowseURL(key string) string
	DevPullRequests(ctx context.Context, issueID string) ([]jira.DevPullRequest, error)
	Download(ctx context.Context, url string, w io.Writer, maxBytes int64) error
}

// jiraClient builds a Jira client from the configuration and the stored token.
func (a *App) jiraClient(c *config.Config) (JiraAPI, error) {
	site, err := config.SiteURL(c.Jira.Site)
	if err != nil {
		return nil, err
	}
	token, _, err := credstore.JiraToken(a.Creds, a.Env.Getenv, site, c.Jira.Email)
	if errors.Is(err, credstore.ErrNotFound) {
		return nil, errors.New("no Jira token: run `tkt auth jira`")
	}
	if err != nil {
		return nil, err
	}
	return a.NewJira(site, c.Jira.Email, token), nil
}

// GitHubAPI is the read-only GitHub surface commands use.
type GitHubAPI interface {
	PullRequests(ctx context.Context, repos []string) ([]ticket.PullRequest, error)
	PullRequestsForKeys(ctx context.Context, repos, keys []string) ([]ticket.PullRequest, error)
	ReviewRequests(ctx context.Context, repos []string, direct bool) ([]ticket.PullRequest, error)
	FailedChecks(ctx context.Context, repo, sha string) ([]github.FailedCheck, error)
	ChangeRequests(ctx context.Context, repo string, number int) ([]github.ChangeRequest, error)
}

func (a *App) githubFor(c *config.Config) GitHubAPI {
	accountFor := func(ctx context.Context, owner string) string {
		return a.githubAccount(ctx, c, owner, a.memberCheck())
	}
	return github.New(github.CachedTokens(accountFor, func(ctx context.Context, account string) (string, error) {
		return github.Token(ctx, a.Run, account)
	}))
}

// memberCheck falls back to "not a member" when no membership check is wired, so detection then only
// matches accounts named like the owner.
func (a *App) memberCheck() func(ctx context.Context, token, org string) (bool, error) {
	if a.OrgMember != nil {
		return a.OrgMember
	}
	return func(context.Context, string, string) (bool, error) { return false, nil }
}

// readers lists the logged-in accounts that can read owner's repositories.
func (a *App) readers(ctx context.Context, accounts []string, owner string) []string {
	var out []string
	member := a.memberCheck()
	for _, acct := range accounts {
		if strings.EqualFold(acct, owner) {
			out = append(out, acct)
			continue
		}
		tok, err := github.Token(ctx, a.Run, acct)
		if err != nil {
			continue
		}
		if ok, err := member(ctx, tok, owner); err == nil && ok {
			out = append(out, acct)
		}
	}
	return out
}

// githubAccount picks the gh account for owner: the configured one, the one detected earlier, or the
// logged-in account that can read owner. Empty means gh's active account.
func (a *App) githubAccount(ctx context.Context, c *config.Config, owner string, member func(ctx context.Context, token, org string) (bool, error)) string {
	if acct := c.GitHubAccount(owner); acct != "" {
		return acct
	}
	st := a.loadState()
	if acct := st.GitHubAccounts[owner]; acct != "" {
		return acct
	}
	accounts := github.LoggedInAccounts(ctx, a.Run)
	if len(accounts) < 2 {
		return ""
	}
	acct := github.DetectAccount(ctx, accounts, owner, func(ctx context.Context, account string) (string, error) {
		return github.Token(ctx, a.Run, account)
	}, member)
	if acct == "" {
		return ""
	}
	if st.GitHubAccounts == nil {
		st.GitHubAccounts = map[string]string{}
	}
	st.GitHubAccounts[owner] = acct
	a.saveState(st)
	return acct
}
