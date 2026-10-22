# tkt — status columns and marks

Objective: map each space's Jira statuses to the board's four columns and choose each status's card mark, from Settings and `tkt init`. Delivery: Stacked PRs to `main`, budget 400. Test runner: `go test ./...`.

## Specs

- S1 Settings gets a section "para agrupar en las 4 columnas segun los estados disponible de los tickets de jira? asi queda agnostico a la empresa": one block per space listing every Jira status of the space with its column; the user changes a status's column, statuses left alone follow the automatic rule (shown as `auto`); only changes are saved, in `status_overrides`.
- S2 "no solo en settings si no que lo debera preguntar en el init tambien": `tkt init` asks each space's column mapping.
- S3 Tickets in a status the user moved out of its automatic column show "un identificativo para saber que estan en pending test": the card header shows `◇ <status name>` ("va el nombre, perfecto"), or `◇` alone when it does not fit.
- S4 Marks are configurable per status ("las marcas tambien deberian poder configurarse? o setearse de manera automatica? para los casos por ejemplo de blocked y pending test"): automatic by default (`⊘` for names with "block"/"bloque", `◇` for moved statuses), changed in the Settings section with `m` (none, `◇`, `⊘`); only changes are saved, in `status_marks`. Assumption: init does not ask marks.
- S5 Automatic column rule, unchanged: override wins; a "block"/"bloque" status waits in To Do; Jira "done" → Done; "indeterminate" with "review"/"revisi" → In Review; other "indeterminate" → In Progress; the rest → To Do.

## Tasks

| ID | Specs | Slice / branch | Route | Status | Commit |
|---|---|---|---|---|---|
| T1 | S3, S4 (data), S5 | 1 `feat/status-columns-1-marker` (base `main`) | inline, no Writer reason | done (199 lines) | 21f4e0a |
| T2 | S2 | 2 `feat/status-columns-2-init` (base slice 1) | inline, no Writer reason | done (138 lines) | (this slice) |
| T3 | S1, S4 (Settings) | 3 `feat/status-columns-3-settings` (base slice 2) | inline, no Writer reason | pending | |

Forecast: slice 1 80–140, slice 2 80–135, slice 3 250–390 (total 410–665). Native grouping: once the last slice's PR is open, group with `gh stack link <pr1> <pr2> <pr3>`; merging any PR then takes every unmerged PR below it. Every branch lives in one repository; read back each PR's base and diff after linking (`gh stack unstack <stack>` before correcting a base).

> Before each commit, run `git diff --numstat main` and sum additions + deletions, skipping binary rows (`-\t-`) and generated paths (lockfiles, `__snapshots__/`, files marked `linguist-generated` in `.gitattributes`); if the current work unit — slice or single unit — is over 400 real changed lines, stop: hand the accumulated diff to `slice-diff` (budget 400), then re-forecast every slice not yet started with `slice-plan` (budget 400) — a slice that overran invalidates the estimates that assumed its size, not just its own. Slices already merged stay as they are.
Slice bases: 1 → `main`, 2 → `feat/status-columns-1-marker`, 3 → `feat/status-columns-2-init`.

## Log

- L1 (2026-10-22) "no tenemos una columna para el estado de pendiente test, que tal si esos ticekts los mostramos en In REview pero con un identificativo para saber que estan en pending test?"
- L2 "y que tal si en settings agregamos una seccion para agrupar en las 4 columnas segun los estados disponible de los tickets de jira? asi queda agnostico a la empresa?"
- L3 "y por su puesto no solo en settings si no que lo debera preguntar en el init tambien"
- L4 "igual tengo una pregunta, las marcas tambien deberian poder configurarse? o setearse de manera automatica? para los casos por ejemplo de blocked y pending test?" → configurable marks proposed.
- L5 "oh ok va el nombre, perfecto. vamos con stacked." Strategy Stacked.
- L6 Discovery: `ToTicket` (internal/board/board.go:522) sends "block"/"bloque" statuses to To Do unless overridden; kept as part of the automatic rule (S5). `CategoryFor` stays as is because `tkt start` uses it.
