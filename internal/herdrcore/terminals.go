package herdrcore

import (
	"context"
	"maps"
	"slices"

	"github.com/pavp/tkt/internal/herdrport"
)

// Herdr desktop records where it shows a terminal as labels on it: herdr.space is the port's workspace.
const (
	labelSpace = "herdr.space"
	labelTab   = "herdr.tab"
)

// Terminal is an hc terminal. Agent and Status come from the program's own report (OSC 7501), so a
// terminal is an agent once its program reports a status; plain programs report none.
type Terminal struct {
	Address string
	Name    string
	Cwd     string
	Space   string
	Tab     string
	Agent   string
	Status  string
	Labels  map[string]string
}

type terminal struct {
	Address string            `json:"address"`
	Name    *string           `json:"name"`
	Cwd     string            `json:"cwd"`
	Labels  map[string]string `json:"labels"`
	Status  *struct {
		App   string `json:"app"`
		State string `json:"state"`
	} `json:"status"`
}

func (t terminal) port() Terminal {
	p := Terminal{Address: t.Address, Cwd: t.Cwd, Space: t.Labels[labelSpace], Tab: t.Labels[labelTab], Labels: t.Labels}
	if t.Name != nil {
		p.Name = *t.Name
	}
	if t.Status != nil {
		p.Agent, p.Status = t.Status.App, t.Status.State
	}
	return p
}

// Terminals lists live terminals, here and on the machines hc connects to (`machine/t7`).
func (c *Client) Terminals(ctx context.Context) ([]Terminal, error) {
	var r struct {
		Terminals []terminal `json:"terminals"`
	}
	if err := c.call(ctx, "terminal.list", map[string]any{}, &r); err != nil {
		return nil, err
	}
	out := make([]Terminal, len(r.Terminals))
	for i, t := range r.Terminals {
		out[i] = t.port()
	}
	return out, nil
}

func (c *Client) Panes(ctx context.Context) ([]herdrport.Pane, error) {
	ts, err := c.Terminals(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]herdrport.Pane, len(ts))
	for i, t := range ts {
		out[i] = herdrport.Pane{ID: t.Address, WorkspaceID: t.Space, Cwd: t.Cwd}
	}
	return out, nil
}

// Agents lists the terminals whose program reports a status; Tokens are the terminal's labels.
func (c *Client) Agents(ctx context.Context) ([]herdrport.Agent, error) {
	ts, err := c.Terminals(ctx)
	if err != nil {
		return nil, err
	}
	var out []herdrport.Agent
	for _, t := range ts {
		if t.Agent == "" {
			continue
		}
		out = append(out, herdrport.Agent{PaneID: t.Address, WorkspaceID: t.Space, Agent: t.Agent, Name: t.Name, Status: t.Status, Cwd: t.Cwd, Tokens: t.Labels})
	}
	return out, nil
}

// ReportPaneTokens sets each token as a label of the same name, removing it for a nil value. hc labels
// have no source, so source is not recorded: the token name alone identifies it, as tkt reads it back.
func (c *Client) ReportPaneTokens(ctx context.Context, paneID, source string, tokens map[string]*string) error {
	if len(tokens) == 0 {
		return nil
	}
	args := []string{"label", paneID}
	for _, k := range slices.Sorted(maps.Keys(tokens)) {
		v := ""
		if tokens[k] != nil {
			v = *tokens[k]
		}
		args = append(args, k+"="+v)
	}
	_, err := c.run(ctx, nil, args...)
	return err
}
