# tkt — Design

`tkt` is a multi-project ticket manager for agent-driven development. It lists the current sprint's tickets across several Jira projects, starts work on a ticket (opens an agent that creates the branch and worktree per project rules), and shows the status of every ticket, including its pull requests and CI, on one board.

Status: design. Phase 1 (CLI) has not started. Sections marked **Draft** are refined when their phase begins.

## 1. Goals and non-goals

Goals:

- One view of "my sprint" across Jira projects (DEMO, OPS, and others), joined with GitHub pull requests and CI.
- Start a ticket in one step: open a Claude Code agent in Herdr that creates the branch and worktree per project rules and registers them with `tkt`.
- A deterministic core that works without any LLM.
- Runs on macOS, Linux, and Windows through WSL2.

Out of scope: voice, system control, own memory, multi-user. No early generalization: the user's projects live in configuration, not in a generic plugin system.

## 2. Architecture

```
┌──────────────┐ ┌─────────────┐ ┌──────────────┐ ┌────────────────────┐
│ Herdr plugin │ │ Web UI      │ │ Tray app     │ │ Claude Code        │
│ (calls CLI)  │ │ (browser)   │ │ (same UI)    │ │ skill → MCP        │
└──────┬───────┘ └──────┬──────┘ └──────┬───────┘ └─────────┬──────────┘
       │ exec           └───── HTTP + WebSocket ─────┐       │ exec / stdio
       ▼                                             ▼       ▼
┌──────────────────────────────────────────────────────────────────────┐
│ tkt (single Go binary)                                               │
│  CLI/TUI · daemon (SQLite cache, background sync, API v1) · MCP     │
└───────────────┬───────────────────────────────┬──────────────────────┘
                ▼                               ▼
            Jira Cloud                     GitHub (PRs, CI)
```

Rules:

- `tkt` is the only component that talks to Jira and GitHub. Every other component is a thin client.
- One binary, three roles: `tkt <command>` (CLI/TUI), `tkt daemon` (cache, sync, local API, embedded UI), `tkt mcp` (MCP server over stdio).
- When the daemon is running, the CLI and MCP read through it; when it is not, the CLI works directly against Jira and GitHub (phase 1 has no daemon).

### Proposed code layout

```
cmd/tkt/             entrypoint, command wiring
internal/ticket/     domain: ticket, sprint, status, branch-name convention (no I/O)
internal/jira/       Jira adapter (REST, ADF → Markdown)
internal/github/     GitHub adapter (PRs, checks)
internal/workspace/  worktree discovery, links, agent launch (never creates branches)
internal/config/     ~/.config/tkt loading and validation
internal/secrets/    keychain / credential store
internal/daemon/     cache, sync loop, API v1, event hub   (phase 3)
internal/mcp/        MCP server                           (phase 4)
web/                 React + Vite UI, embedded with go:embed (phase 3)
plugin/herdr/        Herdr plugin manifest and scripts    (phase 2)
```

Domain code depends on nothing; adapters implement ports defined next to the use cases that need them.

## 3. Daemon API v1 (Draft — the most important contract)

Designed now, implemented in phase 3. Every client depends on it, so it is versioned from the first commit.

Transport and auth:

- Listens on `127.0.0.1` only (never `0.0.0.0`). Default port configurable; the chosen port is written next to the token.
- A random 256-bit token is generated at first start and stored in `<data>/token` with mode `0600`.
- HTTP requests send `Authorization: Bearer <token>`. `GET /health` is the only unauthenticated route and reveals nothing beyond liveness and version.
- WebSocket at `/v1/ws`. Browsers cannot set headers on a WebSocket upgrade, so the first client message must be `{"type":"hello","token":"…","protocol":1}`; otherwise the socket closes with code `4401` (pattern taken from gentle-dot). The upgrade also checks `Origin` against an allowlist (the daemon's own origin and the tray shell's origin).

Versioning:

- HTTP routes are prefixed with `/v1`. Breaking changes create `/v2`; additive changes (new fields, new event types) do not.
- Clients must ignore unknown fields and unknown event types.

Resources (Draft):

| Method | Route | Purpose |
|---|---|---|
| GET | `/health` | liveness, version, protocol |
| GET | `/v1/tickets` | sprint tickets across projects, with PR and CI summary |
| GET | `/v1/tickets/{key}` | one ticket with description, comments, PRs, checks |
| POST | `/v1/tickets/{key}/start` | open the agent in the selected repositories (via Herdr) |
| POST | `/v1/tickets/{key}/links` | register a branch and worktree created by the agent |
| POST | `/v1/sync` | request an immediate sync |
| POST | `/v1/confirmations/{id}` | user accepts or rejects a pending write |

Events over WebSocket:

- Envelope: `{"seq": <n>, "type": "<event>", "ts": "<RFC 3339>", "data": {…}}`.
- `seq` starts at 1 per connection and increases by exactly 1. A client that sees a jump knows it missed events, discards its cached state, and refetches with `GET /v1/tickets` (or reconnects).
- After `hello`, the daemon sends `snapshot` with the full current state; events after that are deltas.
- Initial event types: `snapshot`, `ticket.updated`, `ticket.removed`, `link.added`, `pr.updated`, `checks.updated`, `agent.updated`, `sync.started`, `sync.finished`, `sync.failed`, `auth.expired`, `confirmation.requested`, `confirmation.resolved`.
- `agent.updated` carries the agent state of each linked worktree (working, blocked, idle, none) and since when. The daemon reads it from Herdr's socket API (`pane.agent_status_changed` events); without Herdr the state is `none`.
- Errors: `{"type":"error","code":"<machine code>","message":"…"}`.

### Phase 3a: the daemon for the Herdr plugin

Phase 3a builds the part of this contract the Herdr plugin, the board and `tkt list` need; the rest follows with the web UI.

- Routes: `GET /health`, `GET /v1/tickets` (the board snapshot), `POST /v1/sync`, and `/v1/ws`. Events: `snapshot`, `ticket.updated`, `ticket.removed`, `agent.updated`, `sync.started`, `sync.finished`, `sync.failed`.
- Sync: every minute and on `POST /v1/sync`. Jira and GitHub are polled, since their webhooks need a public endpoint a `127.0.0.1` daemon does not have. A ticket's development panel is asked again only when the issue's `updated` changed or the last answer is older than ten minutes.
- Herdr: the daemon subscribes to `pane.agent_status_changed` (`events.subscribe`), maps each workspace to its ticket, rewrites the sidebar lines at once and sends `agent.updated`. Herdr plugins cannot keep a process alive, so the daemon lives outside the plugin.
- Start: automatic. The Herdr `[[startup]]` hook, the board and `tkt list` start `tkt daemon run` in the background when `GET /health` does not answer; a lock file in the data directory keeps one daemon per user. `tkt daemon status` and `tkt daemon stop` manage it; a launchd or systemd service can come later.
- Cache: the JSON snapshot cache the CLI already keeps, written only by the daemon while it runs; SQLite arrives with the web UI.
- Without the daemon, every client works as before, straight against Jira and GitHub.

## 4. Configuration and data

- Configuration: `~/.config/tkt/` (`$XDG_CONFIG_HOME/tkt` when set). Nothing about a specific organization is hard-coded; everything comes from configuration or is discovered from Jira. Format and exact fields are decided at the start of phase 1.
- Configured, because Jira cannot know it: Jira base URL and account, each project's repositories (`owner/name`, optional) and their local paths, the `gh` account per GitHub organization, optional acceptance-criteria field, and status overrides.
- Local clones are found, not typed: `start`, discovery, and Herdr work on the local checkout, so `tkt` resolves each `owner/name` to a folder by (1) an explicit `path` in that repository's configuration, else (2) scanning the configured root folders (`repo_roots`, for example `~/dev/work`) for git repositories whose remote matches, cached until it stops matching, else (3) offering to clone it into the first root with `gh repo clone`.
- Discovered from Jira, never configured: statuses per project (`/project/{key}/statuses`) and transitions, fetched at runtime per issue (`/issue/{key}/transitions`) because they depend on each issue's workflow.
- Board columns default to Jira's `statusCategory` (To Do, In Progress, Done); configuration overrides only what categories cannot express, such as which status means "in review".
- `tkt init` asks for the Jira URL, reads the user's projects and statuses, and writes a draft configuration; the user only fills in repositories.
- A Jira project can span many repositories, one per product. Reference test case: site `https://example.atlassian.net`, project `DEMO` (Example Product), whose product Web lives in `example-org/web`; sample ticket `DEMO-170450`.
- Data: its own directory with mode `0700` — `$XDG_DATA_HOME/tkt` (default `~/.local/share/tkt`) on Linux and WSL, `~/Library/Application Support/tkt` on macOS. Holds the SQLite cache, the daemon token, and logs.
- Secrets are never stored in configuration or data files (see section 7).

## 5. Conventions

- The ticket key is part of the branch name (for example `feature/DEMO-123-slug`). This joins Jira and GitHub without manual mappings: a PR belongs to a ticket when its head branch contains the key.
- `tkt` never runs the git commands that create branches or worktrees. The agent creates them, following each project's own rules for branch names (fix, feature, docs, ...), base branches, and tooling (plain git, git flow, project scripts). `tkt` enforces one invariant: the branch name must contain the ticket key, in any case (`feature/DEMO-123-x` and `test/acto-123-x` both work); only keys of configured spaces count, so words like `fix-123` never match.
- `tkt start <KEY>` lets the user pick the repositories (interactive, preselected with the last set used for that space; `--repo` skips the prompt) and opens Claude Code in each repository's main checkout with an initial prompt: work on the ticket, create the branch and worktree per project rules, save the context with `tkt context`, and register with `tkt link`. In phase 2 the agent creates the worktree through `herdr worktree create`, so Herdr sees the new workspace.
- `tkt link [KEY]`, run from the worktree, registers the branch and path (the key is inferred from the branch when omitted), and the UI updates immediately. It rejects a branch without the key and explains how to fix it. In phase 4 the same operation is an MCP tool.
- Discovery keeps the board correct when nobody calls `tkt link`: `tkt` scans `git worktree list` and the local and remote branches of configured repositories, and associates every branch that contains a ticket key. Linking gives immediacy; discovery gives correctness.
- Repositories are configured as `owner/name` and may belong to any GitHub organization; nothing is tied to a specific organization.
- A ticket can span several repositories (for example, one user story with backend and frontend work). A Jira space (project) maps to a list of repositories; `tkt start` opens one agent per selected repository, and each creates its own branch and worktree.
- A ticket aggregates the pull requests and checks of every repository whose branches carry its key. Its board status is the most urgent one among them (for example, failing CI in any repository marks the ticket).
- `tkt context <KEY>` prints the ticket as Markdown: title, description (ADF converted to Markdown), acceptance criteria, attachments, and the latest comments. `--images <dir>` downloads image attachments so the agent can see them; other attachments are linked. The agent saves it as `.ticket.md` in the worktree (gitignored); both the Herdr `ticket` pane and the agent read it.
- `tkt start` on a ticket that already has a linked or discovered worktree opens the agent there instead of asking it to create a new one.
- Commands print human-readable output by default and offer `--json` with a stable schema for Herdr, the UI, and agents.
- In phase 1, `tkt start` only suggests the Jira transition to "In Progress"; executing it is a write and arrives later with UI confirmation.
- Usually one ticket is active, but several can be active at once; nothing assumes a single active ticket.

### Board scope

- Tickets assigned to the user in open sprints across configured projects, plus finished tickets the user once held (teams often reassign a ticket to QA or a reviewer when it is done), plus pull requests where the user's review is requested.
- Each space has its own sprint; the UI shows the active sprints the user's tickets belong to (for example `DEMO S42 · 4d left`), read from the tickets' own Sprint field. Listing every board of a space shows other teams' sprints and costs one request per board, so it is avoided.
- `reviews` in the configuration selects review requests: `direct` (default; requests addressed to the user in configured repositories), `team` (also requests to the user's teams, which include CODEOWNERS and dependabot traffic), or `off`.
- Repositories are optional configuration: `tkt` infers each space's repositories from the pull requests Jira links to the user's tickets (development panel) and from the PRs matched to them, and uses that set for CI, review requests and the GitHub account; configured repositories add to it and set local paths. The `gh` account for an owner is the configured one, else the logged-in account named like the owner or belonging to the organization (`GET /user/memberships/orgs/{org}`), remembered in the data directory.
- "Needs you" means: one of my tickets has failing CI or requested changes. PRs awaiting my review are listed apart, under "Review requested" and the reviews filter.
- Subtasks are grouped under their parent story.
- CI status comes from GitHub Actions checks.

## 6. Integrations

Jira — base URL, authentication method, projects, statuses, and transitions are asked before phase 1 code.

Jira — Jira Cloud REST API with email + API token stored in the keychain. "My sprint" is `assignee = currentUser() AND sprint in openSprints()` across configured projects (every project uses sprints). `.ticket.md` always includes the full description; a project may declare an optional acceptance-criteria custom field, rendered as its own section, because where criteria live varies by project and author. The latest 5 comments are included (configurable).

GitHub — direct REST/GraphQL API (one GraphQL query fetches PRs and checks for many repositories; testable with `httptest`; no subprocess per call). The token comes from the user's `gh` login via `gh auth token --user <account>`, with the account configured per organization, so the active `gh` account does not matter. When `gh` is unavailable, `tkt` asks for a fine-grained PAT and stores it in the keychain. Tokens are held in memory only.

### Herdr plugin (phase 2)

Based on https://herdr.dev/docs/plugins/ and https://herdr.dev/docs/socket-api/.

- A plugin is a directory with a `herdr-plugin.toml` manifest; the whole Herdr CLI is the plugin API. The plugin stays thin: it calls `tkt` and calls Herdr back through `HERDR_BIN_PATH` (portable across Unix sockets and Windows named pipes).
- Action `start`: `agent.start` with Claude Code in the repository's workspace and the initial prompt from `tkt start`. An open workspace at the repository with no agent is reused (claude gets a split next to a busy pane); otherwise a new workspace opens. When the reused workspace's checkout is free (no local changes, on the branch `origin/HEAD` points to), the agent creates only its branch there, with no worktree. Otherwise it creates its branch and worktree with `herdr worktree create` (`worktree.create` creates the branch when it does not exist and returns the new workspace and root pane), so the worktree appears as a Herdr workspace.
- Pane `board` with `placement = "popup"` running `tkt board`, a TUI with two views toggled with `v`: columns (To Do / In Progress / In Review / Done, like the web board; arrows, `wasd` or `hjkl` move between columns and tickets) and a list; it falls back to the list when the terminal is narrower than about 100 columns. `n` and `r` filter to "Needs you" and reviews. Columns hold only the user's own tickets; PRs awaiting the user's review appear in a "Review requested" strip above them and in the reviews filter, never in "Needs you", as on the web board. Without the daemon, the board reads a local disk cache in the data directory and refreshes it in the background, so it opens instantly; phase 3 replaces it with the daemon's SQLite cache. `f` sends the selected ticket's failing check log, or its requested-change review comments, to that ticket's agent with `agent.prompt` (also available as `tkt fix`, which prints the prompt outside Herdr); it writes nothing to Jira or GitHub; pane `ticket` opened as a split running `tkt show --watch` for the workspace's ticket (key taken from its branch). Both are ordinary pane processes that exit with their pane, not daemons.
- `link_handlers` with regex patterns for Jira issue URLs and GitHub PR URLs.
- Sidebar status through `workspace.report_metadata` tokens, rendered as `$name`. A report may set at most 16 keys and a workspace keeps at most 32; names are 1–32 ASCII letters, digits, `_`, `-`.
- CLI support for the plugin: `tkt board` (a TUI board for the popup pane) and `tkt herdr <restore|open-board|open|start|on-worktree|setup|install>` (glue commands the manifest calls).
- Setup from Herdr: a `setup` action opens a tab running `tkt init` (a tab, not a popup, because Herdr popups cannot be moved or closed by hand); `tkt auth jira` remembers the Jira site and email so setup does not ask again; opening the board without configuration shows "tkt isn't set up · enter to set up" and launches the same popup. The logic stays in the CLI; the agent never sees the token, because the popup has no pane id it could read.
- Link handlers for Jira issue and GitHub PR URLs run an `open` action: it focuses the ticket's workspace when a worktree is linked, otherwise it shows the ticket in a popup with Start. Clicking never starts an agent by itself.
- An event hook on `worktree.created` runs `tkt herdr on-worktree`, which links the new worktree automatically when its branch carries a ticket key, so linking does not depend on the agent remembering to run `tkt link`.
- Keybindings: plugins cannot declare keys, so `tkt herdr install` offers `prefix+t` (board) and `prefix+i` (ticket pane), skipping any key the user's config or Herdr's defaults (`herdr --default-config`) already bind, writing them in a marked block after backing up the config, and reloading it; `tkt herdr uninstall` removes that block.
- Limitations to design around: tokens do not survive Herdr restarts; values are limited to 80 characters (per the project brief); plugins have no daemons — `[[startup]]` hooks are one-shot and must restore state and exit; plugins ship no default keybindings (users bind actions in their own config).

## 7. Security

- Daemon: `127.0.0.1` only, 256-bit token in a `0600` file, `Origin` check plus `hello` token on WebSocket (section 3).
- Jira and GitHub tokens live in the OS credential store, behind one secrets interface with a backend per platform: macOS Keychain; Linux Secret Service; on WSL, Windows Credential Manager reached through `powershell.exe` (read once at startup and held in memory, since PowerShell takes about a second to start). No encrypted-file backend: with the key already in Credential Manager, a second encrypted copy adds a moving part and no security. `TKT_JIRA_TOKEN` works everywhere as a documented, less secure fallback for tests and headless use. Never in plain text in the repository, configuration, logs, or chat.
- Writes (Jira transitions, comments, merges) require confirmation in the UI, never inside the agent. The MCP server pauses, emits `confirmation.requested` over WebSocket, and executes only after the user accepts in the UI.
- During development, only read operations run against real Jira and GitHub. Any write against real services requires explicit approval first.

## 8. LLM integration (phase 6, optional)

- The core is deterministic and does not use an LLM.
- One-shot tasks (ticket summary, PR description): `claude -p --output-format json --json-schema <schema>`, with or without `--bare` per the constraint below.
- Panel chat: the daemon supervises one persistent Claude Code process with `--input-format stream-json --output-format stream-json`, restarts it with exponential backoff, and forwards events to the UI. Isolated configuration: `--bare`, only the `tkt` MCP server, restricted tools (no shell).
- Claude Code in workspaces reaches `tkt` first through a skill that uses the CLI, then through MCP with typed tools.
- Every AI feature is optional by configuration; when it fails, `tkt` keeps working.
- `--bare` constraint (verified with `claude --help`): it skips hooks, plugins, auto-memory, keychain reads, and `CLAUDE.md` auto-discovery, and authenticates only with `ANTHROPIC_API_KEY` or `apiKeyHelper` via `--settings`, never OAuth. A subscription (OAuth) user needs an API key for every `--bare` call; otherwise isolate with explicit flags instead. Decide per feature in phase 6.

## 9. Platforms and tray app (phase 5)

- Web first: the daemon serves the React UI (embedded with `go:embed`) on localhost, which works on every OS including WSL.
- Tray app wraps the same UI. Shell is interchangeable: Wails v3 or Tauri; evaluate Wails v3 maturity when phase 5 starts.
- Two surfaces, always designed together: the full window (browser or app window) and the tray panel (about 400×720). Every screen and state exists in both.
- The tray panel opens from the menu bar or system tray icon (labelled `tkt`, default on), the global shortcut, or an optional floating `tkt` button that the user can drag to any screen edge (off by default). It expands into the app window instead of opening the browser; the only exception is external links to Jira and GitHub, always marked with ↗.
- Settings (in both surfaces) write the same `config.toml` the CLI reads: accounts, spaces and repositories, panel and shortcut, sync and notifications, AI, advanced.
- Onboarding: on first run, or when a token expires, the UI asks for the Jira site and email, opens Atlassian's API-token page, validates the pasted token against Jira, and stores it in the platform's credential store without asking where. GitHub needs no step when `gh` is logged in. `tkt init` offers the same flow in the terminal. Expired or failing credentials never break the board: it keeps showing cached data, marked as stale, with a one-click renew.
- WSL: the daemon runs inside WSL next to the repositories; the tray app runs on Windows. They talk over HTTP on localhost (WSL2 forwards `localhost` to Windows by default); the tray app's origin joins the WebSocket `Origin` allowlist. No Unix sockets across the boundary.
- Inside WSL live everything else: CLI, daemon, `git`, `gh` (logged in inside WSL), `claude`, and Herdr. Herdr runs inside WSL, so the plugin targets `linux` and calls `tkt` directly.
- The tray app checks `GET /health` and starts the daemon if it does not answer. It captures the user's login-shell `PATH` first: apps launched from Finder get a minimal `PATH`, and the daemon needs `git`, `herdr`, and `claude`.
- Single-instance app with `--toggle`, for Wayland, where apps cannot register global shortcuts.

### Installation

One command installs `tkt`; the onboarding offers everything else.

| When | How |
|---|---|
| Phase 1 (single user) | `go install github.com/pavp/tkt/cmd/tkt@latest` (public repository) |
| Releases | GitHub Releases built with GoReleaser (macOS and Linux, arm64 and amd64) on every `v*` tag, and the Homebrew formula in `pavp/homebrew-tap`: `brew install pavp/tap/tkt`, which also covers WSL through Linuxbrew |
| First run | `tkt init` in the terminal, or the onboarding in the UI (same steps) |
| Daemon (phase 3) | `tkt daemon install` registers a launchd agent (macOS) or a systemd user service (Linux; on WSL systemd must be enabled in `wsl.conf`) so the daemon starts at login; the tray app also starts it when `GET /health` does not answer |
| Herdr plugin (phase 2) | `tkt herdr install` links the plugin shipped inside `tkt` |
| Claude Code (phase 4) | `tkt claude install` adds the skill and registers the MCP server (`claude mcp add tkt -- tkt mcp`) |
| Tray app (phase 5) | `.dmg` on macOS (signed and notarized, or Gatekeeper warns), an installer on Windows, AppImage on Linux |

- The onboarding ends with an **Integrations** step: it detects Herdr and Claude Code and offers to install the plugin and the MCP server, one click each. Each `install` command has a matching `uninstall`.
- `tkt doctor` checks the whole chain: binary version, configuration, credentials, `gh`, `git`, `claude`, Herdr plugin, daemon service.
- Installers, the release pipeline, and service registration change the user's environment and are hard to undo, so each lands in its own reviewed commit.

## 10. Technical notes

- Reading line-delimited JSON in Go: `bufio.Scanner` caps lines at 64 KB. Enlarge its buffer (`Scanner.Buffer`) or use `bufio.Reader`. Applies to stream-json from Claude Code and to any JSONL protocol.

## 11. Phases

Each phase starts only after explicit approval.

1. CLI: `tkt init`, `tkt auth`, `tkt list`, `tkt show`, `tkt context`, `tkt start`, `tkt link` (Jira, GitHub, worktree discovery).
2. Herdr plugin on top of the CLI.
3. Daemon with cache, sync, and local API; web UI served on localhost. The visual design starts from the UI prototype (section 14); changes to it are agreed with the user, and every screen keeps its full-window and tray-panel versions.
   - 3a (Herdr first): the daemon without the web UI, serving the Herdr plugin, the board and `tkt list` (see "Phase 3a" in section 3).
   - 3b: the web UI, SQLite cache, and the remaining routes and events.
4. Claude Code: skill first, then a read-only MCP server; writes with UI confirmation.
5. Tray app (status icon, panel, global shortcut, optional floating dot).
6. Optional AI (summaries, PR descriptions).

## 12. Open questions


## 13. UI glossary

| Concept | Term used everywhere |
|---|---|
| Items that need action | Needs you |
| Ticket with no linked worktree | action **Start** |
| Ticket with a linked worktree | action **Open agent** |
| Jira statuses | Jira's own names, verbatim (To Do, In Progress, In Review, Done) |
| PR awaiting my review | Review requested |
| Row badges | CI failing · Changes requested · Review requested · Waiting · Not started |
| Worktree source | linked by agent · discovered |

## 14. References

- gentle-dot design (https://github.com/Gentleman-Programming/gentle-dot/blob/main/docs/design.md), part of the research behind this design. Adopted from it: daemon plus thin clients over one local protocol; `127.0.0.1` bind with a 256-bit `0600` token; WebSocket `hello` handshake carrying the token with close code `4401`; `Origin` allowlist on upgrades; agent supervision with restart; a configurable agent executable so tests can use a fake agent. Not adopted: its fd-3 app channel between the desktop app and the daemon, because `tkt` must cross the WSL boundary over HTTP.
- UI prototype (Claude Design canvas, private to the owner; every screen in full-window and tray-panel versions): https://claude.ai/artifact/KCuagxsVRtNbn7umfzSmyY. It replaces the "UI arrives as a Claude Design handoff" plan for phase 3; the visual design starts from it.
- Herdr plugins: https://herdr.dev/docs/plugins/
- Herdr socket API: https://herdr.dev/docs/socket-api/
