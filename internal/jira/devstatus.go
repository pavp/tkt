package jira

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
)

// DevPullRequest is a pull request Jira's development panel links to an issue.
type DevPullRequest struct {
	Repo   string
	Number int
	Title  string
	URL    string
	Branch string
	// Status is Jira's: OPEN, MERGED or DECLINED.
	Status string
}

type devSummary struct {
	Summary struct {
		PullRequest struct {
			Overall struct {
				Count int `json:"count"`
			} `json:"overall"`
			ByInstanceType map[string]json.RawMessage `json:"byInstanceType"`
		} `json:"pullrequest"`
	} `json:"summary"`
}

type devDetail struct {
	Detail []struct {
		PullRequests []struct {
			ID     string `json:"id"`
			Name   string `json:"name"`
			URL    string `json:"url"`
			Status string `json:"status"`
			Repo   string `json:"repositoryName"`
			Source struct {
				Branch string `json:"branch"`
			} `json:"source"`
		} `json:"pullRequests"`
	} `json:"detail"`
}

// DevPullRequests lists the pull requests linked to an issue through a development-tool integration
// such as GitHub for Jira. It relies on Jira's undocumented dev-status API, so callers treat errors as
// "unknown" and fall back to their own search.
func (c *Client) DevPullRequests(ctx context.Context, issueID string) ([]DevPullRequest, error) {
	c.mu.Lock()
	apps := c.devApps
	c.mu.Unlock()
	if apps == nil {
		var s devSummary
		if err := c.do(ctx, http.MethodGet, "/rest/dev-status/latest/issue/summary?issueId="+url.QueryEscape(issueID), nil, &s); err != nil {
			return nil, err
		}
		if s.Summary.PullRequest.Overall.Count == 0 {
			return nil, nil
		}
		for app := range s.Summary.PullRequest.ByInstanceType {
			apps = append(apps, app)
		}
		sort.Strings(apps)
		c.mu.Lock()
		c.devApps = apps
		c.mu.Unlock()
	}
	var out []DevPullRequest
	for _, app := range apps {
		q := url.Values{"issueId": {issueID}, "applicationType": {app}, "dataType": {"pullrequest"}}
		var d devDetail
		if err := c.do(ctx, http.MethodGet, "/rest/dev-status/latest/issue/detail?"+q.Encode(), nil, &d); err != nil {
			return nil, err
		}
		for _, det := range d.Detail {
			for _, p := range det.PullRequests {
				n, _ := strconv.Atoi(strings.TrimPrefix(p.ID, "#"))
				out = append(out, DevPullRequest{Repo: p.Repo, Number: n, Title: p.Name, URL: p.URL, Branch: p.Source.Branch, Status: p.Status})
			}
		}
	}
	return out, nil
}
