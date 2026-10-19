package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/pavp/tkt/internal/config"
	"github.com/pavp/tkt/internal/paths"
	"github.com/pavp/tkt/internal/repos"
	"github.com/pavp/tkt/internal/ticket"
	"github.com/pavp/tkt/internal/workspace"
)

func (a *App) links() workspace.Links {
	return workspace.NewLinks(a.Env.DataDir())
}

// allWork merges registered links with branches discovered in the clones that exist locally.
func (a *App) allWork(ctx context.Context, c *config.Config) ([]workspace.Work, error) {
	res := a.Resolver(c)
	var clones []workspace.Clone
	for _, r := range c.Repos() {
		dir, err := res.Resolve(ctx, r)
		if err == nil {
			clones = append(clones, workspace.Clone{Repo: r.Name, Dir: dir})
		}
	}
	discovered, err := workspace.Discover(ctx, a.Run, clones, c.SpaceKeys())
	if err != nil {
		return nil, err
	}
	links, err := a.links().Load()
	if err != nil {
		return nil, err
	}
	return workspace.Merge(links, discovered), nil
}

type checkout struct {
	branch, top, repo string
}

func (a *App) inspectCheckout(ctx context.Context, dir string) (checkout, error) {
	git := func(args ...string) (string, error) {
		out, err := a.Run.Run(ctx, dir, "git", args, nil)
		return strings.TrimSpace(string(out)), err
	}
	branch, err := git("rev-parse", "--abbrev-ref", "HEAD")
	if err != nil {
		return checkout{}, errors.New("not inside a git checkout: run `tkt link` from the ticket's worktree")
	}
	if branch == "HEAD" {
		return checkout{}, errors.New("HEAD is detached: check out the ticket's branch first")
	}
	top, err := git("rev-parse", "--show-toplevel")
	if err != nil {
		return checkout{}, err
	}
	co := checkout{branch: branch, top: top}
	if remote, err := git("remote", "get-url", "origin"); err == nil {
		if owner, name, ok := repos.ParseRemote(remote); ok {
			co.repo = owner + "/" + name
		}
	}
	return co, nil
}

func newLinkCmd(a *App) *cobra.Command {
	var dir, source string
	cmd := &cobra.Command{
		Use:   "link [KEY]",
		Short: "Register the current worktree as the work on a ticket",
		Long: "Register the current worktree and branch as the work on a ticket, so the board shows it right away.\n" +
			"Agents run it after creating their branch. The branch name must contain the ticket key; without\n" +
			"KEY, the key is taken from the branch.",
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := a.LoadConfig()
			if err != nil {
				return err
			}
			co, err := a.inspectCheckout(cmd.Context(), dir)
			if err != nil {
				return err
			}
			key, err := linkKey(c, co.branch, args)
			if err != nil {
				return err
			}
			if co.repo == "" {
				return errors.New("the checkout has no GitHub origin remote, so tkt cannot match it to pull requests")
			}
			src := workspace.SourceAgent
			if source == "hook" {
				src = workspace.SourceHook
			}
			w := workspace.Work{Key: key, Repo: co.repo, Branch: co.branch, Path: co.top, Source: src, LinkedAt: a.Now()}
			if err := a.links().Add(w); err != nil {
				return err
			}
			known := false
			for _, r := range c.Repos() {
				known = known || strings.EqualFold(r.Name, co.repo)
			}
			if a.jsonOut {
				return a.printJSON(w)
			}
			a.printf("✓ %s ← %s (%s)\n", key, co.branch, co.repo)
			if !known {
				fmt.Fprintf(a.Stderr, "! %s is not in your config; add it under the %s space to see its pull requests\n", co.repo, key.Space())
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&dir, "dir", "", "worktree to register (default: current directory)")
	cmd.Flags().StringVar(&source, "source", "agent", "who registers the work: agent or hook")
	_ = cmd.Flags().MarkHidden("source")
	return cmd
}

// linkKey enforces the one branch-naming invariant: the branch must contain the ticket key.
func linkKey(c *config.Config, branch string, args []string) (ticket.Key, error) {
	inBranch, found := ticket.KeyInBranch(branch, c.SpaceKeys())
	if len(args) == 0 {
		if !found {
			return "", fmt.Errorf("branch %q has no ticket key; rename it to include one, e.g. feature/%s-123-short-name", branch, firstSpace(c))
		}
		return inBranch, nil
	}
	want := ticket.Key(strings.ToUpper(args[0]))
	for _, k := range ticket.KeysIn(branch) {
		if k == want {
			return want, nil
		}
	}
	return "", fmt.Errorf("branch %q does not contain %s; rename it to include the key, e.g. feature/%s-short-name", branch, want, want)
}

func firstSpace(c *config.Config) string {
	if len(c.Spaces) == 0 {
		return "DEMO"
	}
	return c.Spaces[0].Key
}

// state remembers small per-user choices, such as the repositories last picked for each space.
type state struct {
	LastRepos map[string][]string `json:"last_repos,omitempty"`
}

func (a *App) statePath() string { return filepath.Join(a.Env.DataDir(), "state.json") }

func (a *App) loadState() state {
	var s state
	b, err := os.ReadFile(a.statePath())
	if err == nil {
		_ = json.Unmarshal(b, &s)
	} else if !errors.Is(err, fs.ErrNotExist) {
		fmt.Fprintf(a.Stderr, "! could not read %s: %v\n", a.statePath(), err)
	}
	if s.LastRepos == nil {
		s.LastRepos = map[string][]string{}
	}
	return s
}

func (a *App) saveState(s state) {
	b, err := json.MarshalIndent(s, "", "  ")
	if err == nil {
		err = paths.WritePrivateFile(a.statePath(), b)
	}
	if err != nil {
		fmt.Fprintf(a.Stderr, "! could not save %s: %v\n", a.statePath(), err)
	}
}
