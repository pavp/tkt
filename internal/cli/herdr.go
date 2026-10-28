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
	FocusPane(ctx context.Context, paneID string) error
	ReportWorkspaceTokens(ctx context.Context, workspaceID, source string, tokens map[string]*string, ttl time.Duration) error
	ReportPaneTokens(ctx context.Context, paneID, source string, tokens map[string]*string) error
	Agents(ctx context.Context) ([]herdr.Agent, error)
	Panes(ctx context.Context) ([]herdr.Pane, error)
	StartAgent(ctx context.Context, paneID, kind, name string, args []string) error
	WaitAgent(ctx context.Context, target, status string, timeout time.Duration) error
	SplitPane(ctx context.Context, paneID, cwd string) (herdr.Pane, error)
	Prompt(ctx context.Context, target, text string) error
	OpenPluginPane(ctx context.Context, p herdr.PaneOpen) error
	Watch(ctx context.Context, subs []herdr.Subscription, on func(herdr.Event)) error
	FocusPluginPane(ctx context.Context, paneID string) error
	ClosePluginPane(ctx context.Context, paneID string) error
	ClosePane(ctx context.Context, paneID string) error
	CloseWorkspace(ctx context.Context, id string) error
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
	var worktree, agent string
	start := &cobra.Command{
		Use:   "start KEY",
		Short: "Open an agent on a ticket in Herdr workspaces",
		Long: "Open an agent on a ticket in Herdr workspaces. A ticket with a worktree opens its agent there. Otherwise,\n" +
			"per repository, a setup agent creates the worktree with `herdr worktree create`; tkt then starts an agent\n" +
			"in the worktree's workspace and closes the setup agent, so the main checkout stays free.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return a.herdrStart(cmd, args, startOpts{repos: repoFlags, clone: clone, worktree: worktree, agent: agent})
		},
	}
	start.Flags().StringArrayVar(&repoFlags, "repo", nil, "repository to start in; repeat for several")
	start.Flags().BoolVar(&clone, "clone", false, "clone missing repositories without asking")
	start.Flags().StringVar(&worktree, "worktree", "", "open the ticket's worktree at this path instead of asking which one")
	start.Flags().StringVar(&agent, "agent", "", "agent to open, such as claude or opencode, instead of asking")
	start.MarkFlagsMutuallyExclusive("worktree", "repo")

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
		&cobra.Command{Use: "open-board-pane", Short: "Open the board in a split of the current workspace (setup when tkt is not configured)", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
			entry := "board-split"
			if _, err := os.Stat(a.ConfigPath()); err != nil {
				entry = "setup"
			}
			return a.NewHerdr().OpenPluginPane(cmd.Context(), herdr.PaneOpen{PluginID: a.pluginID(), Entrypoint: entry, Focus: true})
		}},
		&cobra.Command{Use: "open-board", Short: "Open the board popup (setup when tkt is not configured)", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
			if _, err := os.Stat(a.ConfigPath()); err != nil {
				return a.NewHerdr().OpenPluginPane(cmd.Context(), herdr.PaneOpen{PluginID: a.pluginID(), Entrypoint: "setup"})
			}
			return a.herdrOpenBoard(cmd.Context())
		}},
		&cobra.Command{Use: "setup", Short: "Open `tkt init` in a Herdr tab", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
			return a.NewHerdr().OpenPluginPane(cmd.Context(), herdr.PaneOpen{PluginID: a.pluginID(), Entrypoint: "setup"})
		}},
		&cobra.Command{Use: "open-ticket", Short: "Open the live ticket pane for the current workspace", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error { return a.herdrOpenTicket(cmd) }},
		&cobra.Command{Use: "open [URL]", Short: "Open the ticket behind a clicked Jira or pull request link", Args: cobra.MaximumNArgs(1), RunE: func(cmd *cobra.Command, args []string) error { return a.herdrOpen(cmd, args) }},
		&cobra.Command{Use: "ticket-popup", Short: "Show the ticket in $TKT_KEY and offer to start it", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error { return a.herdrTicketPopup(cmd) }},
		&cobra.Command{Use: "refresh", Short: "Fetch fresh data and update the Herdr sidebar", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error { return a.herdrSidebar(cmd, false) }},
		&cobra.Command{Use: "restore", Short: "Update the Herdr sidebar from the cache (startup hook)", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
			a.syncHerdrPlugin(cmd.Context())
			return a.herdrSidebar(cmd, true)
		}},
		&cobra.Command{Use: "on-worktree", Short: "Link a worktree Herdr just created and hand its ticket to an agent there (event hook)", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error { return a.herdrOnWorktree(cmd) }},
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
	next := herdrplugin.AddSidebarBlock(string(current))
	done := "✓ Added tkt's ticket and agent lines to Herdr's sidebar"
	switch herdrplugin.PlanSidebar(string(current)) {
	case herdrplugin.SidebarPresent:
		a.printf("✓ tkt's sidebar lines are already in %s\n", path)
		return nil
	case herdrplugin.SidebarConflict:
		a.printf("! you configure [ui.sidebar.spaces] yourself; add tkt's rows to show each ticket and its agent:\n%s", herdrplugin.SidebarRows)
		return nil
	case herdrplugin.SidebarOutdated:
		next, done, yes = herdrplugin.UpdateSidebarBlock(string(current)), "✓ Updated tkt's sidebar lines", true
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
	if err := os.WriteFile(path, []byte(next), 0o644); err != nil {
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
	a.printf("%s (%s)\n", done, path)
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
	// A shortcut tkt has moved leaves its own block, so the new key takes its place.
	kept, dropped := herdrplugin.DropRetired(string(current))
	plan, err := herdrplugin.PlanKeys(kept, string(defaults))
	if err != nil {
		return err
	}
	if len(plan.Present) > 0 {
		a.printf("✓ Already bound: %s\n", herdrplugin.Describe(plan.Present))
	}
	for key, what := range plan.Conflict {
		a.printf("! %s is taken by %q; bind tkt there yourself if you want another key\n", key, what)
	}
	if len(plan.Add) == 0 && len(dropped) == 0 {
		return nil
	}
	if !yes && len(plan.Add) > 0 {
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
	next := kept
	if len(plan.Add) > 0 {
		next = herdrplugin.AddBlock(kept, plan.Add)
	}
	if err := os.WriteFile(path, []byte(next), 0o644); err != nil {
		return err
	}
	for _, d := range dropped {
		a.printf("✓ Moved %s off %s\n", d.Description, d.Key)
	}
	if err := a.NewHerdr().ReloadConfig(cmd.Context()); err != nil {
		a.printf("! keybindings saved; reload Herdr's config to use them (%v)\n", err)
	}
	if len(plan.Add) > 0 {
		a.printf("✓ Added %s to %s\n", herdrplugin.Describe(plan.Add), path)
	}
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

// herdrStart opens one Herdr workspace with an agent per repository, or focuses the ticket's worktree.
func (a *App) herdrStart(cmd *cobra.Command, args []string, opts startOpts) error {
	h := a.NewHerdr()
	opts.prefer = func(ws []workspace.Work) (workspace.Work, bool) { return workWithAgent(cmd.Context(), h, ws) }
	plan, err := a.planStart(cmd, args, opts)
	if err != nil {
		return err
	}
	ctx := cmd.Context()
	if plan.Existing != nil {
		ws, wsOK := workspaceAt(ctx, h, plan.Existing.Path)
		ag, agOK := agentAt(ctx, h, plan.Existing.Path)
		if wsOK || agOK {
			if wsOK {
				a.printf("Focusing %s\n", ws.Label)
			} else {
				ws.ID = ag.WorkspaceID
				a.printf("Focusing the agent of %s\n", plan.Key)
			}
			if err := h.FocusWorkspace(ctx, ws.ID); err != nil {
				return err
			}
			// The workspace alone may show another tab; the agent's pane brings its own tab forward.
			if agOK && ag.WorkspaceID == ws.ID {
				_ = h.FocusPane(ctx, ag.PaneID)
			}
			return nil
		}
		kind, err := a.pickAgent(ctx, opts.agent)
		if err != nil {
			return err
		}
		pane, err := a.startInWorkspace(ctx, h, kind, plan.Existing.Path, string(plan.Key), plan.Prompt, true)
		markAgent(ctx, h, pane, plan.Key)
		return err
	}
	kind, err := a.pickAgent(ctx, opts.agent)
	if err != nil {
		return err
	}
	for i, dir := range plan.Dirs {
		label := string(plan.Key) + " · " + filepath.Base(dir)
		if err := a.startSetupAgent(ctx, h, plan, kind, dir, plan.Repos[i], label, i == 0); err != nil {
			return err
		}
	}
	return nil
}

// startSetupAgent opens the agent that creates the ticket's worktree in dir, in a pane tkt opens for it so the
// handoff can close it without closing anything of the user's: a split beside a pane open at dir, else a workspace.
// The handoff is saved before the agent gets its prompt, as the worktree event may follow right after.
func (a *App) startSetupAgent(ctx context.Context, h HerdrAPI, plan startPlan, kind, dir, repo, label string, focus bool) error {
	ho := handoff{Key: plan.Key, Summary: plan.Summary, Repo: repo, Clone: dir, Kind: kind}
	var where string
	if p, ok := paneAt(ctx, h, dir); ok {
		if split, err := h.SplitPane(ctx, p.ID, dir); err == nil {
			ho.Pane, where = split.ID, "a pane beside workspace "+p.WorkspaceID
			if focus {
				_ = h.FocusWorkspace(ctx, p.WorkspaceID)
				_ = h.FocusPane(ctx, split.ID)
			}
		}
	}
	if ho.Pane == "" {
		ws, pane, err := h.CreateWorkspace(ctx, dir, label, focus)
		if err != nil {
			return err
		}
		ho.Pane, ho.Workspace, where = pane.ID, ws.ID, fmt.Sprintf("workspace %s (%s)", label, ws.ID)
	}
	name, err := startAgent(ctx, h, ho.Pane, kind)
	if err != nil && !notReady(err) {
		return err
	}
	markAgent(ctx, h, ho.Pane, plan.Key)
	ho.Agent, ho.At = name, a.Now()
	st := a.loadState()
	st.Handoffs = append(slices.DeleteFunc(st.Handoffs, func(x handoff) bool {
		return x.Key == ho.Key && strings.EqualFold(x.Repo, ho.Repo)
	}), ho)
	a.saveState(st)
	return a.promptStarted(ctx, h, name, where, workspace.SetupPrompt(plan.Key, plan.Summary), err)
}

// startInWorkspace names the agent after its kind ("claude"), as Herdr shows agents it detects itself; the workspace
// above it already names the ticket. Herdr wants agent names unique, so a taken name gets a number.
// The prompt goes through agent.prompt, not as an argument: Herdr refuses multi-line arguments for the shell.
// An open workspace at dir with no agent is reused; when its pane is busy (an editor, a server), the agent gets a
// split next to it, and only when that fails a new workspace opens.
// It returns the pane the agent started in, empty when none did.
func (a *App) startInWorkspace(ctx context.Context, h HerdrAPI, kind, dir, label, prompt string, focus bool) (string, error) {
	if p, ok := idlePaneAt(ctx, h, dir); ok {
		used := p.ID
		name, err := startAgent(ctx, h, used, kind)
		if err != nil && !notReady(err) {
			if split, serr := h.SplitPane(ctx, p.ID, dir); serr == nil {
				used = split.ID
				name, err = startAgent(ctx, h, used, kind)
			}
		}
		if err == nil || notReady(err) {
			if focus {
				_ = h.FocusWorkspace(ctx, p.WorkspaceID)
			}
			return used, a.promptStarted(ctx, h, name, "open workspace "+p.WorkspaceID, prompt, err)
		}
	}
	ws, pane, err := h.CreateWorkspace(ctx, dir, label, focus)
	if err != nil {
		return "", err
	}
	name, err := startAgent(ctx, h, pane.ID, kind)
	return pane.ID, a.promptStarted(ctx, h, name, fmt.Sprintf("workspace %s (%s)", label, ws.ID), prompt, err)
}

// ticketToken is the pane value naming the ticket an agent works on, which tkt reports as it hands the agent a
// ticket, so the agent belongs to it before its branch names the ticket.
const ticketToken = "tkt_key"

// markAgent records on an agent's pane the ticket it was given.
func markAgent(ctx context.Context, h HerdrAPI, pane string, key ticket.Key) {
	if pane == "" || key == "" {
		return
	}
	k := string(key)
	_ = h.ReportPaneTokens(ctx, pane, sidebarSource, map[string]*string{ticketToken: &k})
}

// startAgent returns the agent's name, which later calls target: Herdr may not resolve the pane id to it yet.
func startAgent(ctx context.Context, h HerdrAPI, paneID, kind string) (string, error) {
	name := kind
	for n := 2; ; n++ {
		err := h.StartAgent(ctx, paneID, kind, name, []string{})
		if err == nil || n > 9 || !nameTaken(err) {
			return name, err
		}
		name = fmt.Sprintf("%s-%d", kind, n)
	}
}

// promptStarted sends the first prompt once the agent is ready. An agent showing a question at startup (such as
// Claude Code's folder trust prompt) gets the prompt after the user answers; the wait stays under the socket
// deadline. When the prompt cannot be sent, the user gets it to paste, since the agent is already open.
func (a *App) promptStarted(ctx context.Context, h HerdrAPI, agent, where, prompt string, startErr error) error {
	if notReady(startErr) {
		a.printf("! %s in %s is asking a question; answer it there and the prompt follows…\n", agent, where)
		if err := h.WaitAgent(ctx, agent, "idle", 4*time.Minute); err != nil {
			a.printf("! It did not get ready in time; give it this prompt:\n\n%s\n", prompt)
			return nil
		}
	} else if startErr != nil {
		return startErr
	}
	if err := a.promptWhenNamed(ctx, h, agent, prompt); err != nil {
		a.printf("! %s started in %s but did not get its first prompt (%v); give it this prompt:\n\n%s\n", agent, where, err, prompt)
		return nil
	}
	a.printf("✓ %s started in %s\n", agent, where)
	return nil
}

// promptWhenNamed retries while Herdr has not yet made the started agent's name active, which on WSL lags
// behind agent.start returning.
func (a *App) promptWhenNamed(ctx context.Context, h HerdrAPI, agent, prompt string) error {
	var err error
	for range 20 {
		if err = h.Prompt(ctx, agent, prompt); err == nil || !strings.Contains(err.Error(), "not an active named agent") {
			return err
		}
		if serr := a.Sleep(ctx, 500*time.Millisecond); serr != nil {
			return err
		}
	}
	return err
}

// checkoutFree reports whether dir has no local changes and sits on the branch origin/HEAD points to.
func (a *App) checkoutFree(ctx context.Context, dir string) bool {
	git := func(args ...string) (string, bool) {
		out, err := a.Run.Run(ctx, dir, "git", args, nil)
		return strings.TrimSpace(string(out)), err == nil
	}
	status, ok := git("status", "--porcelain")
	if !ok || status != "" {
		return false
	}
	head, ok := git("rev-parse", "--abbrev-ref", "HEAD")
	if !ok {
		return false
	}
	def, ok := git("symbolic-ref", "--short", "refs/remotes/origin/HEAD")
	return ok && def == "origin/"+head
}

// idlePaneAt finds a pane at dir in a workspace where no agent runs.
func idlePaneAt(ctx context.Context, h HerdrAPI, dir string) (herdr.Pane, bool) {
	panes, err := h.Panes(ctx)
	if err != nil {
		return herdr.Pane{}, false
	}
	agents, err := h.Agents(ctx)
	if err != nil {
		return herdr.Pane{}, false
	}
	busy := map[string]bool{}
	for _, ag := range agents {
		busy[ag.WorkspaceID] = true
	}
	for _, p := range panes {
		if !busy[p.WorkspaceID] && p.Cwd != "" && filepath.Clean(p.Cwd) == filepath.Clean(dir) {
			return p, true
		}
	}
	return herdr.Pane{}, false
}

// paneAt is a pane open at dir, whether or not an agent runs there.
func paneAt(ctx context.Context, h HerdrAPI, dir string) (herdr.Pane, bool) {
	panes, err := h.Panes(ctx)
	if err != nil {
		return herdr.Pane{}, false
	}
	for _, p := range panes {
		if p.Cwd != "" && filepath.Clean(p.Cwd) == filepath.Clean(dir) {
			return p, true
		}
	}
	return herdr.Pane{}, false
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

// workWithAgent picks the one worktree an agent works in; with none or several there is nothing to tell.
func workWithAgent(ctx context.Context, h HerdrAPI, ws []workspace.Work) (workspace.Work, bool) {
	var found []workspace.Work
	for _, w := range ws {
		if _, ok := agentAt(ctx, h, w.Path); ok {
			found = append(found, w)
		}
	}
	if len(found) != 1 {
		return workspace.Work{}, false
	}
	return found[0], true
}

// agentAt finds an agent working inside path, which Herdr reports as a worktree only when it created that
// worktree itself; an agent without a folder is placed by its pane's.
func agentAt(ctx context.Context, h HerdrAPI, path string) (herdr.Agent, bool) {
	agents, err := h.Agents(ctx)
	if err != nil {
		return herdr.Agent{}, false
	}
	var cwd map[string]string
	for _, ag := range agents {
		dir := ag.Cwd
		if dir == "" {
			if cwd == nil {
				cwd = map[string]string{}
				if panes, err := h.Panes(ctx); err == nil {
					for _, p := range panes {
						cwd[p.ID] = p.Cwd
					}
				}
			}
			dir = cwd[ag.PaneID]
		}
		if dir != "" && within(dir, path) {
			return ag, true
		}
	}
	return herdr.Agent{}, false
}

func within(dir, root string) bool {
	dir, root = filepath.Clean(dir), filepath.Clean(root)
	return dir == root || strings.HasPrefix(dir, root+string(filepath.Separator))
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
	dirs, err := workspaceDirs(ctx, h, id)
	if err != nil {
		return err
	}
	for _, dir := range dirs {
		if key, ok := a.keyAt(ctx, c, dir); ok {
			return h.OpenPluginPane(ctx, herdr.PaneOpen{
				PluginID: a.pluginID(), Entrypoint: "ticket", Cwd: dir,
				Env: map[string]string{"TKT_KEY": string(key)}, WorkspaceID: id, TargetPaneID: targetPane(ctx, h, id),
			})
		}
	}
	return h.Notify(ctx, "tkt", "This workspace's branch carries no ticket key")
}

// targetPane picks the pane a split opens beside in a workspace: its focused pane, else its first.
func targetPane(ctx context.Context, h HerdrAPI, workspaceID string) string {
	panes, err := h.Panes(ctx)
	if err != nil {
		return ""
	}
	var first string
	for _, p := range panes {
		if p.WorkspaceID != workspaceID {
			continue
		}
		if p.Focused {
			return p.ID
		}
		if first == "" {
			first = p.ID
		}
	}
	return first
}

// workspaceDirs lists where a workspace works: its worktree, else its panes' folders with the focused one first,
// since Herdr knows the worktree only of workspaces it created on one.
func workspaceDirs(ctx context.Context, h HerdrAPI, id string) ([]string, error) {
	all, err := h.Workspaces(ctx)
	if err != nil {
		return nil, err
	}
	for _, ws := range all {
		if ws.ID == id && ws.Worktree != nil {
			return []string{ws.Worktree.CheckoutPath}, nil
		}
	}
	panes, err := h.Panes(ctx)
	if err != nil {
		return nil, err
	}
	var dirs []string
	for _, p := range panes {
		switch {
		case p.WorkspaceID != id || p.Cwd == "":
		case p.Focused:
			dirs = append([]string{p.Cwd}, dirs...)
		default:
			dirs = append(dirs, p.Cwd)
		}
	}
	return dirs, nil
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
	d, err := a.ticketDetail(cmd.Context(), c, ticket.Key(key))
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
	return a.herdrStart(cmd, []string{key}, startOpts{})
}

// herdrSidebar labels every workspace whose checkout carries a ticket key. cachedOnly is for the startup
// hook, which must not wait on the network.
func (a *App) herdrSidebar(cmd *cobra.Command, cachedOnly bool) error {
	c, err := a.LoadConfig()
	if err != nil {
		return err
	}
	// Herdr's startup hook runs once and exits, so it starts the daemon that keeps the sidebar fresh.
	if cachedOnly && a.StartDetached != nil && a.Executable != nil {
		_, _ = a.ensureDaemon(cmd.Context())
	}
	snap, _, err := a.loadSnapshot(cmd.Context(), c, cachedOnly)
	if err != nil {
		if cachedOnly {
			return nil
		}
		return err
	}
	n, _, err := a.updateSidebar(cmd.Context(), c, snap, true)
	if err != nil {
		return err
	}
	a.printf("✓ Updated %d Herdr workspace(s)\n", n)
	return nil
}

// updateSidebar returns the agent status shown for every workspace that holds a ticket and, when report
// is set, labels that workspace in the sidebar.
func (a *App) updateSidebar(ctx context.Context, c *config.Config, snap board.Snapshot, report bool) (int, map[string]string, error) {
	h := a.NewHerdr()
	all, err := h.Workspaces(ctx)
	if err != nil {
		return 0, nil, err
	}
	statuses := map[string][]string{}
	marked := map[string]ticket.Key{}
	if agents, err := h.Agents(ctx); err == nil {
		for _, ag := range agents {
			if ag.Agent != "" {
				statuses[ag.WorkspaceID] = append(statuses[ag.WorkspaceID], ag.Status)
			}
			if key, ok := ticket.KeyInBranch(ag.Tokens[ticketToken], c.SpaceKeys()); ok {
				marked[ag.WorkspaceID] = key
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
		// An agent given a ticket before its branch names it still belongs to it.
		if !ok {
			key, ok = marked[ws.ID]
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
		if !report {
			continue
		}
		if err := h.ReportWorkspaceTokens(ctx, ws.ID, sidebarSource, sidebarTokens(t), sidebarTTL); err != nil {
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
	if ho, ok := a.takeHandoff(key, co.repo); ok {
		a.handOff(cmd.Context(), ho, eventWorkspace(a.Env.Getenv("HERDR_PLUGIN_EVENT_JSON")))
	}
	if snap, ok, _ := a.cache().Load(); ok {
		_, _, _ = a.updateSidebar(cmd.Context(), c, snap, true)
	}
	return nil
}

// refreshSidebarQuietly updates Herdr labels after a fresh fetch when tkt runs inside Herdr; any failure
// is ignored because the sidebar is a convenience, never a reason for a command to fail.
func (a *App) refreshSidebarQuietly(ctx context.Context, c *config.Config, snap board.Snapshot) map[string]string {
	return a.readSidebarQuietly(ctx, c, snap, true)
}

// agentStatesQuietly reads the agent states without touching the sidebar, so a cached board never
// overwrites lines a fresh fetch may already have written.
func (a *App) agentStatesQuietly(ctx context.Context, c *config.Config, snap board.Snapshot) map[string]string {
	return a.readSidebarQuietly(ctx, c, snap, false)
}

func (a *App) readSidebarQuietly(ctx context.Context, c *config.Config, snap board.Snapshot, report bool) map[string]string {
	if a.Env.Getenv("HERDR_ENV") != "1" || a.NewHerdr == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	_, agents, _ := a.updateSidebar(ctx, c, snap, report)
	return agents
}

// Herdr colors a token, not a value, so the ticket line is reported under the token whose color fits it
// and the other is cleared; the sidebar row tkt herdr install adds shows whichever is set.
const (
	tokenTicket      = "tkt"
	tokenTicketAlert = "tkt_alert"
)

func sidebarTokens(t ticket.Ticket) map[string]*string {
	tokens := map[string]*string{tokenTicket: nil, tokenTicketAlert: nil}
	line, alert := board.TicketLine(t)
	if alert {
		tokens[tokenTicketAlert] = &line
	} else {
		tokens[tokenTicket] = &line
	}
	return tokens
}

func nameTaken(err error) bool {
	msg := err.Error()
	return strings.Contains(msg, "agent_name_taken") || strings.Contains(msg, "is already used")
}

// notReady means Herdr started the agent but it is showing a question, such as Claude Code's folder trust prompt.
func notReady(err error) bool {
	return err != nil && strings.Contains(err.Error(), "agent_not_ready")
}
