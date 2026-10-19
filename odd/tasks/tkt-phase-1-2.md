# tkt — phases 1 and 2 (CLI + Herdr plugin)

Objective: a working `tkt` CLI (phase 1) and Herdr plugin (phase 2) per `docs/design.md`. Branch chain `feat/tkt-phase-1-2-<n>-<slug>`, delivery **Stacked** PRs to `main`, budget **2000** authored changed lines per slice (user: "2000 lineas"). Test runner: `go test ./...`.

## Specs

- S1 Scope: "ok ahora que tenemos todo el panorama para una primera fase quiero implementar solo hasta el plugin de herdr" — phases 1 and 2 of `docs/design.md` §11, nothing from phase 3+ (no daemon, no web UI).
- S2 Repository location: "me parece bien combinar las 3" — explicit `path`, else scan `repo_roots` for a clone whose remote matches, else offer `gh repo clone` (design §4).
- S3 Branches: tkt never creates branches or worktrees; "y si el agente crea todo y le pasa la info a tkt y este actualiza en su UI?" — the agent creates them and registers with `tkt link`; discovery by ticket key as fallback (design §5).
- S4 CLI: "dejemoslo como el original pero si cambia lo de el agente crea la rama y avisa" — human-oriented CLI (`init`, `auth`, `list`, `show`, `context`, `start`, `link`, `doctor`) with `--json` (design §5, §11).
- S5 Secrets: macOS Keychain, Linux Secret Service, WSL Windows Credential Manager via `powershell.exe`, `TKT_JIRA_TOKEN` fallback (design §7).
- S6 GitHub: "API directa + token de gh (Recomendado)" — `gh auth token --user <account>` per organization (design §6).
- S7 Herdr board: "si, añadelo al prototipo. Poder alternar entre las dos vistas" — `tkt board` TUI with columns and list views toggled with `v`; list fallback on narrow terminals (design §6).
- S8 Reviews: "Solo filtro (Recomendado)" — review-requested PRs never in columns; strip, "Needs you", and `r` filter.
- S9 Fix CI: "Sí, en fase 2 (Recomendado)" — `f` sends the failing check log or requested-change comments to the ticket's agent via `agent.prompt`; no Jira/GitHub writes.
- S10 Cache: "Sí (Recomendado)" — local disk cache so the board opens instantly; background refresh.
- S11 Setup from Herdr: "el plugin de herdr deberia de poder iniciar toda la configuracion?" — plugin action and first-run detection open `tkt init` in a popup; logic stays in the CLI.
- S12 Read-only: only reads against real Jira and GitHub during development; ask before any write (CLAUDE.md).
- S13 Org-agnostic: no space keys, orgs, URLs, or real data in code or tests; fixtures use `DEMO-123`, `example-org` (CLAUDE.md).

## Tasks

Forecast (items 1–23, see L5): 4,310–7,040 authored changed lines.

| ID | Slice | Specs | Forecast | Route | Branch (base) | Status | Commits |
|---|---|---|---|---|---|---|---|
| T1a | Core: paths, config + repo location, ticket domain, docs | S2 S12 S13 | measured 1,397 | inline, no Writer reason | `feat/tkt-phase-1-2-1a-core` (`main`) | done · 39 tests · RDD high (process boundary), review deferred | cd1579b 427ed55 e0e5891 |
| T1b | Credentials (incl. WSL), CLI root, `auth`, `doctor` | S4 S5 S12 | measured 1,272 | inline, no Writer reason | `feat/tkt-phase-1-2-1b-cli` (T1a) | done · 55 tests · RDD high (auth hot path), review deferred | c401219 fdfe4a7 b90fdfb |
| T2a | Data clients: Jira, GitHub | S6 S13 | measured 1,169 | inline, no Writer reason | `feat/tkt-phase-1-2-2a-clients` (T1b) | done · RDD high (auth hot path), review deferred | b7e8045 02a5aed 17f0566 |
| T2b | Cache, board assembly, `list` | S4 S10 | measured 910 | inline, no Writer reason | `feat/tkt-phase-1-2-2b-list` (T2a) | done · 84 tests · RDD medium, under budget (pending in slice) | 0bba4f9 87ec9c6 |
| T3a | ADF, `show`, `context` | S4 | measured 1,074 | inline, no Writer reason | `feat/tkt-phase-1-2-3a-show` (T2b) | done · RDD medium, under budget (pending in slice) | e88349d f3f4426 f2ce6d8 |
| T3b | `init`, discovery, `link`, `start` | S2 S3 S4 S11 | measured 1,393 | inline, no Writer reason | `feat/tkt-phase-1-2-3b-work` (T3a) | done · 41 CLI tests · RDD high (auth hot path, process boundary), review deferred | 494c074 24629af 9a4b977 |
| T4 | Herdr: client, `tkt herdr` commands, sidebar + agent state, manifest, setup action | S3 S11 | measured 1,568 | inline, no Writer reason | `feat/tkt-phase-1-2-4-herdr` (T3b) | done · RDD medium (escalate item 5: `tkt herdr install` links into the user's Herdr) | e758a7f 3a3e807 06502e6 |
| T5 | Board TUI, `f`, docs | S7 S8 S9 S10 | measured 1,490 | inline, no Writer reason | `feat/tkt-phase-1-2-5-board` (T4) | done · RDD high (process boundary in board actions), review deferred | 1fa67aa 3b76378 bb8f7f6 43ec080 |

High-risk units get their own commit: secrets (T1, item 2), new dependencies (T1, item 5), `tkt herdr install` (T4, item 5).

Native grouping: once T5's PR is open, group with `gh stack link <T1a-pr> <T1b-pr> <T2a-pr> <T2b-pr> <T3a-pr> <T3b-pr> <T4-pr> <T5-pr>`; merging any PR then takes every unmerged PR below it. Every branch must live in this one repository; after grouping, read back each PR's base and diff and report what was observed (a PR whose diff grew is mis-grouped; `gh stack unstack <stack>` dissolves the stack before any base can be corrected).

> Before each commit, run `git diff --numstat main` and sum additions + deletions, skipping binary rows (`-\t-`) and generated paths (lockfiles, `__snapshots__/`, files marked `linguist-generated` in `.gitattributes`); if the current work unit — slice or single unit — is over 2000 real changed lines, stop: hand the accumulated diff to `slice-diff` (budget 2000), then re-forecast every slice not yet started with `slice-plan` (budget 2000) — a slice that overran invalidates the estimates that assumed its size, not just its own. Slices already merged stay as they are.

Per-slice base for the checkpoint: each slice's base is the previous slice's branch (T1a: `main`).

## Log

- L1 (2026-10-19) User's original request: the pasted tkt brief (phases, decisions, security, Herdr plugin, LLM integration) — see `docs/design.md`, which records it and every later decision.
- L2 (2026-10-19) "ok ahora que tenemos todo el panorama para una primera fase quiero implementar solo hasta el plugin de herdr, que necesitariamos?"
- L3 (2026-10-19) Decisions: reviews "Solo filtro (Recomendado)"; `f` "Sí, en fase 2 (Recomendado)"; cache "Sí (Recomendado)"; budget "2000 lineas"; repo location "me parece bien combinar las 3"; setup from Herdr asked and recommended (plugin opens `tkt init`).
- L4 (2026-10-19) Delivery: "ok vamos con stacked".
- L5 (2026-10-19) Forecast items: 1 skeleton 60–120 · 2 config + repo location 260–400 · 3 secrets + auth 100–190 · 4 WSL backend 100–180 · 5 domain 200–320 · 6 Jira client 340–540 · 7 ADF 300–460 · 8 GitHub client 360–560 · 9 cache 150–250 · 10 list 180–300 · 11 show 140–250 · 12 context 140–220 · 13 init 220–380 · 14 discovery + link 220–340 · 15 start 160–260 · 16 doctor 80–150 · 17 Herdr client 120–200 · 18 `tkt herdr` 280–440 · 19 sidebar + agent 150–250 · 20 board TUI 450–700 · 21 `f` 160–260 · 22 manifest 80–150 · 23 docs 60–120.
- L6 Next: T1.
- L7 (2026-10-19) Checkpoint fired: slice 1 measured 2,621 authored changed lines against the 2,000 budget (forecast 800–1,360; tests run about as long as the code, so estimates were roughly half). Split into T1a/T1b along the core vs. credentials-and-CLI boundary; every slice not yet started re-forecast at about 1.9× and cut so each stays within 2,000. Still Stacked and linear. A user permission rule denies Read/Edit on `**/secrets/*`, so the credential package is `internal/credstore`.
- L8 (2026-10-19) All slices committed locally on the stacked chain; nothing pushed. Authored changed lines per slice: 1,397 · 1,272 · 1,169 · 910 · 1,074 · 1,393 · 1,568 · 1,490 (10,273 total). Herdr API details taken from `herdr api schema --json` of Herdr 0.9.5 (socket protocol 22). RDD: high on T1a, T1b, T2a, T3b, T5 and T4 to be escalated (item 5); reviews deferred to the end at the user's request. Next: one independent verifier over the whole feature, then the user decides on reviews, push and PRs.
- L9 (2026-10-19) Independent verifier: S1–S3 and S5–S13 met; one blocker on S4 (`start` and `init` silently ignored `--json`). One correction batch: both now refuse `--json` (dec6f7b, regression test `TestInteractiveCommandsRejectJSON`), plus advisories fixed alongside (go.mod tidy b97022f, `init` names TKT_JIRA_TOKEN, TOML-safe manifest quoting). Recheck of the blocker: passes. Open advisories: `herdr install` treats any "already" link error as success; a Jira 403 during `auth jira` saves the token with a warning; the repo scan is not cached; `board --view` accepts unknown values. Suite: 14 packages ok. Nothing pushed; RDD reviews and push/PR are the user's decision.
- L10 (2026-10-19) Local-test follow-ups on the board branch. User: "y la info de los pr no lo puedes sacar de jira?" → "si implementalo". Each ticket's PRs now come from Jira's development panel (undocumented `/rest/dev-status/latest/issue/{summary,detail}`; the applicationType is the integration's instance type, e.g. `oAuth-com.github.integration.production`, discovered once from the summary). GitHub's copy wins for checks and review state; the key search runs only for tickets whose panel lookup failed. Verified live: DEMO-156522 shows its 4 merged PRs. Commit ac34fbd · inline, no Writer reason · Risk: none.
- L11 (2026-10-19) User: "en el board la primera fila aparece pegada a la izquierda pero a partir de la segunda tiene un espacio a la izquierda. comparalo con el diseno". The prototype pads every section of the popup equally; the board now renders inside a one-column margin on both sides (header included), with clicks shifted to match. RED `TestEveryRowSharesOneLeftMargin` → GREEN. Commit b43fbb1 · inline, no Writer reason · Risk: none.
- L12 (2026-10-19) User (screenshot of a card): "la parte de DEMO sale seleccionada con un icono pero la siguiente linea tiene un espacio a la izquierda". Prototype cards show `▸ KEY` with the title flush under the mark; cards now share a one-column padding and drop the two-column indent (RED `TestCardLinesStartAtTheSameColumn`, dde183b). User: "en el diseno aparece el menu arriba a la derecha porque aca esta a la izqueirda en otra fila?" Filters, views and the sync state now sit right of the title on the first row, wrapping to a second row only when the pane is too narrow; header clicks follow the layout (RED `TestHeaderControlsShareTheTitleRow`). Inline, no Writer reason · Risk: none.
