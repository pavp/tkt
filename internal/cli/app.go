package cli

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"golang.org/x/term"

	"github.com/pavp/tkt/internal/config"
	"github.com/pavp/tkt/internal/credstore"
	"github.com/pavp/tkt/internal/execx"
	"github.com/pavp/tkt/internal/github"
	"github.com/pavp/tkt/internal/jira"
	"github.com/pavp/tkt/internal/paths"
	"github.com/pavp/tkt/internal/repos"
	"github.com/pavp/tkt/internal/workspace"
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
	Launch   func(dir, name string, args []string) error
	NewHerdr func() HerdrAPI
	// HerdrRunning reports which Herdrs answer: Herdr 0.9.5 on its socket, and Herdr desktop.
	HerdrRunning func(ctx context.Context) (classic, desktop bool)
	// herdrOf opens a client for one Herdr; the daemon, which follows every running Herdr, uses it.
	herdrOf    func(herdrFlavor) HerdrAPI
	Executable func() (string, error)
	// OS is the operating system tkt runs on as runtime.GOOS names it; empty means runtime.GOOS.
	OS string
	// shellCache keeps the interactive shell's PATH across agent lookups; nil asks every time.
	shellCache *shellPathCache
	// OrgMember reports whether a gh token belongs to an active member of a GitHub organization.
	OrgMember func(ctx context.Context, token, org string) (bool, error)
	// StartDetached starts a program that outlives tkt, writing its output to logPath, and returns its pid.
	StartDetached func(name string, args []string, logPath string) (int, error)
	// Signal sends sig to the process pid.
	Signal func(pid int, sig syscall.Signal) error

	// tokens remembers Jira tokens read from the credential store, which under WSL costs a PowerShell
	// start per read; they stay in memory only.
	tokenMu sync.Mutex
	tokens  map[string]string
	// jiraClients keeps one Jira client per site, email and token; tokenMu guards it too.
	jiraClients map[string]JiraAPI

	herdrsOnce sync.Once
	herdrs     *herdrSet
	// windowNoted makes noteWindow tell the user once.
	windowNoted sync.Once

	// syncWarnings are the last sync's warnings, so the daemon logs only the new ones.
	warnMu       sync.Mutex
	syncWarnings []string

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
	a.OrgMember = github.New(nil).OrgMember
	a.NewHerdr = a.chosenHerdr
	a.HerdrRunning = a.herdrRunning
	a.herdrOf = a.newHerdrOf
	a.StartDetached = startDetached
	a.Signal = syscall.Kill
	a.Executable = executable
	a.shellCache = &shellPathCache{}
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
	a.expandPaths(c)
	return c, nil
}

// inLanguage is prompt in the language the configuration asks agents to talk in.
func (a *App) inLanguage(prompt string) string {
	c, err := config.Load(a.ConfigPath())
	if err != nil {
		return prompt
	}
	return workspace.InLanguage(prompt, c.Language)
}

func (a *App) expandPaths(c *config.Config) {
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
// plugin and agent launch commands store this path. On Linux exe names the running binary even after an
// upgrade removed it, so the daemon's upgrade check then takes Homebrew's link or the tkt on PATH instead.
func stableExecutable(exe string, lookPath func(string) (string, error), eval func(string) (string, error)) (string, error) {
	resolved, err := eval(exe)
	if errors.Is(err, fs.ErrNotExist) {
		if prefix, _, ok := strings.Cut(exe, "/Cellar/tkt/"); ok {
			link := filepath.Join(prefix, "bin", "tkt")
			if _, lerr := eval(link); lerr == nil {
				return link, nil
			}
		}
		if onPath, lerr := lookPath("tkt"); lerr == nil {
			return filepath.Abs(onPath)
		}
	}
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
