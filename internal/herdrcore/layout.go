package herdrcore

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/pavp/tkt/internal/herdrport"
)

var (
	// ErrNoWindow means the Herdr desktop window is closed: its terminals keep running in herdr-core, but
	// spaces, tabs and panes exist only in the window.
	ErrNoWindow = errors.New("herdr desktop window is not open: open Herdr")
	// ErrNotifyUnsupported: only mods running inside the window can post notifications.
	ErrNotifyUnsupported = fmt.Errorf("herdr desktop: notifications come only from mods: %w", errors.ErrUnsupported)
)

// AppError is a failure herdr-app reported.
type AppError struct {
	Command string
	Message string
}

func (e *AppError) Error() string { return "herdr-app " + e.Command + ": " + e.Message }

func (e *AppError) Is(target error) bool {
	return target == ErrNoWindow && strings.Contains(e.Message, "no Herdr window")
}

func (c *Client) appRun(ctx context.Context, args ...string) ([]byte, error) {
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
		Terminals []struct {
			Terminal string `json:"terminal"`
			Pane     string `json:"pane"`
		} `json:"terminals"`
	} `json:"tabs"`
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
		out[i] = herdrport.Workspace{ID: s.ID, Label: cmp.Or(s.Name, filepath.Base(s.Folder))}
	}
	return out, nil
}

// CreateWorkspace opens a space on this computer. The window always shows a new space, whatever focus says.
func (c *Client) CreateWorkspace(ctx context.Context, cwd, label string, _ bool) (herdrport.Workspace, herdrport.Pane, error) {
	r, err := c.do(ctx, "space.new", map[string]any{"name": label, "folder": cwd, "machine": ""})
	if err != nil {
		return herdrport.Workspace{}, herdrport.Pane{}, err
	}
	if r.Space == "" {
		return herdrport.Workspace{}, herdrport.Pane{}, errors.New("herdr-app space.new: no space in its reply")
	}
	if r.Terminal == "" {
		ss, err := c.spaces(ctx)
		if err != nil {
			return herdrport.Workspace{}, herdrport.Pane{}, err
		}
		for _, s := range ss {
			if s.ID == r.Space && len(s.Tabs) > 0 && len(s.Tabs[0].Terminals) > 0 {
				r.Terminal = s.Tabs[0].Terminals[0].Terminal
			}
		}
	}
	if r.Terminal == "" {
		return herdrport.Workspace{}, herdrport.Pane{}, fmt.Errorf("herdr-app space.new: space %s has no terminal", r.Space)
	}
	return herdrport.Workspace{ID: r.Space, Label: label}, herdrport.Pane{ID: r.Terminal, WorkspaceID: r.Space, Cwd: cwd}, nil
}

func (c *Client) FocusWorkspace(ctx context.Context, id string) error {
	_, err := c.do(ctx, "space.select", map[string]any{"id": id})
	return err
}

func (c *Client) FocusPane(ctx context.Context, paneID string) error {
	_, err := c.do(ctx, "pane.focus", map[string]any{"terminal": paneID})
	return err
}

// SplitPane opens a terminal right of paneID, in cwd (default: paneID's folder). pane.split cannot choose a
// folder, so it focuses paneID and opens beside the focused pane, which leaves the new terminal focused.
func (c *Client) SplitPane(ctx context.Context, paneID, cwd string) (herdrport.Pane, error) {
	t, err := c.get(ctx, paneID)
	if err != nil {
		return herdrport.Pane{}, err
	}
	if err := c.FocusPane(ctx, t.Address); err != nil {
		return herdrport.Pane{}, err
	}
	cwd = cmp.Or(cwd, t.Cwd)
	r, err := c.do(ctx, "terminal.open", map[string]any{"folder": cwd, "place": "right"})
	if err != nil {
		return herdrport.Pane{}, err
	}
	if r.Terminal == "" {
		return herdrport.Pane{}, errors.New("herdr-app terminal.open: no terminal in its reply")
	}
	return herdrport.Pane{ID: r.Terminal, WorkspaceID: t.Space, Cwd: cwd}, nil
}

// ClosePane closes the pane showing terminal paneID, which ends it.
func (c *Client) ClosePane(ctx context.Context, paneID string) error {
	ss, err := c.spaces(ctx)
	if err != nil {
		return err
	}
	for _, s := range ss {
		for _, tab := range s.Tabs {
			for _, t := range tab.Terminals {
				if t.Terminal == paneID {
					_, err := c.do(ctx, "pane.close", map[string]any{"pane": t.Pane})
					return err
				}
			}
		}
	}
	return fmt.Errorf("%s is not shown in the window: %w", paneID, ErrNotFound)
}

func (c *Client) CloseWorkspace(ctx context.Context, id string) error {
	_, err := c.do(ctx, "space.close", map[string]any{"id": id, "confirmed": true})
	return err
}

// ReportWorkspaceTokens does nothing: a space carries no metadata and the desktop has no sidebar rows that
// read it. Labelling the space's terminals instead would show nowhere and mix with the agents' own tokens.
func (c *Client) ReportWorkspaceTokens(context.Context, string, string, map[string]*string, time.Duration) error {
	return nil
}

func (c *Client) Notify(context.Context, string, string) error {
	return ErrNotifyUnsupported
}
