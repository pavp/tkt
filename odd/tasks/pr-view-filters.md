# tkt: repository filter and collapsible groups on the board

Objective: choose which repositories the board shows, in the PRs filter and in All, and collapse the PRs filter's groups; both remembered between openings of the board. Delivery **Stacked** PRs to `main` (budget 400, default): slice 1 `feat/pr-view-filters-1-repo-filter` on `main`, slice 2 `feat/pr-view-filters-2-collapse` on slice 1. Test runner: `go test ./...`.
Forecast (slice-plan, 2026-10-21): slice 1 330–540, slice 2 140–240 authored changed lines; the earlier work (772 lines) shipped as PR #48 (68748e4) before these slices.

## Specs

- S1. "que tal si damos la opcion de tener un filtro de repositorios que no quiero que aparezcan sus PR?" + "con el punto 1, lo quiero a nivel de vista de PR como una especie de filtro para hacer hidden de o seleccionar realmente los repos que quieres ver las PRs. Nada que ver con settings" → in the PRs filter, `R` (or a click on the filter line) opens a modal listing every repository with pull requests (the user's and review requests), each with a checkbox and its count; space toggles, `a` all/none, enter applies, esc cancels. Nothing is written to the configuration or Settings.
- S2. "de hecho ese mismo filtro de repositorios tendria sentido que este en la pantalla de all tambien" → the same filter applies in All: a ticket hides when every one of its pull requests is in a hidden repository; tickets without pull requests always show; the review-request strip hides hidden repositories too. Assumption (stated to the user, not contradicted): this rule for tickets.
- S3. While any repository is hidden, a line says so above the list, "Showing N of M repositories · R change"; the board never filters silently.
- S4. The hidden repositories are remembered between openings of the board, in the data directory (not the configuration); a repository seen for the first time shows.
- S5. "tambien estaria bueno poder colapsar los grupos de PR" → in the PRs filter, each section title ("Mine · owner/repo", "Review requested · owner/repo") is a selectable row; enter, space or a click collapses or expands it; a collapsed section keeps its title line with its count and how many need the user, e.g. "▸ Mine · owner/repo  4 · 1 needs you".
- S6. Collapsed sections are remembered between openings, like S4.
- S7. Every screen works in the full window and the tray panel (narrow); the prototype has no such screen and the design was approved in chat.

## Tasks

| ID | Specs | Slice | Route | Status | Commit |
|---|---|---|---|---|---|
| T1 | S4 (board state file, tui ↔ cli) | 1 | inline, no Writer reason | done (RED: did not compile → GREEN) | f2523fc |
| T2 | S1, S2, S3, S7 (repository filter) | 1 | inline, no Writer reason | done (RED: 3 tests → GREEN; rendered at 100 and 56 columns); slice 1 = 342 changed lines | 493af24 |
| T3 | S5, S6, S7 (collapsible groups) | 2 | inline, no Writer reason | done (RED: did not compile → GREEN; rendered at 100 and 56 columns); slice 2 = 235 changed lines | 29b148e |

Native grouping: once the last slice's PR is open, group with `gh stack link <pr-slice-1> <pr-slice-2>`; merging any PR then takes every unmerged PR below it. Every branch must live in ONE repository, and the implementer reads back each PR's base and diff and reports that, never the attempt — a PR whose diff grew is mis-grouped, and `gh stack unstack <stack>` must dissolve the stack before any base can be corrected.

> Before each commit, run `git diff --numstat <base>` and sum additions + deletions, skipping binary rows (`-\t-`) and generated paths (lockfiles, `__snapshots__/`, files marked `linguist-generated` in `.gitattributes`); if the current work unit — slice or single unit — is over 400 real changed lines, stop: hand the accumulated diff to `slice-diff` (budget 400), then re-forecast every slice not yet started with `slice-plan` (budget 400) — a slice that overran invalidates the estimates that assumed its size, not just its own. Slices already merged stay as they are.
> Slice 1 base: `main`. Slice 2 base: `feat/pr-view-filters-1-repo-filter`.

## Log

- L1 (2026-10-21, user): "que tal si damos la opcion de tener un filtro de repositorios que no quiero que aparezcan sus PR? tambien estaria bueno poder colapsar los grupos de PR"
- L2 (2026-10-21, user): "con el punto 1, lo quiero a nivel de vista de PR como una especie de filtro para hacer hidden de o seleccionar realmente los repos que quieres ver las PRs. Nada que ver con settings"
- L3 (2026-10-21, user): "implementalo aca mismo" then "de hecho ese mismo filtro de repositorios tendria sentido que este en la pantalla de all tambien"
- L4 (2026-10-21, user, after the slice plan): "ok hagamos algo, sube el codigo que tenemos hasta ahora. luego continua con las 2 nuevas slices con stacked" → PR #48 squash-merged (68748e4), no tag or release; slices start from main.
- L5: design notes — the board persisted nothing before (view and density are per session); `item` gains section-title rows for S5, so every use of `m.current()` must tolerate them.
- L6: slice 1 done: board-state.json in the data dir; R / a click on the header notice opens the filter (picker in filter mode: "Apply", `a` all/none, at least one repository); filter applied in tickets(), myPRs() and reviews(); All counts what it shows. 342 changed lines against main.
- L7: slice 2 done: section titles are item rows (group, title, count, needs); prItems builds the PRs filter; enter/space/click toggles and saves; title rows skipped by width math, menus and p. Found and fixed: toggleGroup mutated the collapsed map shared by model copies (now copied first). Next: push both branches, open the stacked PRs, `gh stack link`, read back each PR base and diff (user decision).
- L8 (2026-10-21, user): "cuando colapso la barra de arriba se mueve" / "el filtro de reposotirios no puedo mover nada con el teclado" / "el filtro de repositorios deberia poder abrirse y cerrarse con el mouse" / "en el filtro de repositorios, deberian aparecer todos los que tenemos agregados no? los que se ven en settings?"
- L9: analysis — the column widths were measured on visible rows only, so collapsing the widest status moved the header; the keyboard worked, but the user's board had pull requests in one repository only (Settings holds 3), so the filter had one row. Fix f4c1f05 on slice 2: widths measure collapsed rows (prLayout(true)); the filter lists configured repositories (actions.Config().Repos()) with "0 open"; a "Repos" header button (shown where the boxed buttons fit; "Repos N/M" while filtering) opens the filter, a click on it or outside closes it. The repository fixes belong to slice 1's feature but share its files, so they ride on slice 2. Slice 2 is now 407 changed lines against slice 1, 7 over the 400 budget: checkpoint stop, user decision.
