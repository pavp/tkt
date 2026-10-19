package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/pavp/tkt/internal/board"
	"github.com/pavp/tkt/internal/config"
	"github.com/pavp/tkt/internal/herdr"
	"github.com/pavp/tkt/internal/herdrplugin"
	"github.com/pavp/tkt/internal/paths"
	"github.com/pavp/tkt/internal/ticket"
	"github.com/pavp/tkt/internal/workspace"
)

// HerdrAPI is the part of Herdr's socket API tkt uses.
type HerdrAPI interface {
	Workspaces(ctx context.Context) ([]herdr.Workspace, error)
	CreateWorkspace(ctx context.Context, cwd, label string, focus bool) (herdr.Workspace, herdr.Pane, error)
	FocusWorkspace(ctx context.Context, id string) error
	ReportWorkspaceTokens(ctx context.Context, workspaceID, source string, tokens map[string]*string, ttl time.Duration) error
	Agents(ctx context.Context) ([]herdr.Agent, error)
	Panes(ctx context.Context) ([]herdr.Pane, error)
	StartAgent(ctx context.Context, paneID, kind, name string, args []string) error
	Prompt(ctx context.Context, target, text string) error
	OpenPluginPane(ctx context.Context, p herdr.PaneOpen) error
	Notify(ctx context.Context, title, body string) error
	LinkPlugin(ctx context.Context, path string) error
	UnlinkPlugin(ctx context.Context, id string) error
	ReloadConfig(ctx context.Context) error
}

const (
	sidebarSource = "plugin:tkt"
	// sidebarTTL lets labels fade instead of lying when nothing refreshes them, since plugins cannot run daemons.
	sidebarTTL = time.Hour
)

func (a *App) pluginID() string {
	if id := a.Env.Getenv("HERDR_PLUGIN_ID"); id != "" {
		return id
	}
	return herdrplugin.ID
}

func newHerdrCmd(a *App) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "herdr",
		Short: "Herdr integration: install the plugin, and the commands it runs",
		Long: "Herdr integration. `tkt herdr install` links the tkt plugin into a running Herdr; the other\n" +
			"subcommands are what the plugin's actions, panes and hooks run.",
	}
	var repoFlags []string
	var clone bool
	start := &cobra.Command{
		Use:   "start KEY",
		Short: "Open Claude Code on a ticket in Herdr workspaces",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return a.herdrStart(cmd, args, repoFlags, clone)
		},
	}
	start.Flags().StringArrayVar(&repoFlags, "repo", nil, "repository to start in; repeat for several")
	start.Flags().BoolVar(&clone, "clone", false, "clone missing repositories without asking")

	var keysFlag, noKeys, sidebarFlag, noSidebar bool
	installCmd := &cobra.Command{
		Use:   "install",
		Short: "Link the tkt plugin into the running Herdr and offer keybindings",
		Long: "Link the tkt plugin into the running Herdr. Herdr plugins cannot bring their own keys, so install\n" +
			"offers to add prefix+t (board) and prefix+i (ticket pane) to your Herdr config when those keys are free,\n" +
			"backing the config up first. --keys adds them without asking; --no-keys skips them.\n" +
			"It also offers sidebar rows that show each workspace's ticket and agent (Herdr only shows tkt's lines\n" +
			"when its config lists them); --sidebar adds them without asking, --no-sidebar skips them.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return a.herdrInstall(cmd, keysFlag, noKeys, sidebarFlag, noSidebar)
		},
	}
	installCmd.Flags().BoolVar(&keysFlag, "keys", false, "add the keybindings without asking")
	installCmd.Flags().BoolVar(&noKeys, "no-keys", false, "do not add keybindings")
	installCmd.Flags().BoolVar(&sidebarFlag, "sidebar", false, "add the sidebar rows without asking")
	installCmd.Flags().BoolVar(&noSidebar, "no-sidebar", false, "do not add sidebar rows")

	cmd.AddCommand(
		start,
		installCmd,
		&cobra.Command{Use: "uninstall", Short: "Unlink the tkt plugin from Herdr and remove its keybindings", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
			if err := a.NewHerdr().UnlinkPlugin(cmd.Context(), a.pluginID()); err != nil {
				return err
			}
			a.printf("✓ Unlinked the tkt plugin from Herdr\n")
			if err := a.removeHerdrKeys(cmd); err != nil {
				return err
			}
			return a.removeHerdrSidebar(cmd)
		}},
		&cobra.Command{Use: "open-board", Short: "Open the board popup (setup when tkt is not configured)", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
			entry := "board"
			if _, err := os.Stat(a.ConfigPath()); err != nil {
				entry = "setup"
			}
			return a.NewHerdr().OpenPluginPane(cmd.Context(), herdr.PaneOpen{PluginID: a.pluginID(), Entrypoint: entry})
		}},
		&cobra.Command{Use: "setup", Short: "Open `tkt init` in a Herdr tab", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
			return a.NewHerdr().OpenPluginPane(cmd.Context(), herdr.PaneOpen{PluginID: a.pluginID(), Entrypoint: "setup"})
		}},
		&cobra.Command{Use: "open-ticket", Short: "Open the live ticket pane for the current workspace", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error { return a.herdrOpenTicket(cmd) }},
		&cobra.Command{Use: "open [URL]", Short: "Open the ticket behind a clicked Jira or pull request link", Args: cobra.MaximumNArgs(1), RunE: func(cmd *cobra.Command, args []string) error { return a.herdrOpen(cmd, args) }},
		&cobra.Command{Use: "ticket-popup", Short: "Show the ticket in $TKT_KEY and offer to start it", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error { return a.herdrTicketPopup(cmd) }},
		&cobra.Command{Use: "refresh", Short: "Fetch fresh data and update the Herdr sidebar", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error { return a.herdrSidebar(cmd, false) }},
		&cobra.Command{Use: "restore", Short: "Update the Herdr sidebar from the cache (startup hook)", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error { return a.herdrSidebar(cmd, true) }},
		&cobra.Command{Use: "on-worktree", Short: "Link a worktree Herdr just created (event hook)", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error { return a.herdrOnWorktree(cmd) }},
	)
	return cmd
}

func (a *App) herdrInstall(cmd *cobra.Command, keys, noKeys, sidebar, noSidebar bool) error {
	exe, err := a.Executable()
	if err != nil {
		return fmt.Errorf("locate the tkt binary: %w", err)
	}
	dir := filepath.Join(a.Env.DataDir(), "herdr-plugin")
	if err := paths.WritePrivateFile(filepath.Join(dir, herdrplugin.FileName), []byte(herdrplugin.Render(exe, Version))); err != nil {
		return err
	}
	if err := a.NewHerdr().LinkPlugin(cmd.Context(), dir); err != nil && !strings.Contains(strings.ToLower(err.Error()), "already") {
		return err
	}
	a.printf("✓ Linked the tkt plugin into Herdr (%s)\n", dir)
	if !noKeys {
		if err := a.addHerdrKeys(cmd, keys); err != nil {
			return err
		}
	}
	if noSidebar {
		return nil
	}
	return a.addHerdrSidebar(cmd, sidebar)
}

// addHerdrSidebar adds the rows that show tkt's ticket and agent lines, never touching space rows the
// user configures, and undoes the change when Herdr rejects it so its next start still works.
func (a *App) addHerdrSidebar(cmd *cobra.Command, yes bool) error {
	path := a.herdrConfigPath()
	current, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	switch herdrplugin.PlanSidebar(string(current)) {
	case herdrplugin.SidebarPresent:
		a.printf("✓ tkt's sidebar lines are already in %s\n", path)
		return nil
	case herdrplugin.SidebarConflict:
		a.printf("! you configure [ui.sidebar.spaces] yourself; add tkt's rows to show each ticket and its agent:\n%s", herdrplugin.SidebarRows)
		return nil
	}
	if !yes {
		ans, err := a.readLine("Show each ticket and its agent in Herdr's sidebar? [Y/n] ")
		if err != nil || strings.HasPrefix(strings.ToLower(ans), "n") {
			a.printf("Skipped sidebar lines; `tkt herdr install --sidebar` adds them later\n")
			return nil
		}
	}
	if len(current) > 0 {
		if err := os.WriteFile(path+".bak-tkt", current, 0o644); err != nil {
			return fmt.Errorf("back up %s: %w", path, err)
		}
	}
	h := a.NewHerdr()
	// Warnings the config already causes are not tkt's doing; only new ones mean Herdr rejected the rows.
	known := map[string]bool{}
	var before *herdr.ReloadRejected
	if errors.As(h.ReloadConfig(cmd.Context()), &before) {
		for _, d := range before.Diagnostics {
			known[d] = true
		}
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(path, []byte(herdrplugin.AddSidebarBlock(string(current))), 0o644); err != nil {
		return err
	}
	err = h.ReloadConfig(cmd.Context())
	var rejected *herdr.ReloadRejected
	if errors.As(err, &rejected) {
		var added []string
		for _, d := range rejected.Diagnostics {
			if !known[d] {
				added = append(added, d)
			}
		}
		if rejected.Status == "failed" || len(added) > 0 {
			if werr := os.WriteFile(path, current, 0o644); werr != nil {
				return fmt.Errorf("restore %s after Herdr rejected it: %w", path, werr)
			}
			_ = h.ReloadConfig(cmd.Context())
			if len(added) == 0 {
				added = rejected.Diagnostics
			}
			a.printf("! Herdr rejected the sidebar rows (%s); your config is back as it was\n", strings.Join(added, "; "))
			return nil
		}
		err = nil
	}
	switch {
	case err != nil:
		a.printf("! sidebar lines saved; reload Herdr's config to see them (%v)\n", err)
		return nil
	}
	a.printf("✓ Added tkt's ticket and agent lines to Herdr's sidebar (%s)\n", path)
	return nil
}

func (a *App) removeHerdrSidebar(cmd *cobra.Command) error {
	path := a.herdrConfigPath()
	current, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	updated, ok := herdrplugin.RemoveSidebarBlock(string(current))
	if !ok {
		return nil
	}
	if err := os.WriteFile(path, []byte(updated), 0o644); err != nil {
		return err
	}
	_ = a.NewHerdr().ReloadConfig(cmd.Context())
	a.printf("✓ Removed tkt's sidebar lines from %s\n", path)
	return nil
}

func (a *App) herdrConfigPath() string {
	if x := a.Env.Getenv("XDG_CONFIG_HOME"); x != "" {
		return filepath.Join(x, "herdr", "config.toml")
	}
	return filepath.Join(a.Env.Home, ".config", "herdr", "config.toml")
}

func (a *App) herdrBin() string {
	if p := a.Env.Getenv("HERDR_BIN_PATH"); p != "" {
		return p
	}
	return "herdr"
}

// addHerdrKeys offers tkt's shortcuts, never taking a key the user or Herdr already binds.
func (a *App) addHerdrKeys(cmd *cobra.Command, yes bool) error {
	path := a.herdrConfigPath()
	current, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	defaults, err := a.Run.Run(cmd.Context(), "", a.herdrBin(), []string{"--default-config"}, nil)
	if err != nil {
		return fmt.Errorf("read Herdr's default keys: %w", err)
	}
	plan, err := herdrplugin.PlanKeys(string(current), string(defaults))
	if err != nil {
		return err
	}
	if len(plan.Present) > 0 {
		a.printf("✓ Already bound: %s\n", herdrplugin.Describe(plan.Present))
	}
	for key, what := range plan.Conflict {
		a.printf("! %s is taken by %q; bind tkt there yourself if you want another key\n", key, what)
	}
	if len(plan.Add) == 0 {
		return nil
	}
	if !yes {
		ans, err := a.readLine(fmt.Sprintf("Add Herdr keybindings %s? [Y/n] ", herdrplugin.Describe(plan.Add)))
		if err != nil || strings.HasPrefix(strings.ToLower(ans), "n") {
			a.printf("Skipped keybindings; `tkt herdr install --keys` adds them later\n")
			return nil
		}
	}
	if len(current) > 0 {
		if err := os.WriteFile(path+".bak-tkt", current, 0o644); err != nil {
			return fmt.Errorf("back up %s: %w", path, err)
		}
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(path, []byte(herdrplugin.AddBlock(string(current), plan.Add)), 0o644); err != nil {
		return err
	}
	if err := a.NewHerdr().ReloadConfig(cmd.Context()); err != nil {
		a.printf("! keybindings saved; reload Herdr's config to use them (%v)\n", err)
	}
	a.printf("✓ Added %s to %s\n", herdrplugin.Describe(plan.Add), path)
	return nil
}

func (a *App) removeHerdrKeys(cmd *cobra.Command) error {
	path := a.herdrConfigPath()
	current, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	updated, ok := herdrplugin.RemoveBlock(string(current))
	if !ok {
		return nil
	}
	if err := os.WriteFile(path, []byte(updated), 0o644); err != nil {
		return err
	}
	_ = a.NewHerdr().ReloadConfig(cmd.Context())
	a.printf("✓ Removed tkt's keybindings from %s\n", path)
	return nil
}

// herdrStart opens one Herdr workspace with Claude Code per repository, or focuses the ticket's worktree.
func (a *App) herdrStart(cmd *cobra.Command, args, repoFlags []string, clone bool) error {
	plan, err := a.planStart(cmd, args, repoFlags, clone)
	if err != nil {
		return err
	}
	h := a.NewHerdr()
	ctx := cmd.Context()
	if plan.Existing != nil {
		if ws, ok := workspaceAt(ctx, h, plan.Existing.Path); ok {
			a.printf("Focusing %s\n", ws.Label)
			return h.FocusWorkspace(ctx, ws.ID)
		}
		return a.startInWorkspace(ctx, h, plan.Existing.Path, string(plan.Key), plan.Prompt, true)
	}
	for i, dir := range plan.Dirs {
		label := string(plan.Key) + " · " + filepath.Base(dir)
		if err := a.startInWorkspace(ctx, h, dir, label, plan.Prompt, i == 0); err != nil {
			return err
		}
	}
	return nil
}

func (a *App) startInWorkspace(ctx context.Context, h HerdrAPI, dir, label, prompt string, focus bool) error {
	ws, pane, err := h.CreateWorkspace(ctx, dir, label, focus)
	if err != nil {
		return err
	}
	if err := h.StartAgent(ctx, pane.ID, "claude", label, []string{prompt}); err != nil {
		return err
	}
	a.printf("✓ Claude Code started in workspace %s (%s)\n", label, ws.ID)
	return nil
}

func workspaceAt(ctx context.Context, h HerdrAPI, path string) (herdr.Workspace, bool) {
	all, err := h.Workspaces(ctx)
	if err != nil {
		return herdr.Workspace{}, false
	}
	for _, ws := range all {
		if ws.Worktree != nil && filepath.Clean(ws.Worktree.CheckoutPath) == filepath.Clean(path) {
			return ws, true
		}
	}
	return herdr.Workspace{}, false
}

// keyAt returns the ticket key of the branch checked out in dir.
func (a *App) keyAt(ctx context.Context, c *config.Config, dir string) (ticket.Key, bool) {
	out, err := a.Run.Run(ctx, dir, "git", []string{"rev-parse", "--abbrev-ref", "HEAD"}, nil)
	if err != nil {
		return "", false
	}
	return ticket.KeyInBranch(strings.TrimSpace(string(out)), c.SpaceKeys())
}

func (a *App) herdrOpenTicket(cmd *cobra.Command) error {
	c, err := a.LoadConfig()
	if err != nil {
		return err
	}
	ctx := cmd.Context()
	h := a.NewHerdr()
	id := a.Env.Getenv("HERDR_WORKSPACE_ID")
	all, err := h.Workspaces(ctx)
	if err != nil {
		return err
	}
	for _, ws := range all {
		if ws.ID != id || ws.Worktree == nil {
			continue
		}
		key, ok := a.keyAt(ctx, c, ws.Worktree.CheckoutPath)
		if !ok {
			break
		}
		return h.OpenPluginPane(ctx, herdr.PaneOpen{
			PluginID: a.pluginID(), Entrypoint: "ticket", Cwd: ws.Worktree.CheckoutPath,
			Env: map[string]string{"TKT_KEY": string(key)}, WorkspaceID: ws.ID,
		})
	}
	return h.Notify(ctx, "tkt", "This workspace's branch carries no ticket key")
}

var jiraBrowse = regexp.MustCompile(`/browse/([A-Z][A-Z0-9]+-[0-9]+)`)

// keyFromURL finds the ticket behind a Jira issue URL or a pull request URL known to the cached board.
func keyFromURL(u string, snap board.Snapshot) (ticket.Key, bool) {
	if m := jiraBrowse.FindStringSubmatch(u); m != nil {
		return ticket.Key(m[1]), true
	}
	trimmed := strings.TrimRight(strings.SplitN(u, "#", 2)[0], "/")
	for _, t := range snap.Tickets {
		for _, tk := range append([]ticket.Ticket{t}, t.Subtasks...) {
			for _, pr := range tk.PRs {
				if strings.HasPrefix(trimmed, pr.URL) {
					return tk.Key, true
				}
			}
		}
	}
	for _, pr := range snap.Reviews {
		if strings.HasPrefix(trimmed, pr.URL) {
			if keys := ticket.BranchKeys(pr.Branch); len(keys) > 0 {
				return keys[0], true
			}
		}
	}
	return "", false
}

func (a *App) herdrOpen(cmd *cobra.Command, args []string) error {
	ctx := cmd.Context()
	h := a.NewHerdr()
	u := a.Env.Getenv("HERDR_PLUGIN_CLICKED_URL")
	if len(args) > 0 {
		u = args[0]
	}
	c, err := a.LoadConfig()
	if err != nil {
		return err
	}
	snap, _, _ := a.cache().Load()
	key, ok := keyFromURL(u, snap)
	if !ok {
		return h.Notify(ctx, "tkt", "No ticket found for "+u)
	}
	if all, err := a.allWork(ctx, c); err == nil {
		for _, w := range workspace.For(all, key) {
			if w.Path == "" {
				continue
			}
			if ws, ok := workspaceAt(ctx, h, w.Path); ok {
				return h.FocusWorkspace(ctx, ws.ID)
			}
		}
	}
	return h.OpenPluginPane(ctx, herdr.PaneOpen{PluginID: a.pluginID(), Entrypoint: "ticket-popup", Env: map[string]string{"TKT_KEY": string(key)}})
}

func (a *App) herdrTicketPopup(cmd *cobra.Command) error {
	key := a.Env.Getenv("TKT_KEY")
	if key == "" {
		return errors.New("TKT_KEY is not set")
	}
	c, err := a.LoadConfig()
	if err != nil {
		return err
	}
	d, err := a.loadDetail(cmd.Context(), c, ticket.Key(key))
	if err != nil {
		return err
	}
	a.renderShow(a.Stdout, d)
	ans, err := a.readLine(fmt.Sprintf("\nStart %s now? [y/N] ", key))
	if err != nil && !errors.Is(err, io.EOF) {
		return err
	}
	if !strings.HasPrefix(strings.ToLower(ans), "y") {
		return nil
	}
	return a.herdrStart(cmd, []string{key}, nil, false)
}

// herdrSidebar labels every workspace whose checkout carries a ticket key. cachedOnly is for the startup
// hook, which must not wait on the network.
func (a *App) herdrSidebar(cmd *cobra.Command, cachedOnly bool) error {
	c, err := a.LoadConfig()
	if err != nil {
		return err
	}
	snap, _, err := a.loadSnapshot(cmd.Context(), c, cachedOnly)
	if err != nil {
		if cachedOnly {
			return nil
		}
		return err
	}
	n, _, err := a.updateSidebar(cmd.Context(), c, snap)
	if err != nil {
		return err
	}
	a.printf("✓ Updated %d Herdr workspace(s)\n", n)
	return nil
}

// updateSidebar labels every workspace that holds a ticket and returns the agent status shown for each.
func (a *App) updateSidebar(ctx context.Context, c *config.Config, snap board.Snapshot) (int, map[string]string, error) {
	h := a.NewHerdr()
	all, err := h.Workspaces(ctx)
	if err != nil {
		return 0, nil, err
	}
	statuses := map[string][]string{}
	if agents, err := h.Agents(ctx); err == nil {
		for _, ag := range agents {
			if ag.Agent != "" {
				statuses[ag.WorkspaceID] = append(statuses[ag.WorkspaceID], ag.Status)
			}
		}
	}
	// Herdr reports a worktree only for workspaces it manages as git worktrees; one opened on a plain
	// checkout (as tkt herdr start does) is found through its panes' folders.
	paneDirs := map[string][]string{}
	if panes, err := h.Panes(ctx); err == nil {
		for _, p := range panes {
			if p.Cwd != "" && !slices.Contains(paneDirs[p.WorkspaceID], p.Cwd) {
				paneDirs[p.WorkspaceID] = append(paneDirs[p.WorkspaceID], p.Cwd)
			}
		}
	}
	n := 0
	agents := map[string]string{}
	for _, ws := range all {
		dirs := paneDirs[ws.ID]
		if ws.Worktree != nil {
			dirs = []string{ws.Worktree.CheckoutPath}
		}
		var key ticket.Key
		ok := false
		for _, dir := range dirs {
			if key, ok = a.keyAt(ctx, c, dir); ok {
				break
			}
		}
		if !ok {
			continue
		}
		t, ok := snap.Find(key)
		if !ok {
			t = ticket.Ticket{Key: key, Status: "not in your sprint"}
		}
		agent := board.MostUrgentAgent(statuses[ws.ID])
		if agent != "" {
			agents[string(key)] = board.MostUrgentAgent([]string{agents[string(key)], agent})
		}
		if err := h.ReportWorkspaceTokens(ctx, ws.ID, sidebarSource, sidebarTokens(t, agent), sidebarTTL); err != nil {
			return n, agents, err
		}
		n++
	}
	return n, agents, nil
}

// worktreePath digs the checkout path out of a worktree.created event payload without depending on its
// exact nesting, which Herdr does not document.
func worktreePath(raw string) string {
	var v any
	if json.Unmarshal([]byte(raw), &v) != nil {
		return ""
	}
	var walk func(any, bool) string
	walk = func(x any, inWorktree bool) string {
		m, ok := x.(map[string]any)
		if !ok {
			return ""
		}
		if p, ok := m["checkout_path"].(string); ok && p != "" {
			return p
		}
		if p, ok := m["path"].(string); ok && p != "" && inWorktree {
			return p
		}
		for k, child := range m {
			if p := walk(child, k == "worktree"); p != "" {
				return p
			}
		}
		return ""
	}
	return walk(v, false)
}

func (a *App) herdrOnWorktree(cmd *cobra.Command) error {
	path := worktreePath(a.Env.Getenv("HERDR_PLUGIN_EVENT_JSON"))
	if path == "" {
		fmt.Fprintln(a.Stderr, "tkt: worktree event without a path; nothing to link")
		return nil
	}
	c, err := a.LoadConfig()
	if err != nil {
		return err
	}
	co, err := a.inspectCheckout(cmd.Context(), path)
	if err != nil {
		fmt.Fprintf(a.Stderr, "tkt: %s: %v\n", path, err)
		return nil
	}
	key, ok := ticket.KeyInBranch(co.branch, c.SpaceKeys())
	if !ok || co.repo == "" {
		fmt.Fprintf(a.Stderr, "tkt: branch %q in %s carries no ticket key; not linked\n", co.branch, path)
		return nil
	}
	w := workspace.Work{Key: key, Repo: co.repo, Branch: co.branch, Path: co.top, Source: workspace.SourceHook, LinkedAt: a.Now()}
	if err := a.links().Add(w); err != nil {
		return err
	}
	a.printf("✓ %s ← %s (%s)\n", key, co.branch, co.repo)
	if snap, ok, _ := a.cache().Load(); ok {
		_, _, _ = a.updateSidebar(cmd.Context(), c, snap)
	}
	return nil
}

// refreshSidebarQuietly updates Herdr labels after a fresh fetch when tkt runs inside Herdr; any failure
// is ignored because the sidebar is a convenience, never a reason for a command to fail.
func (a *App) refreshSidebarQuietly(ctx context.Context, c *config.Config, snap board.Snapshot) map[string]string {
	if a.Env.Getenv("HERDR_ENV") != "1" || a.NewHerdr == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	_, agents, _ := a.updateSidebar(ctx, c, snap)
	return agents
}

// Herdr colors a token, not a value, so each line is reported under the token whose color fits it and
// the others are cleared; the sidebar rows tkt herdr install adds show whichever is set.
const (
	tokenTicket      = "tkt"
	tokenTicketAlert = "tkt_alert"
	tokenAgent       = "tkt_agent"
	tokenAgentAlert  = "tkt_agent_alert"
	tokenAgentIdle   = "tkt_agent_idle"
)

func sidebarTokens(t ticket.Ticket, agentStatus string) map[string]*string {
	tokens := map[string]*string{tokenTicket: nil, tokenTicketAlert: nil, tokenAgent: nil, tokenAgentAlert: nil, tokenAgentIdle: nil}
	line, alert := board.TicketLine(t)
	if alert {
		tokens[tokenTicketAlert] = &line
	} else {
		tokens[tokenTicket] = &line
	}
	agent, kind := board.AgentLine(agentStatus)
	switch kind {
	case board.AgentWorking:
		tokens[tokenAgent] = &agent
	case board.AgentNeedsYou:
		tokens[tokenAgentAlert] = &agent
	default:
		tokens[tokenAgentIdle] = &agent
	}
	return tokens
}
