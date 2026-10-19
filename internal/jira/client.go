package jira

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// ErrUnauthorized means Jira rejected the credentials: the token is wrong, revoked, or expired.
var ErrUnauthorized = errors.New("jira rejected the token (expired or revoked?): run `tkt auth jira`")

type Client struct {
	base  string
	email string
	token string
	http  *http.Client

	// sprintField is the id of the site's Sprint custom field; it differs between Jira sites.
	sprintField  string
	sprintLooked bool
}

// New builds a read-only client for site (for example "https://example.atlassian.net").
func New(site, email, token string) *Client {
	return &Client{
		base:  strings.TrimRight(site, "/"),
		email: email,
		token: token,
		http:  &http.Client{Timeout: 30 * time.Second},
	}
}

// WithHTTPClient replaces the HTTP client, mainly for tests.
func (c *Client) WithHTTPClient(h *http.Client) *Client {
	c.http = h
	return c
}

func (c *Client) BrowseURL(key string) string {
	return c.base + "/browse/" + key
}

type apiError struct {
	ErrorMessages []string          `json:"errorMessages"`
	Errors        map[string]string `json:"errors"`
}

func (c *Client) do(ctx context.Context, method, path string, body any, out any) error {
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, rd)
	if err != nil {
		return err
	}
	req.SetBasicAuth(c.email, c.token)
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("jira %s %s: %w", method, path, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusUnauthorized {
		return ErrUnauthorized
	}
	if resp.StatusCode >= 300 {
		var ae apiError
		_ = json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&ae)
		msgs := ae.ErrorMessages
		for k, v := range ae.Errors {
			msgs = append(msgs, k+": "+v)
		}
		if len(msgs) == 0 {
			msgs = []string{http.StatusText(resp.StatusCode)}
		}
		return fmt.Errorf("jira %s %s: %d %s", method, path, resp.StatusCode, strings.Join(msgs, "; "))
	}
	if out == nil {
		return nil
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("jira %s %s: decode response: %w", method, path, err)
	}
	return nil
}

type User struct {
	AccountID   string `json:"accountId"`
	DisplayName string `json:"displayName"`
	Email       string `json:"emailAddress"`
}

// Myself returns the authenticated user; it doubles as a token check.
func (c *Client) Myself(ctx context.Context) (User, error) {
	var u User
	err := c.do(ctx, http.MethodGet, "/rest/api/3/myself", nil, &u)
	return u, err
}

type Issue struct {
	Key            string
	Summary        string
	Type           string
	Subtask        bool
	Status         string
	StatusCategory string
	Parent         string
	Updated        time.Time
	Description    json.RawMessage
	Attachments    []Attachment
	Sprints        []Sprint
	Fields         map[string]json.RawMessage
}

type Attachment struct {
	Filename string `json:"filename"`
	MimeType string `json:"mimeType"`
	Size     int64  `json:"size"`
	Content  string `json:"content"`
}

type rawIssue struct {
	Key    string                     `json:"key"`
	Fields map[string]json.RawMessage `json:"fields"`
}

type issueFields struct {
	Summary   string `json:"summary"`
	IssueType struct {
		Name    string `json:"name"`
		Subtask bool   `json:"subtask"`
	} `json:"issuetype"`
	Status struct {
		Name           string `json:"name"`
		StatusCategory struct {
			Key string `json:"key"`
		} `json:"statusCategory"`
	} `json:"status"`
	Parent *struct {
		Key string `json:"key"`
	} `json:"parent"`
	Updated     string          `json:"updated"`
	Description json.RawMessage `json:"description"`
	Attachment  []Attachment    `json:"attachment"`
}

func (r rawIssue) decode(sprintField string) (Issue, error) {
	all, err := json.Marshal(r.Fields)
	if err != nil {
		return Issue{}, err
	}
	var f issueFields
	if err := json.Unmarshal(all, &f); err != nil {
		return Issue{}, fmt.Errorf("decode %s: %w", r.Key, err)
	}
	is := Issue{
		Key:            r.Key,
		Summary:        f.Summary,
		Type:           f.IssueType.Name,
		Subtask:        f.IssueType.Subtask,
		Status:         f.Status.Name,
		StatusCategory: f.Status.StatusCategory.Key,
		Description:    f.Description,
		Attachments:    f.Attachment,
		Fields:         r.Fields,
	}
	if f.Parent != nil {
		is.Parent = f.Parent.Key
	}
	if f.Updated != "" {
		if t, err := time.Parse("2006-01-02T15:04:05.000-0700", f.Updated); err == nil {
			is.Updated = t
		}
	}
	if raw, ok := r.Fields[sprintField]; ok && sprintField != "" {
		var sprints []struct {
			Name    string `json:"name"`
			State   string `json:"state"`
			EndDate string `json:"endDate"`
		}
		if json.Unmarshal(raw, &sprints) == nil {
			space, _, _ := strings.Cut(r.Key, "-")
			for _, sp := range sprints {
				out := Sprint{Space: space, Name: sp.Name, State: sp.State}
				if t, err := time.Parse(time.RFC3339, sp.EndDate); err == nil {
					out.End = t
				}
				is.Sprints = append(is.Sprints, out)
			}
		}
	}
	return is, nil
}

// SprintFieldName stands for the site's Sprint custom field in field lists; Search and Issue resolve it.
const SprintFieldName = "sprint"

// resolveFields replaces SprintFieldName with the site's Sprint field id, looked up once per client.
func (c *Client) resolveFields(ctx context.Context, fields []string) ([]string, error) {
	out := make([]string, 0, len(fields))
	for _, f := range fields {
		if f != SprintFieldName {
			out = append(out, f)
			continue
		}
		if !c.sprintLooked {
			var all []struct {
				ID     string `json:"id"`
				Schema struct {
					Custom string `json:"custom"`
				} `json:"schema"`
			}
			if err := c.do(ctx, http.MethodGet, "/rest/api/3/field", nil, &all); err != nil {
				return nil, err
			}
			for _, fd := range all {
				if fd.Schema.Custom == "com.pyxis.greenhopper.jira:gh-sprint" {
					c.sprintField = fd.ID
					break
				}
			}
			c.sprintLooked = true
		}
		if c.sprintField != "" {
			out = append(out, c.sprintField)
		}
	}
	return out, nil
}

// ListFields are the fields the board needs; Issue() asks for more.
var ListFields = []string{"summary", "issuetype", "status", "parent", "updated", SprintFieldName}

// Search runs jql over every result page (POST /rest/api/3/search/jql).
func (c *Client) Search(ctx context.Context, jql string, fields []string) ([]Issue, error) {
	fields, err := c.resolveFields(ctx, fields)
	if err != nil {
		return nil, err
	}
	var out []Issue
	token := ""
	for page := 0; page < 50; page++ {
		body := map[string]any{"jql": jql, "fields": fields, "maxResults": 100}
		if token != "" {
			body["nextPageToken"] = token
		}
		var resp struct {
			Issues        []rawIssue `json:"issues"`
			NextPageToken string     `json:"nextPageToken"`
			IsLast        bool       `json:"isLast"`
		}
		if err := c.do(ctx, http.MethodPost, "/rest/api/3/search/jql", body, &resp); err != nil {
			return nil, err
		}
		for _, r := range resp.Issues {
			is, err := r.decode(c.sprintField)
			if err != nil {
				return nil, err
			}
			out = append(out, is)
		}
		if resp.IsLast || resp.NextPageToken == "" {
			return out, nil
		}
		token = resp.NextPageToken
	}
	return nil, errors.New("jira search returned too many pages")
}

// MySprintJQL selects the user's tickets in open sprints of the given spaces: assigned to the user, plus
// finished tickets the user once held, since teams often reassign a ticket (to QA, to a reviewer) when
// it is done.
func MySprintJQL(spaces []string) string {
	quoted := make([]string, len(spaces))
	for i, s := range spaces {
		quoted[i] = `"` + s + `"`
	}
	return fmt.Sprintf("sprint in openSprints() AND project in (%s) AND (assignee = currentUser() OR (statusCategory = Done AND assignee WAS currentUser())) ORDER BY updated DESC", strings.Join(quoted, ", "))
}

// Issue fetches one issue with the board fields, its description, attachments, and any extra fields.
func (c *Client) Issue(ctx context.Context, key string, extraFields ...string) (Issue, error) {
	fields := append(append([]string{}, ListFields...), "description", "attachment")
	fields, err := c.resolveFields(ctx, append(fields, extraFields...))
	if err != nil {
		return Issue{}, err
	}
	q := url.Values{"fields": {strings.Join(fields, ",")}}
	var r rawIssue
	if err := c.do(ctx, http.MethodGet, "/rest/api/3/issue/"+url.PathEscape(key)+"?"+q.Encode(), nil, &r); err != nil {
		return Issue{}, err
	}
	return r.decode(c.sprintField)
}

type Comment struct {
	Author  string
	Created time.Time
	Body    json.RawMessage
}

// Comments returns the latest max comments, newest first.
func (c *Client) Comments(ctx context.Context, key string, max int) ([]Comment, error) {
	if max <= 0 {
		return nil, nil
	}
	q := url.Values{"orderBy": {"-created"}, "maxResults": {fmt.Sprint(max)}}
	var resp struct {
		Comments []struct {
			Author struct {
				DisplayName string `json:"displayName"`
			} `json:"author"`
			Created string          `json:"created"`
			Body    json.RawMessage `json:"body"`
		} `json:"comments"`
	}
	if err := c.do(ctx, http.MethodGet, "/rest/api/3/issue/"+url.PathEscape(key)+"/comment?"+q.Encode(), nil, &resp); err != nil {
		return nil, err
	}
	out := make([]Comment, 0, len(resp.Comments))
	for _, rc := range resp.Comments {
		cm := Comment{Author: rc.Author.DisplayName, Body: rc.Body}
		if t, err := time.Parse("2006-01-02T15:04:05.000-0700", rc.Created); err == nil {
			cm.Created = t
		}
		out = append(out, cm)
	}
	return out, nil
}

type Status struct {
	Name     string
	Category string
}

// Statuses lists a space's statuses across its issue types, without duplicates.
func (c *Client) Statuses(ctx context.Context, project string) ([]Status, error) {
	var resp []struct {
		Statuses []struct {
			Name           string `json:"name"`
			StatusCategory struct {
				Key string `json:"key"`
			} `json:"statusCategory"`
		} `json:"statuses"`
	}
	if err := c.do(ctx, http.MethodGet, "/rest/api/3/project/"+url.PathEscape(project)+"/statuses", nil, &resp); err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	var out []Status
	for _, t := range resp {
		for _, s := range t.Statuses {
			if !seen[s.Name] {
				seen[s.Name] = true
				out = append(out, Status{Name: s.Name, Category: s.StatusCategory.Key})
			}
		}
	}
	return out, nil
}

type Project struct {
	Key  string `json:"key"`
	Name string `json:"name"`
}

// Projects lists the spaces the user can browse.
func (c *Client) Projects(ctx context.Context) ([]Project, error) {
	var resp struct {
		Values []Project `json:"values"`
	}
	if err := c.do(ctx, http.MethodGet, "/rest/api/3/project/search?maxResults=100&orderBy=key", nil, &resp); err != nil {
		return nil, err
	}
	return resp.Values, nil
}

type Sprint struct {
	Space string    `json:"space"`
	Name  string    `json:"name"`
	State string    `json:"state,omitempty"`
	End   time.Time `json:"end"`
}

// Download writes an attachment to w. It refuses URLs outside the Jira site so the credentials are never
// sent elsewhere, and stops after maxBytes.
func (c *Client) Download(ctx context.Context, rawURL string, w io.Writer, maxBytes int64) error {
	u, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("attachment URL %q: %w", rawURL, err)
	}
	base, _ := url.Parse(c.base)
	if u.Scheme != base.Scheme || u.Host != base.Host {
		return fmt.Errorf("attachment URL %s is outside %s", rawURL, c.base)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return err
	}
	req.SetBasicAuth(c.email, c.token)
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("download %s: %w", rawURL, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusUnauthorized {
		return ErrUnauthorized
	}
	if resp.StatusCode >= 300 {
		return fmt.Errorf("download %s: %d %s", rawURL, resp.StatusCode, http.StatusText(resp.StatusCode))
	}
	n, err := io.Copy(w, io.LimitReader(resp.Body, maxBytes+1))
	if err != nil {
		return fmt.Errorf("download %s: %w", rawURL, err)
	}
	if n > maxBytes {
		return fmt.Errorf("download %s: larger than %d bytes", rawURL, maxBytes)
	}
	return nil
}
