# tkt: optional repositories and board settings

Objective: repositories become optional (inferred from Jira-linked PRs), GitHub accounts are detected, and the board gets a Settings view for repositories and local paths. Branches: `feat/tkt-optional-repos-1-repos-inferred` → `main`, `feat/tkt-optional-repos-2-settings` → slice 1 (Stacked). Test runner: `go test ./...`.
Forecast: 1210–2040 authored changed lines against the user's 2000-line budget → two slices (660–1150, 550–890).

## Specs

- S1. Repositories are inferred per space from the pull requests Jira links to the user's tickets, and used for CI, review requests and the GitHub account; agreed: "la otra PC funcionaría sin tocar la configuración: los PRs de tus tickets ya apuntan a `nxl-engineering/cloud-uac-accounts-web`".
- S2. The repository list in `config.toml` stays as an optional override (a repo with no PRs yet, a base branch); configured and inferred repositories are merged.
- S3. The `gh` account for each organization is detected by trying every logged-in account and keeping the one that can read it; it asks only when several qualify. A configured `[github.accounts]` entry wins.
- S4. When no account can read an organization, or `gh` has no session, `tkt init` offers `gh auth login`.
- S5. `tkt list`, the board and `tkt doctor` warn when a space has no repositories, configured or inferred, instead of staying silent.
- S6. `tkt start` derives the repository from the ticket's pull requests when its space has none; agreed: "En un espacio nuevo, sin ningún PR todavía ... `tkt start` preguntaría el repo la primera vez".
- S7. "agrega uno settings para poder setear los repos desde el board": a Settings view in the board edits each space's repositories (add, remove, base branch).
- S8. "al igual que la parte de los path locales": the same Settings view edits the local clone paths (`repo_roots` and each repository's `path`).
- S10. "en los settings deberia haber una opcion de quitar repository" → a visible, clickable ✕ on every configured repository and folder, removing after confirmation (inferred repositories have none).
- S11. "en el local path abra algo para rellenar el path o buscar en local" → the local path is prefilled with the clone found in the clone folders (matching `origin`), choosing among several.
- S12. Same request → `ctrl+o` in the path field opens the system folder picker (macOS `osascript`, WSL the Windows dialog, Linux zenity/kdialog); "recuerda que en wsl los proyectos pueden estar en wsl y no en windows" → under WSL the dialog opens in the Linux home (`\\wsl.localhost\<distro>\home\…`) and the chosen path is converted back (`\\wsl.localhost\<distro>\…` → `/…`, `C:\…` → `/mnt/c/…`).
- S9. "cuando verticalmente no cabe todas las cards o tickets en el board, se hace un scroll en todo el board, se podria hacer scroll solo en esa columna?" → agreed: each column scrolls on its own keeping the selected card visible, with `↑ N more` / `↓ N more` markers; header, review strip and legend stay; the wheel scrolls the column under the pointer.

## Tasks

| ID | Specs | Slice | Route | Status | Commit |
|---|---|---|---|---|---|
| T1 | S1 S2 | 1 | inline, no Writer reason | done · RED `TestFetchInfersRepositoriesFromJiraLinkedPRs` | 5942a5f |
| T2 | S3 | 1 | inline, no Writer reason · Risk item 2 (credentials), own commit | done · RED accounts tests · RDD high, consent declined | 5b09bec |
| T3 | S4 | 1 | inline, no Writer reason | done · RED `TestInitSuggestsReposAndDetectsTheGitHubAccount`, `TestInitOffersGhLoginWithoutAnAccount` | dad3b16 |
| T4 | S5 | 1 | inline, no Writer reason | done · RED board/tui/doctor warning tests | a266c7c |
| T5 | S6 | 1 | inline, no Writer reason | done · RED `TestStartDerivesTheRepositoryFromTheTicketsPullRequests`, `TestStartAsksForARepositoryWhenNoneIsKnown` | b4dd320 |
| T6 | S7 S8 | 2 | inline, no Writer reason · checks the prototype's Settings screen first | done · tests written with the code (no separate RED); a mutation of the form apply fails 3 of 7 | a755112 |
| T7 | S7 S8 | 2 | inline, no Writer reason · Risk item 1 (writes the user's config), own commit | done · RED `TestBoardSaveConfig*`; verifier blockers fixed in 25013b6 | 9f6b0aa 25013b6 |
| T9 | S10 | 2 | inline, no Writer reason | done · RED `TestSettingsShowsARemoveButtonOnConfiguredRows` | (feat(tui): remove buttons…) |
| T10 | S11 | 2 | inline, no Writer reason | done · RED `TestFindAllListsEveryClone`, `TestRepoAt`, `TestSettingsFillsTheLocalPathFromFoundClones`, `TestBoardFindsClonesAndReadsOrigins` | (same) |
| T11 | S12 | 2 | inline, no Writer reason | done · RED `TestWSLPath`, `TestFolderPickerCommand`, `TestPickedPath`, `TestPickerCancelled`, `TestSettingsFillsTheRepositoryFromThePickedFolder`, `TestSettingsPicksACloneFolder`; AppleScript checked with osacompile; WSL and Linux dialogs not run here | (same) |
| T8 | S9 | 2 | inline, no Writer reason | done · RED `TestColumnsScrollOnTheirOwn`, `TestWheelScrollsTheColumnUnderThePointer` | (this commit) |

## Log

- L1 (2026-10-19) User: "si hazlo así, ademas agrega uno settings para poder setear los repos desde el board, al igual que la parte de los path locales" — accepting the proposal: optional repositories inferred from Jira-linked PRs, gh account detection, `gh auth login` offer, warnings, `start` deriving the repo.
- L2 (2026-10-19) Trigger: on the second PC (WSL) `tkt init` left ACTO without repositories, so no GitHub calls ran and the 8 review requests were missing; the GitHub step was skipped entirely because it iterates organizations of configured repositories.
- L3 (2026-10-19) Slice plan confirmed, delivery "ok stacked". Native grouping once both PRs are open: `gh stack link <pr-1> <pr-2>`.
- L4 (2026-10-19) Engram mirror pending: `mem_save` rejected project "tkt" as unknown (mem_current_project resolves it from the git remote, but no session backs it yet); auto-detect would file it under another project. Resync when available.
- L5 (2026-10-19) Mid-slice user reports, fixed on the slice-1 branch: "no sirve la opcion de cambiar vista en esa computadora" → the board forces the list below 100 columns and said nothing; `v` and the Columns button now explain why (3f6e5c1). "quiero que aumentes el tamano del board con respecto a herdr" → board popup 85% → 95% (a27d839; applies after `tkt herdr install`). "en la mac cuando le doy a start ... en la otra pc ... tkt: space acto has no repositories in the config" → covered by T5.
- L6 (2026-10-19) Slice 1 complete: 795 authored changed lines (forecast 660–1150). RDD per commit: medium/under_budget for T1, the view fix, T3, T4, T5; T2 high (escalated item 2), consent declined by the user. Next: independent verifier for T2's risk, then slice 2.
- L7 (2026-10-19) Independent verifier on slice 1: S1–S6 pass on the happy path; differential vs main identical for configured setups. Blockers: B1 `gh auth status` ran on every GitHub request (CachedTokens resolved the account before its cache), B2 account listing ignored stderr (gh prints the status there when any account fails). One correction batch with the verifier probes committed as regression tests (`TestCachedTokensResolvesEachOwnerOnce`, `TestTokenRequestsRunGhAuthStatusOnce`, `TestLoggedInAccountsReadsStatusFromTheError`, `TestStateHoldsOnlyAccountNames`); recheck: all pass, race clean. Advisories open: a wrongly cached account never re-detects; doctor/auth status ignore detected accounts and inferred owners; non-GitHub dev-status repos would be queried on GitHub; repo-level collaborators fall back to the active account. RDD consent for the whole slice declined by the user.
- L8 (2026-10-19) User: "no, vamos con slice 2 y luego publicamos" (no release of slice 1 alone). User: "si implementa lo del scroll tambien" → S9/T8 added to slice 2; slice 2 re-forecast 700–1140 (scroll 150–250), within the 2000 budget. Settings design from the prototype's Settings screen ("Spaces & repositories": repository, base branch, remove, Add repository; "saved to ~/.config/tkt/config.toml"); local paths are not in the prototype, so the TUI adds a LOCAL PATH column and a "Local clones" section for repo_roots. Accounts section left out (not requested).
- L9 (2026-10-19) RDD is off globally (`gentle-ai review mode status`: off, decided by global); no reviews started or re-enabled. Independent verifier on T7 (Risk item 1): Clone deep, validation before write, 0600/0700, atomic temp+rename write, race clean; blockers B1 saving rewrote `~/` paths as absolute (the editor got the expanded config) and B2 the board then used `~` literally. One correction batch: the editor edits the config as on disk, only the in-use copy is expanded (regression `TestBoardSaveConfigKeepsTildePaths`); recheck passes. Advisories: comments and key order are lost on save; defaults are written out; no fsync.
- L10 (2026-10-19) User: "cuando termines lo instalas aca para ver los settings". Slice 2 complete; local install for the user to try it.
- L11 (2026-10-19) User: "si implementalo así, pero recuerda que en wsl los proyectos pueden estar en wsl y no en windows". Slice 2 re-forecast: +350–500 lines (979 so far) → ~1330–1480, within 2000.
- L12 (2026-10-19) User: "tengo una pregunta es necesario tener seteado el remoto y el local? con uno delos dos basta.. cual es la diferencia?" → answered: the repository is GitHub identity (PRs, CI, reviews, account), the path only where the clone lives (`tkt start`, worktrees), found under repo_roots when unset; either fills the other: the form finds the clone from the name and reads the name from a folder's origin. T9–T11 committed together (shared files).
- L13 (2026-10-19) User (screenshot): "encontre un bug los review requested se cortan al final el texto de cantidad" → the strip now keeps room for "+N more · press 3" before adding a chip (RED `TestReviewStripKeepsRoomForTheMoreLabel`, reproduced at 160 columns: 165 wide). Inline · Risk: none.
