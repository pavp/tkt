# tkt

Multi-project ticket manager for agent-driven development.

`tkt` lists your sprint tickets across Jira spaces, starts work on them (opens an agent that creates the branch and worktree), and shows the status of each ticket, including its pull requests and CI, on a single board.

Status: early development. Design: [`docs/design.md`](docs/design.md).

## Install

```bash
GOPRIVATE=github.com/pavp/* go install github.com/pavp/tkt/cmd/tkt@latest
```

## Set up

1. Copy [`docs/config.example.toml`](docs/config.example.toml) to `~/.config/tkt/config.toml` and fill in your Jira site, email, spaces and repositories.
2. Store your Jira API token (create one at https://id.atlassian.com/manage-profile/security/api-tokens):

   ```bash
   tkt auth jira
   ```

   It goes to macOS Keychain, the Linux Secret Service, or Windows Credential Manager under WSL. `TKT_JIRA_TOKEN` overrides it for tests and headless use.
3. Make sure `gh auth login` covers every GitHub organization in the config.
4. Check everything:

   ```bash
   tkt doctor
   ```

## Commands

| Command | What it does |
|---|---|
| `tkt auth jira [--site S --email E]` | Store the Jira API token (hidden prompt, or stdin when piped) |
| `tkt auth status` | Show which credentials tkt can use, without printing them |
| `tkt doctor` | Check config, credentials, tools (`git`, `gh`, `claude`, `herdr`) and local clones |

Every command accepts `--json` and `--config <path>`.

## Develop

```bash
go build -o tkt ./cmd/tkt
go test ./...
```
