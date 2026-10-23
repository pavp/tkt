# tkt: a backlog kept fresh and the board in a pane

Objective: the Backlog opens at once from a copy the daemon refreshes every five minutes, and the board can live in a Herdr pane that stays open. Delivery **Stacked** (budget 400, default): `feat/backlog-autosync` on `main`, then `feat/board-pane` on it; both published when ready ("cuando esten listo subes y publicas"). Test runner: `go test ./...`.
Forecast (slice-plan, 2026-10-23): 370–640 authored changed lines; slice 1 240–400, slice 2 130–240.

## Specs

- S1. "el costo de llamada y tiempo de backlog (a ver si lo podemos automatizar como el resto o no)" → measured: 1.3–1.7 s and 8–9 Jira calls per load against the sprint sync's 1.8 s and 13 calls a minute; the daemon refreshes the backlog every five minutes when team boards exist and keeps a copy on disk; the Backlog tab opens from that copy, fetches only when it is older than that, refreshes itself while open, and `g` forces a fetch.
- S2. "como abrir tkt en un pane (un atajo mas serviria tambien)" → a "tkt: board in a pane" action opens the board in a split of the current workspace, bound by `tkt herdr install` to `prefix+alt+t` (free in Herdr's defaults); a board in a pane stays open when it hands work to an agent, saying so instead of closing.
- S3. "si, hazlos como dos slices. cuando esten listo subes y publicas" → two slices, then pushed, merged and released without asking again.

## Tasks

| ID | Specs | Slice | Route | Status | Commit |
|---|---|---|---|---|---|
| T1 | S1 | 1 | inline, no Writer reason | done (RED → GREEN; -race) | slice 1 commit |
| T2 | S2 | 2 | inline, no Writer reason | pending | |

Native grouping: not grouped; both PRs merge at once, in order.

> Before each commit, run `git diff --numstat <base>` and sum additions + deletions, skipping binary rows (`-\t-`) and generated paths (lockfiles, `__snapshots__/`, files marked `linguist-generated` in `.gitattributes`); if the current work unit — slice or single unit — is over 400 real changed lines, stop: hand the accumulated diff to `slice-diff` (budget 400), then re-forecast every slice not yet started with `slice-plan` (budget 400) — a slice that overran invalidates the estimates that assumed its size, not just its own. Slices already merged stay as they are.
> Slice 1 base: `main`. Slice 2 base: `feat/backlog-autosync`.

## Log

- L1 (2026-10-23, user): "quiero que revises dos cosas: - el costo de llamada y tiempo de backlog (a ver si lo podemos automatizar como el resto o no) - como abrir tkt en un pane (un atajo mas serviria tambien)"
- L2: read-only probe on the user's three team boards: FetchBacklog 1.3–1.7 s, 8–9 HTTP calls; sprint sync's Jira part 1.8 s, 13 calls. The board is a Herdr popup (95%), one per workspace, `prefix+t`; it quits after handing work to an agent (SendTask, startTask, Fix), which a pane must not.
- L3 (2026-10-23, user): "si, hazlos como dos slices. cuando esten listo subes y publicas"
- L4: T1 done: board.BacklogCache (backlog.json, 0600; Cache and it share loadJSON/saveJSON) and BacklogEvery (5 min); daemon keepBacklog refreshes it every BacklogEvery when team boards exist (refreshBacklog asks nothing otherwise); Actions.Backlog(force): a copy fresher than BacklogEvery opens without Jira, an older one is sent at once (BacklogMsg.Cached, still loading) while Jira answers, every fetch is saved; g, an assignment and a move force; the open tab reloads the copy once it is BacklogEvery old.
