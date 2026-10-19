package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"

	"golang.org/x/term"

	"github.com/pavp/tkt/internal/config"
	"github.com/pavp/tkt/internal/credstore"
	"github.com/pavp/tkt/internal/execx"
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

	configPath string
	jsonOut    bool
}

func NewApp() (*App, error) {
	env, err := paths.Current()
	if err != nil {
		return nil, err
	}
	stdin := int(os.Stdin.Fd())
	return &App{
		Stdin:        os.Stdin,
		Stdout:       os.Stdout,
		Stderr:       os.Stderr,
		Env:          env,
		Run:          execx.OS{},
		Creds:        credstore.Default(env.GOOS),
		IsTerminal:   func() bool { return term.IsTerminal(stdin) },
		ReadPassword: func() ([]byte, error) { return term.ReadPassword(stdin) },
		NewJira:      func(site, email, token string) JiraAPI { return jira.New(site, email, token) },
	}, nil
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
