package cli

import (
	"context"
	"errors"
	"io"

	"github.com/pavp/tkt/internal/config"
	"github.com/pavp/tkt/internal/credstore"
	"github.com/pavp/tkt/internal/execx"
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
	ReviewRequests(ctx context.Context, repos []string, direct bool) ([]ticket.PullRequest, error)
	FailedChecks(ctx context.Context, repo, sha string) ([]github.FailedCheck, error)
	ChangeRequests(ctx context.Context, repo string, number int) ([]github.ChangeRequest, error)
}

func githubFor(c *config.Config, run execx.Runner) GitHubAPI {
	return github.New(github.CachedTokens(c.GitHubAccount, func(ctx context.Context, account string) (string, error) {
		return github.Token(ctx, run, account)
	}))
}
