# Worktree handoff

Objective: inside Herdr, `tkt start` always ends with one agent in the ticket's worktree and the main clone free. A setup agent creates the worktree; tkt hands the work to a new agent born in it.
Branch: tracker `feat/worktree-handoff` (draft), slices `feat/worktree-handoff-1-relay` → `feat/worktree-handoff-2-start`. Delivery: Chain (chosen 2026-10-28), never grouped natively (a stack would merge the draft tracker). Tests: `go test ./...`.

## Specs

- S1. Always a worktree: Start inside Herdr never works in the main clone in place; the in-place branch path (`HerdrBranchPrompt`, `checkoutFree` in Start) goes away. "siempre worktree y el principal siempre libre".
- S2. The setup agent (A) picks the branch name and base per the project's rules and creates the worktree with `herdr worktree create`, then stops: its prompt says the work continues in the new workspace. tkt never creates branches or worktrees ("el punto 3 no me cuadra y por eso prefiriria que lo hiciera el agente, porque tkt no tiene manera de saber la base").
- S3. A runs in a pane tkt creates: a split beside a pane already open at the clone, else a new workspace at the clone, so closing it never closes a pane of the user's.
- S4. tkt records a pending handoff in `state.json`: ticket, repository, A's pane, agent name and kind, the workspace when tkt created it for A, and when.
- S5. `tkt herdr on-worktree` links as today; then, when a pending handoff matches the worktree's ticket and repository, it starts the same agent kind in the new workspace's root pane (the empty shell Herdr just opened, found through `pane.list` since the `worktree_created` event carries the workspace but not its root pane) with a work prompt, focuses it, closes A (its workspace when tkt created it, else its pane), and clears the handoff.
- S6. The work prompt asks the new agent to save the ticket context, confirm the link, copy from the main clone the files git ignores that the project needs to run (such as `.env.local`) when the project's rules call for it, and implement the ticket.
- S7. "Si algo falla en el relevo, el Agente A sigue vivo y todo queda como hoy": no root pane, or the agent does not start → A is left alone, tkt notifies, the handoff is cleared. A worktree with no matching handoff is only linked, as today. Stale handoffs (older than an hour) are ignored.
- S8. Help text, README and `docs/design.md` describe the flow.

## Tasks

- [x] T1 (S4, S5, S6, S7) Relay in `on-worktree`, handoff state, Herdr `ClosePane`/`CloseWorkspace`, work prompt, tests — slice 1 — inline, no Writer reason. Commit 69e2c07 (346 lines). RED: build failed on `handoff` undefined; GREEN: `go test ./...` ok.
- [x] T2 (S1, S2, S3, S4, S8) Start runs the setup agent in a pane tkt creates and records the handoff; in-place path removed; docs, tests — slice 2 — inline, no Writer reason. Commit on feat/worktree-handoff-2-start (218 lines). RED: build failed on `SetupPrompt` undefined; GREEN: `go test ./...` ok, including the end-to-end `TestStartThenWorktreeLeavesOneAgentInTheWorktree`.

Risk: item 1 (closing an agent's pane or workspace is irreversible) → one independent verifier at the feature's end.

## Log

- L1 (2026-10-28) User pasted a report: "When tkt start opens a new ticket, its agent stays outside the ticket's Herdr workspace … What I expected: After the worktree is created and linked, the agent is in the worktree's workspace and that is the only workspace shown for the ticket." Plus ".env.local aren't present in the new worktree".
- L2 Decisions: relay (A prepares, B works) over moving the pane (cwd stays in the clone); "siempre worktree" over keeping the free-clone path; agent creates the worktree, not tkt (base unknown to tkt).
- L3 Evidence: Herdr 0.9.5 schema — event `worktree_created` = {workspace: WorkspaceInfo, worktree: WorktreeInfo}, no root pane; the `worktree.create` response carries `root_pane`. Default path `~/.herdr/worktrees/<repo>/<branch slug>`; renaming the branch leaves the workspace label and folder unchanged.
- L4 Forecast 406–660 lines → two slices; user chose Chain.
