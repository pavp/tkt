# tkt phase 3a: a daemon for the Herdr plugin

Objective: a `tkt daemon` that syncs Jira and GitHub in one place, follows Herdr's agent events, keeps the Herdr sidebar fresh and feeds the board and `tkt list`, started automatically; no web UI or tray (later phase 3 work). Branches `feat/daemon-herdr-<n>-<slug>`, delivery **Stacked** PRs to `main` (each slice on the previous), grouped with `gh stack link` once PR 5 is open; no release until slice 5 lands. Test runner: `go test ./...` (`-race` for concurrency).
Forecast: 2010–3090 authored changed lines, budget 1000 per slice (user: "sube el presupuesto a 1000") → five slices.

## Specs

- S1. "con respecto al daemon que pasa si solo lo acotamos ahora para lo que necesitamos para el plugin de herdr?" + "si, haz la tarjeta y empieza la fase 3a": `docs/design.md` records phase 3a — the daemon subset for Herdr (sync loop, local API subset, WebSocket events, Herdr agent events, sidebar), automatic start, and the JSON snapshot cache standing in for SQLite until the web UI.
- S2. Incremental sync: a ticket's Jira development panel is asked again only when the issue's `updated` changed or its last answer is older than 10 minutes (assumption: a new pull request may not change the issue's `updated`).
- S3. `tkt daemon run` listens on `127.0.0.1` only; a 256-bit token in `<data>/token` (mode `0600`) and the port in `<data>/daemon.json`; `GET /health` unauthenticated (liveness, version, protocol); every `/v1` route needs `Authorization: Bearer <token>`.
- S4. A sync loop (every minute, and on `POST /v1/sync`) runs the existing fetch, writes the snapshot cache and serves it on `GET /v1/tickets`.
- S5. `GET /v1/ws`: `Origin` checked against the allowlist; the first message must be `{"type":"hello","token":"…","protocol":1}` or the socket closes with `4401`; envelope `{"seq","type","ts","data"}` with `seq` from 1 per connection; `snapshot` after hello.
- S6. Delta events after each sync: `ticket.updated`, `ticket.removed`, `sync.started`, `sync.finished`, `sync.failed`.
- S7. The daemon subscribes to Herdr's `pane.agent_status_changed`, maps each workspace to its ticket, updates the sidebar lines at once and sends `agent.updated`.
- S8. "vamos con A": the daemon starts automatically — the Herdr startup hook, the board and `tkt list` start it in the background when `/health` does not answer; a lock keeps one daemon per user; `tkt daemon status` and `tkt daemon stop`.
- S9. The board and `tkt list` read through the daemon when it runs (the board live from its events) and work as today when it does not; `tkt herdr restore` starts it.

## Tasks

| ID | Specs | Slice / branch | Route | Status | Commit |
|---|---|---|---|---|---|
| T1 | S1, S2 | 1 `feat/daemon-herdr-1-design-devstatus` | inline, no Writer reason | done (RED→GREEN) | c390490 (222 lines) |
| T2 | S3, S4 | 2 `feat/daemon-herdr-2-server-sync` | inline, no Writer reason | done (RED→GREEN, live smoke test) | see branch |
| T3 | S5, S6 | 3 `feat/daemon-herdr-3-ws-events` | inline, no Writer reason | todo | |
| T4 | S7, S8 | 4 `feat/daemon-herdr-4-agents-autostart` | inline, no Writer reason | todo | |
| T5 | S9 | 5 `feat/daemon-herdr-5-clients-board` | inline, no Writer reason | todo | |

> Before each commit, run `git diff --numstat main` and sum additions + deletions, skipping binary rows (`-\t-`) and generated paths (lockfiles, `__snapshots__/`, files marked `linguist-generated` in `.gitattributes`); if the current work unit — slice or single unit — is over 1000 real changed lines, stop: hand the accumulated diff to `slice-diff` (budget 1000), then re-forecast every slice not yet started with `slice-plan` (budget 1000) — a slice that overran invalidates the estimates that assumed its size, not just its own. Slices already merged stay as they are.
> Per slice, measure against its base branch (slice n against slice n-1). Native grouping: once PR 5 is open, `gh stack link <pr1> … <pr5>`; every branch in one repository; read back each PR's base and diff; `gh stack unstack <stack>` before correcting a base.

## Log

- L1 (2026-10-20) Request: "como mejorar o improvement de la carga de los tickets/estados? hay mejor manera tambien de mantener el board actualizado?" → options presented (Herdr events, cheaper sync, daemon). "la fase 3 incluiria todo?", "ese daemon que hace por detras? lanza queries a cada rato o se subscribe…?" → Jira/GitHub by polling (webhooks need a public endpoint), Herdr by subscription.
- L2 "con respecto al daemon que pasa si solo lo acotamos ahora para lo que necesitamos para el plugin de herdr?" → phase 3a scoped to Herdr. "si la fase 3a ya incluye la 1 y 2 porque hariamos primero esas y no directamente la 3a?" → go straight to 3a. "si, haz la tarjeta y empieza la fase 3a" (explicit phase approval). Start mode: "vamos con A" (automatic start).
- L3 Plan: "sube el presupuesto a 1000", strategy "stacked" (Chain was recommended; warned that slices 2–4 land an unused daemon, so no release until slice 5).
- L4 Herdr 0.9.5 API checked: `events.subscribe` with `{"subscriptions":[{"type":"pane.agent_status_changed"}]}`; events carry `workspace_id` and `agent_status`.
- L5 T1: design.md gains "Phase 3a" (section 3) and 3a/3b (section 11). `board.FetchSince` reuses a ticket's development panel while its `updated` is unchanged and the answer is under 10 minutes old (`Snapshot.Panels`, saved in the JSON cache; failed panels are not kept; panels are cloned before the ignore filter). Measured `tkt list` on macOS: 4.5 s filling the cache, then 2.7–3.0 s. "jira o github no tienen ningun servicio de websocket o algo parecido?" → no public streaming API; webhooks need a public URL and admin rights (gh webhook forward is a dev tool), so polling stays.
- L6 T2: `internal/daemon` (token 0600, daemon.json 0600 with port/pid/version, loopback listener, /health, Bearer-protected /v1/tickets and POST /v1/sync, seed from the cache, a failed sync keeps the last board) and `tkt daemon run` (config reread per sync). Live on macOS: /health ok, /v1/tickets served 4 tickets, 401 without token, bound to 127.0.0.1, daemon.json removed on SIGINT.
