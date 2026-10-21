package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/coder/websocket"

	"github.com/pavp/tkt/internal/board"
)

// ErrNotSynced means the daemon runs but has no board yet: it started without a cache and its first
// sync has not finished.
var ErrNotSynced = errors.New("the tkt daemon has no board yet")

// Client talks to a running daemon on behalf of the board and tkt list.
type Client struct {
	base  string
	ws    string
	token string
	http  *http.Client
	slow  *http.Client
}

// Event is one message the daemon streams.
type Event struct {
	Seq  int             `json:"seq"`
	Type string          `json:"type"`
	TS   time.Time       `json:"ts"`
	Data json.RawMessage `json:"data"`
}

// Dial prepares a client for the daemon info describes; it reads the token but never creates one.
func Dial(dir string, info Info) (*Client, error) {
	b, err := os.ReadFile(filepath.Join(dir, "token"))
	if err != nil {
		return nil, err
	}
	addr := fmt.Sprintf("127.0.0.1:%d", info.Port)
	return &Client{
		base: "http://" + addr, ws: "ws://" + addr + "/v1/ws", token: strings.TrimSpace(string(b)),
		http: &http.Client{Timeout: 5 * time.Second}, slow: &http.Client{Timeout: 30 * time.Second},
	}, nil
}

func (c *Client) do(ctx context.Context, method, path string) (*http.Response, error) {
	return c.send(ctx, c.http, method, path)
}

func (c *Client) send(ctx context.Context, hc *http.Client, method, path string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	return hc.Do(req)
}

// Ticket decodes one ticket's detail into out. The daemon asks Jira for it, so it may take seconds.
func (c *Client) Ticket(ctx context.Context, key string, out any) error {
	path := "/v1/tickets/" + url.PathEscape(key)
	resp, err := c.send(ctx, c.slow, http.MethodGet, path)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		var e struct{ Message string }
		if json.NewDecoder(resp.Body).Decode(&e) == nil && e.Message != "" {
			return fmt.Errorf("tkt daemon: GET %s: %s", path, e.Message)
		}
		return fmt.Errorf("tkt daemon: GET %s: %s", path, resp.Status)
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("tkt daemon: GET %s: %w", path, err)
	}
	return nil
}

// Tickets returns the board the daemon serves.
func (c *Client) Tickets(ctx context.Context) (board.Snapshot, error) {
	resp, err := c.do(ctx, http.MethodGet, "/v1/tickets")
	if err != nil {
		return board.Snapshot{}, err
	}
	defer resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusOK:
	case http.StatusServiceUnavailable:
		return board.Snapshot{}, ErrNotSynced
	default:
		return board.Snapshot{}, fmt.Errorf("tkt daemon: GET /v1/tickets: %s", resp.Status)
	}
	var snap board.Snapshot
	if err := json.NewDecoder(resp.Body).Decode(&snap); err != nil {
		return board.Snapshot{}, fmt.Errorf("tkt daemon: GET /v1/tickets: %w", err)
	}
	return snap, nil
}

// Sync asks the daemon to sync now; it does not wait for the sync to finish.
func (c *Client) Sync(ctx context.Context) error {
	resp, err := c.do(ctx, http.MethodPost, "/v1/sync")
	if err != nil {
		return err
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted {
		return fmt.Errorf("tkt daemon: POST /v1/sync: %s", resp.Status)
	}
	return nil
}

// Events streams the daemon's events to on until ctx ends or the daemon goes away.
func (c *Client) Events(ctx context.Context, on func(Event)) error {
	conn, _, err := websocket.Dial(ctx, c.ws, nil)
	if err != nil {
		return err
	}
	defer conn.CloseNow()
	conn.SetReadLimit(-1)
	hello, err := json.Marshal(map[string]any{"type": "hello", "token": c.token, "protocol": Protocol})
	if err != nil {
		return err
	}
	if err := conn.Write(ctx, websocket.MessageText, hello); err != nil {
		return err
	}
	for {
		_, b, err := conn.Read(ctx)
		if err != nil {
			return err
		}
		var e Event
		if json.Unmarshal(b, &e) == nil {
			on(e)
		}
	}
}
