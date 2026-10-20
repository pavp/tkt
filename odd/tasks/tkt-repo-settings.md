# tkt: ignore inferred repositories and complete remote repositories

Objective: inferred repositories can be ignored from Settings (keyboard and mouse), and the repository field completes from the user's GitHub repositories with Tab. Branches `feat/repo-settings-<n>-<slug>`, delivery **Stacked** PRs to `main` (slice 2 bases on slice 1). Test runner: `go test ./...`.
Forecast: 380–555 authored changed lines, budget 400 per slice (default) → two slices (110–170, 270–385).

## Specs

- S1. "no puedo borrar los repositorios inferidos por los pull request" + "ok vamos con el ignored" + "recuerda que el ignored deberia poderse hacer con mouse tambien" → `d` (or the ✕ clicked with the mouse) on an inferred repository asks for confirmation and adds it to the space's `ignored` list in the config; an ignored repository is no longer used (CI, review requests, account detection) and no longer shown in Settings. Existing configs without `ignored` stay valid.
- S3. "una pregunta en completation muestra una lista o algo asi?" → "añade el desplegable": when Tab finds several matches (repository or path field), a dropdown of up to 8 rows opens under the field: ↑↓/Tab move and show the match, enter picks without saving the form, esc closes only the dropdown, typing filters without asking GitHub again, a click picks a row.
- S2. "mostrar lista de repositorios remotos en settings.. un autocompletado o un selector?" + "ok vamos con el tab" → in the repository field, Tab completes `owner/name` from the GitHub repositories of the owners tkt knows (configured and inferred repositories); a repeated Tab cycles the matches; repositories are fetched in the background and cached for the session.

## Tasks

| ID | Specs | Slice / branch | Route | Status | Commit |
|---|---|---|---|---|---|
| T1 | S1 | 1 `feat/repo-settings-1-ignored` | inline, no Writer reason | done (RED→GREEN, independent review) | dc83e66 (303 lines) |
| T2 | S2 | 2 `feat/repo-settings-2-repo-completion` | inline, no Writer reason | done (RED→GREEN) | f4a4f2f (380 lines) |
| T3 | S3 | 3 `feat/repo-settings-3-dropdown` | inline, no Writer reason | done (RED→GREEN) | see branch |

> Before each commit, run `git diff --numstat main` and sum additions + deletions, skipping binary rows (`-\t-`) and generated paths (lockfiles, `__snapshots__/`, files marked `linguist-generated` in `.gitattributes`); if the current work unit — slice or single unit — is over 400 real changed lines, stop: hand the accumulated diff to `slice-diff` (budget 400), then re-forecast every slice not yet started with `slice-plan` (budget 400) — a slice that overran invalidates the estimates that assumed its size, not just its own. Slices already merged stay as they are.
> Per slice, measure against its base branch. Native grouping: once PR 2 is open, `gh stack link <pr1> <pr2>`.

## Log

- L1 (2026-10-20) Original request: "quiero implementar otra cosa: * mostrar lista de repositorios remotos en settings.. un autocompletado o un selector? que te parece? - otra cosa, no puedo borrar los repositorios inferidos por los pull request"
- L2 Agreed: "1. ok vamos con el tab 2. ok vamos con el ignored — recuerda que el ignored deberia poderse hacer con mouse tambien?"; plan confirmed "si, stacked". Risk: item 1 (new optional config field).
- L3 T1 independent review found: `tkt start` and `suggestRepos` still offered ignored repos (blocker); Jira-linked PRs of ignored repos still shown; the offline cache ignored the list; the prompt said "y remove"; a misleading warning. All fixed in one batch with tests (`board.DropIgnored` for cached boards; configured repos are never ignored; `--repo` still wins).
- L4 T2: Tab in the repository field completes owners (from configured and inferred repositories, ignored ones left out) and then `owner/name` from GitHub (GraphQL `repositoryOwner.repositories`, most recently pushed first, up to 500, cached per board session); a single match also looks up its local clone. Tab no longer moves between fields in the repository form; the arrows do.
- Separate fix on `fix/menu-copy-key` (from main): the actions menu shows one row per link, enter opens it and `c` copies it ("ok vamos con la A").
