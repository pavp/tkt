package cli

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/pavp/tkt/internal/config"
	"github.com/pavp/tkt/internal/credstore"
	"github.com/pavp/tkt/internal/herdr"
	"github.com/pavp/tkt/internal/ticket"
	"github.com/pavp/tkt/internal/tui"
	"github.com/pavp/tkt/internal/workspace"
)

func newBoardCmd(a *App) *cobra.Command {
	var view string
	cmd := &cobra.Command{
		Use:   "board",
		Short: "Interactive board: columns or list, filters, start, fix CI (the Herdr popup)",
		Long: "Interactive board. It opens instantly from the cached board and refreshes in the background.\n" +
			"Keys: arrows, wasd or hjkl move · enter actions menu · f send CI failures or review comments to the agent ·\n" +
			"i ticket pane · o Jira · p pull request · n needs you · r reviews · v columns/list · g refresh · q close.\n" +
			"When tkt is not configured, it offers to run the setup; without a Jira token, to connect Jira first.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, err := a.LoadConfig()
			if errors.Is(err, config.ErrNotConfigured) {
				if _, err := a.readLine("tkt isn't set up · press Enter to set up (Ctrl-C to close) "); err != nil {
					return nil
				}
				return a.runInit(cmd)
			}
			if err != nil {
				return err
			}
			if ok, err := a.ensureJiraToken(cmd.Context(), c); err != nil || !ok {
				return err
			}
			snap, have, err := a.cache().Load()
			if err != nil {
				return err
			}
			forceColor(a.Env.Getenv)
			model := tui.New(snap, have, &boardActions{a: a, c: c, ctx: cmd.Context()}, view)
			_, err = tea.NewProgram(model, tea.WithAltScreen(), tea.WithMouseCellMotion(), tea.WithInput(a.Stdin), tea.WithOutput(a.Stdout), tea.WithContext(cmd.Context())).Run()
			if errors.Is(err, tea.ErrProgramKilled) {
				return nil
			}
			return err
		},
	}
	cmd.Flags().StringVar(&view, "view", "columns", "initial view: columns or list")
	return cmd
}

// ensureJiraToken offers to connect Jira before the board opens when no token is stored, so the board
// never sits on a cache it cannot refresh. ok is false when the user closed the pane instead.
func (a *App) ensureJiraToken(ctx context.Context, c *config.Config) (bool, error) {
	site, err := config.SiteURL(c.Jira.Site)
	if err != nil {
		return false, err
	}
	if _, _, err := credstore.JiraToken(a.Creds, a.Env.Getenv, site, c.Jira.Email); !errors.Is(err, credstore.ErrNotFound) {
		return true, nil
	}
	if _, err := a.readLine("No Jira token for " + c.Jira.Email + " · press Enter to connect Jira (Ctrl-C to close) "); err != nil {
		return false, nil
	}
	if err := a.connectJira(ctx, site, c.Jira.Email); err != nil {
		return false, err
	}
	return true, nil
}

// boardActions carries out what the board asks for. Its commands run on their own goroutines, so the
// configuration, which the settings view can replace, is read through cfg.
type boardActions struct {
	a   *App
	c   *config.Config
	ctx context.Context
	mu  sync.Mutex
}

func (b *boardActions) cfg() *config.Config {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.c
}

// Config returns the configuration as written on disk, with "~" unexpanded, so saving it keeps the
// user's paths portable; the board itself runs on an expanded copy.
func (b *boardActions) Config() config.Config {
	if c, err := config.Load(b.a.ConfigPath()); err == nil {
		return *c
	}
	return b.cfg().Clone()
}

func (b *boardActions) FindClones(repo string) tea.Cmd {
	return func() tea.Msg {
		paths := b.a.Resolver(b.cfg()).FindAll(b.ctx, repo)
		for i, p := range paths {
			paths[i] = b.a.tildePath(p)
		}
		return tui.ClonesFoundMsg{Repo: repo, Paths: paths}
	}
}

func (b *boardActions) RepoAt(path string) tea.Cmd {
	return func() tea.Msg {
		repo, _ := b.a.Resolver(b.cfg()).RepoAt(b.ctx, b.a.Env.ExpandHome(path))
		return tui.RepoFoundMsg{Path: path, Repo: repo}
	}
}

func (b *boardActions) PickFolder(start string) tea.Cmd {
	return func() tea.Msg {
		p, err := b.a.pickFolder(b.ctx, start)
		return tui.FolderPickedMsg{Path: p, Err: err}
	}
}

func (b *boardActions) SaveConfig(c config.Config) tea.Cmd {
	return func() tea.Msg {
		c = c.Clone()
		if err := config.Save(b.a.ConfigPath(), &c); err != nil {
			return tui.SettingsSavedMsg{Err: err}
		}
		inUse := c.Clone()
		b.a.expandPaths(&inUse)
		b.mu.Lock()
		b.c = &inUse
		b.mu.Unlock()
		return tui.SettingsSavedMsg{Config: c.Clone()}
	}
}

func (b *boardActions) inHerdr() bool { return b.a.Env.Getenv("HERDR_ENV") == "1" }

func (b *boardActions) Refresh() tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(b.ctx, time.Minute)
		defer cancel()
		snap, stale, err := b.a.loadSnapshot(ctx, b.cfg(), false)
		if err == nil && stale == nil {
			snap.Agents = b.a.refreshSidebarQuietly(ctx, b.cfg(), snap)
		}
		return tui.SnapshotMsg{Snap: snap, Stale: stale, Err: err}
	}
}

// Start hands the terminal to `tkt start` (or `tkt herdr start` inside Herdr), which may ask for repositories.
func (b *boardActions) Start(key ticket.Key) tea.Cmd {
	exe, err := b.a.Executable()
	if err != nil {
		return status("cannot find the tkt binary: " + err.Error())
	}
	args := []string{"start", string(key)}
	if b.inHerdr() {
		args = []string{"herdr", "start", string(key)}
	}
	return tea.ExecProcess(exec.Command(exe, args...), func(err error) tea.Msg {
		if err != nil {
			return tui.StatusMsg("start " + string(key) + ": " + err.Error())
		}
		return tui.StatusMsg("✓ started " + string(key))
	})
}

func (b *boardActions) Detail(key ticket.Key) tea.Cmd {
	return func() tea.Msg {
		d, err := b.a.loadDetail(b.ctx, b.cfg(), key)
		if err != nil {
			return tui.DetailMsg{Key: key, Err: err}
		}
		var buf bytes.Buffer
		b.a.renderShow(&buf, d)
		return tui.DetailMsg{Key: key, Text: buf.String()}
	}
}

// TicketPane closes the board and opens the live ticket pane in the ticket's own workspace, next to its
// agent; without a worktree it opens in the current workspace. Outside Herdr it shows the ticket in place.
func (b *boardActions) TicketPane(key ticket.Key) tea.Cmd {
	if b.inHerdr() {
		return tea.Sequence(b.ticketPaneOpen(key), tea.Quit)
	}
	exe, err := b.a.Executable()
	if err != nil {
		return status("cannot find the tkt binary: " + err.Error())
	}
	return tea.ExecProcess(exec.Command("sh", "-c", `"$0" show "$1"; printf '\nPress Enter to return '; read _`, exe, string(key)), func(err error) tea.Msg {
		if err != nil {
			return tui.StatusMsg("show " + string(key) + ": " + err.Error())
		}
		return nil
	})
}

func (b *boardActions) ticketPaneOpen(key ticket.Key) tea.Cmd {
	return func() tea.Msg {
		h := b.a.NewHerdr()
		pane := herdr.PaneOpen{PluginID: b.a.pluginID(), Entrypoint: "ticket", Env: map[string]string{"TKT_KEY": string(key)}}
		if all, err := b.a.allWork(b.ctx, b.cfg()); err == nil {
			for _, w := range workspace.For(all, key) {
				if w.Path == "" {
					continue
				}
				if ws, ok := workspaceAt(b.ctx, h, w.Path); ok {
					_ = h.FocusWorkspace(b.ctx, ws.ID)
					pane.WorkspaceID, pane.Cwd = ws.ID, w.Path
					break
				}
			}
		}
		if err := h.OpenPluginPane(b.ctx, pane); err != nil {
			return tui.StatusMsg("ticket pane: " + err.Error())
		}
		return nil
	}
}

func (b *boardActions) Open(url string) tea.Cmd {
	return func() tea.Msg {
		if err := b.a.openURL(b.ctx, url); err != nil {
			return tui.StatusMsg("could not open " + url + ": " + err.Error())
		}
		return tui.StatusMsg("opened " + url)
	}
}

func (b *boardActions) Fix(key ticket.Key) tea.Cmd {
	return func() tea.Msg {
		msg, err := b.a.sendFeedback(b.ctx, b.cfg(), key, false)
		if err != nil {
			return tui.StatusMsg(err.Error())
		}
		if !b.inHerdr() {
			return tui.StatusMsg("outside Herdr: run `tkt fix " + string(key) + "` to print the prompt")
		}
		return tui.StatusMsg(msg)
	}
}

func (b *boardActions) Copy(text, what string) tea.Cmd {
	return func() tea.Msg {
		if err := b.a.copyToClipboard(b.ctx, text); err != nil {
			return tui.StatusMsg("could not copy: " + err.Error())
		}
		return tui.StatusMsg("✓ copied the " + what)
	}
}

func (b *boardActions) CopyContext(key ticket.Key) tea.Cmd {
	return func() tea.Msg {
		d, err := b.a.loadDetail(b.ctx, b.cfg(), key)
		if err != nil {
			return tui.StatusMsg(err.Error())
		}
		if err := b.a.copyToClipboard(b.ctx, renderContext(d, nil)); err != nil {
			return tui.StatusMsg("could not copy: " + err.Error())
		}
		return tui.StatusMsg("✓ copied " + string(key) + "'s context as Markdown")
	}
}

// copyToClipboard pipes text into the platform clipboard tool.
func (a *App) copyToClipboard(ctx context.Context, text string) error {
	type tool struct {
		name string
		args []string
	}
	var tools []tool
	switch {
	case a.Env.GOOS == "darwin":
		tools = []tool{{"pbcopy", nil}}
	case credstore.IsWSL(a.Env.Getenv, os.ReadFile):
		tools = []tool{{a.windowsExe("clip.exe"), nil}}
	default:
		tools = []tool{{"wl-copy", nil}, {"xclip", []string{"-selection", "clipboard"}}, {"xsel", []string{"--clipboard", "--input"}}}
	}
	for _, t := range tools {
		if _, err := a.Run.LookPath(t.name); err != nil {
			continue
		}
		_, err := a.Run.Run(ctx, "", t.name, t.args, []byte(text))
		return err
	}
	return errors.New("no clipboard tool found")
}

func status(s string) tea.Cmd {
	return func() tea.Msg { return tui.StatusMsg(s) }
}

func (a *App) windowsExe(name string) string {
	return credstore.WindowsExe(name, a.Run.LookPath, func(p string) bool {
		_, err := os.Stat(p)
		return err == nil
	})
}

// openURL opens a link in the default browser: open on macOS, wslview or explorer.exe under WSL, xdg-open elsewhere.
func (a *App) openURL(ctx context.Context, url string) error {
	if !strings.HasPrefix(url, "https://") {
		return errors.New("only https links are opened")
	}
	var candidates []string
	switch {
	case a.Env.GOOS == "darwin":
		candidates = []string{"open"}
	case credstore.IsWSL(a.Env.Getenv, os.ReadFile):
		candidates = []string{"wslview", a.windowsExe("explorer.exe")}
	default:
		candidates = []string{"xdg-open"}
	}
	var last error
	for _, name := range candidates {
		if _, err := a.Run.LookPath(name); err != nil {
			last = err
			continue
		}
		_, err := a.Run.Run(ctx, "", name, []string{url}, nil)
		return err
	}
	return last
}

// forceColor turns colors on for the board when stdout is a terminal: inside Herdr panes the color
// detection can come up empty and render everything plain. NO_COLOR still wins.
func forceColor(getenv func(string) string) {
	if getenv("NO_COLOR") != "" || !term.IsTerminal(int(os.Stdout.Fd())) {
		return
	}
	profile := termenv.ANSI256
	if ct := getenv("COLORTERM"); ct == "truecolor" || ct == "24bit" {
		profile = termenv.TrueColor
	}
	lipgloss.SetColorProfile(profile)
}
