# tkt: pull request stacks

Objective: show a ticket's pull requests as stacks (a PR whose base is another PR's branch sits on it) in the ticket's details and in the PRs view, with an action that copies every URL of the stack. Delivery **Stacked** PRs to `main` (budget 400, default); `gh stack` installed. Test runner: `go test ./...` and `node --test 'internal/herdrmod/test/*.test.js'`.
Forecast (slice-plan, 2026-11-24): total 410–715 authored changed lines; slice 1 180–300, slice 2 140–240, slice 3 95–175.

## Specs

- S1. "me gustaria pintar el stack de las PR" → a pull request whose base branch is the head branch of another pull request of the same repository is shown on it, indented under it, ordered from the bottom of the stack to the top. Base comes from GitHub's `baseRefName` in the queries the sync already makes; no new API calls ("pero pide a nivel de rendimiento en las llamadas al api, velocidad").
- S2. "ademas agregarle una opcion para copiar directamente todas las url del stack" → an action copies the URLs of the stack's pull requests, one per line, from bottom to top.
- S3. "vamos con ambas" → both places: the ticket's details (S1, S2) and the PRs view (S1, S2).
- S4. Assumption (stated to the user, plan confirmed): the TUI gets the same as the mod (slice 3), since the mod and the TUI keep parity.
- S5. Assumption (stated to the user, plan confirmed): in the PRs view a stack is built inside each section; a pull request whose parent is not shown says "on `<base branch>`" instead of silently cutting the chain. Design approved in chat before slice 2, in full window and tray panel.

## Tasks

| ID | Specs | Slice | Route | Status | Commit |
|---|---|---|---|---|---|
| T1 | S1 (base from GitHub, stack order in `ticket`) | 1 | inline, no Writer reason | done (RED: did not compile → GREEN) | slice 1 commit |
| T2 | S1, S2 (details in the mod, daemon `pr_stacks`) | 1 | inline, no Writer reason | done (RED: 1 Go + 3 node tests → GREEN); slice 1 = 241 changed lines | slice 1 commit |
| T3 | S1, S2, S3, S5 (PRs view: board, daemon, mod) | 2 | inline, no Writer reason | pending | |
| T4 | S1, S2, S4 (TUI details and PRs tab) | 3 | inline, no Writer reason | pending | |

| Slice | Branch | Base | Est. lines | Depends on |
|---|---|---|---|---|
| 1 | `feat/pr-stack-1-detail` | `main` | 180–300 | — |
| 2 | `feat/pr-stack-2-pr-view` | `feat/pr-stack-1-detail` | 140–240 | 1 |
| 3 | `feat/pr-stack-3-tui` | `feat/pr-stack-2-pr-view` | 95–175 | 2 |

Native grouping: once the last slice's PR is open, group with `gh stack link <pr-slice-1> <pr-slice-2> <pr-slice-3>`; merging any PR then takes every unmerged PR below it. Every branch must live in ONE repository, and the implementer reads back each PR's base and diff and reports that, never the attempt — a PR whose diff grew is mis-grouped, and `gh stack unstack <stack>` must dissolve the stack before any base can be corrected.

> Before each commit, run `git diff --numstat <base>` and sum additions + deletions, skipping binary rows (`-\t-`) and generated paths (lockfiles, `__snapshots__/`, files marked `linguist-generated` in `.gitattributes`); if the current work unit — slice or single unit — is over 400 real changed lines, stop: hand the accumulated diff to `slice-diff` (budget 400), then re-forecast every slice not yet started with `slice-plan` (budget 400) — a slice that overran invalidates the estimates that assumed its size, not just its own. Slices already merged stay as they are.
> Slice 1 base: `main`. Slice 2 base: `feat/pr-stack-1-detail`. Slice 3 base: `feat/pr-stack-2-pr-view`.

## Log

- L1 (2026-11-24, user): "me gustaria pintar el stack de las PR y ademas agregarle una opcion para copiar directamente todas las url del stack"
- L2 (2026-11-24, user): "analiza primero el costo de ambas" → detail ~100–150 + base 100–150; PRs view ~200–300 more.
- L3 (2026-11-24, user): "pero pide a nivel de rendimiento en las llamadas al api, velocidad" → `baseRefName` is a scalar in the existing queries (client.go:278-279, 338, 375): no new calls, no rate-limit cost, ~2 KB more per repository; stacking is in memory.
- L4 (2026-11-24, user): "vamos con ambas" → slice plan of 3 slices. User: "Stacked, adelante".
- L5 (2026-11-24) Slice 1: `go test ./...` 1550 passed, node 188 passed, `go vet` and `gofmt -l` clean. Not yet observed on the running mod: rendering against the canvas and the blind walk run before the PRs open.
