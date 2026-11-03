package herdrcore

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/pavp/tkt/internal/herdrport"
)

var (
	ErrExited  = errors.New("herdr desktop: the program has ended")
	ErrTimeout = errors.New("herdr desktop: timed out waiting for the agent")
)

// kindApps maps tkt's agent kinds, named after their executables as Herdr 0.9.5 names them, to the app
// each one reports to hc where the two differ.
var kindApps = map[string]string{"claude": "claude-code"}

// kindArgs are what a kind needs to run under hc: Codex otherwise runs its commands outside hc.
var kindArgs = map[string][]string{"codex": {"--no-daemon"}}

func kindOf(app string) string {
	for k, a := range kindApps {
		if a == app {
			return k
		}
	}
	return app
}

func (c *Client) get(ctx context.Context, target string) (Terminal, error) {
	var t terminal
	if err := c.call(ctx, "terminal.get", map[string]any{"terminal": target}, &t); err != nil {
		return Terminal{}, err
	}
	return t.port(), nil
}

// StartAgent runs kind in a new terminal named name: hc cannot start a program inside a live terminal, so
// it opens beside paneID, in its folder and, through the labels herdr-app places terminals by, in its space
// and tab. paneID stays the terminal it was; later calls reach the agent by name. Like Herdr 0.9.5 it
// returns once the agent is ready, with ErrNotReady when it starts on a question.
func (c *Client) StartAgent(ctx context.Context, paneID, kind, name string, args []string) error {
	pane, err := c.get(ctx, paneID)
	if err != nil {
		return err
	}
	run := []string{"run", "--bg", "--json", "--no-parent", "--name", name, "--cwd", pane.Cwd}
	for _, k := range []string{labelSpace, labelTab} {
		if v := pane.Labels[k]; v != "" {
			run = append(run, "--label", k+"="+v)
		}
	}
	run = append(append(append(run, "--", kind), kindArgs[kind]...), args...)
	out, err := c.run(ctx, nil, run...)
	if err != nil {
		return err
	}
	var started struct {
		Address string `json:"address"`
	}
	if err := json.Unmarshal(out, &started); err != nil || started.Address == "" {
		return fmt.Errorf("hc run: unexpected output: %s", out)
	}
	return c.until(ctx, started.Address, 2*time.Minute, func(state string) (bool, error) {
		switch state {
		case "idle", "done":
			return true, nil
		case "blocked":
			return true, herdrport.ErrNotReady
		case "error":
			return true, fmt.Errorf("herdr desktop: %s reported an error at startup", name)
		}
		return false, nil
	})
}

// WaitAgent waits until target reports status; done counts as idle, since both wait for input.
func (c *Client) WaitAgent(ctx context.Context, target, status string, timeout time.Duration) error {
	return c.until(ctx, target, timeout, func(state string) (bool, error) {
		return state == status || status == "idle" && state == "done", nil
	})
}

// until follows target's status until accept settles it. hc wait cannot: it returns on any status but
// working, so it never outlasts a question.
func (c *Client) until(ctx context.Context, target string, timeout time.Duration, accept func(state string) (bool, error)) error {
	t, err := c.get(ctx, target)
	if err != nil {
		return err
	}
	if ok, err := accept(t.Status); ok {
		return err
	}
	wctx, cancel := context.WithTimeout(ctx, timeout)
	rc, err := c.hc.Stream(wctx, "events", "--json", "--terminal", t.Address)
	if err != nil {
		cancel()
		return fmt.Errorf("hc events: %w", err)
	}
	// The stream ends only once its context does, so cancel before Close waits for hc.
	defer func() {
		cancel()
		_ = rc.Close()
	}()
	r := bufio.NewReader(rc)
	for {
		b, rerr := r.ReadBytes('\n')
		if state, gone, ok := statusIn(b); gone {
			return fmt.Errorf("%s: %w", target, ErrExited)
		} else if ok {
			if done, err := accept(state); done {
				return err
			}
		}
		if rerr != nil {
			switch {
			case ctx.Err() != nil:
				return ctx.Err()
			case wctx.Err() != nil:
				return fmt.Errorf("%s after %s: %w", target, timeout, ErrTimeout)
			}
			return errors.New("hc events: stream ended")
		}
	}
}

// statusIn reads one events line, filtered to one terminal: its status, or gone once it ended.
func statusIn(b []byte) (state string, gone, ok bool) {
	var l line
	if len(b) == 0 || json.Unmarshal(b, &l) != nil {
		return "", false, false
	}
	switch l.Method {
	case "":
		return snapshotStatus(l.Terminals)
	case "missed":
		var s snapshot
		if json.Unmarshal(l.Params, &s) != nil {
			return "", false, false
		}
		return snapshotStatus(s.Terminals)
	case "event":
		var f fact
		if json.Unmarshal(l.Params, &f) != nil {
			return "", false, false
		}
		switch f.Type {
		case "status":
			if f.Data.Status != nil {
				state = f.Data.Status.State
			}
			return state, false, true
		case "finished", "lost", "removed":
			return "", true, false
		}
	}
	return "", false, false
}

func snapshotStatus(ts []terminal) (state string, gone, ok bool) {
	if len(ts) == 0 {
		return "", true, false
	}
	return ts[0].port().Status, false, true
}

// Prompt pastes text into target and submits it. On a slow terminal the agent can still be reading a long
// paste when Enter arrives, leaving it unsent, so Enter is pressed again when the agent neither started
// working nor is asking a question; a question is never answered by that Enter.
func (c *Client) Prompt(ctx context.Context, target, text string) error {
	out, err := c.run(ctx, nil, "send", "--json", "--", target, text)
	if err != nil {
		return err
	}
	var sent struct {
		Started *bool `json:"started"`
	}
	if err := json.Unmarshal(out, &sent); err != nil {
		return fmt.Errorf("hc send: decode: %w", err)
	}
	if sent.Started == nil || *sent.Started {
		return nil
	}
	t, err := c.get(ctx, target)
	if err != nil || t.Status == "blocked" || t.Status == "working" {
		return err
	}
	_, err = c.run(ctx, nil, "keys", "--json", t.Address, "enter")
	return err
}
