# tkt

Multi-project ticket manager for agent-driven development.

`tkt` lists your sprint tickets across Jira spaces, starts work on them (opens an agent that creates the branch and worktree), and shows the status of each ticket, including its pull requests and CI, on a single board.

Status: early development. Design: [`docs/design.md`](docs/design.md).

## Install

macOS, Linux, and Windows through WSL:

```bash
brew install pavp/tap/tkt
```

Or with Go 1.27+:

```bash
go install github.com/pavp/tkt/cmd/tkt@latest
```

Then, inside Herdr, `tkt herdr install` adds the board and ticket panes, offers the `prefix+t` / `prefix+i` shortcuts, and offers sidebar rows that show each workspace's ticket (`ACTO-123 · ✕ CI · #812`) and agent (`● claude working`, `○ claude idle`, `! claude needs you`, `no agent`). The board shows the same agent state on its cards and in an AGENT column of the list view. Herdr only shows those lines when its config lists them; `tkt herdr uninstall` removes both blocks.

### Releasing

Push a `vX.Y.Z` tag: the release workflow runs GoReleaser, publishes the archives on GitHub Releases and updates the formula in `pavp/homebrew-tap`. It needs a `TKT_HOMEBREW_TAP_TOKEN` secret with write access to that tap. Validate locally with `goreleaser release --snapshot --clean --skip=publish`.

## Set up

1. Run the setup. It asks for your Jira site and email, stores and checks the API token (create one at https://id.atlassian.com/manage-profile/security/api-tokens), reads your spaces from Jira, and writes `~/.config/tkt/config.toml`:

   ```bash
   tkt init
   ```

   The token goes to macOS Keychain, the Linux Secret Service, or Windows Credential Manager under WSL. `TKT_JIRA_TOKEN` overrides it for tests and headless use. You can also write the config by hand from [`docs/config.example.toml`](docs/config.example.toml) and store the token with `tkt auth jira`.
2. GitHub access comes from `gh`. Repositories are optional: tkt infers them from the pull requests Jira links to your tickets, and picks the logged-in `gh` account that can read each organization (setup offers `gh auth login` when none can). `[github.accounts]` in the config pins an account. An inferred repository you do not want is ignored from the board's settings (`d` or its ✕), which adds it to the space's `ignored` list.
3. Check everything:

   ```bash
   tkt doctor
   ```

## Commands

| Command | What it does |
|---|---|
| `tkt init` | Interactive setup: Jira account and token, spaces, repositories, GitHub accounts |
| `tkt auth jira [--site S --email E]` | Store the Jira API token: opens Atlassian's token page; press Enter once the token is copied (tkt reads it from the clipboard, asks before using it, and clears it once saved) or paste it at the hidden prompt; stdin when piped |
| `tkt auth status` | Show which credentials tkt can use, without printing them |
| `tkt doctor` | Check config, credentials, tools (`git`, `gh`, `claude`, `herdr`) and local clones |
| `tkt list [--space S] [--needs-you] [--cached]` | Your open-sprint tickets with PRs and CI; tickets with failing CI or requested changes first, then review requests. Falls back to the cached board when offline |
| `tkt show [KEY] [--watch]` | One ticket: PRs and CI, description, acceptance criteria, attachments, latest comments. Without `KEY`, uses the current branch |
| `tkt context [KEY] [--images DIR]` | The ticket as Markdown for an agent (`> .ticket.md`); `--images` downloads image attachments |
| `tkt start KEY [--repo R] [--clone]` | Open Claude Code on a ticket: in its worktree if it has one, otherwise in the chosen repositories with instructions to create the branch per project rules |
| `tkt link [KEY]` | Register the current worktree as the work on a ticket; the branch must contain the key |
| `tkt board [--view list]` | Interactive board: arrows or `wasd` move, columns or list (`v`), filters (`n` needs you, `r` reviews), `enter` or double-click actions menu (start, ticket pane, Jira, PRs, fix CI, copy; `c` copies the highlighted link), `f` fix CI, `i` ticket pane, `o` Jira, `p` PR, `O`/`P` copy their link (over SSH, copies reach your own clipboard through the terminal with OSC 52), `,` settings (repositories per space, local paths and clone folders, saved to the config; `tab` in the repository field completes `owner/name` from your GitHub repositories, with a dropdown of the matches when there are several (`↑↓` choose, `enter` picks, typing filters, `esc` closes); the path is found among your clones, completed with `tab` as in a shell, or picked with `ctrl+o` (the system's folder chooser on a local desktop; over SSH, under WSL, or without zenity/kdialog, a folder browser inside the terminal: arrows move, `→` opens, `←` goes up, `enter` chooses the highlighted folder, `space` the folder shown, `/` filters, `.` shows hidden folders), and a picked folder names its repository). Offers the setup when tkt is not configured, and to connect Jira when no token is stored and nothing is cached (a cached board opens at once) |
| `tkt fix [KEY] [--print]` | Send the failing check logs and requested-change comments of the ticket's PRs to its agent in Herdr (or print the prompt) |
| `tkt daemon start\|status\|stop\|run` | The background sync: it refreshes Jira and GitHub once a minute in one place (asking Jira only about tickets that changed), follows Herdr's agents live, keeps the Herdr sidebar current, and feeds the board and `tkt list` over `127.0.0.1` with a private token. The board, `tkt list` and Herdr's startup hook start it when it is not running; without it they work as before |

Every command accepts `--json` and `--config <path>`.

## Herdr

```bash
tkt herdr install
```

links the tkt plugin into the running Herdr (0.9.5 or later). Herdr plugins cannot bring their own keys, so install offers to add `prefix+t` (board) and `prefix+i` (ticket pane) to your Herdr config when those keys are free, backing it up first (`--keys` adds them without asking, `--no-keys` skips them). The plugin gives you:

- a board popup (`tkt.board` action; offers setup when tkt is not configured); there is one board, so opening it from another workspace moves it there,
- a live ticket pane for the current workspace (`tkt.ticket`),
- Ctrl-click on Jira and pull request links to open their ticket,
- sidebar labels such as `DEMO-1 · ✕ CI · #812 · claude working`, refreshed by `tkt list`, the `tkt.refresh` action, and at Herdr startup from the cache,
- automatic `tkt link` when an agent creates a worktree with `herdr worktree create`.

`tkt herdr start KEY` opens Claude Code in a new Herdr workspace per repository instead of the current terminal; when the ticket already has a worktree with an agent working in it, it focuses that agent's workspace instead (the board's menu then reads "Go to the agent in Herdr"). `tkt herdr uninstall` removes the plugin and the keybindings it added.

## Develop

```bash
go build -o tkt ./cmd/tkt
go test ./...
```

## License

MIT, see [LICENSE](LICENSE).
