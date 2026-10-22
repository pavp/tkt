# CLAUDE.md — tkt

Multi-project ticket manager (Jira + GitHub) for agent-driven development. Single Go binary with three roles: CLI/TUI, daemon, MCP server. Full design: `docs/design.md` — read it before changing architecture, the daemon API, or security behavior.

## Commands

```bash
go build -o tkt ./cmd/tkt   # build
go test ./...               # all tests
go test -race ./...         # before committing concurrency changes
go vet ./...                # static checks
gofmt -l .                  # must print nothing
```

UI (phase 3 onward) lives in `web/` and is embedded with `go:embed`; its commands are added here when it exists.

## Conventions

- Go module: `github.com/pavp/tkt`. Entry point in `cmd/tkt/`; packages under `internal/`.
- Domain code (`internal/ticket`) has no I/O and no dependency on adapters. Adapters (Jira, GitHub, git, Herdr) implement ports declared by the code that uses them.
- `tkt` is the only component that talks to Jira and GitHub. Plugins, UI, tray app, and agents are thin clients.
- `tkt` never creates branches or worktrees; the agent does, per project rules, and registers them with `tkt link`. The one invariant: branch names contain the ticket key (e.g. `feature/DEMO-123-slug`); `tkt link` rejects names without it.
- `.ticket.md` is generated per worktree and is gitignored; never commit it.
- Daemon API changes: additive within `/v1`; breaking changes need a new version and an update to `docs/design.md` section 3.
- Reading JSONL: never use a default `bufio.Scanner` (64 KB line cap). Set `Scanner.Buffer` or use `bufio.Reader`.
- The core is deterministic. LLM features are optional and must degrade gracefully when disabled or failing.
- Tests: table-driven, through public interfaces. Jira and GitHub are faked with `httptest` servers or recorded fixtures; tests never call real services.
- Organization-agnostic: no Jira space key, GitHub org, site URL, or real company data in code or tests; those live only in the user's configuration. Ticket keys are detected with a generic pattern (`[A-Z][A-Z0-9]+-[0-9]+`). Fixtures use fictitious data (`DEMO-123`, `example-org`, `example.atlassian.net`); never commit recorded responses from a real Jira or GitHub without anonymizing them.
- Commits: Conventional Commits, no AI attribution.
- Releases: Semantic Versioning from the commits since the last tag (`feat` → minor, only `fix`/`perf` → patch, breaking → minor while `0.y.z`), always an annotated tag on `main` after the merge; see README › Releasing.
- Comments: none by default; only a non-obvious why.

## Security rules

- Never write, log, print, or commit Jira or GitHub tokens. Secrets come only from the OS credential store (macOS Keychain, Linux Secret Service, WSL: Windows Credential Manager via `powershell.exe`) or the documented `TKT_JIRA_TOKEN` fallback. Never ask the user to paste a token into chat.
- The daemon binds `127.0.0.1` only, authenticates with the 256-bit token in a `0600` file, and checks `Origin` plus the `hello` token on WebSocket.
- Config dir `~/.config/tkt/`; data dir is created with mode `0700`, token file `0600`.
- Mutating actions (Jira transitions, comments, merges) require confirmation in the UI, never inside the agent or MCP alone.
- During development, only read operations against real Jira and GitHub. Ask before any write against real services.

## Workflow

- Work phase by phase (`docs/design.md` section 11). Do not start a new phase without explicit approval.
- The visual UI follows the prototype linked in `docs/design.md` section 14. Do not invent screens outside it without agreement, and design every screen in both versions: full window and tray panel.
