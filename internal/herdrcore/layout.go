package herdrcore

import (
	"cmp"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/pavp/tkt/internal/herdrport"
)

var (
	// ErrNoWindow means herdr-app cannot reach the Herdr desktop window: it is closed, or it runs on another machine
	// of the core. Terminals keep running in herdr-core; selecting, focusing and naming happen only in the window.
	ErrNoWindow = errors.New("herdr desktop window is not open: open Herdr")
	// ErrNotifyUnsupported: only mods running inside the window can post notifications.
	ErrNotifyUnsupported = fmt.Errorf("herdr desktop: notifications come only from mods: %w", errors.ErrUnsupported)
)

// AppError is a failure herdr-app reported.
type AppError struct {
	Command string
	Message string
}

func (e *AppError) Error() string {
	if e.Is(ErrNoWindow) {
		return ErrNoWindow.Error() + " (herdr-app " + e.Command + ": " + e.Message + ")"
	}
	return "herdr-app " + e.Command + ": " + e.Message
}

func (e *AppError) Is(target error) bool {
	return target == ErrNoWindow && strings.Contains(e.Message, "no Herdr window")
}

// windowFor is how long window keeps its answer.
const windowFor = 30 * time.Second

// window reports, through ErrNoWindow, that herdr-app cannot reach the window: it is closed, or it runs on another
// machine of the core, as when tkt runs over SSH, since only the window's own machine has its socket. Every herdr-app
// answer refreshes it, so a closed window costs one status, not one per action.
func (c *Client) window(ctx context.Context) error {
	c.mu.Lock()
	err, fresh := c.windowErr, !c.windowAt.IsZero() && time.Since(c.windowAt) < windowFor
	c.mu.Unlock()
	if fresh {
		return err
	}
	if _, err = c.appRun(ctx, "status"); err != nil && !errors.Is(err, ErrNoWindow) {
		err = fmt.Errorf("%w (%v)", ErrNoWindow, err)
		c.saw(err)
	}
	return err
}

func (c *Client) saw(windowErr error) {
	c.mu.Lock()
	c.windowErr, c.windowAt = windowErr, time.Now()
	c.mu.Unlock()
}

func (c *Client) appRun(ctx context.Context, args ...string) ([]byte, error) {
	out, err := c.appExec(ctx, args...)
	var app *AppError
	switch {
	case err == nil, errors.As(err, &app) && !app.Is(ErrNoWindow):
		c.saw(nil)
	case errors.Is(err, ErrNoWindow):
		c.saw(err)
	}
	return out, err
}

func (c *Client) appExec(ctx context.Context, args ...string) ([]byte, error) {
	if c.app == nil {
		return nil, fmt.Errorf("herdr-app is not on PATH: %w", ErrNoWindow)
	}
	cmd := args[0]
	if cmd == "do" {
		cmd = args[1]
	}
	out, err := c.app.Run(ctx, nil, args...)
	if err != nil {
		return nil, fmt.Errorf("herdr-app %s: %w", cmd, err)
	}
	if out.Code == 0 {
		return out.Stdout, nil
	}
	for _, b := range [][]byte{out.Stdout, out.Stderr} {
		var r struct {
			Error string `json:"error"`
		}
		if json.Unmarshal(b, &r) == nil && r.Error != "" {
			return nil, &AppError{Command: cmd, Message: r.Error}
		}
	}
	msg := strings.TrimPrefix(strings.TrimSpace(string(out.Stderr)+string(out.Stdout)), "herdr-app: ")
	return nil, &AppError{Command: cmd, Message: cmp.Or(msg, fmt.Sprintf("exit %d", out.Code))}
}

// do runs one window command with its arguments as one JSON object, and returns the IDs it created.
func (c *Client) do(ctx context.Context, command string, args map[string]any) (created, error) {
	if err := c.window(ctx); err != nil {
		return created{}, err
	}
	b, err := json.Marshal(args)
	if err != nil {
		return created{}, err
	}
	out, err := c.appRun(ctx, "do", command, string(b))
	if err != nil {
		return created{}, err
	}
	var r created
	if len(strings.TrimSpace(string(out))) > 0 {
		if err := json.Unmarshal(out, &r); err != nil {
			return created{}, fmt.Errorf("herdr-app %s: decode: %w", command, err)
		}
	}
	return r, nil
}

type created struct {
	Space    string `json:"space"`
	Pane     string `json:"pane"`
	Terminal string `json:"terminal"`
}

type space struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Folder string `json:"folder"`
	Tabs   []struct {
		Terminals []shown `json:"terminals"`
	} `json:"tabs"`
}

// shown is a terminal the window shows, and the pane it shows it in.
type shown struct {
	Terminal string `json:"terminal"`
	Pane     string `json:"pane"`
}

func (s space) shown() []shown {
	var out []shown
	for _, tab := range s.Tabs {
		out = append(out, tab.Terminals...)
	}
	return out
}

// shownWhere finds the first terminal in ss that match accepts.
func shownWhere(ss []space, match func(shown) bool) (shown, bool) {
	for _, s := range ss {
		for _, t := range s.shown() {
			if match(t) {
				return t, true
			}
		}
	}
	return shown{}, false
}

// focusedTerminal is the terminal in the window's focused pane; empty when the window cannot tell, as when closed.
func (c *Client) focusedTerminal(ctx context.Context) string {
	if c.app == nil {
		return ""
	}
	out, err := c.appRun(ctx, "status")
	var st struct {
		FocusedPane string `json:"focused_pane"`
	}
	if err != nil || json.Unmarshal(out, &st) != nil || st.FocusedPane == "" {
		return ""
	}
	ss, err := c.spaces(ctx)
	if err != nil {
		return ""
	}
	t, _ := shownWhere(ss, func(t shown) bool { return t.Pane == st.FocusedPane })
	return t.Terminal
}

func (c *Client) spaces(ctx context.Context) ([]space, error) {
	out, err := c.appRun(ctx, "ls")
	if err != nil {
		return nil, err
	}
	var r struct {
		Spaces []space `json:"spaces"`
	}
	if err := json.Unmarshal(out, &r); err != nil {
		return nil, fmt.Errorf("herdr-app ls: decode: %w", err)
	}
	return r.Spaces, nil
}

// Workspaces lists the window's spaces. A space without a name is called after its folder, as the window
// calls it. Herdr desktop manages no worktrees, so none has one: callers find work through pane folders.
func (c *Client) Workspaces(ctx context.Context) ([]herdrport.Workspace, error) {
	ss, err := c.spaces(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]herdrport.Workspace, len(ss))
	for i, s := range ss {
		out[i] = herdrport.Workspace{ID: s.ID, Label: cmp.Or(s.Name, filepath.Base(s.Folder)), Folder: s.Folder}
	}
	return out, nil
}

// CreateWorkspace opens a space at cwd whose first terminal is a shell. herdr-core starts the shell labelled with a
// space id the window does not know yet, which makes the window open a space for it: unlike herdr-app, which reaches
// the window only on its own machine, hc works on every machine of the core, as over SSH. The space is named label
// where the window can be reached; an empty label leaves it to Herdr, which names it after its repository.
func (c *Client) CreateWorkspace(ctx context.Context, cwd, label string, _ bool) (herdrport.Workspace, herdrport.Pane, error) {
	return c.newSpace(ctx, cwd, label, nil)
}

// promptArgs are the kinds that take their first message as an argument, which reaches them however long they take
// to start; any other kind is prompted once ready.
var promptArgs = map[string]bool{"claude": true, "codex": true}

// OpenAgentSpace opens a space at folder whose first terminal runs kind, given task as its first message. It returns
// the space and the agent's terminal.
func (c *Client) OpenAgentSpace(ctx context.Context, folder, name, kind, task string) (herdrport.Workspace, herdrport.Pane, error) {
	argv := append([]string{c.program(kind)}, kindArgs[kind]...)
	if promptArgs[kind] {
		argv = append(argv, task)
	}
	ws, pane, err := c.newSpace(ctx, folder, name, argv)
	if err != nil || promptArgs[kind] {
		return ws, pane, err
	}
	err = c.ready(ctx, pane.ID, kind)
	if errors.Is(err, herdrport.ErrNotReady) {
		err = c.WaitAgent(ctx, pane.ID, "idle", 4*time.Minute)
	}
	if err == nil {
		err = c.Prompt(ctx, pane.ID, task)
	}
	return ws, pane, err
}

func (c *Client) newSpace(ctx context.Context, cwd, label string, argv []string) (herdrport.Workspace, herdrport.Pane, error) {
	id := c.newID("sp")
	address, err := c.start(ctx, runReq{argv: argv, cwd: cwd, labels: map[string]string{labelSpace: id, labelTab: c.newID("tb")}})
	if err != nil {
		return herdrport.Workspace{}, herdrport.Pane{}, err
	}
	if label != "" && c.window(ctx) == nil {
		_ = c.doSettled(ctx, "space.rename", map[string]any{"id": id, "name": label})
	}
	return herdrport.Workspace{ID: id, Label: label}, herdrport.Pane{ID: address, WorkspaceID: id, Cwd: cwd}, nil
}

// newID makes an id like the window's own (sp, tb), which it cannot know yet.
func newID(prefix string) string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return prefix + hex.EncodeToString(b)
}

// FocusWorkspace selects the space, which the window may show only a moment after hc started its first terminal.
func (c *Client) FocusWorkspace(ctx context.Context, id string) error {
	return c.doSettled(ctx, "space.select", map[string]any{"id": id})
}

func (c *Client) FocusPane(ctx context.Context, paneID string) error {
	return c.doSettled(ctx, "pane.focus", map[string]any{"terminal": paneID})
}

// doSettled is do, tried again while the window refuses it, as it does until it shows what hc just started.
func (c *Client) doSettled(ctx context.Context, command string, args map[string]any) error {
	var err error
	for i := range max(c.settleTries, 1) {
		var app *AppError
		if _, err = c.do(ctx, command, args); err == nil || !errors.As(err, &app) || app.Is(ErrNoWindow) {
			return err
		}
		if i+1 < c.settleTries && sleep(ctx, c.settle) != nil {
			return err
		}
	}
	return err
}

func sleep(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// SplitPane opens a shell in cwd (default: paneID's folder) in paneID's space and tab, where the window places it.
func (c *Client) SplitPane(ctx context.Context, paneID, cwd string) (herdrport.Pane, error) {
	t, err := c.get(ctx, paneID)
	if err != nil {
		return herdrport.Pane{}, err
	}
	cwd = cmp.Or(cwd, t.Cwd)
	address, err := c.start(ctx, runReq{cwd: cwd, labels: placedLike(t)})
	if err != nil {
		return herdrport.Pane{}, err
	}
	return herdrport.Pane{ID: address, WorkspaceID: t.Space, Cwd: cwd}, nil
}

// ClosePane ends terminal paneID and removes it from hc, which keeps an ended terminal listed until then. The window
// closes its pane, and its space once no terminal is left in it.
func (c *Client) ClosePane(ctx context.Context, paneID string) error {
	if _, err := c.run(ctx, nil, "kill", "--json", paneID); err != nil {
		return err
	}
	var err error
	for i := range max(c.settleTries, 1) {
		if _, err = c.run(ctx, nil, "rm", "--json", paneID); err == nil || errors.Is(err, ErrNotFound) {
			return nil
		}
		if i+1 < c.settleTries && sleep(ctx, c.settle) != nil {
			break
		}
	}
	return err
}

// CloseWorkspace ends and removes every terminal of the space, which closes it, then, where the window can be reached,
// closes whatever it may still show there, such as a mod pane.
func (c *Client) CloseWorkspace(ctx context.Context, id string) error {
	ts, err := c.Terminals(ctx)
	if err != nil {
		return err
	}
	var errs []error
	for _, t := range ts {
		if inSpace(t, id) {
			errs = append(errs, c.ClosePane(ctx, t.Address))
		}
	}
	if c.window(ctx) == nil {
		_, _ = c.do(ctx, "space.close", map[string]any{"id": id, "confirmed": true})
	}
	return errors.Join(errs...)
}

// inSpace reports whether t shows in space id. The window prefixes another machine's space ids with `machine/`, which
// that machine's terminals carry in their address, not in their labels.
func inSpace(t Terminal, id string) bool {
	machine, local := "", id
	if i := strings.LastIndex(id, "/"); i >= 0 {
		machine, local = id[:i+1], id[i+1:]
	}
	at := ""
	if i := strings.LastIndex(t.Address, "/"); i >= 0 {
		at = t.Address[:i+1]
	}
	return t.Space == local && at == machine
}

// ReportWorkspaceTokens does nothing: a space carries no metadata and the desktop has no sidebar rows that
// read it. Labelling the space's terminals instead would show nowhere and mix with the agents' own tokens.
func (c *Client) ReportWorkspaceTokens(context.Context, string, string, map[string]*string, time.Duration) error {
	return nil
}

func (c *Client) Notify(context.Context, string, string) error {
	return ErrNotifyUnsupported
}
