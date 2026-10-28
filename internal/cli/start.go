package cli

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/pavp/tkt/internal/config"
	"github.com/pavp/tkt/internal/jira"
	"github.com/pavp/tkt/internal/repos"
	"github.com/pavp/tkt/internal/ticket"
	"github.com/pavp/tkt/internal/tui"
	"github.com/pavp/tkt/internal/workspace"
)

func newStartCmd(a *App) *cobra.Command {
	var repoFlags []string
	var clone bool
	var worktree, agent string
	cmd := &cobra.Command{
		Use:   "start KEY",
		Short: "Open an agent to work on a ticket",
		Long: "Open an agent to work on a ticket. When the ticket already has a worktree, the agent opens there.\n" +
			"Otherwise you pick the repositories (the last ones used for the space are preselected), and the\n" +
			"agent opens in each local clone with instructions to create the branch and worktree following that\n" +
			"project's rules, save the ticket context, and register the work with `tkt link`. tkt itself never\n" +
			"creates branches or changes Jira.\n" +
			"The agents offered are those Herdr can start (`herdr agent start --help`) that are installed; with\n" +
			"several, you pick one (the last one used is preselected) or name it with --agent. Agents take their\n" +
			"first prompt differently, so it goes on the clipboard to paste.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if a.jsonOut {
				return errInteractive("start")
			}
			plan, err := a.planStart(cmd, args, startOpts{repos: repoFlags, clone: clone, worktree: worktree})
			if err != nil {
				return err
			}
			kind, err := a.pickAgent(cmd.Context(), agent)
			if err != nil {
				return err
			}
			var dir, where string
			if plan.Existing != nil {
				dir, where = plan.Existing.Path, fmt.Sprintf("%s (%s)", plan.Existing.Path, plan.Existing.Branch)
			} else {
				dir, where = plan.Dirs[0], plan.Dirs[0]
				for _, d := range plan.Dirs[1:] {
					a.printf("Also start it in %s:\n  cd %q && %s\n", d, d, kind)
				}
			}
			if err := a.copyText(cmd.Context(), plan.Prompt); err != nil {
				a.printf("Opening %s in %s; copy its first prompt (the clipboard failed: %v) and paste it there\n\n%s\n\n", kind, where, err, plan.Prompt)
			} else {
				a.printf("Opening %s in %s; its first prompt is on the clipboard: paste it there\n\n%s\n\n", kind, where, plan.Prompt)
			}
			return a.Launch(dir, kind, nil)
		},
	}
	cmd.Flags().StringArrayVar(&repoFlags, "repo", nil, "repository to start in (owner/name or name); repeat for several")
	cmd.Flags().BoolVar(&clone, "clone", false, "clone missing repositories without asking")
	cmd.Flags().StringVar(&worktree, "worktree", "", "open the ticket's worktree at this path instead of asking which one")
	cmd.Flags().StringVar(&agent, "agent", "", "agent to open, such as claude or opencode, instead of asking")
	cmd.MarkFlagsMutuallyExclusive("worktree", "repo")
	return cmd
}

// startPlan says where the agent should open: an existing worktree, or the local clones of the chosen
// repositories, with the first prompt for the agent.
type startPlan struct {
	Key      ticket.Key
	Summary  string
	Existing *workspace.Work
	Dirs     []string
	Prompt   string
}

// startOpts are the choices a Start may arrive with; prefer, when set, picks among several worktrees
// without asking, and reports false when it cannot tell.
type startOpts struct {
	repos    []string
	clone    bool
	worktree string
	// agent is the agent kind asked for, empty to pick one.
	agent  string
	prefer func([]workspace.Work) (workspace.Work, bool)
}

func (a *App) planStart(cmd *cobra.Command, args []string, opts startOpts) (startPlan, error) {
	repoFlags, clone := opts.repos, opts.clone
	c, err := a.LoadConfig()
	if err != nil {
		return startPlan{}, err
	}
	key, err := a.resolveKey(cmd.Context(), c, args)
	if err != nil {
		return startPlan{}, err
	}
	space, ok := c.Space(key.Space())
	if !ok {
		return startPlan{}, fmt.Errorf("space %s is not configured", key.Space())
	}
	js, err := a.jiraClient(c)
	if err != nil {
		return startPlan{}, err
	}
	is, err := js.Issue(cmd.Context(), string(key))
	if err != nil {
		return startPlan{}, err
	}
	plan := startPlan{Key: key, Summary: is.Summary}
	if len(space.Repos) == 0 {
		if space.Repos, err = a.inferStartRepos(cmd.Context(), js, is, key, repoFlags, space.Ignores); err != nil {
			return startPlan{}, err
		}
		for i := range c.Spaces {
			if c.Spaces[i].Key == space.Key {
				c.Spaces[i].Repos = space.Repos
			}
		}
	}

	if len(repoFlags) == 0 {
		all, err := a.allWork(cmd.Context(), c)
		if err != nil {
			return startPlan{}, err
		}
		var existing []workspace.Work
		for _, w := range workspace.For(all, key) {
			if w.Path != "" {
				existing = append(existing, w)
			}
		}
		if opts.worktree != "" {
			existing = slices.DeleteFunc(existing, func(w workspace.Work) bool {
				return filepath.Clean(w.Path) != filepath.Clean(opts.worktree)
			})
			if len(existing) == 0 {
				return startPlan{}, fmt.Errorf("%s has no worktree at %s", key, opts.worktree)
			}
		}
		if len(existing) > 0 {
			w, ok := workspace.Work{}, false
			if len(existing) > 1 && opts.prefer != nil {
				w, ok = opts.prefer(existing)
			}
			if !ok {
				var err error
				if w, err = a.pickWork(existing); err != nil {
					return startPlan{}, err
				}
			}
			plan.Existing = &w
			plan.Prompt = workspace.ContinuePrompt(key, is.Summary)
			return plan, nil
		}
	}

	selected, err := a.pickRepos(space, repoFlags)
	if err != nil {
		return startPlan{}, err
	}
	st := a.loadState()
	st.LastRepos[space.Key] = names(selected)
	a.saveState(st)

	res := a.Resolver(c)
	for _, r := range selected {
		dir, err := res.Resolve(cmd.Context(), r)
		if errors.Is(err, repos.ErrNotFound) {
			dir, err = a.offerClone(cmd, res, r, clone)
		}
		if err != nil {
			fmt.Fprintf(a.Stderr, "! skipping %s: %v\n", r.Name, err)
			continue
		}
		plan.Dirs = append(plan.Dirs, dir)
	}
	if len(plan.Dirs) == 0 {
		return startPlan{}, errors.New("no repository to start in")
	}
	if cat := ticket.CategoryFor(is.Status, is.StatusCategory, space.Categories()); cat == ticket.ToDo {
		a.printf("→ Suggested: move %s to In Progress in Jira (tkt does not change Jira yet)\n", key)
	}
	plan.Prompt = workspace.StartPrompt(key, is.Summary)
	return plan, nil
}

// errInteractive rejects --json on commands that hand the terminal to the user or an agent.
func errInteractive(name string) error {
	return fmt.Errorf("--json is not supported by `tkt %s`: it is interactive", name)
}

func names(rs []config.Repo) []string {
	out := make([]string, len(rs))
	for i, r := range rs {
		out[i] = r.Name
	}
	return out
}

func (a *App) pickWork(ws []workspace.Work) (workspace.Work, error) {
	if len(ws) == 1 {
		return ws[0], nil
	}
	for i, w := range ws {
		fmt.Fprintf(a.Stderr, "  %d) %s  %s  %s\n", i+1, w.Repo, w.Branch, w.Path)
	}
	ans, err := a.readLine("Open which worktree? [1] ")
	if err != nil && !errors.Is(err, io.EOF) {
		return workspace.Work{}, err
	}
	if ans == "" {
		return ws[0], nil
	}
	n, err := strconv.Atoi(ans)
	if err != nil || n < 1 || n > len(ws) {
		return workspace.Work{}, fmt.Errorf("%q is not one of 1-%d", ans, len(ws))
	}
	return ws[n-1], nil
}

// startChoices says what a Start of key would ask, so the board can ask it first: the worktrees when
// several remain and no single agent tells which, otherwise the space's repositories when there are
// several. h is nil outside Herdr, where agents are unknown.
func (a *App) startChoices(ctx context.Context, c *config.Config, key ticket.Key, h HerdrAPI) (tui.StartChoices, error) {
	space, ok := c.Space(key.Space())
	if !ok {
		return tui.StartChoices{}, fmt.Errorf("space %s is not configured", key.Space())
	}
	all, err := a.allWork(ctx, c)
	if err != nil {
		return tui.StartChoices{}, err
	}
	var worktrees []tui.WorktreeChoice
	withAgent := 0
	for _, w := range workspace.For(all, key) {
		if w.Path == "" {
			continue
		}
		choice := tui.WorktreeChoice{Repo: w.Repo, Branch: w.Branch, Path: w.Path}
		if h != nil {
			if ag, ok := agentAt(ctx, h, w.Path); ok {
				choice.Agent = cmp.Or(ag.Status, "unknown")
				withAgent++
			}
		}
		worktrees = append(worktrees, choice)
	}
	switch {
	case len(worktrees) == 1:
		return tui.StartChoices{Opens: worktrees[0].Agent == ""}, nil
	case len(worktrees) > 1 && withAgent == 1:
		return tui.StartChoices{}, nil
	case len(worktrees) > 1:
		return tui.StartChoices{Worktrees: worktrees}, nil
	case len(space.Repos) <= 1:
		return tui.StartChoices{Opens: true}, nil
	}
	last := map[string]bool{}
	for _, n := range a.loadState().LastRepos[space.Key] {
		last[n] = true
	}
	repos := make([]tui.RepoChoice, len(space.Repos))
	anyLast := false
	for i, r := range space.Repos {
		repos[i] = tui.RepoChoice{Name: r.Name, LastUsed: last[r.Name], Checked: last[r.Name]}
		anyLast = anyLast || last[r.Name]
	}
	if !anyLast {
		repos[0].Checked = true
	}
	return tui.StartChoices{Repos: repos, Opens: true}, nil
}

// pickRepos resolves --repo flags, or asks which of the space's repositories to use.
func (a *App) pickRepos(space config.Space, flags []string) ([]config.Repo, error) {
	if len(space.Repos) == 0 {
		return nil, fmt.Errorf("space %s has no repositories in the config", space.Key)
	}
	if len(flags) > 0 {
		var out []config.Repo
		for _, f := range flags {
			r, ok := findRepo(space, f)
			if !ok {
				return nil, fmt.Errorf("%s is not a repository of space %s (%s)", f, space.Key, strings.Join(names(space.Repos), ", "))
			}
			out = append(out, r)
		}
		return out, nil
	}
	if len(space.Repos) == 1 {
		return space.Repos, nil
	}
	last := map[string]bool{}
	for _, n := range a.loadState().LastRepos[space.Key] {
		last[n] = true
	}
	var def []string
	for i, r := range space.Repos {
		mark := ""
		if last[r.Name] {
			mark = "  (last used)"
			def = append(def, strconv.Itoa(i+1))
		}
		fmt.Fprintf(a.Stderr, "  %d) %s%s\n", i+1, r.Name, mark)
	}
	if len(def) == 0 {
		def = []string{"1"}
	}
	ans, err := a.readLine(fmt.Sprintf("Start in which repositories? (e.g. 1,2) [%s] ", strings.Join(def, ",")))
	if err != nil && !errors.Is(err, io.EOF) {
		return nil, err
	}
	if ans == "" {
		ans = strings.Join(def, ",")
	}
	var out []config.Repo
	seen := map[int]bool{}
	for _, part := range strings.Split(ans, ",") {
		n, err := strconv.Atoi(strings.TrimSpace(part))
		if err != nil || n < 1 || n > len(space.Repos) {
			return nil, fmt.Errorf("%q is not one of 1-%d", strings.TrimSpace(part), len(space.Repos))
		}
		if !seen[n] {
			seen[n] = true
			out = append(out, space.Repos[n-1])
		}
	}
	return out, nil
}

func findRepo(space config.Space, name string) (config.Repo, bool) {
	for _, r := range space.Repos {
		if strings.EqualFold(r.Name, name) || strings.EqualFold(r.ShortName(), name) {
			return r, true
		}
	}
	return config.Repo{}, false
}

func (a *App) offerClone(cmd *cobra.Command, res repos.Resolver, r config.Repo, yes bool) (string, error) {
	if len(res.Roots) == 0 {
		return "", errors.New("not cloned locally and no repo_roots configured to clone into")
	}
	if !yes {
		ans, err := a.readLine(fmt.Sprintf("%s is not cloned locally. Clone it into %s? [Y/n] ", r.Name, res.Roots[0]))
		if err != nil && !errors.Is(err, io.EOF) {
			return "", err
		}
		if errors.Is(err, io.EOF) || strings.HasPrefix(strings.ToLower(ans), "n") {
			return "", errors.New("not cloned locally (pass --clone to clone it)")
		}
	}
	a.printf("Cloning %s…\n", r.Name)
	return res.Clone(cmd.Context(), r)
}

// inferStartRepos finds repositories for a space that configures none: those of the ticket's Jira-linked
// pull requests, those the board already knows for the space, owner/name --repo flags, those of the
// space's other sprint tickets, or, in a terminal, the user's answer; repositories the space ignores are
// skipped unless named with --repo. They live only in memory; the config is never written.
func (a *App) inferStartRepos(ctx context.Context, js JiraAPI, is jira.Issue, key ticket.Key, flags []string, ignores func(string) bool) ([]config.Repo, error) {
	var names []string
	add := func(n string) {
		n = strings.ToLower(strings.TrimSpace(n))
		if n != "" && !slices.Contains(names, n) {
			names = append(names, n)
		}
	}
	found := func(n string) {
		if !ignores(n) {
			add(n)
		}
	}
	if prs, err := js.DevPullRequests(ctx, is.ID); err == nil {
		for _, p := range prs {
			found(p.Repo)
		}
	}
	if snap, ok, err := a.cache().Load(); err == nil && ok {
		for _, r := range snap.Repos[key.Space()] {
			found(r)
		}
	}
	for _, f := range flags {
		if strings.Count(f, "/") == 1 {
			add(f)
		}
	}
	if len(names) == 0 {
		for _, r := range suggestRepos(ctx, js, key.Space()) {
			found(r)
		}
	}
	if len(names) == 0 && a.IsTerminal() {
		ans, err := a.ask("Repository for "+string(key)+" (owner/name)", "")
		if err != nil {
			return nil, err
		}
		if strings.Count(ans, "/") == 1 {
			add(ans)
		}
	}
	if len(names) == 0 {
		return nil, fmt.Errorf("no repository known for %s: pass --repo owner/name or add one to space %s in the config", key, key.Space())
	}
	slices.Sort(names)
	repos := make([]config.Repo, len(names))
	for i, n := range names {
		repos[i] = config.Repo{Name: n}
	}
	return repos, nil
}
