# tkt: friendly Start pickers for repositories and worktrees

Objective: Start never asks about a phantom worktree, goes straight to the worktree whose agent runs, and asks the remaining questions in a board modal usable with keyboard and mouse. Branch `feat/start-picker`, delivery **single PR** to `main` (budget 1000, set by the user: "sube el budget a 1000"). Test runner: `go test ./...`.
Forecast: 435–730 authored changed lines → single unit; one work-unit commit per task.

## Specs

- S1. "sigue apareciendo lo de los worktree que no lo entiendo nisiquiera tiene que ver con ese ticket" (screenshot: both choices share the path `…/refactor-acto-170450-homogenize-organizations-module`, branches `organizations-4-read-repository` and `homogenize-organizations-module`) → a link whose path is a discovered worktree now on another branch is stale and is dropped; git's branch wins.
- S2. "le di para ir al agente y me pregunto por los worktress, seleccione el primero (el que tiene el agente) y me llevo bien al space y tab.. que te parece de este flujo de seleccionar el worktree?" + "si" (to the proposal) → when several worktrees remain and exactly one has an agent working in it, Start goes to that one without asking.
- S3. "en caso de quedarse deberia ser mas friendly tambien" → when a choice of worktree remains, the board asks in a modal ("Open KEY · Which worktree?", each row its branch and agent state; enter opens, esc cancels) and runs Start for the chosen worktree; the screen is cleared before Start takes the terminal, so earlier output never shows.
- S4. "Me parece bien que deje escoger los repositorios pero podria ser mas friendly?" → when Start must choose repositories (no worktree, more than one repository), the board asks in a modal ("Start KEY · Which repositories?", last used ones checked; space toggles, enter starts, esc cancels) and runs Start with those repositories.
- S5. "recuerda hacerlos mouse friendly tambien" → both modals work with the mouse: a click toggles a repository, a click on a worktree opens it, and the Start / Cancel buttons are clickable.
- S6. The modals are the same in the full window and the tray panel (narrower); the prototype has no such screen and the user approved this design in chat.

## Tasks

| ID | Specs | Route | Status | Commit |
|---|---|---|---|---|
| T1 | S1, S2 | inline, no Writer reason | done (S1 RED→GREEN; S2 RED observed through an overlay without the preference, since the code came first) | 1c5b0ef |
| T2 | S3, S4 (CLI side: `--worktree` flag, choices for the board) | inline, no Writer reason | done (RED: did not compile → GREEN) | fb467da |
| T3 | S3, S5, S6 (worktree modal, wiring, cleared screen) | inline, no Writer reason | done (RED: did not compile → GREEN; rendered at 120 and 56 columns) | 163e901 |
| T4 | S4, S5, S6 (repository modal) | inline, no Writer reason | done (RED → GREEN; rendered at 120 and 56 columns) | see branch |

> Before each commit, run `git diff --numstat main` and sum additions + deletions, skipping binary rows (`-\t-`) and generated paths (lockfiles, `__snapshots__/`, files marked `linguist-generated` in `.gitattributes`); if the current work unit — slice or single unit — is over 1000 real changed lines, stop: hand the accumulated diff to `slice-diff` (budget 1000), then re-forecast every slice not yet started with `slice-plan` (budget 1000) — a slice that overran invalidates the estimates that assumed its size, not just its own. Slices already merged stay as they are.

## Log

- L1 (2026-10-20, user): "/Users/macbook/Desktop/Screenshot 2026-10-20 at 22.29.43.png intente iniciar otro ticket y me aparece esta pantalla. Me parece bien que deje escoger los repositorios pero podria ser mas friendly? ademas sigue apareciendo lo de los worktree que no lo entiendo nisiquiera tiene que ver con ese ticket"
- L2 (2026-10-20, user): "/Users/macbook/Desktop/Screenshot 2026-10-20 at 22.36.49.png le di para ir al agente y me pregunto por los worktress, seleccione el primero (el que tiene el agente) y me llevo bien al space y tab.. que te parece de este flujo de seleccionar el worktree? en caso de quedarse deberia ser mas friendly tambien"
- L3 (2026-10-20): analysis — the first screenshot's worktree lines were left over from the previous Start of ACTO-170450 (the board hands the terminal to `tkt herdr start`); the second shows one worktree listed twice because `workspace.Merge` joins links and discovered worktrees by repo + branch and a stale `tkt link` kept the old branch name.
- L4 (2026-10-20, user): "si" (to: drop stale links, pick the worktree with the agent, modal for the rest and for repositories); "recuerda hacerlos mouse friendly tambien"; "sube el budget a 1000".
- L5: risk item 3 for T1 — `workspace.Merge` is shared by `tkt work`/`tkt link` listings; the differential check covers them.
- L6: all tasks done on feat/start-picker, 747 authored changed lines (budget 1000); go vet, go test ./... and go test -race for tui and cli pass. Known gap: a space with no configured repositories still asks in the terminal, since its repositories are inferred from Jira at Start.
