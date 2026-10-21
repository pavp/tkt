# tkt: the team comes from the Jira boards the user picks

Objective: the board's Team mode shows the active sprints of the Jira boards the user configures per space (empty by default), set in Settings and in `tkt init`. Delivery **Chain** (budget 400, default): tracker `feat/team-boards` (draft PR to `main`), slices `feat/team-boards-1-data` → `feat/team-boards-2-board-init` → `feat/team-boards-3-settings`. Test runner: `go test ./...`.
Forecast (slice-plan, 2026-10-21): 595–1020 authored changed lines; slice 1 225–380, slice 2 145–270, slice 3 225–370.

## Specs

- S1. "en jira veo dos tareas blocked 165803 y 165823 que no veo en el tkt board (team)" + "si, como manejamos los boards que aparecen?" + "y si hacemos algo similar a los repositorios en settings?" + "el board UAC no aparece siempre, es uno de los que trabajamos. Comienza en vacio y abra que setear desde settings o quizas al comienzo dle init poder agregar varios boards? al igual que en el init poder agregar los repositorios?" → each space has a list of team boards (Jira scrum boards, kept as id and name in the configuration), empty by default, with no automatic board.
- S2. The Team mode's tickets are those of the active sprints of the configured boards, assigned to others or to nobody (the user's own tickets keep coming from their own query); with no board configured there is no team search, and turning Team on says "add team boards in Settings (,)". Each team ticket knows its board.
- S3. In the list view the team is split by board: "Team · <board name>".
- S4. `tkt init` asks, per space, for team boards by name (comma separated), matched against the space's scrum boards, like the repositories step.
- S5. Settings lists each space's team boards under its repositories: add one by name with tab completion from the space's scrum boards (a dropdown, as for repositories), remove one.
- S6. Every screen works in the full window and the tray panel.

## Tasks

| ID | Specs | Slice | Route | Status | Commit |
|---|---|---|---|---|---|
| T1 | S1, S2 (data: Jira boards and sprints, config, sync) | 1 | inline, no Writer reason | done (RED → GREEN; -race; live: Authorization Center gives 6 team tickets incl. ACTO-165803/165823, 3.9 s) | 73eefdb |
| T2 | S2, S3, S4, S6 (board notice and sections, init step) | 2 | inline, no Writer reason | done (RED → GREEN; -race on cli and tui) | 1343300 |
| T3 | S5, S6 (Settings) | 3 | inline, no Writer reason | done (RED → GREEN; -race on tui, cli, board; rendered at 140 and 56 columns) | b186d5a |

Native grouping: never grouped: a stack would merge the draft tracker.

> Before each commit, run `git diff --numstat <base>` and sum additions + deletions, skipping binary rows (`-\t-`) and generated paths (lockfiles, `__snapshots__/`, files marked `linguist-generated` in `.gitattributes`); if the current work unit — slice or single unit — is over 400 real changed lines, stop: hand the accumulated diff to `slice-diff` (budget 400), then re-forecast every slice not yet started with `slice-plan` (budget 400) — a slice that overran invalidates the estimates that assumed its size, not just its own. Slices already merged stay as they are.
> Slice 1 base: `feat/team-boards`. Slice 2 base: `feat/team-boards-1-data`. Slice 3 base: `feat/team-boards-2-board-init`.

## Log

- L1 (2026-10-21, user): "ok faltaria mostrar las tareas blocked tambien en todo con el identificador" then "en jira veo dos tareas blocked 165803 y 165823 que no veo en el tkt board (team)" → read-only: both are Blocked, unassigned, in active sprint 11807 of board 5205 "Authorization Center"; the user's tickets are in sprint 11803 of board 418 "User Access Center"; both sprints are named "015 - 20261023 - 26". The team query matched board 418 exactly (14 tickets: 11 as team, 3 done-and-was-mine among the user's).
- L2 (2026-10-21, user): "seguro pertenece a otro board? no lo entiendo" → board filters confirmed it; ACTO has 126 boards (58 scrum), listed in ~0.9 s.
- L3 (2026-10-21, user): "si confirmo el plan con chain" (after "una pregunta, ahora mismo solo muestra tickets de board > sprints activos?" was answered: today only sprints, the user's tickets in any open sprint, the team in the user's sprints).
- L4: slice 1 done (73eefdb): jira Boards(space) (scrum, paginated by startAt) and ActiveSprints(board); config TeamBoard and Space.TeamBoards (`team_boards`), cloned; FetchWith starts the team goroutine at once: activeSprints over the configured boards in parallel, then TeamSprintJQL over their ids; no boards → no search; Ticket.Board from the sprint's board. Live (read-only, Authorization Center configured in memory): both tickets show as team with their board; their Jira status has since changed (Ready to be Released, En revisión).
- L5: slice 2 done (1343300): init asks "Team boards for KEY (names, comma separated; N scrum boards)" only when the space has scrum boards (so existing flows keep their answers), matching an exact name (any case) or a single name containing the text, warning on none or several; turning Team on without team boards says "no team boards yet · add team boards in Settings (,)"; the list splits the team into "Team · <board>" sections.
- L6: slice 3 done (b186d5a): Settings lists "TEAM BOARDS" under each space's repositories (rows with ✕, "+ Add team board"); the add form asks Actions.JiraBoards at once, tab completes by prefix, enter accepts an exact name or a single containing one (config.MatchBoard, shared with init) and refuses unknown, ambiguous or repeated boards; d or ✕ removes after confirming; column width counts board names. Next: delivery (Chain) on the user's go.
