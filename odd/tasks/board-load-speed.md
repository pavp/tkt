# Board load speed and the mod's cache
Objective: make the Sprint board with the team on load fast, and the Herdr mod's cache reliable. Branch: fix/herdr-mod-polish. Delivery: commits on the branch, the user decides the PR. Runners: `go test ./...`, `node --test 'internal/herdrmod/test/*.test.js'`. Forecast ~350–450 authored lines.

## Specs
- S1 "la carga de sprint team tarda bastante podrias revisarla?" — turning the team on shows the last team tickets at once; the daemon keeps them while the team is off and filters them when serving; the first paint does not wait for the team search when earlier team tickets exist.
- S2 The mod reloads the board after the sync it asked for finishes, not before.
- S3 "creo que el cache no funciona bien" — a 503 Sprint never overwrites a good cache nor replaces cached data on screen (bug A); a team toggle saves the cache again (bug B).
- S4 The mod discovers its machine once on open, not about three times.
- S5 The daemon log trims long error bodies (GitHub HTML pages) and records each sync's duration.
- S6 Existing behavior holds: with the team off the board, counts and the PRs repository filter show only the user's tickets.

## Tasks
| ID | Specs | Route | Status |
|---|---|---|---|
| T1 | S1 S6 | inline, no Writer reason | done 8bec748: keptTeam, ForTeam, first paint; tests failed first; race clean |
| T2 | S5 | inline, no Writer reason | done 186587c: one line of at most 300 bytes; slow syncs (≥5 s) logged, not every minute's |
| T3 | S2 | inline, no Writer reason | done (mod commit): tests failed first; checked live |
| T4 | S3 | inline, no Writer reason | done (mod commit): tests failed first; checked live |
| T5 | S4 | inline, no Writer reason | done (mod commit): tests failed first; checked live |

## Log
- **L1** (2026-11-07) User: "la carga de sprint team tarda bastante podrias revisarla? ademas creo que el cache no funciona bien". Measured: GET /v1/board answers in 30 ms; a mapper found the causes (team toggle forces a full sync and drops team tickets; first paint waits for the team chain; mod reloads before the sync; cache bugs A and B; triple discovery; log dumps GitHub HTML, no sync timing). User: "aplica los cinco".
- **L2** Risk: item 4 (async ordering between the team toggle and the sync).
- **L3** Live, branch daemon: team toggle on answered in 47 ms with 22 team tickets after a completed team-off sync; syncs logged at 5.2–6.8 s. Mod: team off shows 5, on shows 22 about a second later while it refreshes. Config and board state unchanged. Deviation: slow syncs only (≥5 s) are logged, since a sync runs every minute. Turning the team off no longer asks for a sync. The running daemon is the branch build until the user restarts it.
