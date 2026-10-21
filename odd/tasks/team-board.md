# tkt: assignee on every ticket and a Team toggle for the board

Objective: show each ticket's assignee always, and let the board also show the team's tickets (assigned to others or unassigned) of the open sprints, off by default and cheap. Delivery **Chain** (budget 400, default): tracker `feat/team-board` (draft PR to `main`), slices `feat/team-board-1-assignee` → `feat/team-board-2-team-data` → `feat/team-board-3-team-toggle`, each based on the previous. Test runner: `go test ./...`.
Forecast (slice-plan, 2026-10-21): 550–1000 authored changed lines; slice 1 115–225, slice 2 195–375, slice 3 240–400.

## Specs

- S1. "prefiero verlo siempre" (answering whether the assignee's name shows only on team tickets) → every ticket shows its assignee's name, on cards and list rows, whether the Team toggle is on or not; unassigned tickets say so.
- S2. "que tal mostrar solo en TODO los tickets que tambien no los tengo asignados? pero agregando un filtro en el top de la columna (apagado por default)" + "ambos" (unassigned and assigned to teammates) + "Aunque pensnadolo bien que tal seria esta funcionalidad para todo el tablero?" + "si es asi" (to: off shows only mine, as today; on also shows the team's tickets in every column) → a Team toggle, off by default, adds the tickets of the user's own active sprints (the sprints the user's tickets are in, by id) that are assigned to others or unassigned, in every column and in the list view; their sprints never join the board's title.
- S3. "como afectaria en el rendimiento, las llamadas?" → while the toggle is off nothing extra is fetched; while on, one broader Jira search per sync; team tickets skip the Jira development panel and the GitHub search of older pull requests by key; their pull requests come from the per-repository GitHub listing already fetched. Turning it on asks for a sync at once.
- S4. Team tickets never need the user: no "!", no Herdr sidebar alert, no `f`, no Start or agent; their menu offers details, Jira and copying the key.
- S5. The toggle is a "Team" header button and the `t` key; team tickets are dimmed; the choice is remembered between openings like the repository filter.
- S6. Every screen works in the full window and the tray panel (narrow); the prototype has no such screen and the design was approved in chat.

## Tasks

| ID | Specs | Slice | Route | Status | Commit |
|---|---|---|---|---|---|
| T1 | S1, S6 | 1 | inline, no Writer reason | done (RED: did not compile → GREEN; live read-only check returned the real assignee) | b6d9932 |
| T2 | S2, S3, S4 (data side) | 2 | inline, no Writer reason | done (RED: did not compile → GREEN; -race on board and cli) | 5d9f4cb |
| T3 | S2, S4, S5, S6 (board side) | 3 | inline, no Writer reason | pending | |

Native grouping: never grouped: a stack would merge the draft tracker.

> Before each commit, run `git diff --numstat <base>` and sum additions + deletions, skipping binary rows (`-\t-`) and generated paths (lockfiles, `__snapshots__/`, files marked `linguist-generated` in `.gitattributes`); if the current work unit — slice or single unit — is over 400 real changed lines, stop: hand the accumulated diff to `slice-diff` (budget 400), then re-forecast every slice not yet started with `slice-plan` (budget 400) — a slice that overran invalidates the estimates that assumed its size, not just its own. Slices already merged stay as they are.
> Slice 1 base: `feat/team-board`. Slice 2 base: `feat/team-board-1-assignee`. Slice 3 base: `feat/team-board-2-team-data`.

## Log

- L1 (2026-10-21, user): "que tal mostrar solo en TODO los tickets que tambien no los tengo asignados? pero agregando un filtro en el top de la columna (apagado por default) es posible? como afectaria en el rendimiento, las llamadas?"
- L2 (2026-10-21, user): "ambos. y si selecciono el filtro pues no mostrara el resto de ticket solo los mios. Aunque pensnadolo bien que tal seria esta funcionalidad para todo el tablero? aumentaria el costo?"
- L3 (2026-10-21, user): "si es asi, quiere decir que ahora se agregara el nombre del asignado correcto? este encendido o no" then "prefiero verlo siempre"
- L4 (2026-10-21, user): "si confirmo con chain" → plan of 3 slices, Chain strategy.
- L5: findings — a sync is one Jira search (MySprintJQL), one development-panel call per ticket (cached until updated or 10 minutes) and per-repository GitHub listings; the daemon can read board-state.json each sync.
- L6: slice 1 done (b6d9932): jira Issue.Assignee from assignee.displayName (in ListFields, so search and detail), Ticket.Assignee, cards "type · Ana L. · agent" (type gives way first, then the name, never the agent), list ASSIGNEE column (dropped after AGENT), preview and `tkt show` full name, "unassigned" when empty; card and mockup tests adapted to the assignee. 245 changed lines with this document.
- L7: slice 2 done (5d9f4cb): jira.TeamSprintJQL (shared quotedSpaces), Ticket.Team with empty Attention, board.FetchWith(Options{Team}) running the team search beside the user's, no development panel or older-PR search for team tickets, PRs attached from the listing, a failed team search is a warning; cli fetchSnapshot passes board-state.json's Team (BoardState.Team); `tkt list` text leaves team tickets out. Next: slice 3, the board toggle (which must also keep BoardState.Team when saving other state).
- L9 (2026-10-21, user, reviewing the preview build): "creo que la query esta mal, trae mas de 300 tickets en todo, parece que trae varios sprints o algo parecido" → confirmed on the live snapshot: 1723 team tickets, 180 assignees, 30 active sprints, all ACTO (`sprint in openSprints()` is every board's open sprints), and the team's sprints flooded the title. Fix on slice 2: jira.Sprint.ID decoded; TeamSprintJQL(spaces, sprintIDs) uses `sprint in (ids)` of the user's active sprints; the team search starts once those are known (beside the development panels) and is skipped without any; team sprints never join snap.Sprints. S2 rewritten.
