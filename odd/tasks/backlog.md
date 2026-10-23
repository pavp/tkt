# tkt: a Backlog screen and a boards filter

Objective: a list-only Backlog screen of the selected team boards' future sprints and backlog, with work started from it, and a boards filter replacing the repositories filter outside the PRs filter. Delivery **Chain** (budget 800, user's explicit number: "sube a 800 el presupuesto"): tracker `feat/backlog` (draft PR to `main`), slices `feat/backlog-1-board-filter` → `feat/backlog-2-backlog-view` → `feat/backlog-3-backlog-actions`. Test runner: `go test ./...`.
Forecast (slice-plan, 2026-10-23): 930–1540 authored changed lines; slice A 230–390, slice B 470–750, slice C 230–380.

## Specs

- S1. "quiero implementar una nueva pantalla backlog, mostrar el backlog solo de los team boards que se tienen seleccionados. Quiero que sea solo modo lista. Agrupados por sprint." → a Backlog screen in the board, list mode only, from the configured team boards only; loaded when it opens and on refresh, not in the minute sync.
- S2. "en la agrupacion en el backlog, hay tickets ya asignados a futuros sprints y otros que no (agrupados en la seccion backlog)" + "lo dejamos fuera" (the active sprint) → one group per future sprint, in Jira's order, then a "Backlog" group of tickets in no sprint; the active sprint is not shown.
- S3. "en la cabecera del sprint muestre una guia de story points, basicamente sin iniciar, en curso y completados" + "en vez de puntos que tal encerrados en un cuadrado con diferentes colores de fondo?" → each group header shows three one-line filled badges (not started, in progress, done by tkt column: To Do; In Progress and In Review; Done), background the column's color, dark text; no badges without a points field.
- S4. "quiero poder comenzar a trabajar en tickets desde el backlog (aunque no aparezcan en el sprint)" + "2. no me parece porque realmente es adelantar un trabajo que no esta en el sprint actual" → Start works on backlog tickets; a started backlog ticket does not join the board, it stays in the Backlog screen (Open agent once it has a worktree).
- S5. "Si podria ofrecermelo, tambien puede ocurrir que yo me lo asigne y luego lo comience." → Start on a ticket not assigned to the user offers to assign it to them first (confirmed); an assigned ticket starts without asking.
- S6. "los tickets del backlog deberian tener la opciond esde el menu de poder moverlo al sprint actual" → a backlog ticket's menu moves it to the active sprint of the board it is listed under, after confirming; with several active sprints, the user picks one.
- S7. "tambien puede tener un filtro de boards, que de hecho ahora pensandolo bien en el sprint tenemos un filtro de repos no deberia ser de boards? el filtro de repos deberia ser en la de PR solamente?" + "en el mismo plan" → Sprint and Backlog get a boards filter (the user's tickets placed by their sprint's board); the repositories filter stays only in the PRs filter.
- S8. Every screen works in the full window and the tray panel; the Backlog screen's design is agreed with the user before slice B.

## Tasks

| ID | Specs | Slice | Route | Status | Commit |
|---|---|---|---|---|---|
| T1 | S7, S8 (boards filter in Sprint) | A | inline, no Writer reason | done (RED: compile + behavior → GREEN; -race tui/board; live check pending on the user) | see slice A commit |
| T2 | S1, S2, S3, S7, S8 (Backlog data and read-only screen) | B | inline, no Writer reason | pending (design to agree) | |
| T3 | S4, S5, S6, S8 (Backlog actions) | C | inline, no Writer reason | pending | |

Native grouping: never grouped: a stack would merge the draft tracker.

> Before each commit, run `git diff --numstat <base>` and sum additions + deletions, skipping binary rows (`-\t-`) and generated paths (lockfiles, `__snapshots__/`, files marked `linguist-generated` in `.gitattributes`); if the current work unit — slice or single unit — is over 800 real changed lines, stop: hand the accumulated diff to `slice-diff` (budget 800), then re-forecast every slice not yet started with `slice-plan` (budget 800) — a slice that overran invalidates the estimates that assumed its size, not just its own. Slices already merged stay as they are.
> Slice A base: `feat/backlog`. Slice B base: `feat/backlog-1-board-filter`. Slice C base: `feat/backlog-2-backlog-view`.

## Log

- L1 (2026-10-23, user): "hola, quiero implementar una nueva pantalla backlog, mostrar el backlog solo de los team boards que se tienen seleccionados. Quiero que sea solo modo lista. Agrupados por sprint. Que piensas? Ademas quiero poder comenzar a trabajar en tickets desde el backlog (aunque no aparezcan en el sprint)"
- L2: mapped: jira has only Boards and ActiveSprints Agile calls; Sprint does not parse boardId (client.go:241); the TUI calls Jira through tui.Actions (cli/board.go), so the backlog needs no daemon route; `tkt start KEY` does not need the snapshot; Assign to… Me is on main (assign.go). Risk for slice C: agent states are built from snapshot tickets (cli/herdr.go:877). Jira Agile docs confirm board backlog, board sprints by state, move issues to sprint, sprint originBoardId.
- L3 (2026-10-23, user): "sube a 800 el presupuesto" then "vamos con Chain".
- L4: slice A done: jira parses the Sprint field's boardId (Sprint.Board); FetchWith gives the user's tickets the name of the configured team board whose active sprint holds them; the TUI's R repository filter applies only in the PRs filter (review strip in Sprint unfiltered), B or a Boards button opens a board filter elsewhere (hidden_boards in board-state.json, "Showing N of M boards · B change", tickets on no team board always show, team tickets counted only while shown). Only team-guarded code read Ticket.Board before, so mine carrying it changes nothing else; the snapshot's "board" field is additive in /v1. Tray panel: a "Boards N/M" button does not fit beside the title at 56 columns, so B and the notice open it there (existing "where the boxed buttons fit" rule). Measured 430 changed lines against feat/backlog, this document included. Not verified live: that Jira Cloud sends boardId in the Sprint field.
