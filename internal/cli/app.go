package cli

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/term"

	"github.com/pavp/tkt/internal/config"
	"github.com/pavp/tkt/internal/credstore"
	"github.com/pavp/tkt/internal/execx"
	"github.com/pavp/tkt/internal/herdr"
	"github.com/pavp/tkt/internal/jira"
	"github.com/pavp/tkt/internal/paths"
	"github.com/pavp/tkt/internal/repos"
)

var Version = "dev"

// App carries every dependency commands touch, so tests can replace the outside world.
type App struct {
	Stdin  io.Reader
	Stdout io.Writer
	Stderr io.Writer
	Env    paths.Env
	Run    execx.Runner
	Creds  credstore.Store

	IsTerminal   func() bool
	ReadPassword func() ([]byte, error)
	NewJira      func(site, email, token string) JiraAPI
	NewGitHub    func(c *config.Config) GitHubAPI
	Now          func() time.Time
	Sleep        func(ctx context.Context, d time.Duration) error
	// Launch runs an interactive program attached to the terminal and waits for it.
	Launch     func(dir, name string, args []string) error
	NewHerdr   func() HerdrAPI
	Executable func() (string, error)

	in *bufio.Reader

	configPath string
	jsonOut    bool
}

func NewApp() (*App, error) {
	env, err := paths.Current()
	if err != nil {
		return nil, err
	}
	stdin := int(os.Stdin.Fd())
	a := &App{
		Stdin:        os.Stdin,
		Stdout:       os.Stdout,
		Stderr:       os.Stderr,
		Env:          env,
		Run:          execx.OS{},
		Creds:        credstore.Default(env.GOOS),
		IsTerminal:   func() bool { return term.IsTerminal(stdin) },
		ReadPassword: func() ([]byte, error) { return term.ReadPassword(stdin) },
		NewJira:      func(site, email, token string) JiraAPI { return jira.New(site, email, token) },
		Now:          time.Now,
		Sleep:        sleep,
		Launch:       launch,
	}
	a.NewGitHub = a.githubFor
	a.NewHerdr = func() HerdrAPI { return herdr.New(herdr.SocketPath(a.Env.Getenv, a.Env.Home)) }
	a.Executable = executable
	return a, nil
}

func (a *App) ConfigPath() string {
	if a.configPath != "" {
		return a.configPath
	}
	return a.Env.ConfigFile()
}

// LoadConfig reads the configuration and expands "~" in every path it holds.
func (a *App) LoadConfig() (*config.Config, error) {
	c, err := config.Load(a.ConfigPath())
	if err != nil {
		return nil, err
	}
	for i, r := range c.RepoRoots {
		c.RepoRoots[i] = a.Env.ExpandHome(r)
	}
	for i := range c.Spaces {
		for j := range c.Spaces[i].Repos {
			if p := c.Spaces[i].Repos[j].Path; p != "" {
				c.Spaces[i].Repos[j].Path = a.Env.ExpandHome(p)
			}
		}
	}
	return c, nil
}

func (a *App) Resolver(c *config.Config) repos.Resolver {
	return repos.Resolver{Roots: c.RepoRoots, Run: a.Run}
}

func (a *App) printJSON(v any) error {
	enc := json.NewEncoder(a.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

func (a *App) printf(format string, args ...any) {
	fmt.Fprintf(a.Stdout, format, args...)
}

// errQuiet ends a command with a non-zero exit status after it already explained the problem.
var errQuiet = errors.New("quiet failure")

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

func launch(dir, name string, args []string) error {
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	return cmd.Run()
}

// readLine prints prompt on stderr and reads one answer line; io.EOF means no more input.
func (a *App) readLine(prompt string) (string, error) {
	if a.in == nil {
		a.in = bufio.NewReader(a.Stdin)
	}
	if prompt != "" {
		fmt.Fprint(a.Stderr, prompt)
	}
	line, err := a.in.ReadString('\n')
	if err != nil && line == "" {
		return "", err
	}
	return strings.TrimSpace(line), nil
}

func executable() (string, error) {
	p, err := os.Executable()
	if err != nil {
		return "", err
	}
	return stableExecutable(p, exec.LookPath, filepath.EvalSymlinks)
}

// stableExecutable resolves exe, but returns the tkt found on PATH when it is the same binary: package
// managers such as Homebrew run tkt through a symlink whose target moves on every upgrade, and the Herdr
// plugin and agent launch commands store this path.
func stableExecutable(exe string, lookPath func(string) (string, error), eval func(string) (string, error)) (string, error) {
	resolved, err := eval(exe)
	if err != nil {
		return "", err
	}
	if onPath, err := lookPath("tkt"); err == nil {
		if abs, err := filepath.Abs(onPath); err == nil {
			if r, err := eval(abs); err == nil && r == resolved {
				return abs, nil
			}
		}
	}
	return resolved, nil
}
