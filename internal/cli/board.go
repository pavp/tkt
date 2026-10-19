package cli

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"strings"
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
)

func newBoardCmd(a *App) *cobra.Command {
	var view string
	cmd := &cobra.Command{
		Use:   "board",
		Short: "Interactive board: columns or list, filters, start, fix CI (the Herdr popup)",
		Long: "Interactive board. It opens instantly from the cached board and refreshes in the background.\n" +
			"Keys: arrows, wasd or hjkl move · enter Open agent / Start · f send CI failures or review comments to the agent ·\n" +
			"i ticket pane · o Jira · p pull request · n needs you · r reviews · v columns/list · g refresh · q close.\n" +
			"When tkt is not configured, it offers to run the setup.",
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

// boardActions carries out what the board asks for.
type boardActions struct {
	a   *App
	c   *config.Config
	ctx context.Context
}

func (b *boardActions) inHerdr() bool { return b.a.Env.Getenv("HERDR_ENV") == "1" }

func (b *boardActions) Refresh() tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(b.ctx, time.Minute)
		defer cancel()
		snap, stale, err := b.a.loadSnapshot(ctx, b.c, false)
		if err == nil && stale == nil {
			b.a.refreshSidebarQuietly(ctx, b.c, snap)
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

func (b *boardActions) TicketPane(key ticket.Key) tea.Cmd {
	if b.inHerdr() {
		return func() tea.Msg {
			err := b.a.NewHerdr().OpenPluginPane(b.ctx, herdr.PaneOpen{
				PluginID: b.a.pluginID(), Entrypoint: "ticket", Env: map[string]string{"TKT_KEY": string(key)},
			})
			if err != nil {
				return tui.StatusMsg("ticket pane: " + err.Error())
			}
			return tui.StatusMsg("opened the ticket pane for " + string(key))
		}
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
		msg, err := b.a.sendFeedback(b.ctx, b.c, key, false)
		if err != nil {
			return tui.StatusMsg(err.Error())
		}
		if !b.inHerdr() {
			return tui.StatusMsg("outside Herdr: run `tkt fix " + string(key) + "` to print the prompt")
		}
		return tui.StatusMsg(msg)
	}
}

func status(s string) tea.Cmd {
	return func() tea.Msg { return tui.StatusMsg(s) }
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
		candidates = []string{"wslview", "explorer.exe"}
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
