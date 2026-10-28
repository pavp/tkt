# tkt: start any agent Herdr can start, chosen by the user

Objective: tkt detects the agents installed on the machine (the kinds Herdr can start that are on PATH), asks which one when there are several, and no longer assumes Claude Code. Delivery **Stacked** to `main` (budget 400): slice 1 `feat/agent-choice-1-cli` on `main`, slice 2 `feat/agent-choice-2-board` on slice 1; grouped with `gh stack link <pr1> <pr2>` once both PRs are open. Test runner: `go test ./...`.
Forecast: 476–755 authored changed lines → 2 slices (CLI 260–390, board 216–365).

## Specs

- S1. "al abrir un ticket en un agente o enviarle bugs o lo que sea desde tkt, solo funciona con claude code? que pasa si hay otros agentes disponibles instalados?" + "si quiero soportar otros e incluso que el usuario pueda escoger con cual trabajar al momento tkt deberia preguntarle" → every place that opens a new agent (Start, "A new agent" for feedback and reviews) can open any agent kind; with several available, tkt asks which one (the last one chosen preselected).
- S2. "hay manera de no tener una lista escrita si no tkt la pueda detectar que agentes tiene disponible el usuario en la maquina?" → no list of agents in tkt: the kinds come from Herdr (`herdr agent start --help`, "[possible values: …]" of `--kind`, each "canonical executable"), kept when the executable is on PATH.
- S3. "no me cuadra lo de respaldo a claude, y si el usuario no tiene claude? en tal caso el respaldo no deberia haber y decirle al usuario un mensaje" → no fallback to claude: when Herdr's list cannot be read, or none of its kinds is installed, tkt opens nothing and says why.
- S4. Outside Herdr (`tkt start` in a plain terminal), user's choice "Abrir y copiar el prompt": tkt opens the chosen agent with no arguments and puts the prompt on the clipboard (and prints it) to paste. Assumption: this applies to claude too (no per-agent syntax table).
- S5. Texts stop naming Claude Code where any agent may be meant (start help, messages, doctor, agent status lines).

## Tasks

| ID | Specs | Route | Status | Commit |
|---|---|---|---|---|
| T1 | S1 (CLI: `--agent`, terminal ask), S2, S3, S4, S5 (CLI) | inline, no Writer reason | done (RED: build failed on `LastAgent` → GREEN; `go test ./...`, vet, gofmt clean; live `tkt doctor` → "pi, claude, opencode, copilot") | slice 1 |
| T2 | S1 (board: Start agent picker, "A new agent ▸"), S5 (board) | inline, no Writer reason | pending | |

## Log

- L1 (2026-10-28): "al abrir un ticket en un agente o enviarle bugs o lo que sea desde tkt, solo funciona con claude code? que pasa si hay otros agentes disponibles instalados? como maneja esto tkt?"
- L2: "si quiero soportar otros e incluso que el usuario pueda escoger con cual trabajar al momento tkt deberia preguntarle, pero hay manera de no tener una lista escrita si no tkt la pueda detectar que agentes tiene disponible el usuario en la maquina?"
- L3: "si pero no me cuadra lo de respaldo a claude, y si el usuario no tiene claude? en tal caso el respaldo no deberia haber y decirle al usuario un mensaje"
- L4: outside Herdr → "Abrir y copiar el prompt (Recommended)".
- L5: slice plan confirmed: "confirmo vamos con stacked".
- Evidence: Herdr 0.9.5 API schema has `AgentStartParams.kind` as a free string; only the CLI help lists kinds. `herdr integration status` reports per-agent hooks. Prompt syntax differs per agent (claude positional, opencode `--prompt`, pi positional, copilot `-i`).
- L6: slice 1 measured 492 changed lines (forecast 260–390, tests underestimated); user: "Aceptar como un PR (Recommended)".
