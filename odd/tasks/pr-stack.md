# tkt: pull request stacks

Objective: show a ticket's pull requests as stacks (a PR whose base is another PR's branch sits on it) in the ticket's details and in the PRs view, with an action that copies every URL of the stack. Delivery **Stacked** PRs to `main` (budget 400, default); `gh stack` installed. Test runner: `go test ./...` and `node --test 'internal/herdrmod/test/*.test.js'`.
Forecast (slice-plan, 2026-11-24): total 410–715 authored changed lines; slice 1 180–300, slice 2 140–240, slice 3 95–175.

## Specs

- S1. "me gustaria pintar el stack de las PR" + "si al mismo nivel pero unidas por una linea que diga el stack como en la foto que te he pasado" → a stack (pull requests of one repository where one's base is another's branch) shows as one group: a title line "Stack of N · <the branches' common prefix> · <the stack's worst status> ▾" that folds it, then its pull requests at the same level, in merge order (the bottom of the stack first), each on two lines: "#922  <title>  14h" and "<status>  after #921  ✓". Base and age come from GitHub's `baseRefName` and `updatedAt` in the queries the sync already makes; no new API calls ("pero pide a nivel de rendimiento en las llamadas al api, velocidad").
- S2. "ademas agregarle una opcion para copiar directamente todas las url del stack" → "Copy the stack's N URLs" copies the stack's URLs one per line in merge order (bottom first), stacks apart by a blank line.
- S3. "vamos con ambas" → both places: the ticket's details (S1, S2) and the PRs view (S1, S2).
- S4. Assumption (stated to the user, plan confirmed): the TUI gets the same as the mod (slice 3), since the mod and the TUI keep parity.
- S6. "si, la opción 2" → the details build each stack from every pull request the daemon's last sync knows (the user's tickets', review requests, everyone else's), keeping the stacks that hold one of the ticket's pull requests; a pull request of another ticket names that ticket's key. No extra API call. A pull request the sync never listed stays out.
- S5. Assumption (stated to the user, plan confirmed): in the PRs view a stack is built inside each section; a pull request whose parent is not shown says "on `<base branch>`" instead of silently cutting the chain. Design approved in chat before slice 2, in full window and tray panel.

## Tasks

| ID | Specs | Slice | Route | Status | Commit |
|---|---|---|---|---|---|
| T1 | S1 (base and age from GitHub, stack order in `ticket`) | 1 | inline, no Writer reason | done (RED: did not compile → GREEN, twice) | 9e6ad4c, 8d057be |
| T2 | S2 (details: stacks in merge order, Copy the stack's N URLs) | 1 | inline, no Writer reason | done (RED: 1 Go + 3 node tests → GREEN); slice 1 = 319 changed lines | 9e6ad4c, 8d057be |
| T3 | S1 (stack group in the mod's details: board stacks, daemon shape, card) | 2 | inline, no Writer reason | done (RED: Go build + 5 node tests → GREEN); slice 2 = 378 changed lines | 29d6fcf |
| T6 | S6 (whole stacks across tickets in the details) | 3 | inline, no Writer reason | done (RED: Go build + daemon/cli/board assertions, 1 node test → GREEN) | 37b0073, fix in slice 3 |
| T4 | S1, S2, S3, S5 (PRs view in the mod) | 4 | inline, no Writer reason | done (RED: board build, contract fixtures missing, 3+1 node tests → GREEN); slice 4 = 235 changed lines | 949fde5, fix in slice 4 |
| T5 | S1, S2, S4 (TUI details and PRs tab) | 5 | inline, no Writer reason | done (RED: TUI view, context line and menus, show output → GREEN); slice 5 = 330 changed lines | 6c5ef48 |

Re-forecast (slice-plan, 2026-11-24, after L7): total ~800–1160.

| Slice | Branch | Base | Est. lines | Depends on |
|---|---|---|---|---|
| 1 | `feat/pr-stack-1-detail` | `main` | measured 319 | — |
| 2 | `feat/pr-stack-2-detail-card` | `feat/pr-stack-1-detail` | 205–340 | 1 |
| 3 | `feat/pr-stack-3-whole-stack` | `feat/pr-stack-2-detail-card` | 60–100 | 2 |
| 4 | `feat/pr-stack-4-pr-view` | `feat/pr-stack-3-whole-stack` | 220–340 | 3 |
| 5 | `feat/pr-stack-5-tui` | `feat/pr-stack-4-pr-view` | 120–220 | 4 |

Native grouping: once the last slice's PR is open, group with `gh stack link <pr-slice-1> <pr-slice-2> <pr-slice-3> <pr-slice-4> <pr-slice-5>`; merging any PR then takes every unmerged PR below it. Every branch must live in ONE repository, and the implementer reads back each PR's base and diff and reports that, never the attempt — a PR whose diff grew is mis-grouped, and `gh stack unstack <stack>` must dissolve the stack before any base can be corrected.

> Before each commit, run `git diff --numstat <base>` and sum additions + deletions, skipping binary rows (`-\t-`) and generated paths (lockfiles, `__snapshots__/`, files marked `linguist-generated` in `.gitattributes`); if the current work unit — slice or single unit — is over 400 real changed lines, stop: hand the accumulated diff to `slice-diff` (budget 400), then re-forecast every slice not yet started with `slice-plan` (budget 400) — a slice that overran invalidates the estimates that assumed its size, not just its own. Slices already merged stay as they are.
> Slice 1 base: `main`. Slice 2 base: `feat/pr-stack-1-detail`. Slice 3 base: `feat/pr-stack-2-detail-card`. Slice 4 base: `feat/pr-stack-3-whole-stack`. Slice 5 base: `feat/pr-stack-4-pr-view`.

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
- L11 (2026-11-24, side note relayed by the user): a ticket's details stack only that ticket's pull requests, so a stack split across tickets shows in part and "Copy the stack's N URLs" copies that part silently. Options given: warn only, or build the whole stack from every pull request the sync knows. User: "si, la opción 2" → new slice 3 `feat/pr-stack-3-whole-stack` (60–100 lines); the PRs view becomes slice 4 and the TUI slice 5.
- L12 (2026-11-24, user): "cuando termines abres el mod para yo ver el resultado" → install the branch build and open the mod once slice 3 is committed.
- L13 (2026-11-24) Slice 3: 191 changed lines (forecast 60–100, under the 400 budget). Live check on the branch build (read-only): ACTO-175968 gives one stack #284 → #285 → #287 with bases trunk → slice 1 → slice 2; its branches mix chore/ and docs/, which named the stack after the bottom's branch, so the name now takes what they share after their kind ("acto-175968-calver"), with a RED test. Branch daemon running on this computer and the branch mod installed (backup in the session scratchpad); the mod was on machine wsl-dev, whose released tkt sends no pr_stacks.
- L14 (2026-11-24, user, screenshot of the details): "lo veo bien" → slice 2/3 details approved on the running mod (branch daemon built as 0.35.99 so Homebrew's tkt does not replace it). Starting slice 4.
- L15 (2026-11-24, side note relayed by the user): in the PRs view a stack built from one group's rows shows cut, and S5's "on <base>" was missing. Done within S5 and S6: rows stay in their group, but the stack's name, status and copied URLs come from the whole stack the sync knows; the title says "N here" when the group holds part of it, and the first row shown says "on <base branch>" when the one it sits on is elsewhere.
- L16 (2026-11-24) Live on the branch build: the PRs tab gives #920 → #921 → #922 and #284 → #285 → #287 as stacks of 3; a team ticket's pull requests were counted twice ("3 of 5") since they are also among everyone else's, fixed with a RED test. `go test ./...` and node 195 green.
- L17 (2026-11-24, user, screenshot of the PRs tab): "no me encanta esas lineas verticales a la izquierda, tmbien veo que queda mas a la derecha el titulo del stack /impeccable como mejorarlo?" → proposed: no rail, the stack title on the table's columns (status under STATUS), "after #N" at the row's end. User: "si ademas tambien le falta un boton directo para copiar?" → the virtual list takes one click per row (gpui-kit.d.ts:851), so on the PRs tab `P` on a selected stack title copies its URLs and the title shows that key; the details' stack title gets a real copy button.
- L18 (2026-11-24, user): "sigue pareciendo un poco raro que esten al mismo nivel el titulo del stack y las prs no? /impeccable como mejorarias este tab de PR? para que funcione con stacks y sin stacks" → proposed a stack as one tinted, bordered block (caption row, rows on the table's columns, lone parts "⧉ 1 of N"); user: "si". Rendered on the running mod: both real stacks show as blocks, plain rows unchanged.
- L19 (2026-11-24, user): "no me gusta que el ready to merge (Status) del stack aparezca a la derecha de todo" → status right after the caption. Then: "me refiero al numero de stack tambien en la cabecera de cad astack , por ejemplo stack of 3 / peroo me gusta lo de stacks en la linea de resumen tambien / el after dale una vuelta mejor /impeccable" → "Stack of N" caption, "N stacks" in the context line, and "after #N" replaced by a step number (whole stack's merge order, shared by siblings) before the title; user: "si". Rendered on the running mod.
- L20 (2026-11-24, user): "me gusta" → PRs tab approved. Slice 5 (TUI): whole stacks moved to `board.Snapshot.WholeStacks`, shared by the daemon and the TUI; the TUI's PRs filter shows a stack caption (fold with enter, P copies), step numbers, `on <branch>` and "N stacks" in its context line; ticket and pull request menus add "Copy the stack's N URLs"; `tkt show` and the TUI's details list a stack of several under "Stack of N · name · status", numbered (the daemon's `pr_stacks` when it answers, else the ticket's own). `go test ./...` 1563, node 197, vet and gofmt clean. Not seen on a running TUI.
