package cli

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/pavp/tkt/internal/board"
	"github.com/pavp/tkt/internal/config"
	"github.com/pavp/tkt/internal/credstore"
	"github.com/pavp/tkt/internal/daemon"
	"github.com/pavp/tkt/internal/herdr"
	"github.com/pavp/tkt/internal/jira"
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
			"Keys: arrows, wasd or hjkl move · enter actions menu · f send CI failures or review comments to the agent and go to it ·\n" +
			"i ticket pane · o Jira · p pull request (a list when there are several) · O/P copy their link · n needs you · r reviews · v (or the ◫/≣ view switch) columns/list · R (or the Repos button) repositories to show · t (or the Team button) the team's tickets too · enter or a click on a PRs group title collapses it · g refresh · q close.\n" +
			"When tkt is not configured, it offers to run the setup; without a Jira token and a cached board, to connect Jira\n" +
			"first (with a cached board it opens at once and its status line says how to connect).",
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
			snap, have, ok, err := a.boardStart(cmd.Context(), c)
			if err != nil || !ok {
				return err
			}
			forceColor(a.Env.Getenv)
			defer a.claimBoardPane()()
			actions := &boardActions{a: a, c: c, ctx: cmd.Context()}
			prog := tea.NewProgram(tui.New(snap, have, actions, a.boardView(view)).WithState(a.loadBoardState()), tea.WithAltScreen(), tea.WithMouseCellMotion(), tea.WithInput(a.Stdin), tea.WithOutput(a.Stdout), tea.WithContext(cmd.Context()))
			actions.send = prog.Send
			follow, stop := context.WithCancel(cmd.Context())
			defer stop()
			go a.followDaemon(follow, actions, prog.Send)
			_, err = prog.Run()
			if errors.Is(err, tea.ErrProgramKilled) {
				return nil
			}
			return err
		},
	}
	cmd.Flags().StringVar(&view, "view", "", "initial view: columns or list (default: the one last used, else columns)")
	return cmd
}

// ensureJiraToken offers to connect Jira before the board opens when no token is stored, so the board
// never sits on a cache it cannot refresh. ok is false when the user closed the pane instead.
// boardStart loads the cached board; only without one does it first check for a Jira token, since
// reading the token can take seconds (PowerShell under WSL) and a cached board should show at once.
func (a *App) boardStart(ctx context.Context, c *config.Config) (board.Snapshot, bool, bool, error) {
	snap, have, err := a.cache().Load()
	if err != nil {
		return board.Snapshot{}, false, false, err
	}
	if !have {
		if ok, err := a.ensureJiraToken(ctx, c); err != nil || !ok {
			return snap, false, false, err
		}
	}
	return snap, have, true, nil
}

func (a *App) ensureJiraToken(ctx context.Context, c *config.Config) (bool, error) {
	site, err := config.SiteURL(c.Jira.Site)
	if err != nil {
		return false, err
	}
	if _, err := a.jiraToken(site, c.Jira.Email); !errors.Is(err, credstore.ErrNotFound) {
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
	// repos caches each owner's GitHub repositories for the life of the board.
	repos map[string][]string
	// live is the daemon the board reads from while connected; nil means asking Jira and GitHub itself.
	live *daemon.Client
	// send delivers a message to the running board, such as the partial board a refresh paints first.
	send func(tea.Msg)
}

func (b *boardActions) setLive(c *daemon.Client) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.live = c
}

func (b *boardActions) liveClient() *daemon.Client {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.live
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

func (b *boardActions) ListDirs(path string) tea.Cmd {
	return func() tea.Msg {
		abs := filepath.Clean(b.a.Env.ExpandHome(path))
		names, broken, err := listDirs(abs)
		return tui.DirsListedMsg{Path: path, Dir: withSlash(b.a.tildePath(abs)), Abs: withSlash(abs), Names: names, Broken: broken, Err: err}
	}
}

func withSlash(p string) string {
	if strings.HasSuffix(p, "/") {
		return p
	}
	return p + "/"
}

func (b *boardActions) ListRepos(owner string) tea.Cmd {
	return func() tea.Msg {
		b.mu.Lock()
		names, ok := b.repos[owner]
		b.mu.Unlock()
		if !ok {
			ctx, cancel := context.WithTimeout(b.ctx, 30*time.Second)
			defer cancel()
			var err error
			if names, err = b.a.NewGitHub(b.cfg()).Repos(ctx, owner); err != nil {
				return tui.ReposListedMsg{Owner: owner, Err: err}
			}
			b.mu.Lock()
			if b.repos == nil {
				b.repos = map[string][]string{}
			}
			b.repos[owner] = names
			b.mu.Unlock()
		}
		return tui.ReposListedMsg{Owner: owner, Names: names}
	}
}

func (b *boardActions) NativePicker() bool { return b.a.nativePicker() }

func (b *boardActions) JiraBoards(space string) tea.Cmd {
	return func() tea.Msg {
		js, err := b.a.jiraClient(b.cfg())
		if err != nil {
			return tui.JiraBoardsListedMsg{Space: space, Err: err}
		}
		boards, err := b.a.teamBoardChoices(b.ctx, js, space)
		return tui.JiraBoardsListedMsg{Space: space, Boards: boards, Err: err}
	}
}

func (b *boardActions) JiraStatuses(space string) tea.Cmd {
	return func() tea.Msg {
		js, err := b.a.jiraClient(b.cfg())
		if err != nil {
			return tui.JiraStatusesListedMsg{Space: space, Err: err}
		}
		statuses, err := js.Statuses(b.ctx, space)
		out := make([]tui.JiraStatus, len(statuses))
		for i, st := range statuses {
			out[i] = tui.JiraStatus{Name: st.Name, Category: st.Category}
		}
		return tui.JiraStatusesListedMsg{Space: space, Statuses: out, Err: err}
	}
}

func (b *boardActions) Transitions(key ticket.Key) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(b.ctx, 30*time.Second)
		defer cancel()
		js, err := b.a.jiraClient(b.cfg())
		if err != nil {
			return tui.TransitionsMsg{Key: key, Err: err}
		}
		trs, err := js.Transitions(ctx, string(key))
		out := make([]tui.Transition, len(trs))
		for i, t := range trs {
			out[i] = tui.Transition{ID: t.ID, To: t.To, NeedsForm: t.NeedsForm}
			for _, f := range t.Fields {
				field := tui.Field{ID: f.ID, Name: f.Name, Kind: f.Kind, Required: f.Required}
				for _, o := range f.Options {
					field.Options = append(field.Options, tui.Option{ID: o.ID, Value: o.Value})
				}
				out[i].Fields = append(out[i].Fields, field)
			}
		}
		return tui.TransitionsMsg{Key: key, Transitions: out, Err: err}
	}
}

func (b *boardActions) Users(key ticket.Key, query string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(b.ctx, 30*time.Second)
		defer cancel()
		js, err := b.a.jiraClient(b.cfg())
		if err != nil {
			return tui.UsersListedMsg{Key: key, Query: query, Err: err}
		}
		found, err := js.AssignableUsers(ctx, string(key), query)
		users := make([]tui.User, len(found))
		for i, u := range found {
			users[i] = tui.User{ID: u.AccountID, Name: u.DisplayName}
		}
		return tui.UsersListedMsg{Key: key, Query: query, Users: users, Err: err}
	}
}

func (b *boardActions) Assign(key ticket.Key, who tui.Assignee) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(b.ctx, 30*time.Second)
		defer cancel()
		js, err := b.a.jiraClient(b.cfg())
		if err != nil {
			return tui.AssignedMsg{Key: key, Who: who, Err: err}
		}
		id := who.ID
		if who.Me {
			me, err := js.Myself(ctx)
			if err != nil {
				return tui.AssignedMsg{Key: key, Who: who, Err: err}
			}
			id = me.AccountID
		}
		return tui.AssignedMsg{Key: key, Who: who, Err: js.Assign(ctx, string(key), id)}
	}
}

func (b *boardActions) Transition(key ticket.Key, id, to string, values []tui.Value, comment string) tea.Cmd {
	vs := make([]jira.Value, len(values))
	for i, v := range values {
		vs[i] = jira.Value{Field: v.Field, Kind: v.Kind, ID: v.ID}
	}
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(b.ctx, 30*time.Second)
		defer cancel()
		js, err := b.a.jiraClient(b.cfg())
		if err == nil {
			err = js.Transition(ctx, string(key), id, vs, comment)
		}
		return tui.TransitionedMsg{Key: key, To: to, Err: err}
	}
}

func (b *boardActions) SaveState(s tui.BoardState) tea.Cmd {
	return func() tea.Msg {
		if err := b.a.saveBoardState(s); err != nil {
			return tui.StatusMsg("could not remember the board's filters: " + err.Error())
		}
		return nil
	}
}

func (b *boardActions) JiraFields() tea.Cmd {
	return func() tea.Msg {
		js, err := b.a.jiraClient(b.cfg())
		if err != nil {
			return tui.JiraFieldsListedMsg{Err: err}
		}
		ctx, cancel := context.WithTimeout(b.ctx, 30*time.Second)
		defer cancel()
		names, err := js.FieldNames(ctx)
		return tui.JiraFieldsListedMsg{Names: names, Err: err}
	}
}

// listDirs returns the folders inside dir, symlinks to folders included, and the symlinks whose target
// is gone, both sorted ignoring case.
func listDirs(dir string) (names, broken []string, err error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, nil, err
	}
	for _, e := range entries {
		switch {
		case e.IsDir():
			names = append(names, e.Name())
		case e.Type()&os.ModeSymlink != 0:
			fi, err := os.Stat(filepath.Join(dir, e.Name()))
			if errors.Is(err, os.ErrNotExist) {
				broken = append(broken, e.Name())
			} else if err == nil && fi.IsDir() {
				names = append(names, e.Name())
			}
		}
	}
	byName := func(a, b string) int { return strings.Compare(strings.ToLower(a), strings.ToLower(b)) }
	slices.SortFunc(names, byName)
	slices.SortFunc(broken, byName)
	return names, broken, nil
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

func (b *boardActions) Agents(snap board.Snapshot) tea.Cmd {
	return func() tea.Msg {
		return tui.AgentsMsg{FetchedAt: snap.FetchedAt, Agents: b.a.agentStatesQuietly(b.ctx, b.cfg(), snap)}
	}
}

func (b *boardActions) Refresh() tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(b.ctx, time.Minute)
		defer cancel()
		// With the daemon, a refresh asks it to sync; the outcome arrives with its events, so the board
		// keeps showing the refresh until the sync ends.
		if c := b.liveClient(); c != nil && c.Sync(ctx) == nil {
			return nil
		}
		var partial func(board.Snapshot)
		if b.send != nil {
			partial = func(s board.Snapshot) { b.send(tui.SnapshotMsg{Snap: s, Quiet: true}) }
		}
		snap, stale, err := b.a.loadSnapshotWith(ctx, b.cfg(), false, partial)
		if err == nil && stale == nil {
			snap.Agents = b.a.refreshSidebarQuietly(ctx, b.cfg(), snap)
		}
		return tui.SnapshotMsg{Snap: snap, Stale: stale, Err: err}
	}
}

// Start hands the terminal to `tkt start` (or `tkt herdr start` inside Herdr) with the answers the board
// already asked for.
func (b *boardActions) Start(key ticket.Key, opts tui.StartOptions) tea.Cmd {
	cmd, err := b.startCommand(key, opts)
	if err != nil {
		return status("cannot find the tkt binary: " + err.Error())
	}
	return tea.ExecProcess(cmd, func(err error) tea.Msg {
		if err != nil {
			return tui.StatusMsg("start " + string(key) + ": " + err.Error())
		}
		return tui.StatusMsg("✓ started " + string(key))
	})
}

// startCommand clears the screen first: what an earlier Start printed would otherwise still show.
func (b *boardActions) startCommand(key ticket.Key, opts tui.StartOptions) (*exec.Cmd, error) {
	exe, err := b.a.Executable()
	if err != nil {
		return nil, err
	}
	args := []string{"start", string(key)}
	if b.inHerdr() {
		args = []string{"herdr", "start", string(key)}
	}
	if opts.Worktree != "" {
		args = append(args, "--worktree", opts.Worktree)
	}
	for _, r := range opts.Repos {
		args = append(args, "--repo", r)
	}
	return exec.Command("sh", append([]string{"-c", `printf '\033[H\033[2J'; exec "$0" "$@"`, exe}, args...)...), nil
}

// StartChoices reads what a Start of key would ask, with the agents' states when the board runs in Herdr.
func (b *boardActions) StartChoices(key ticket.Key) tea.Cmd {
	return func() tea.Msg {
		var h HerdrAPI
		if b.inHerdr() {
			h = b.a.NewHerdr()
		}
		choices, err := b.a.startChoices(b.ctx, b.cfg(), key, h)
		return tui.StartChoicesMsg{Key: key, Choices: choices, Err: err}
	}
}

func (b *boardActions) Detail(key ticket.Key, known *ticket.Ticket) tea.Cmd {
	return func() tea.Msg {
		d, err := b.a.loadDetail(b.ctx, b.cfg(), key, known)
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
		if pane.WorkspaceID == "" {
			pane.WorkspaceID = b.a.Env.Getenv("HERDR_WORKSPACE_ID")
		}
		pane.TargetPaneID = targetPane(b.ctx, h, pane.WorkspaceID)
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

// Fix sends the ticket's feedback to its agent; inside Herdr the board then steps aside for the agent, as it
// does for the ticket pane, focusing the agent's pane and closing.
func (b *boardActions) Fix(key ticket.Key, known *ticket.Ticket) tea.Cmd {
	return func() tea.Msg {
		var progress func(string)
		if b.send != nil {
			progress = func(s string) { b.send(tui.StatusMsg(s)) }
		}
		msg, pane, err := b.a.deliverFeedback(b.ctx, b.cfg(), key, known, false, progress)
		if err != nil {
			return tui.StatusMsg(err.Error())
		}
		if !b.inHerdr() {
			return tui.StatusMsg("outside Herdr: run `tkt fix " + string(key) + "` to print the prompt")
		}
		if pane == "" {
			return tui.StatusMsg(msg)
		}
		_ = b.a.NewHerdr().FocusPane(b.ctx, pane)
		return tea.QuitMsg{}
	}
}

// copy puts text on the user's clipboard: over SSH through their own terminal, since the remote
// machine's clipboard is not theirs.
func (b *boardActions) copy(text string) error {
	if b.a.overSSH() {
		b.a.copyOverTerminal(text)
		return nil
	}
	return b.a.copyToClipboard(b.ctx, text)
}

func (b *boardActions) Copy(text, what string) tea.Cmd {
	return func() tea.Msg {
		if err := b.copy(text); err != nil {
			return tui.StatusMsg("could not copy: " + err.Error())
		}
		return tui.StatusMsg("✓ copied the " + what)
	}
}

func (b *boardActions) CopyContext(key ticket.Key) tea.Cmd {
	return func() tea.Msg {
		d, err := b.a.loadDetail(b.ctx, b.cfg(), key, nil)
		if err != nil {
			return tui.StatusMsg(err.Error())
		}
		if err := b.copy(renderContext(d, nil)); err != nil {
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

// overSSH reports a session reached over SSH, where a dialog or clipboard tool would act on the remote
// desktop.
func (a *App) overSSH() bool {
	return a.Env.Getenv("SSH_CONNECTION") != "" || a.Env.Getenv("SSH_TTY") != ""
}

// copyOverTerminal asks the user's own terminal to copy text with OSC 52, which travels back through SSH.
func (a *App) copyOverTerminal(text string) {
	_, _ = io.WriteString(a.Stdout, "\x1b]52;c;"+base64.StdEncoding.EncodeToString([]byte(text))+"\a")
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
