package cli

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/pavp/tkt/internal/board"
	"github.com/pavp/tkt/internal/daemon"
	"github.com/pavp/tkt/internal/ticket"
	"github.com/pavp/tkt/internal/workspace"
)

// syncEvery is how often the daemon refreshes the board from Jira and GitHub.
const syncEvery = time.Minute

// watchEvery is how soon the daemon notices that the TUI or another program saved the board's filters or Settings.
const watchEvery = 2 * time.Second

func newDaemonCmd(a *App) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "daemon",
		Short: "Background sync that keeps the board and the Herdr sidebar fresh",
		Long: "The daemon syncs Jira and GitHub once a minute in one place and serves the board on 127.0.0.1,\n" +
			"so the board, tkt list and the Herdr plugin read fresh data without each asking Jira and GitHub.\n" +
			"They start it in the background when it is not running; start, status and stop manage it by hand.",
	}
	cmd.AddCommand(&cobra.Command{
		Use:   "start",
		Short: "Start the daemon in the background unless one runs",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			info, err := a.ensureDaemon(cmd.Context())
			if err != nil {
				return err
			}
			a.printf("running · pid %d · 127.0.0.1:%d\n", info.PID, info.Port)
			return nil
		},
	}, &cobra.Command{
		Use:   "status",
		Short: "Say whether the daemon runs, and where",
		Args:  cobra.NoArgs,
		RunE:  func(cmd *cobra.Command, _ []string) error { return a.daemonStatus(cmd.Context()) },
	}, &cobra.Command{
		Use:   "stop",
		Short: "Stop the running daemon",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			pid, err := a.stopDaemon(cmd.Context())
			if err == nil {
				a.printf("✓ stopped the tkt daemon (pid %d)\n", pid)
			}
			return err
		},
	}, &cobra.Command{
		Use:   "run",
		Short: "Run the daemon in the foreground until interrupted",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
			defer stop()
			return a.runDaemon(ctx)
		},
	})
	return cmd
}

func (a *App) runDaemon(ctx context.Context) error {
	if info, ok := a.runningDaemon(ctx); ok {
		return fmt.Errorf("a tkt daemon is already running (pid %d)", info.PID)
	}
	var seed *board.Snapshot
	if snap, ok, err := a.cache().Load(); err == nil && ok {
		seed = &snap
	}
	s, err := daemon.New(daemon.Options{Dir: a.Env.DataDir(), Version: Version, Sync: a.daemonSync, Interval: syncEvery, Seed: seed, Detail: a.daemonDetail, Context: a.daemonContext, State: a.boardViewState,
		Lists: a.boardListState, SavedState: a.loadBoardState, SaveState: a.saveBoardState, Repos: a.daemonRepos, Backlog: a.daemonBacklog, StartChoices: a.daemonStartChoices, Start: a.daemonStart,
		Transitions: a.daemonTransitions, Transition: a.daemonTransition, Users: a.daemonUsers, Assign: a.daemonAssign,
		TaskAgents: a.daemonTaskAgents, Task: a.daemonTask,
		StateFile: a.boardStateFile(), ConfigFile: a.ConfigPath(), WatchEvery: watchEvery, Log: a.printf, SaveAgentKinds: a.saveAgentKinds,
		Settings: a.savedConfig, SaveSettings: a.saveConfig, JiraStatuses: a.daemonStatuses, JiraBoards: a.daemonBoards, JiraFields: a.daemonFields})
	if err != nil {
		return err
	}
	ln, err := daemon.Listen(0)
	if err != nil {
		return err
	}
	a.printf("tkt daemon listening on %s\n", ln.Addr())
	go a.watchAgents(ctx, s)
	go a.syncHerdrPlugin(ctx)
	go a.watchUpgrade(ctx)
	return s.Serve(ctx, ln)
}

// upgradeEvery is how often the daemon asks its binary whether an upgrade replaced it.
const upgradeEvery = time.Minute

// watchUpgrade hands the daemon over to the binary an upgrade put in its place, within a minute, so the new release
// serves and updates the Herdr plugin without waiting for a board or a hook to start it.
func (a *App) watchUpgrade(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-time.After(upgradeEvery):
		}
		if a.handOverIfUpgraded(ctx) {
			return
		}
	}
}

// handOverIfUpgraded starts `tkt daemon start` from a binary newer than this release, which replaces this daemon
// as any release replaces an older one; a dev build, or a binary that cannot say its version, is left alone.
func (a *App) handOverIfUpgraded(ctx context.Context) bool {
	ours, ok := parseVersion(Version)
	if !ok {
		return false
	}
	exe, err := a.Executable()
	if err != nil {
		return false
	}
	out, err := a.Run.Run(ctx, "", exe, []string{"--version"}, nil)
	fields := strings.Fields(string(out))
	if err != nil || len(fields) == 0 {
		return false
	}
	theirs, ok := parseVersion(fields[len(fields)-1])
	if !ok || slices.Compare(theirs, ours) <= 0 {
		return false
	}
	if _, err := a.StartDetached(exe, []string{"daemon", "start"}, filepath.Join(a.Env.DataDir(), "daemon.log")); err != nil {
		fmt.Fprintf(a.Stderr, "%s upgrade: %v\n", a.Now().UTC().Format(time.RFC3339), err)
		return false
	}
	fmt.Fprintf(a.Stderr, "%s upgrade: handing over to %s\n", a.Now().UTC().Format(time.RFC3339), fields[len(fields)-1])
	return true
}

// ticketView is a ticket's detail with what the details screen shows beside it: why it needs the user, its
// agent's state and its work.
type ticketView struct {
	detail
	Attention []ticket.Reason  `json:"attention,omitempty"`
	Agent     string           `json:"agent,omitempty"`
	Work      []workspace.Work `json:"work,omitempty"`
}

// daemonDetail serves GET /v1/tickets/{key} with the configuration read afresh, like each sync. Work that cannot
// be listed is left out rather than failing the detail.
func (a *App) daemonDetail(ctx context.Context, key ticket.Key, known *ticket.Ticket, agent string) (any, error) {
	c, err := a.LoadConfig()
	if err != nil {
		return nil, err
	}
	d, err := a.loadDetail(ctx, c, key, known)
	if err != nil {
		return nil, err
	}
	v := ticketView{detail: d, Attention: d.Ticket.Attention(), Agent: agent}
	if all, err := a.allWork(ctx, c); err == nil {
		v.Work = workspace.For(all, key)
	}
	return v, nil
}

// daemonContext serves GET /v1/tickets/{key}/context: the Markdown the board's copy context puts on the clipboard.
func (a *App) daemonContext(ctx context.Context, key ticket.Key) (string, error) {
	c, err := a.LoadConfig()
	if err != nil {
		return "", err
	}
	return a.contextText(ctx, c, key)
}

// daemonBacklog serves the daemon's GET /v1/backlog as the board loads its Backlog; when Jira fails it hands back
// the last copy, if any, beside the error.
func (a *App) daemonBacklog(ctx context.Context, force bool) (board.Backlog, error) {
	c, err := a.LoadConfig()
	if err != nil {
		return board.Backlog{}, err
	}
	var last board.Backlog
	bl, err := a.loadBacklog(ctx, c, force, func(older board.Backlog) { last = older })
	if err != nil {
		return last, err
	}
	return bl, nil
}

// daemonRepos names the configured repositories, which the daemon's Repos filter offers.
func (a *App) daemonRepos() []string {
	c, err := a.LoadConfig()
	if err != nil {
		return nil
	}
	var names []string
	for _, r := range c.Repos() {
		names = append(names, r.Name)
	}
	return names
}

// logNewWarnings writes each sync warning the previous sync did not have to the daemon's log, so a failure
// that empties part of the board leaves a trace without repeating itself every sync.
func (a *App) logNewWarnings(warnings []string) {
	a.warnMu.Lock()
	defer a.warnMu.Unlock()
	for _, w := range warnings {
		if !slices.Contains(a.syncWarnings, w) {
			fmt.Fprintf(a.Stderr, "%s sync: %s\n", a.Now().UTC().Format(time.RFC3339), w)
		}
	}
	a.syncWarnings = slices.Clone(warnings)
}

// daemonSync reads the configuration afresh, so changes made in the board's settings apply without a
// restart, fails when the fetch does, so the daemon keeps its last good board, and rewrites the Herdr
// sidebar with the new board when Herdr runs, reading agent states from every Herdr it follows.
func (a *App) daemonSync(ctx context.Context, partial func(board.Snapshot)) (board.Snapshot, error) {
	c, err := a.LoadConfig()
	if err != nil {
		return board.Snapshot{}, err
	}
	snap, stale, err := a.loadSnapshotWith(ctx, c, false, partial)
	if err != nil {
		return board.Snapshot{}, err
	}
	if stale != nil {
		return board.Snapshot{}, stale
	}
	a.logNewWarnings(snap.Warnings)
	hctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	var agents []map[string]string
	for _, h := range a.daemonHerdrs().current(hctx) {
		if _, got, err := a.updateSidebarOn(hctx, h.api, c, snap, h.sidebar()); err == nil {
			agents = append(agents, got)
		}
	}
	if len(agents) > 0 {
		snap.Agents = mostUrgentPerTicket(agents...)
	}
	return snap, nil
}
