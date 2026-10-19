# tkt

Multi-project ticket manager for agent-driven development.

`tkt` lists your sprint tickets across Jira spaces, starts work on them (opens an agent that creates the branch and worktree), and shows the status of each ticket, including its pull requests and CI, on a single board.

Status: early development. Design: [`docs/design.md`](docs/design.md).

## Install

```bash
GOPRIVATE=github.com/pavp/* go install github.com/pavp/tkt/cmd/tkt@latest
```

## Set up

1. Run the setup. It asks for your Jira site and email, stores and checks the API token (create one at https://id.atlassian.com/manage-profile/security/api-tokens), reads your spaces from Jira, and writes `~/.config/tkt/config.toml`:

   ```bash
   tkt init
   ```

   The token goes to macOS Keychain, the Linux Secret Service, or Windows Credential Manager under WSL. `TKT_JIRA_TOKEN` overrides it for tests and headless use. You can also write the config by hand from [`docs/config.example.toml`](docs/config.example.toml) and store the token with `tkt auth jira`.
2. Make sure `gh auth login` covers every GitHub organization in the config.
3. Check everything:

   ```bash
   tkt doctor
   ```

## Commands

| Command | What it does |
|---|---|
| `tkt init` | Interactive setup: Jira account and token, spaces, repositories, GitHub accounts |
| `tkt auth jira [--site S --email E]` | Store the Jira API token (hidden prompt, or stdin when piped) |
| `tkt auth status` | Show which credentials tkt can use, without printing them |
| `tkt doctor` | Check config, credentials, tools (`git`, `gh`, `claude`, `herdr`) and local clones |
| `tkt list [--space S] [--needs-you] [--cached]` | Your open-sprint tickets with PRs and CI; failing CI, requested changes and review requests first. Falls back to the cached board when offline |
| `tkt show [KEY] [--watch]` | One ticket: PRs and CI, description, acceptance criteria, attachments, latest comments. Without `KEY`, uses the current branch |
| `tkt context [KEY] [--images DIR]` | The ticket as Markdown for an agent (`> .ticket.md`); `--images` downloads image attachments |
| `tkt start KEY [--repo R] [--clone]` | Open Claude Code on a ticket: in its worktree if it has one, otherwise in the chosen repositories with instructions to create the branch per project rules |
| `tkt link [KEY]` | Register the current worktree as the work on a ticket; the branch must contain the key |

Every command accepts `--json` and `--config <path>`.

## Develop

```bash
go build -o tkt ./cmd/tkt
go test ./...
```
