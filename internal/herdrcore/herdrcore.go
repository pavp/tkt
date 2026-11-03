// Package herdrcore talks to the Herdr desktop through its hc command line.
package herdrcore

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"time"

	"github.com/pavp/tkt/internal/herdrport"
)

var (
	ErrNotInstalled = errors.New("herdr desktop is not installed (no hc on PATH)")
	ErrNotRunning   = errors.New("herdr desktop is not running: open Herdr")
	ErrNotFound     = errors.New("herdr desktop: no such terminal")
)

// Output is what one hc run printed and how it exited.
type Output struct {
	Stdout, Stderr []byte
	Code           int
}

// Runner runs one program, such as hc. Run fails only when the program could not run; a failing program
// reports through Output.Code. Stream's output ends when ctx ends, and Close reports how the program exited.
type Runner interface {
	Run(ctx context.Context, stdin []byte, args ...string) (Output, error)
	Stream(ctx context.Context, args ...string) (io.ReadCloser, error)
}

type Client struct {
	hc Runner
	// app is herdr-app, which arranges the window; nil when it is not installed, as on a headless core.
	app Runner
}

// New finds hc, and herdr-app when it is there, on PATH.
func New() (*Client, error) {
	path, err := exec.LookPath("hc")
	if err != nil {
		return nil, ErrNotInstalled
	}
	c := NewWith(execRunner{path})
	if app, err := exec.LookPath("herdr-app"); err == nil {
		c.app = execRunner{app}
	}
	return c, nil
}

func NewWith(hc Runner) *Client {
	return &Client{hc: hc}
}

// NewWithApp is NewWith plus the herdr-app that layout operations run.
func NewWithApp(hc, app Runner) *Client {
	return &Client{hc: hc, app: app}
}

// Error is a failure hc reported; its kinds are listed by `hc schema`.
type Error struct {
	Kind    string `json:"kind"`
	Message string `json:"message"`
	Hint    string `json:"hint"`
}

func (e *Error) Error() string {
	s := "herdr desktop: " + e.Kind + ": " + e.Message
	if e.Hint != "" {
		s += " (" + e.Hint + ")"
	}
	return s
}

func (e *Error) Is(target error) bool {
	switch target {
	case herdrport.ErrNameTaken:
		return e.Kind == "name_taken"
	case herdrport.ErrBlocked:
		return e.Kind == "blocked"
	case ErrNotFound:
		return e.Kind == "not_found"
	case ErrExited:
		return e.Kind == "finished"
	case ErrNotRunning:
		return e.Kind == "unreachable" || e.Kind == "unavailable"
	}
	return false
}

// run runs hc and returns what it printed, or hc's error, which it prints on stderr as JSON when it can.
func (c *Client) run(ctx context.Context, stdin []byte, args ...string) ([]byte, error) {
	out, err := c.hc.Run(ctx, stdin, args...)
	if err != nil {
		return nil, fmt.Errorf("hc %s: %w", args[0], err)
	}
	if out.Code != 0 {
		return nil, failure(args[0], out.Code, out.Stderr)
	}
	return out.Stdout, nil
}

func failure(cmd string, code int, stderr []byte) error {
	var r struct {
		Error *Error `json:"error"`
	}
	if json.Unmarshal(stderr, &r) == nil && r.Error != nil {
		return r.Error
	}
	return fmt.Errorf("hc %s: exit %d: %s", cmd, code, strings.TrimSpace(string(stderr)))
}

// call sends one request over `hc connect`, which returns the API's full objects where hc's commands
// print summaries: `hc ls` leaves out a terminal's labels, and with them its space and tab.
func (c *Client) call(ctx context.Context, method string, params, result any) error {
	req, err := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": method, "params": params})
	if err != nil {
		return err
	}
	out, err := c.run(ctx, append(req, '\n'), "connect")
	if err != nil {
		return err
	}
	var resp struct {
		Result json.RawMessage `json:"result"`
		Error  *struct {
			Message string `json:"message"`
			Data    Error  `json:"data"`
		} `json:"error"`
	}
	if err := json.Unmarshal(out, &resp); err != nil {
		return fmt.Errorf("hc %s: decode: %w", method, err)
	}
	if resp.Error != nil {
		e := resp.Error.Data
		e.Message = resp.Error.Message
		return &e
	}
	return json.Unmarshal(resp.Result, result)
}

type execRunner struct {
	path string
}

func (r execRunner) Run(ctx context.Context, stdin []byte, args ...string) (Output, error) {
	cmd := exec.CommandContext(ctx, r.path, args...)
	cmd.WaitDelay = time.Second
	var stdout, stderr bytes.Buffer
	cmd.Stdin, cmd.Stdout, cmd.Stderr = bytes.NewReader(stdin), &stdout, &stderr
	err := cmd.Run()
	var exit *exec.ExitError
	if errors.As(err, &exit) && ctx.Err() == nil {
		return Output{Stdout: stdout.Bytes(), Stderr: stderr.Bytes(), Code: exit.ExitCode()}, nil
	}
	if err != nil {
		return Output{}, err
	}
	return Output{Stdout: stdout.Bytes(), Stderr: stderr.Bytes()}, nil
}

func (r execRunner) Stream(ctx context.Context, args ...string) (io.ReadCloser, error) {
	cmd := exec.CommandContext(ctx, r.path, args...)
	cmd.WaitDelay = time.Second
	s := &stream{cmd: cmd, name: args[0]}
	cmd.Stderr = &s.stderr
	out, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	s.ReadCloser = out
	return s, nil
}

type stream struct {
	io.ReadCloser
	cmd    *exec.Cmd
	name   string
	stderr bytes.Buffer
}

func (s *stream) Close() error {
	err := s.cmd.Wait()
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		return failure(s.name, exit.ExitCode(), s.stderr.Bytes())
	}
	return err
}

// Running reports whether herdr-core answers `hc server status`.
func (c *Client) Running(ctx context.Context) bool {
	out, err := c.hc.Run(ctx, nil, "server", "status")
	return err == nil && out.Code == 0
}
