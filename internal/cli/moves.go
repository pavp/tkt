package cli

import (
	"context"
	"time"

	"github.com/pavp/tkt/internal/board"
	"github.com/pavp/tkt/internal/config"
	"github.com/pavp/tkt/internal/jira"
	"github.com/pavp/tkt/internal/ticket"
)

const jiraWriteTimeout = 30 * time.Second

// transitions lists where Jira lets key move, with the form fields the board can fill.
func (a *App) transitions(ctx context.Context, c *config.Config, key ticket.Key) ([]board.Transition, error) {
	ctx, cancel := context.WithTimeout(ctx, jiraWriteTimeout)
	defer cancel()
	js, err := a.jiraClient(c)
	if err != nil {
		return nil, err
	}
	trs, err := js.Transitions(ctx, string(key))
	out := make([]board.Transition, len(trs))
	for i, t := range trs {
		out[i] = board.Transition{ID: t.ID, To: t.To, NeedsForm: t.NeedsForm}
		for _, f := range t.Fields {
			field := board.Field{ID: f.ID, Name: f.Name, Kind: f.Kind, Required: f.Required}
			for _, o := range f.Options {
				field.Options = append(field.Options, board.Option{ID: o.ID, Value: o.Value})
			}
			out[i].Fields = append(out[i].Fields, field)
		}
	}
	return out, err
}

func (a *App) transition(ctx context.Context, c *config.Config, key ticket.Key, id string, values []board.Value, comment string) error {
	vs := make([]jira.Value, len(values))
	for i, v := range values {
		vs[i] = jira.Value{Field: v.Field, Kind: v.Kind, ID: v.ID}
	}
	ctx, cancel := context.WithTimeout(ctx, jiraWriteTimeout)
	defer cancel()
	js, err := a.jiraClient(c)
	if err != nil {
		return err
	}
	return js.Transition(ctx, string(key), id, vs, comment)
}

// assignableUsers lists who key can be given to whose name matches query.
func (a *App) assignableUsers(ctx context.Context, c *config.Config, key ticket.Key, query string) ([]board.User, error) {
	ctx, cancel := context.WithTimeout(ctx, jiraWriteTimeout)
	defer cancel()
	js, err := a.jiraClient(c)
	if err != nil {
		return nil, err
	}
	found, err := js.AssignableUsers(ctx, string(key), query)
	users := make([]board.User, len(found))
	for i, u := range found {
		users[i] = board.User{ID: u.AccountID, Name: u.DisplayName}
	}
	return users, err
}

// assign gives key to the user (me), to accountID, or to nobody when neither is given.
func (a *App) assign(ctx context.Context, c *config.Config, key ticket.Key, me bool, accountID string) error {
	ctx, cancel := context.WithTimeout(ctx, jiraWriteTimeout)
	defer cancel()
	js, err := a.jiraClient(c)
	if err != nil {
		return err
	}
	if me {
		who, err := js.Myself(ctx)
		if err != nil {
			return err
		}
		accountID = who.AccountID
	}
	return js.Assign(ctx, string(key), accountID)
}

// daemonTransitions, daemonTransition, daemonUsers and daemonAssign serve the daemon's Move and Assign with the
// configuration read afresh, like each sync.
func (a *App) daemonTransitions(ctx context.Context, key ticket.Key) ([]board.Transition, error) {
	c, err := a.LoadConfig()
	if err != nil {
		return nil, err
	}
	return a.transitions(ctx, c, key)
}

func (a *App) daemonTransition(ctx context.Context, key ticket.Key, id string, values []board.Value, comment string) error {
	c, err := a.LoadConfig()
	if err != nil {
		return err
	}
	return a.transition(ctx, c, key, id, values, comment)
}

func (a *App) daemonUsers(ctx context.Context, key ticket.Key, query string) ([]board.User, error) {
	c, err := a.LoadConfig()
	if err != nil {
		return nil, err
	}
	return a.assignableUsers(ctx, c, key, query)
}

func (a *App) daemonAssign(ctx context.Context, key ticket.Key, me bool, accountID string) error {
	c, err := a.LoadConfig()
	if err != nil {
		return err
	}
	return a.assign(ctx, c, key, me, accountID)
}
