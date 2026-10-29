// Package herdr talks to a running Herdr server over its local socket (newline-delimited JSON).
package herdr

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"
)

var ErrNotRunning = errors.New("herdr is not running (no socket): start Herdr, or run this from inside it")

type Client struct {
	socket string
	seq    atomic.Int64
}

func New(socket string) *Client {
	return &Client{socket: socket}
}

// SocketPath returns HERDR_SOCKET_PATH, or Herdr's default location under the config directory.
func SocketPath(getenv func(string) string, home string) string {
	if p := getenv("HERDR_SOCKET_PATH"); p != "" {
		return p
	}
	if x := getenv("XDG_CONFIG_HOME"); x != "" {
		return filepath.Join(x, "herdr", "herdr.sock")
	}
	return filepath.Join(home, ".config", "herdr", "herdr.sock")
}

type RPCError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (e *RPCError) Error() string { return "herdr: " + e.Code + ": " + e.Message }

// Call sends one request and decodes its result into out.
func (c *Client) Call(ctx context.Context, method string, params, out any) error {
	var d net.Dialer
	dctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	conn, err := d.DialContext(dctx, "unix", c.socket)
	if err != nil {
		return ErrNotRunning
	}
	defer conn.Close()
	if dl, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(dl)
	} else {
		_ = conn.SetDeadline(time.Now().Add(5 * time.Minute))
	}
	id := fmt.Sprintf("tkt-%d", c.seq.Add(1))
	if params == nil {
		params = struct{}{}
	}
	req, err := json.Marshal(map[string]any{"id": id, "method": method, "params": params})
	if err != nil {
		return err
	}
	if _, err := conn.Write(append(req, '\n')); err != nil {
		return fmt.Errorf("herdr %s: %w", method, err)
	}
	// Responses can exceed bufio.Scanner's 64 KB line limit (snapshots, agent lists), so read with a Reader.
	line, err := bufio.NewReader(conn).ReadBytes('\n')
	if err != nil && len(line) == 0 {
		return fmt.Errorf("herdr %s: %w", method, err)
	}
	var resp struct {
		ID     string          `json:"id"`
		Result json.RawMessage `json:"result"`
		Error  *RPCError       `json:"error"`
	}
	if err := json.Unmarshal(line, &resp); err != nil {
		return fmt.Errorf("herdr %s: decode: %w", method, err)
	}
	if resp.Error != nil {
		return resp.Error
	}
	if out == nil {
		return nil
	}
	return json.Unmarshal(resp.Result, out)
}

type Worktree struct {
	CheckoutPath string `json:"checkout_path"`
	RepoName     string `json:"repo_name"`
	RepoRoot     string `json:"repo_root"`
	Linked       bool   `json:"is_linked_worktree"`
}

type Workspace struct {
	ID       string            `json:"workspace_id"`
	Label    string            `json:"label"`
	Tokens   map[string]string `json:"tokens"`
	Worktree *Worktree         `json:"worktree"`
}

func (c *Client) Panes(ctx context.Context) ([]Pane, error) {
	var r struct {
		Panes []Pane `json:"panes"`
	}
	err := c.Call(ctx, "pane.list", nil, &r)
	return r.Panes, err
}

func (c *Client) Workspaces(ctx context.Context) ([]Workspace, error) {
	var r struct {
		Workspaces []Workspace `json:"workspaces"`
	}
	err := c.Call(ctx, "workspace.list", nil, &r)
	return r.Workspaces, err
}

type Pane struct {
	ID          string `json:"pane_id"`
	WorkspaceID string `json:"workspace_id"`
	Cwd         string `json:"cwd"`
	Focused     bool   `json:"focused"`
}

// CreateWorkspace opens a workspace at cwd and returns it with its root pane.
func (c *Client) CreateWorkspace(ctx context.Context, cwd, label string, focus bool) (Workspace, Pane, error) {
	var r struct {
		Workspace Workspace `json:"workspace"`
		RootPane  Pane      `json:"root_pane"`
	}
	err := c.Call(ctx, "workspace.create", map[string]any{"cwd": cwd, "label": label, "focus": focus}, &r)
	return r.Workspace, r.RootPane, err
}

func (c *Client) FocusWorkspace(ctx context.Context, id string) error {
	return c.Call(ctx, "workspace.focus", map[string]any{"workspace_id": id}, nil)
}

// FocusPane focuses one pane, switching to its tab.
func (c *Client) FocusPane(ctx context.Context, paneID string) error {
	return c.Call(ctx, "pane.focus", map[string]any{"pane_id": paneID}, nil)
}

// ReportWorkspaceTokens patches sidebar tokens; a nil value clears that token.
func (c *Client) ReportWorkspaceTokens(ctx context.Context, workspaceID, source string, tokens map[string]*string, ttl time.Duration) error {
	params := map[string]any{"workspace_id": workspaceID, "source": source, "tokens": tokens}
	if ttl > 0 {
		params["ttl_ms"] = ttl.Milliseconds()
	}
	return c.Call(ctx, "workspace.report_metadata", params, nil)
}

type Agent struct {
	PaneID      string `json:"pane_id"`
	WorkspaceID string `json:"workspace_id"`
	Agent       string `json:"agent"`
	Name        string `json:"name"`
	Status      string `json:"agent_status"`
	Cwd         string `json:"cwd"`
	// Tokens are the values plugins reported on the agent's pane, such as the ticket tkt gave it.
	Tokens map[string]string `json:"tokens"`
}

// ReportPaneTokens sets or, with a nil value, clears named values on a pane; they last as long as the pane.
func (c *Client) ReportPaneTokens(ctx context.Context, paneID, source string, tokens map[string]*string) error {
	return c.Call(ctx, "pane.report_metadata", map[string]any{"pane_id": paneID, "source": source, "tokens": tokens}, nil)
}

func (c *Client) Agents(ctx context.Context) ([]Agent, error) {
	var r struct {
		Agents []Agent `json:"agents"`
	}
	err := c.Call(ctx, "agent.list", nil, &r)
	return r.Agents, err
}

// StartAgent starts an agent kind (such as "claude") in a pane sitting at its shell prompt.
// StartAgent gives the agent two minutes to become ready, as a cold start on a busy machine outlasts Herdr's 30 s.
func (c *Client) StartAgent(ctx context.Context, paneID, kind, name string, args []string) error {
	return c.Call(ctx, "agent.start", map[string]any{"pane_id": paneID, "kind": kind, "name": name, "args": args, "timeout_ms": (2 * time.Minute).Milliseconds()}, nil)
}

// SplitPane opens a new pane at cwd to the right of paneID, without moving the focus.
func (c *Client) SplitPane(ctx context.Context, paneID, cwd string) (Pane, error) {
	var r struct {
		Pane Pane `json:"pane"`
	}
	err := c.Call(ctx, "pane.split", map[string]any{"target_pane_id": paneID, "direction": "right", "cwd": cwd}, &r)
	return r.Pane, err
}

// WaitAgent blocks until the agent in target reaches status, or fails after timeout.
func (c *Client) WaitAgent(ctx context.Context, target, status string, timeout time.Duration) error {
	return c.Call(ctx, "agent.wait", map[string]any{"target": target, "until": []string{status}, "timeout_ms": timeout.Milliseconds()}, nil)
}

// Prompt submits text to an agent; target is a pane id.
func (c *Client) Prompt(ctx context.Context, target, text string) error {
	return c.Call(ctx, "agent.prompt", map[string]any{"target": target, "text": text}, nil)
}

type PaneOpen struct {
	PluginID     string            `json:"plugin_id"`
	Entrypoint   string            `json:"entrypoint"`
	Placement    string            `json:"placement,omitempty"`
	Cwd          string            `json:"cwd,omitempty"`
	Env          map[string]string `json:"env,omitempty"`
	Width        string            `json:"width,omitempty"`
	Height       string            `json:"height,omitempty"`
	WorkspaceID  string            `json:"workspace_id,omitempty"`
	TargetPaneID string            `json:"target_pane_id,omitempty"`
	// Focus moves the focus to the new pane; Herdr leaves it where it was otherwise.
	Focus bool `json:"focus,omitempty"`
}

func (c *Client) OpenPluginPane(ctx context.Context, p PaneOpen) error {
	// Herdr rejects a split that names a workspace, even beside a target pane; the target already places it.
	if p.TargetPaneID != "" {
		p.WorkspaceID = ""
	}
	return c.Call(ctx, "plugin.pane.open", p, nil)
}

func (c *Client) FocusPluginPane(ctx context.Context, paneID string) error {
	return c.Call(ctx, "plugin.pane.focus", map[string]any{"pane_id": paneID}, nil)
}

func (c *Client) ClosePluginPane(ctx context.Context, paneID string) error {
	return c.Call(ctx, "plugin.pane.close", map[string]any{"pane_id": paneID}, nil)
}

func (c *Client) ClosePane(ctx context.Context, paneID string) error {
	return c.Call(ctx, "pane.close", map[string]any{"pane_id": paneID}, nil)
}

func (c *Client) CloseWorkspace(ctx context.Context, id string) error {
	return c.Call(ctx, "workspace.close", map[string]any{"workspace_id": id}, nil)
}

func (c *Client) Notify(ctx context.Context, title, body string) error {
	return c.Call(ctx, "notification.show", map[string]any{"title": title, "body": body}, nil)
}

func (c *Client) LinkPlugin(ctx context.Context, path string) error {
	return c.Call(ctx, "plugin.link", map[string]any{"path": path, "enabled": true}, nil)
}

func (c *Client) UnlinkPlugin(ctx context.Context, id string) error {
	return c.Call(ctx, "plugin.unlink", map[string]any{"plugin_id": id}, nil)
}

// ReloadRejected means Herdr read its config but did not apply all of it.
type ReloadRejected struct {
	Status      string
	Diagnostics []string
}

func (e *ReloadRejected) Error() string {
	return fmt.Sprintf("herdr config reload %s: %s", e.Status, strings.Join(e.Diagnostics, "; "))
}

// ReloadConfig asks Herdr to reread its config; a failed or partial reload returns *ReloadRejected.
func (c *Client) ReloadConfig(ctx context.Context) error {
	var r struct {
		Status      string   `json:"status"`
		Diagnostics []string `json:"diagnostics"`
	}
	if err := c.Call(ctx, "server.reload_config", nil, &r); err != nil {
		return err
	}
	if r.Status == "failed" || r.Status == "partial" {
		return &ReloadRejected{Status: r.Status, Diagnostics: r.Diagnostics}
	}
	return nil
}

// Subscription names a Herdr event to follow; agent status changes are followed per pane.
type Subscription struct {
	Type   string `json:"type"`
	PaneID string `json:"pane_id,omitempty"`
}

// Event is one event Herdr streams after events.subscribe.
type Event struct {
	Kind string          `json:"event"`
	Data json.RawMessage `json:"data"`
}

// Watch subscribes to subs and calls on for each event until ctx ends, returning ctx's error, or Herdr
// closes the connection.
func (c *Client) Watch(ctx context.Context, subs []Subscription, on func(Event)) error {
	var d net.Dialer
	conn, err := d.DialContext(ctx, "unix", c.socket)
	if err != nil {
		return ErrNotRunning
	}
	defer conn.Close()
	stop := context.AfterFunc(ctx, func() { conn.Close() })
	defer stop()
	req, err := json.Marshal(map[string]any{"id": "tkt-watch", "method": "events.subscribe", "params": map[string]any{"subscriptions": subs}})
	if err != nil {
		return err
	}
	if _, err := conn.Write(append(req, '\n')); err != nil {
		return fmt.Errorf("herdr events.subscribe: %w", err)
	}
	r := bufio.NewReader(conn)
	line, err := r.ReadBytes('\n')
	if err != nil {
		return orCtx(ctx, fmt.Errorf("herdr events.subscribe: %w", err))
	}
	var ack struct {
		Error *RPCError `json:"error"`
	}
	if err := json.Unmarshal(line, &ack); err != nil {
		return fmt.Errorf("herdr events.subscribe: %w", err)
	}
	if ack.Error != nil {
		return ack.Error
	}
	for {
		line, err := r.ReadBytes('\n')
		if err != nil {
			return orCtx(ctx, fmt.Errorf("herdr events: %w", err))
		}
		var e Event
		if json.Unmarshal(line, &e) == nil && e.Kind != "" {
			on(e)
		}
	}
}

// orCtx prefers ctx's error, since closing the connection on cancel surfaces as a read error.
func orCtx(ctx context.Context, err error) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return err
}
