package cli

import (
	"context"
	"errors"
	"io"
	"strings"

	"github.com/pavp/tkt/internal/jira"
)

// fakeJira serves canned Jira data and records the credentials it was built with.
type fakeJira struct {
	token     string
	myselfErr error
	issues    []jira.Issue
	searchErr error
	jql       string
	details   map[string]jira.Issue
	comments  map[string][]jira.Comment
	statuses  map[string][]jira.Status
	projects  []jira.Project
	files     map[string]string
	// devPRs maps issue ids to development-panel PRs; nil means the panel is unavailable.
	devPRs map[string][]jira.DevPullRequest
}

func (f *fakeJira) Myself(context.Context) (jira.User, error) {
	if f.myselfErr != nil {
		return jira.User{}, f.myselfErr
	}
	return jira.User{DisplayName: "Dev Example"}, nil
}

func (f *fakeJira) Search(_ context.Context, jql string, _ []string) ([]jira.Issue, error) {
	f.jql = jql
	return f.issues, f.searchErr
}

func (f *fakeJira) Issue(_ context.Context, key string, _ ...string) (jira.Issue, error) {
	if is, ok := f.details[key]; ok {
		return is, nil
	}
	return jira.Issue{}, errors.New("jira GET issue " + key + ": 404 Issue does not exist")
}

func (f *fakeJira) Comments(_ context.Context, key string, max int) ([]jira.Comment, error) {
	c := f.comments[key]
	if len(c) > max {
		c = c[:max]
	}
	return c, nil
}

func (f *fakeJira) Statuses(_ context.Context, p string) ([]jira.Status, error) {
	return f.statuses[p], nil
}
func (f *fakeJira) Projects(context.Context) ([]jira.Project, error) { return f.projects, nil }
func (f *fakeJira) BrowseURL(key string) string                      { return "https://example.atlassian.net/browse/" + key }

func (f *fakeJira) DevPullRequests(_ context.Context, id string) ([]jira.DevPullRequest, error) {
	if f.devPRs == nil {
		return nil, errors.New("no development panel")
	}
	return f.devPRs[id], nil
}

func (f *fakeJira) Download(_ context.Context, url string, w io.Writer, _ int64) error {
	body, ok := f.files[url]
	if !ok {
		return errors.New("download " + url + ": 404")
	}
	_, err := io.Copy(w, strings.NewReader(body))
	return err
}
