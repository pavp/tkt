package cli

import (
	"context"

	"github.com/pavp/tkt/internal/jira"
)

// JiraAPI is the read-only Jira surface commands use.
type JiraAPI interface {
	Myself(ctx context.Context) (jira.User, error)
	Search(ctx context.Context, jql string, fields []string) ([]jira.Issue, error)
	Issue(ctx context.Context, key string, extraFields ...string) (jira.Issue, error)
	Comments(ctx context.Context, key string, max int) ([]jira.Comment, error)
	Statuses(ctx context.Context, project string) ([]jira.Status, error)
	Projects(ctx context.Context) ([]jira.Project, error)
	ActiveSprints(ctx context.Context, project string) ([]jira.Sprint, error)
	BrowseURL(key string) string
}
