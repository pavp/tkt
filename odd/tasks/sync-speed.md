# tkt: a faster sync and a board that paints Jira first

Objective: cut the sync from ~4.7 s to ~2.3 s and show what Jira says (~1 s) before GitHub answers. Delivery **Stacked** PRs to `main` (budget 400, default): `perf/sync-speed-1-queries` on `main`, `perf/sync-speed-2-two-phase` on slice 1, `perf/sync-speed-3-daemon-partial` on slice 2. Test runner: `go test ./...`.
Forecast (slice-plan, 2026-10-21): 355–680 authored changed lines; slice 1 70–135, slice 2 170–330, slice 3 115–215.

## Specs

- S1. "si desglosalo" (measured, read-only): the GitHub PR listing is the sync's critical path (3.3–4.1 s); merged pull requests need only their number, title, link, state, branch and author, since the board shows them as "merged" → the listing asks merged pull requests for those fields only (measured 3.32 s → 1.50 s).
- S2. The Jira label field id took 0.28 s on every daemon sync because each sync built a new Jira client → the Jira client, with what it has looked up, is kept for the process and rebuilt only when the site, email or token changes.
- S3. "y no se puede separar el pintado? primero jira y cuando llegue la respeusta de github pintar?" + "si plantealo asi, hazlo ya" → a refresh paints twice: once Jira has answered (tickets, statuses, assignees, sprints, the team), with each ticket's pull requests taken from its cached development panel or the previous board, then the full board once GitHub answers. Without the daemon the board's refresh does it (slice 2); with the daemon, the daemon publishes the partial board as an additive `snapshot` event with `partial: true`, and `GET /v1/tickets` serves it meanwhile (slice 3).
- S4. While a partial board shows, the spinner stays and says "updating PRs…"; nothing else changes for the user.

## Tasks

| ID | Specs | Slice | Route | Status | Commit |
|---|---|---|---|---|---|
| T1 | S1, S2 | 1 | inline, no Writer reason | pending | |
| T2 | S3, S4 (board and CLI without the daemon) | 2 | inline, no Writer reason | pending | |
| T3 | S3 (daemon) | 3 | inline, no Writer reason | pending | |

Native grouping: once the last slice's PR is open, group with `gh stack link <pr-1> <pr-2> <pr-3>`; merging any PR then takes every unmerged PR below it. Every branch must live in ONE repository, and the implementer reads back each PR's base and diff and reports that, never the attempt — a PR whose diff grew is mis-grouped, and `gh stack unstack <stack>` must dissolve the stack before any base can be corrected.

> Before each commit, run `git diff --numstat <base>` and sum additions + deletions, skipping binary rows (`-\t-`) and generated paths (lockfiles, `__snapshots__/`, files marked `linguist-generated` in `.gitattributes`); if the current work unit — slice or single unit — is over 400 real changed lines, stop: hand the accumulated diff to `slice-diff` (budget 400), then re-forecast every slice not yet started with `slice-plan` (budget 400) — a slice that overran invalidates the estimates that assumed its size, not just its own. Slices already merged stay as they are.
> Slice 1 base: `main`. Slice 2 base: `perf/sync-speed-1-queries`. Slice 3 base: `perf/sync-speed-2-two-phase`.

## Log

- L1 (2026-10-21, user): "mediste los tiempoes de sync o refresh?" then "si desglosalo" → breakdown: GitHub listing 3.3–4.1 s (critical path), Jira search 0.4–0.5 s, label field id 0.28 s per daemon sync, development panels 1.2 s when uncached, review requests 0.7–0.9 s in parallel.
- L2 (2026-10-21, user): "y no se puede separar el pintado? primero jira y cuando llegue la respeusta de github pintar?" then "igual antes de esto, jira ya no te proporciona informacion de las PRS?" → Jira's development panel gives the PRs, their state, reviewers and approvals, comment counts and builds, but not changes requested, conflicts, drafts, unresolved threads or review requests, and costs ~1.2 s per ticket; the first paint uses the cached panels.
- L3 (2026-10-21, user): "si plantealo asi, hazlo ya" then "si pero tienes razon, primero lleva a main el codigo que ya tenemos y publica, luego sigue con estas 3 slices en stacked" → team board released as v0.1.32 and the `f` fix as v0.1.33 first; slices start from main (682a61e).
