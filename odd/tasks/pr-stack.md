# tkt: pull request stacks

Objective: show a ticket's pull requests as stacks (a PR whose base is another PR's branch sits on it) in the ticket's details and in the PRs view, with an action that copies every URL of the stack. Delivery **Stacked** PRs to `main` (budget 400, default); `gh stack` installed. Test runner: `go test ./...` and `node --test 'internal/herdrmod/test/*.test.js'`.
Forecast (slice-plan, 2026-11-24): total 410–715 authored changed lines; slice 1 180–300, slice 2 140–240, slice 3 95–175.

## Specs

- S1. "me gustaria pintar el stack de las PR" + "si al mismo nivel pero unidas por una linea que diga el stack como en la foto que te he pasado" → a stack (pull requests of one repository where one's base is another's branch) shows as one group: a title line "Stack of N · <the branches' common prefix> · <the stack's worst status> ▾" that folds it, then its pull requests at the same level, in merge order (the bottom of the stack first), each on two lines: "#922  <title>  14h" and "<status>  after #921  ✓". Base and age come from GitHub's `baseRefName` and `updatedAt` in the queries the sync already makes; no new API calls ("pero pide a nivel de rendimiento en las llamadas al api, velocidad").
- S2. "ademas agregarle una opcion para copiar directamente todas las url del stack" → "Copy the stack's N URLs" copies the stack's URLs one per line in merge order (bottom first), stacks apart by a blank line.
- S3. "vamos con ambas" → both places: the ticket's details (S1, S2) and the PRs view (S1, S2).
- S4. Assumption (stated to the user, plan confirmed): the TUI gets the same as the mod (slice 3), since the mod and the TUI keep parity.
- S5. Assumption (stated to the user, plan confirmed): in the PRs view a stack is built inside each section; a pull request whose parent is not shown says "on `<base branch>`" instead of silently cutting the chain. Design approved in chat before slice 2, in full window and tray panel.

## Tasks

| ID | Specs | Slice | Route | Status | Commit |
|---|---|---|---|---|---|
| T1 | S1 (base and age from GitHub, stack order in `ticket`) | 1 | inline, no Writer reason | done (RED: did not compile → GREEN, twice) | 9e6ad4c, slice 1 rework |
| T2 | S2 (details: stacks in merge order, Copy the stack's N URLs) | 1 | inline, no Writer reason | done (RED: 1 Go + 3 node tests → GREEN); slice 1 = 319 changed lines | 9e6ad4c, slice 1 rework |
| T3 | S1 (stack group in the mod's details: board stacks, daemon shape, card) | 2 | inline, no Writer reason | done (RED: Go build + 5 node tests → GREEN); slice 2 = 378 changed lines | slice 2 commit |
| T4 | S1, S2, S3, S5 (PRs view in the mod) | 3 | inline, no Writer reason | pending | |
| T5 | S1, S2, S4 (TUI details and PRs tab) | 4 | inline, no Writer reason | pending | |

Re-forecast (slice-plan, 2026-11-24, after L7): total ~800–1160.

| Slice | Branch | Base | Est. lines | Depends on |
|---|---|---|---|---|
| 1 | `feat/pr-stack-1-detail` | `main` | measured 319 | — |
| 2 | `feat/pr-stack-2-detail-card` | `feat/pr-stack-1-detail` | 205–340 | 1 |
| 3 | `feat/pr-stack-3-pr-view` | `feat/pr-stack-2-detail-card` | 220–340 | 2 |
| 4 | `feat/pr-stack-4-tui` | `feat/pr-stack-3-pr-view` | 120–220 | 3 |

Native grouping: once the last slice's PR is open, group with `gh stack link <pr-slice-1> <pr-slice-2> <pr-slice-3> <pr-slice-4>`; merging any PR then takes every unmerged PR below it. Every branch must live in ONE repository, and the implementer reads back each PR's base and diff and reports that, never the attempt — a PR whose diff grew is mis-grouped, and `gh stack unstack <stack>` must dissolve the stack before any base can be corrected.

> Before each commit, run `git diff --numstat <base>` and sum additions + deletions, skipping binary rows (`-\t-`) and generated paths (lockfiles, `__snapshots__/`, files marked `linguist-generated` in `.gitattributes`); if the current work unit — slice or single unit — is over 400 real changed lines, stop: hand the accumulated diff to `slice-diff` (budget 400), then re-forecast every slice not yet started with `slice-plan` (budget 400) — a slice that overran invalidates the estimates that assumed its size, not just its own. Slices already merged stay as they are.
> Slice 1 base: `main`. Slice 2 base: `feat/pr-stack-1-detail`. Slice 3 base: `feat/pr-stack-2-detail-card`. Slice 4 base: `feat/pr-stack-3-pr-view`.

## Log

- L1 (2026-11-24, user): "me gustaria pintar el stack de las PR y ademas agregarle una opcion para copiar directamente todas las url del stack"
- L2 (2026-11-24, user): "analiza primero el costo de ambas" → detail ~100–150 + base 100–150; PRs view ~200–300 more.
- L3 (2026-11-24, user): "pero pide a nivel de rendimiento en las llamadas al api, velocidad" → `baseRefName` is a scalar in the existing queries (client.go:278-279, 338, 375): no new calls, no rate-limit cost, ~2 KB more per repository; stacking is in memory.
- L4 (2026-11-24, user): "vamos con ambas" → slice plan of 3 slices. User: "Stacked, adelante".
- L5 (2026-11-24) Slice 1: `go test ./...` 1550 passed, node 188 passed, `go vet` and `gofmt -l` clean. Not yet observed on the running mod: rendering against the canvas and the blind walk run before the PRs open.
- L6 (2026-11-24, user): "no entiendo muy bien, por ejemplo 102 y 103 son pr del stack con 101? no deberiane star al mismo nivel?" → the staircase indent is dropped.
- L7 (2026-11-24, user, with a screenshot): "algo asi me imaginaba" — "Stack of 3 · refactor/no-ticket-module-i… · Ready ▾", rows "#922 refactor: … 14h" / "Ready to merge  after #921  ✓", top first. Then: "si al mismo nivel pero unidas por una linea que diga el stack como en la foto que te he pasado". S1, S2 rewritten; T2 reopened (the ↳ rows leave slice 1); re-forecast below.
- L8 (2026-11-24, user): "el orden me gusta mas el que propones" → rows in merge order, bottom of the stack first (#920, #921, #922), as the copy.
- L9 (2026-11-24, user): "si, adelante" → 4 slices confirmed, Stacked. Slice 1 rework: `updatedAt`, staircase rows removed; `go test ./...` 1550, node 188, vet and gofmt clean.
- L10 (2026-11-24) Slice 2: `go test ./...` 1554, node 191, vet and gofmt clean. Not yet observed in the running mod (render against the canvas and the screenshot) nor walked blind.
