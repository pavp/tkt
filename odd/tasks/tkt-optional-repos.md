# tkt: optional repositories and board settings

Objective: repositories become optional (inferred from Jira-linked PRs), GitHub accounts are detected, and the board gets a Settings view for repositories and local paths. Branches: `feat/tkt-optional-repos-1-repos-inferred` → `main`, `feat/tkt-optional-repos-2-settings` → slice 1 (Stacked). Test runner: `go test ./...`.
Forecast: 1210–2040 authored changed lines against the user's 2000-line budget → two slices (660–1150, 550–890).

## Specs

- S1. Repositories are inferred per space from the pull requests Jira links to the user's tickets, and used for CI, review requests and the GitHub account; agreed: "la otra PC funcionaría sin tocar la configuración: los PRs de tus tickets ya apuntan a `nxl-engineering/cloud-uac-accounts-web`".
- S2. The repository list in `config.toml` stays as an optional override (a repo with no PRs yet, a base branch); configured and inferred repositories are merged.
- S3. The `gh` account for each organization is detected by trying every logged-in account and keeping the one that can read it; it asks only when several qualify. A configured `[github.accounts]` entry wins.
- S4. When no account can read an organization, or `gh` has no session, `tkt init` offers `gh auth login`.
- S5. `tkt list`, the board and `tkt doctor` warn when a space has no repositories, configured or inferred, instead of staying silent.
- S6. `tkt start` derives the repository from the ticket's pull requests when its space has none; agreed: "En un espacio nuevo, sin ningún PR todavía ... `tkt start` preguntaría el repo la primera vez".
- S7. "agrega uno settings para poder setear los repos desde el board": a Settings view in the board edits each space's repositories (add, remove, base branch).
- S8. "al igual que la parte de los path locales": the same Settings view edits the local clone paths (`repo_roots` and each repository's `path`).

## Tasks

| ID | Specs | Slice | Route | Status | Commit |
|---|---|---|---|---|---|
| T1 | S1 S2 | 1 | inline, no Writer reason | pending | |
| T2 | S3 | 1 | inline, no Writer reason · Risk item 2 (credentials), own commit | pending | |
| T3 | S4 | 1 | inline, no Writer reason | pending | |
| T4 | S5 | 1 | inline, no Writer reason | pending | |
| T5 | S6 | 1 | inline, no Writer reason | pending | |
| T6 | S7 S8 | 2 | inline, no Writer reason · checks the prototype's Settings screen first | pending | |
| T7 | S7 S8 | 2 | inline, no Writer reason · Risk item 1 (writes the user's config), own commit | pending | |

## Log

- L1 (2026-10-19) User: "si hazlo así, ademas agrega uno settings para poder setear los repos desde el board, al igual que la parte de los path locales" — accepting the proposal: optional repositories inferred from Jira-linked PRs, gh account detection, `gh auth login` offer, warnings, `start` deriving the repo.
- L2 (2026-10-19) Trigger: on the second PC (WSL) `tkt init` left ACTO without repositories, so no GitHub calls ran and the 8 review requests were missing; the GitHub step was skipped entirely because it iterates organizations of configured repositories.
- L3 (2026-10-19) Slice plan confirmed, delivery "ok stacked". Native grouping once both PRs are open: `gh stack link <pr-1> <pr-2>`.
- L4 (2026-10-19) Engram mirror pending: `mem_save` rejected project "tkt" as unknown (mem_current_project resolves it from the git remote, but no session backs it yet); auto-detect would file it under another project. Resync when available.
