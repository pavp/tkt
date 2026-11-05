package herdrcore

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
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
	argv := append(c.argvOf(kind), args...)
	address, err := c.start(ctx, runReq{argv: argv, cwd: pane.Cwd, name: name, labels: placedLike(pane)})
	if err != nil {
		return err
	}
	return c.ready(ctx, address, name)
}

// placedLike are the labels that make the window show a new terminal in t's space and tab.
func placedLike(t Terminal) map[string]string {
	out := map[string]string{}
	for _, k := range []string{labelSpace, labelTab} {
		if v := t.Labels[k]; v != "" {
			out[k] = v
		}
	}
	return out
}

// runReq is one terminal for herdr-core to start, with no parent: it outlives the terminal tkt runs in. No argv
// starts that machine's shell.
type runReq struct {
	argv      []string
	cwd, name string
	labels    map[string]string
	rm        bool
}

// start starts a terminal through the API's terminal.run, which, unlike `hc run`, takes environment variables to set
// over herdr-core's own, and returns its address.
func (c *Client) start(ctx context.Context, r runReq) (string, error) {
	params := map[string]any{"cwd": r.cwd, "no_parent": true, "env": c.env()}
	if len(r.argv) > 0 {
		params["argv"] = r.argv
	}
	if r.name != "" {
		params["name"] = r.name
	}
	if len(r.labels) > 0 {
		params["labels"] = r.labels
	}
	if r.rm {
		params["rm"] = true
	}
	var started struct {
		Address string `json:"address"`
	}
	if err := c.call(ctx, "terminal.run", params, &started); err != nil {
		return "", err
	}
	if started.Address == "" {
		return "", errors.New("hc terminal.run: no address in its reply")
	}
	return started.Address, nil
}

// env is what a terminal tkt starts takes from tkt over herdr-core's environment: herdr-core may run with another
// PATH (on WSL, one without Homebrew's or ~/.local/bin), and an agent's own tkt commands, like the closer, must use the
// config and data of the tkt that started them, and reach the same window (HERDR_APP_HOME). Secrets stay out:
// herdr-core keeps what it is sent.
func (c *Client) env() map[string]string {
	out := map[string]string{}
	if c.environ == nil {
		return out
	}
	for _, kv := range c.environ() {
		k, v, ok := strings.Cut(kv, "=")
		switch {
		case !ok || v == "":
		case k == "PATH", k == "XDG_CONFIG_HOME", k == "XDG_DATA_HOME", k == "HERDR_APP_HOME":
			out[k] = v
		case strings.HasPrefix(k, "TKT_") && !secretName(k):
			out[k] = v
		}
	}
	return out
}

func secretName(k string) bool {
	k = strings.ToUpper(k)
	for _, w := range []string{"TOKEN", "SECRET", "PASS", "KEY", "PAT", "CREDENTIAL", "AUTH"} {
		if strings.Contains(k, w) {
			return true
		}
	}
	return false
}

// ready waits until the agent at address is ready, with ErrNotReady when it starts on a question.
func (c *Client) ready(ctx context.Context, address, name string) error {
	ready := func(state string) (bool, error) {
		switch state {
		case "idle", "done":
			return true, nil
		case "blocked":
			return true, herdrport.ErrNotReady
		case "error":
			return true, fmt.Errorf("herdr desktop: %s reported an error at startup", name)
		}
		return false, nil
	}
	err := c.until(ctx, address, c.questionAfter, ready)
	if !errors.Is(err, ErrTimeout) {
		return err
	}
	// A question at startup, such as Claude Code's folder trust, leaves the terminal live with no status at all,
	// where Herdr 0.9.5 reports it blocked; the caller then waits for the user's answer.
	if t, gerr := c.get(ctx, address); gerr == nil && t.State == "live" && t.Status == "" {
		return herdrport.ErrNotReady
	}
	return c.until(ctx, address, 2*time.Minute-c.questionAfter, ready)
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
// paste when Enter arrives, leaving it unsent, so Enter is pressed again only while the agent stays idle: a
// question is never answered by that Enter. An agent showing no status may be asking one whose default Enter
// would pick (Claude Code's folder trust: "No, exit"), so it gets nothing and the caller waits as for a blocked one.
func (c *Client) Prompt(ctx context.Context, target, text string) error {
	if t, err := c.get(ctx, target); err != nil {
		return err
	} else if t.Status == "" {
		return fmt.Errorf("%s shows no status: %w", target, herdrport.ErrBlocked)
	}
	var sent struct {
		Started *bool `json:"started"`
	}
	// Through the API with no caller: hc send names the terminal tkt runs in as the sender, and agents read a
	// prompt wrapped as <hc from="t3"> as another agent's report, not as their task.
	if err := c.call(ctx, "terminal.send", map[string]any{"terminal": target, "text": text}, &sent); err != nil {
		return err
	}
	if sent.Started == nil || *sent.Started {
		return nil
	}
	t, err := c.get(ctx, target)
	if err != nil || t.Status != "idle" && t.Status != "done" {
		return err
	}
	_, err = c.run(ctx, nil, "keys", "--json", t.Address, "enter")
	return err
}

// AgentPane is the terminal of the agent named name, which StartAgent opened apart from the pane it was given.
func (c *Client) AgentPane(ctx context.Context, name string) (string, error) {
	t, err := c.get(ctx, name)
	return t.Address, err
}

// RunDetached runs argv in a terminal herdr-core owns, with no parent, removed once it exits: it outlives the
// terminal it was started from, which ends every process started inside it when it closes.
func (c *Client) RunDetached(ctx context.Context, cwd string, argv []string) error {
	_, err := c.start(ctx, runReq{argv: argv, cwd: cwd, rm: true})
	return err
}

// program is the program to run for an agent kind: its full path when tkt finds it, else the name for herdr-core to look up.
// argvOf is the command line that starts an agent: Herdr desktop's run for it when there is one, else its program
// with the arguments tkt gives that kind.
func (c *Client) argvOf(kind string) []string {
	if c.runOf != nil {
		if run := c.runOf(kind); len(run) > 0 {
			return append([]string{c.program(run[0])}, run[1:]...)
		}
	}
	return append([]string{c.program(kind)}, kindArgs[kind]...)
}

// programKind is the agent kind of the program that starts kind: its run's first word when Herdr desktop starts it
// through another agent, else kind itself; it decides whether the task goes on the command line.
func (c *Client) programKind(kind string) string {
	if c.runOf != nil {
		if run := c.runOf(kind); len(run) > 0 {
			return run[0]
		}
	}
	return kind
}

func (c *Client) program(kind string) string {
	if c.lookPath != nil {
		if p, err := c.lookPath(kind); err == nil {
			return p
		}
	}
	return kind
}
