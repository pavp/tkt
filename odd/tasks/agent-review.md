# tkt: hand pull requests to agents from the board

Objective: from a review request, ask an agent to review it; from one of the user's pull requests with feedback, ask its agent, an open agent or a new one to resolve it, reusing what is free before opening anything. Delivery **Stacked** (budget 800, user's explicit number: "subamos el presupuesto a 800"): `feat/agent-review-1-open-agents` on `main`, then `feat/agent-review-2-new-agent` on it. Test runner: `go test ./...`.
Forecast (slice-plan, 2026-10-23): 540–920 authored changed lines; slice 1 330–520, slice 2 210–400.

## Specs

- S1. "en el menu de pr requested agrega la opcion para que un agente pueda hacerle review (aca me gustarian varias opciones, poder abrir un nuevo agente o asignarle a un agente que esta abierto)" → a review request's menu offers "Review with ▸": a new agent, an open agent ▸.
- S2. "para mis PR que tengan algun problema o requested tambien me gustaira el mismo comportamiento de poder pedirle que resuelva al agente que tenga asignado o un agente nuevo o seleccionar algun agente" → the user's pull requests with feedback offer "Send … to ▸": its agent, a new agent, another open agent ▸; `f` stays the shortcut to its agent.
- S3. "el agente solo te reporta los hallazgos en su pane, el resto sera entre el agente y yo" → the review prompt asks for findings in the agent's pane only; it never comments, approves or requests changes on GitHub.
- S4. "la idea es que siempre de lo posible si ya tiene un workspace/tab/pane disponible para trabajar porque no hay nadie trabajando (entiendase que esta libre de agentes o el workspace esta libre) utilizar ese mismo" + "puede estar trabajando pero eso no quita que luego no pueda manejar la tarea" → a new task goes first to a free agent (idle or done) in the right folder, else a new pane in that folder's workspace, else a new workspace; a working agent is never chosen automatically but listed in "an open agent ▸" marked working, where the prompt queues; the ticket's own agent gets its fix even while working; panes without an agent are never used.
- S5. "si el review en principio no deberia necesitar una rama, es algo que se le puede dejar al agente hacerlo. Tendra sus propias tools para realizar un review de una PR" → a review runs in the repository's clone without tkt choosing a branch or worktree; a fix works in the ticket's worktree, a checkout on the pull request's branch, the clone when free, or a worktree the agent creates (tkt never creates one).
- S6. Outside Herdr the menus offer "Copy the prompt" instead; every screen works in the full window and the tray panel.

## Tasks

| ID | Specs | Slice | Route | Status | Commit |
|---|---|---|---|---|---|
| T0 | S4 (queueing on a working agent) | 1 | inline, no Writer reason | done (live Herdr probe, workspace closed) | — |
| T1 | S1, S2, S3, S4, S6 (send to its agent or an open agent) | 1 | inline, no Writer reason | done (RED → GREEN; -race) | 924fd83 |
| T2 | S1, S2, S4, S5 (a new agent, placed) | 2 | inline, no Writer reason | done (RED → GREEN; -race) | slice 2 commit |

Native grouping: once the slice 2 PR is open, group with `gh stack link <pr-1> <pr-2>`; merging the top PR then takes the one below. Both branches live in pavp/tkt; read back each PR's base and diff after linking, and `gh stack unstack <stack>` dissolves the stack before any base can be corrected.

> Before each commit, run `git diff --numstat <base>` and sum additions + deletions, skipping binary rows (`-\t-`) and generated paths (lockfiles, `__snapshots__/`, files marked `linguist-generated` in `.gitattributes`); if the current work unit — slice or single unit — is over 800 real changed lines, stop: hand the accumulated diff to `slice-diff` (budget 800), then re-forecast every slice not yet started with `slice-plan` (budget 800) — a slice that overran invalidates the estimates that assumed its size, not just its own. Slices already merged stay as they are.
> Slice 1 base: `main`. Slice 2 base: `feat/agent-review-1-open-agents`.

## Log

- L1 (2026-10-23, user): "en el menu de pr requested agrega la opcion para que un agente pueda hacerle review (aca me gustarian varias opciones, poder abrir un nuevo agente o asignarle a un agente que esta abierto)"
- L2: mapped: herdr.Agents (pane, workspace, name, status, cwd), Prompt (agent.prompt), startInWorkspace (reuses an open workspace at a folder with no agent, splits when its pane is busy, else a new workspace; checkoutFree picks working in place or a worktree), agentPaneFor (the ticket's agent), deliverFeedback (f).
- L3 (2026-10-23, user): "ok vamos con stacked".
- L4 (verification T0): Herdr's docs do not say what agent.prompt does to a working agent, so a throwaway workspace (tkt-queue-test, closed after) ran a claude agent: a 500-word story prompt, then while `working` a second prompt; Herdr accepted it (status working in the response) and Claude Code ran it after the first ("FIRST-DONE", then "SECOND-DONE"). Queueing on a working agent is safe.
- L5: T1 done: tui.AgentTask / OpenAgent / AgentsListedMsg; Actions InHerdr, OpenAgents (Herdr agents with an agent, ranked free+same repo, free, busy; repo from each cwd's origin), SendTask (agent.prompt then pane.focus, board closes), CopyTaskPrompt; review prompt asks for findings in the pane only, never posting to GitHub; review requests get "Review with an open agent ▸"; a ticket with an agent keeps "Send … to the agent" plus "Send its feedback to another agent ▸", without one the fix and `f` list the agents ("… to an agent ▸"), outside Herdr both copy the prompt; the preview's f hint follows. Old fix tests now give their ticket an agent (withAgent).
- L6: T2 done: Actions.NewAgentTask; taskPlace (review → the repository's clone via Resolver; fix → ticket worktree, else a checkout on the PR's branch, else the clone with a prompt note: check the branch out when checkoutFree, else create a worktree per the project's rules and tkt link); startTaskAgent (free agent in the folder → prompt it; else split beside a pane of the folder; else new workspace; never a pane without an agent; quiet, unlike startInWorkspace which prints and takes over agentless panes for Start); menus "Review with ▸" / "… to an agent ▸" / "another agent ▸" open "A new agent" or "An open agent ▸"; f without an agent opens that choice. originRepo replaced by the existing Resolver.RepoAt. The clone is the configured path or the first found; choosing among several clones was left out.
