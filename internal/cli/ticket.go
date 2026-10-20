package cli

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/pavp/tkt/internal/adf"
	"github.com/pavp/tkt/internal/board"
	"github.com/pavp/tkt/internal/config"
	"github.com/pavp/tkt/internal/jira"
	"github.com/pavp/tkt/internal/ticket"
)

type commentView struct {
	Author  string    `json:"author"`
	Created time.Time `json:"created"`
	Body    string    `json:"body"`
}

// detail is one ticket with everything show and context print.
type detail struct {
	Ticket      ticket.Ticket     `json:"ticket"`
	Description string            `json:"description"`
	Acceptance  string            `json:"acceptance_criteria,omitempty"`
	Attachments []jira.Attachment `json:"attachments,omitempty"`
	Comments    []commentView     `json:"comments,omitempty"`
	Warnings    []string          `json:"warnings,omitempty"`
}

// resolveKey takes the ticket key from args, or from the current git branch when args are empty.
func (a *App) resolveKey(ctx context.Context, c *config.Config, args []string) (ticket.Key, error) {
	if len(args) > 0 {
		keys := ticket.KeysIn(strings.ToUpper(args[0]))
		if len(keys) != 1 || string(keys[0]) != strings.ToUpper(args[0]) {
			return "", fmt.Errorf("%q is not a ticket key like DEMO-123", args[0])
		}
		return keys[0], nil
	}
	if env := a.Env.Getenv("TKT_KEY"); env != "" {
		return a.resolveKey(ctx, c, []string{env})
	}
	out, err := a.Run.Run(ctx, "", "git", []string{"rev-parse", "--abbrev-ref", "HEAD"}, nil)
	if err != nil {
		return "", errors.New("no ticket key given and not inside a git checkout: pass a key like DEMO-123")
	}
	branch := strings.TrimSpace(string(out))
	k, ok := ticket.KeyInBranch(branch, c.SpaceKeys())
	if !ok {
		return "", fmt.Errorf("branch %q carries no key of a configured space: pass a key like DEMO-123", branch)
	}
	return k, nil
}

// loadDetail reads a ticket, its comments and its pull requests at once. known, when set, is a board's
// copy of the ticket, whose pull requests stand in for listing every recent pull request on GitHub, by
// far the slowest of the three.
func (a *App) loadDetail(ctx context.Context, c *config.Config, key ticket.Key, known *ticket.Ticket) (detail, error) {
	js, err := a.jiraClient(c)
	if err != nil {
		return detail{}, err
	}
	space, _ := c.Space(key.Space())
	var extra []string
	if space.AcceptanceField != "" {
		extra = append(extra, space.AcceptanceField)
	}
	var (
		wg          sync.WaitGroup
		comments    []jira.Comment
		commentsErr error
		prs         []ticket.PullRequest
		prsErr      error
		listed      bool
	)
	wg.Add(1)
	go func() {
		defer wg.Done()
		comments, commentsErr = js.Comments(ctx, string(key), c.Context.Comments)
	}()
	if repos := c.Repos(); known == nil && len(repos) > 0 {
		listed = true
		names := make([]string, len(repos))
		for i, r := range repos {
			names[i] = r.Name
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			prs, prsErr = a.NewGitHub(c).PullRequests(ctx, names)
		}()
	}
	is, err := js.Issue(ctx, string(key), extra...)
	wg.Wait()
	if err != nil {
		return detail{}, err
	}
	d := detail{Ticket: board.ToTicket(c, is, js.BrowseURL(is.Key)), Attachments: is.Attachments}
	if d.Description, err = adf.ToMarkdown(is.Description); err != nil {
		d.Warnings = append(d.Warnings, "description could not be converted: "+err.Error())
	}
	if space.AcceptanceField != "" {
		if d.Acceptance, err = adf.ToMarkdown(is.Fields[space.AcceptanceField]); err != nil {
			d.Warnings = append(d.Warnings, "acceptance criteria could not be converted: "+err.Error())
		}
	}
	if commentsErr != nil {
		d.Warnings = append(d.Warnings, "comments unavailable: "+commentsErr.Error())
	}
	for _, cm := range comments {
		body, err := adf.ToMarkdown(cm.Body)
		if err != nil {
			body = "(comment could not be converted)"
		}
		d.Comments = append(d.Comments, commentView{Author: cm.Author, Created: cm.Created, Body: body})
	}
	switch {
	case known != nil:
		d.Ticket.PRs = known.PRs
	case listed:
		if prsErr != nil {
			d.Warnings = append(d.Warnings, "pull requests unavailable: "+prsErr.Error())
		}
		d.Ticket = ticket.AttachPRs([]ticket.Ticket{d.Ticket}, prs)[0]
	}
	return d, nil
}
