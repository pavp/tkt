# tkt: board fixes and in-terminal folder picker

Objective: board agent states paint from the cache, one board across Herdr workspaces, and a folder picker that works over SSH and WSL (Tab completion in the path field plus a ctrl+o directory browser). Branches `fix/board-and-picker-<n>-<slug>`, delivery **Stacked** PRs to `main` (slice n bases on slice n-1), grouped with `gh stack link` once the last PR is open. Test runner: `go test ./...` (`-race` for concurrency).
Forecast: 935–1340 authored changed lines, budget 400 per slice (default) → six slices.

## Specs

- S1. "la primera vez que entra tarda en pintar los estados del agente hasta que termina el sync" → the board shows the cached board's agent states without waiting for the Jira/GitHub refresh; reading them never rewrites the Herdr sidebar, and a stale (offline) refresh keeps them.
- S2. "si tengo abierto en una pane o en cualquier lado e intento abrirlo en otra no lo hace" + "si estoy en distintos spaces ... vamos con tu recomendacion" → one board: `prefix+t` focuses it in the same workspace, moves it (close + open) when open in another, opens it when none is open.
- S3. "ctrl + o no funciona en wsl (windows)"; found over SSH into WSL no Windows window appears (notepad.exe too) and the PowerShell dialog hangs. Agreed ("las dos"): Tab completion in the path field — completes child directories of what is typed, suggests known clones and clone folders first.
- S4. Same → a directory browser on ctrl+o inside Settings: directories only, → enters, ← goes up, Enter chooses the current directory (empty directories included), Esc cancels; directory reads run asynchronously.
- S5. Browser extras: `/` filters as you type, hidden directories toggle, symlinks followed (broken ones dimmed), permission errors shown inline.
- S6. The native chooser is used only outside SSH and WSL, and only where one exists (macOS, Linux with zenity/kdialog); elsewhere ctrl+o opens the browser. Revised 2026-10-20 (L8): the Windows dialog code is removed and no timeout is added.

## Tasks

| ID | Specs | Slice / branch | Route | Status | Commit |
|---|---|---|---|---|---|
| T1 | S1 | 1 `fix/board-and-picker-1-board-agents` | inline, no Writer reason | done (RED→GREEN, reviewed) | 091b22d (139 lines) |
| T2 | S2 | 2 `fix/board-and-picker-2-single-board` | inline, no Writer reason | done (RED→GREEN) | 5e7a49c + 59f897b |
| T3 | S3 | 3 `fix/board-and-picker-3-path-completion` | inline, no Writer reason | done (RED→GREEN) | 073e226 (365 lines) |
| T4 | S4, S6 (SSH/WSL detection) | 4 `fix/board-and-picker-4-dir-browser` | inline, no Writer reason | done (RED→GREEN); size:exception, user-approved | d3b3a4b (451 lines) |
| T5 | S5 | 5 `fix/board-and-picker-5-browser-filter` | inline, no Writer reason | done (RED→GREEN) | ea4c369 (286 lines) |
| T6 | S6 (remove the Windows dialog) | 6 `fix/board-and-picker-6-picker-fallback` | inline, no Writer reason | done (suite green; removal, no RED) | 807481d (~102 lines) |

> Before each commit, run `git diff --numstat main` and sum additions + deletions, skipping binary rows (`-\t-`) and generated paths (lockfiles, `__snapshots__/`, files marked `linguist-generated` in `.gitattributes`); if the current work unit — slice or single unit — is over 400 real changed lines, stop: hand the accumulated diff to `slice-diff` (budget 400), then re-forecast every slice not yet started with `slice-plan` (budget 400) — a slice that overran invalidates the estimates that assumed its size, not just its own. Slices already merged stay as they are.
> Per slice, measure against its base branch (slice n against slice n-1).

## Log

- L1 (2026-10-20) Original request: "hola, tengo algunos issues que he encontrado: - ctrl + o no funciona en wsl (windows) - la primera vez que entra tarda en pintar los estados del agente hasta que termina el sync - si tengo abierto en una pane o en cualquier lado e intento abrirlo en otra no lo hace -> que recomiendas como tratar esto?"
- L2 "2. ok arreglalo" → T1 implemented; independent reviewer found two minor issues (stale refresh wiped agents; cached read rewrote the sidebar), both fixed with tests.
- L3 "ok el punto 3 vamos con tu recomendacion" → T2: `tkt board` records `HERDR_PANE_ID`/`HERDR_WORKSPACE_ID` in `<data>/board-pane` (0600); open-board uses `plugin.pane.focus`/`plugin.pane.close`/`plugin.pane.open`. Herdr's plugin log showed open-board exiting 0 while the popup stayed in the old workspace. Unverified: whether Herdr sets those variables in plugin popup panes.
- L4 WSL diagnosis: PowerShell dialog, TopMost owner, no SelectedPath, and notepad.exe all hang; `SSH_CONNECTION` set (user SSHes into WSL); `cmd.exe /c ver` returns rc=0; notepad appeared much later. Research: bubbles/filepicker cannot pick directories (bubbles#659 open, PR #931 closed), huh and gum wrap it → write our own component.
- L5 "las dos" (completion + browser); "si confirmo vamos con stacked".
- L6 T3: Tab in a path field completes (arrows move fields); an empty field cycles known clones and clone folders; folders listed asynchronously via `ListDirs`.
- L7 T4: the browser needed `NativePicker()` to be reachable, so the SSH/WSL detection planned for T6 moved here; the slice measured 424 lines (over 400). slice-diff proposed moving the detection to slice 6 and scrolling to slice 5; user: "has una exception y mantenlo en 1 sola PR" → slice 4 kept whole as size:exception, plus the missing scroll test.
- L8 With WSL always using the browser, the PowerShell dialog became dead code and a timeout would only cut a dialog the user has open locally. Proposed removing the dialog and skipping the timeout; user: "si". T6 removes `folderPicker`'s WSL case, `wslPath`, the Windows path conversion and `credstore.EncodedCommand`.
- L9 Live test on the Mac ("no abre tkt"): open-board failed with "invalid_params: overlay and popup plugin panes target the active pane"; Herdr rejects a workspace_id for popups. Fixed in 59f897b on slice 2 (test asserts no workspace_id). Slice 3 had been committed on branch 2 by mistake; the chain was rebuilt (branch 3 restored, 4-6 rebased, final tree unchanged).
- L10 "sube el codigo a main y genera una nueva version" → squash-merged to main as #10 (slice 1), #11 (2), #12 (3), #16 (4), #14 (5), #17 (6 + this document). The merge script mis-indexed a zsh array and deleted the branches of #13 and #15, which GitHub closed; their slices went in as replacement PRs #16 and #17. main matched each slice's tree after merging. Release: v0.1.9.
- Done; earlier next step was: user decides push, PRs (Stacked, `gh stack link` after the last PR opens), and the open SSH question for `o`/`p` links.
