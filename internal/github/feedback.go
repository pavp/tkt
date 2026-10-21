package github

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// logTailBytes bounds how much of a failing job log goes into an agent prompt.
const logTailBytes = 6000

type FailedCheck struct {
	Name    string `json:"name"`
	URL     string `json:"url"`
	Summary string `json:"summary,omitempty"`
	LogTail string `json:"log_tail,omitempty"`
}

func (c *Client) get(ctx context.Context, owner, path string) (*http.Response, error) {
	tok, err := c.token(ctx, owner)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.rest+path, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+tok)
	req.Header.Set("Accept", "application/vnd.github+json")
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("github GET %s: %w", path, err)
	}
	if resp.StatusCode == http.StatusUnauthorized {
		resp.Body.Close()
		return nil, ErrUnauthorized
	}
	if resp.StatusCode >= 300 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		resp.Body.Close()
		return nil, fmt.Errorf("github GET %s: %d %s", path, resp.StatusCode, strings.TrimSpace(string(b)))
	}
	return resp, nil
}

// FailedChecks returns the failing check runs of a commit, with the tail of each GitHub Actions job log.
func (c *Client) FailedChecks(ctx context.Context, repo, sha string) ([]FailedCheck, error) {
	owner, _, _ := strings.Cut(repo, "/")
	resp, err := c.get(ctx, owner, "/repos/"+repo+"/commits/"+sha+"/check-runs?filter=latest&per_page=100")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var body struct {
		CheckRuns []struct {
			ID         int64  `json:"id"`
			Name       string `json:"name"`
			Conclusion string `json:"conclusion"`
			DetailsURL string `json:"details_url"`
			HTMLURL    string `json:"html_url"`
			App        struct {
				Slug string `json:"slug"`
			} `json:"app"`
			Output struct {
				Title   string `json:"title"`
				Summary string `json:"summary"`
			} `json:"output"`
		} `json:"check_runs"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return nil, fmt.Errorf("decode check runs: %w", err)
	}
	var out []FailedCheck
	for _, r := range body.CheckRuns {
		if r.Conclusion != "failure" && r.Conclusion != "timed_out" {
			continue
		}
		fc := FailedCheck{Name: r.Name, URL: r.HTMLURL, Summary: strings.TrimSpace(r.Output.Title + "\n" + r.Output.Summary)}
		if fc.URL == "" {
			fc.URL = r.DetailsURL
		}
		if r.App.Slug == "github-actions" {
			if tail, err := c.jobLogTail(ctx, owner, repo, r.ID); err == nil {
				fc.LogTail = tail
			}
		}
		out = append(out, fc)
	}
	return out, nil
}

// jobLogTail fetches a GitHub Actions job log (the check run id is the job id) and keeps its end, where
// the failure usually is.
func (c *Client) jobLogTail(ctx context.Context, owner, repo string, id int64) (string, error) {
	resp, err := c.get(ctx, owner, fmt.Sprintf("/repos/%s/actions/jobs/%d/logs", repo, id))
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, 20<<20))
	if err != nil {
		return "", err
	}
	s := string(b)
	if len(s) > logTailBytes {
		s = s[len(s)-logTailBytes:]
		if i := strings.IndexByte(s, '\n'); i >= 0 {
			s = s[i+1:]
		}
	}
	return strings.TrimSpace(s), nil
}

type ReviewComment struct {
	Path string `json:"path"`
	Line int    `json:"line"`
	Body string `json:"body"`
}

type ChangeRequest struct {
	Author   string          `json:"author"`
	Body     string          `json:"body"`
	Comments []ReviewComment `json:"comments"`
}

// ChangeRequests returns the latest reviews that requested changes on a pull request, with their inline comments.
func (c *Client) ChangeRequests(ctx context.Context, repo string, number int) ([]ChangeRequest, error) {
	owner, name, _ := strings.Cut(repo, "/")
	q := `query($owner: String!, $name: String!, $number: Int!) {
  repository(owner: $owner, name: $name) {
    pullRequest(number: $number) {
      reviews(states: CHANGES_REQUESTED, last: 5) {
        nodes { author { login } body comments(first: 50) { nodes { path line body } } }
      }
    }
  }
}`
	var data struct {
		Repository struct {
			PullRequest *struct {
				Reviews struct {
					Nodes []struct {
						Author *struct {
							Login string `json:"login"`
						} `json:"author"`
						Body     string `json:"body"`
						Comments struct {
							Nodes []ReviewComment `json:"nodes"`
						} `json:"comments"`
					} `json:"nodes"`
				} `json:"reviews"`
			} `json:"pullRequest"`
		} `json:"repository"`
	}
	vars := map[string]any{"owner": owner, "name": name, "number": number}
	if err := c.query(ctx, owner, q, vars, &data); err != nil {
		return nil, err
	}
	if data.Repository.PullRequest == nil {
		return nil, fmt.Errorf("pull request %s#%d not found", repo, number)
	}
	var out []ChangeRequest
	for _, n := range data.Repository.PullRequest.Reviews.Nodes {
		cr := ChangeRequest{Body: strings.TrimSpace(n.Body), Comments: n.Comments.Nodes}
		if n.Author != nil {
			cr.Author = n.Author.Login
		}
		out = append(out, cr)
	}
	return out, nil
}

type ThreadComment struct {
	Author string `json:"author"`
	Body   string `json:"body"`
}

// ReviewThread is one review conversation on a line of a pull request's diff.
type ReviewThread struct {
	Path     string          `json:"path"`
	Line     int             `json:"line"`
	Comments []ThreadComment `json:"comments"`
}

// UnresolvedThreads returns a pull request's review threads that nobody has resolved, with their comments.
func (c *Client) UnresolvedThreads(ctx context.Context, repo string, number int) ([]ReviewThread, error) {
	owner, name, _ := strings.Cut(repo, "/")
	q := `query($owner: String!, $name: String!, $number: Int!) {
  repository(owner: $owner, name: $name) {
    pullRequest(number: $number) {
      reviewThreads(first: 100) {
        nodes { isResolved path line comments(first: 20) { nodes { author { login } body } } }
      }
    }
  }
}`
	var data struct {
		Repository struct {
			PullRequest *struct {
				ReviewThreads struct {
					Nodes []struct {
						IsResolved bool   `json:"isResolved"`
						Path       string `json:"path"`
						Line       int    `json:"line"`
						Comments   struct {
							Nodes []struct {
								Author *struct {
									Login string `json:"login"`
								} `json:"author"`
								Body string `json:"body"`
							} `json:"nodes"`
						} `json:"comments"`
					} `json:"nodes"`
				} `json:"reviewThreads"`
			} `json:"pullRequest"`
		} `json:"repository"`
	}
	vars := map[string]any{"owner": owner, "name": name, "number": number}
	if err := c.query(ctx, owner, q, vars, &data); err != nil {
		return nil, err
	}
	if data.Repository.PullRequest == nil {
		return nil, fmt.Errorf("pull request %s#%d not found", repo, number)
	}
	var out []ReviewThread
	for _, n := range data.Repository.PullRequest.ReviewThreads.Nodes {
		if n.IsResolved {
			continue
		}
		th := ReviewThread{Path: n.Path, Line: n.Line}
		for _, cm := range n.Comments.Nodes {
			tc := ThreadComment{Body: strings.TrimSpace(cm.Body)}
			if cm.Author != nil {
				tc.Author = cm.Author.Login
			}
			th.Comments = append(th.Comments, tc)
		}
		out = append(out, th)
	}
	return out, nil
}
