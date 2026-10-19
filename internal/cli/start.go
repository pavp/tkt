package cli

import (
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/pavp/tkt/internal/config"
	"github.com/pavp/tkt/internal/repos"
	"github.com/pavp/tkt/internal/ticket"
	"github.com/pavp/tkt/internal/workspace"
)

func newStartCmd(a *App) *cobra.Command {
	var repoFlags []string
	var clone bool
	cmd := &cobra.Command{
		Use:   "start KEY",
		Short: "Open Claude Code to work on a ticket",
		Long: "Open Claude Code to work on a ticket. When the ticket already has a worktree, the agent opens there.\n" +
			"Otherwise you pick the repositories (the last ones used for the space are preselected), and the\n" +
			"agent opens in each local clone with instructions to create the branch and worktree following that\n" +
			"project's rules, save the ticket context, and register the work with `tkt link`. tkt itself never\n" +
			"creates branches or changes Jira.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			plan, err := a.planStart(cmd, args, repoFlags, clone)
			if err != nil {
				return err
			}
			if plan.Existing != nil {
				a.printf("Opening Claude Code in %s (%s)\n", plan.Existing.Path, plan.Existing.Branch)
				return a.Launch(plan.Existing.Path, "claude", []string{plan.Prompt})
			}
			for _, d := range plan.Dirs[1:] {
				a.printf("Also start it in %s:\n  cd %q && claude %q\n", d, d, plan.Prompt)
			}
			a.printf("Opening Claude Code in %s\n", plan.Dirs[0])
			return a.Launch(plan.Dirs[0], "claude", []string{plan.Prompt})
		},
	}
	cmd.Flags().StringArrayVar(&repoFlags, "repo", nil, "repository to start in (owner/name or name); repeat for several")
	cmd.Flags().BoolVar(&clone, "clone", false, "clone missing repositories without asking")
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

func (a *App) planStart(cmd *cobra.Command, args, repoFlags []string, clone bool) (startPlan, error) {
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
		if len(existing) > 0 {
			w, err := a.pickWork(existing)
			if err != nil {
				return startPlan{}, err
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
