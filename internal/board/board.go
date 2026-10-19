package board

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/pavp/tkt/internal/config"
	"github.com/pavp/tkt/internal/jira"
	"github.com/pavp/tkt/internal/ticket"
)

// Snapshot is everything the board shows, as fetched at one moment.
type Snapshot struct {
	FetchedAt time.Time            `json:"fetched_at"`
	Tickets   []ticket.Ticket      `json:"tickets"`
	Reviews   []ticket.PullRequest `json:"reviews"`
	Sprints   []jira.Sprint        `json:"sprints"`
	Warnings  []string             `json:"warnings,omitempty"`
}

type JiraSource interface {
	Search(ctx context.Context, jql string, fields []string) ([]jira.Issue, error)
	ActiveSprints(ctx context.Context, project string) ([]jira.Sprint, error)
	BrowseURL(key string) string
}

type GitHubSource interface {
	PullRequests(ctx context.Context, repos []string) ([]ticket.PullRequest, error)
	ReviewRequests(ctx context.Context, owner string) ([]ticket.PullRequest, error)
}

// Fetch builds a snapshot. Jira failures are fatal because there is no board without tickets; GitHub and
// sprint failures become warnings so the tickets still show.
func Fetch(ctx context.Context, c *config.Config, js JiraSource, gh GitHubSource, now time.Time) (Snapshot, error) {
	snap := Snapshot{FetchedAt: now}
	if len(c.Spaces) == 0 {
		return snap, errors.New("no Jira spaces configured: add [[spaces]] to the config or run `tkt init`")
	}
	issues, err := js.Search(ctx, jira.MySprintJQL(c.SpaceKeys()), jira.ListFields)
	if err != nil {
		return snap, err
	}
	tickets := make([]ticket.Ticket, 0, len(issues))
	for _, is := range issues {
		tickets = append(tickets, ToTicket(c, is, js.BrowseURL(is.Key)))
	}

	repos := c.Repos()
	names := make([]string, len(repos))
	owners := map[string]bool{}
	for i, r := range repos {
		names[i] = r.Name
		owners[r.Owner()] = true
	}
	if len(names) > 0 {
		prs, err := gh.PullRequests(ctx, names)
		if err != nil {
			snap.Warnings = append(snap.Warnings, "pull requests unavailable: "+err.Error())
		}
		tickets = ticket.AttachPRs(tickets, prs)
	}
	snap.Tickets = ticket.GroupSubtasks(tickets)

	seen := map[string]bool{}
	for _, owner := range sortedKeys(owners) {
		reviews, err := gh.ReviewRequests(ctx, owner)
		if err != nil {
			snap.Warnings = append(snap.Warnings, "review requests unavailable: "+err.Error())
			continue
		}
		for _, pr := range reviews {
			id := fmt.Sprintf("%s#%d", pr.Repo, pr.Number)
			if !seen[id] {
				seen[id] = true
				snap.Reviews = append(snap.Reviews, pr)
			}
		}
	}

	for _, s := range c.Spaces {
		sprints, err := js.ActiveSprints(ctx, s.Key)
		if err != nil {
			snap.Warnings = append(snap.Warnings, fmt.Sprintf("sprints of %s unavailable: %v", s.Key, err))
			continue
		}
		snap.Sprints = append(snap.Sprints, sprints...)
	}
	return snap, nil
}

// ToTicket converts a Jira issue into a board ticket using its space's status overrides.
func ToTicket(c *config.Config, is jira.Issue, url string) ticket.Ticket {
	t := ticket.Ticket{
		Key:     ticket.Key(is.Key),
		Type:    is.Type,
		Summary: is.Summary,
		Status:  is.Status,
		Parent:  ticket.Key(is.Parent),
		URL:     url,
	}
	space, _ := c.Space(t.Space())
	t.Category = ticket.CategoryFor(is.Status, is.StatusCategory, space.Categories())
	return t
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// Attention is one item that needs the user: a ticket with failing CI or requested changes, or a PR
// awaiting the user's review.
type Attention struct {
	Ref    string              `json:"ref"`
	Title  string              `json:"title"`
	Reason ticket.Reason       `json:"reason"`
	Ticket *ticket.Ticket      `json:"ticket,omitempty"`
	PR     *ticket.PullRequest `json:"pr,omitempty"`
}

// NeedsYou lists attention items: tickets first (in board order), then review requests.
func (s Snapshot) NeedsYou() []Attention {
	var out []Attention
	for i := range s.Tickets {
		t := &s.Tickets[i]
		for _, r := range t.Attention() {
			out = append(out, Attention{Ref: string(t.Key), Title: t.Summary, Reason: r, Ticket: t})
		}
	}
	for i := range s.Reviews {
		pr := &s.Reviews[i]
		out = append(out, Attention{Ref: fmt.Sprintf("#%d", pr.Number), Title: pr.Title, Reason: ticket.ReasonReviewRequested, PR: pr})
	}
	return out
}

var columnOrder = map[ticket.Category]int{ticket.InProgress: 0, ticket.InReview: 1, ticket.ToDo: 2, ticket.Done: 3}

// Sorted orders tickets for lists: needing attention first, then In Progress, In Review, To Do, Done.
func Sorted(tickets []ticket.Ticket) []ticket.Ticket {
	out := append([]ticket.Ticket(nil), tickets...)
	sort.SliceStable(out, func(i, j int) bool {
		ai, aj := len(out[i].Attention()) > 0, len(out[j].Attention()) > 0
		if ai != aj {
			return ai
		}
		if ci, cj := columnOrder[out[i].Category], columnOrder[out[j].Category]; ci != cj {
			return ci < cj
		}
		return out[i].Key < out[j].Key
	})
	return out
}

// Column returns the tickets in one board column.
func (s Snapshot) Column(c ticket.Category) []ticket.Ticket {
	var out []ticket.Ticket
	for _, t := range s.Tickets {
		if t.Category == c {
			out = append(out, t)
		}
	}
	return out
}

// Filter keeps the tickets and reviews of one space; an empty space keeps everything.
func (s Snapshot) Filter(space string) Snapshot {
	if space == "" {
		return s
	}
	out := s
	out.Tickets = nil
	for _, t := range s.Tickets {
		if t.Space() == space {
			out.Tickets = append(out.Tickets, t)
		}
	}
	out.Reviews = nil
	for _, pr := range s.Reviews {
		for _, k := range ticket.KeysIn(pr.Branch) {
			if k.Space() == space {
				out.Reviews = append(out.Reviews, pr)
				break
			}
		}
	}
	out.Sprints = nil
	for _, sp := range s.Sprints {
		if sp.Space == space {
			out.Sprints = append(out.Sprints, sp)
		}
	}
	return out
}

// DaysLeft returns whole days until end, rounding up; a past end gives 0.
func DaysLeft(end, now time.Time) int {
	if end.IsZero() || !end.After(now) {
		return 0
	}
	return int((end.Sub(now) + 24*time.Hour - time.Nanosecond) / (24 * time.Hour))
}
