# tkt: support Herdr desktop (herdr-app + herdr-core) beside Herdr 0.9.5

Objective: one tkt core with an adapter per Herdr (0.9.5 socket, herdr-core/herdr-app), and a native Herdr desktop mod that mirrors the TUI board, per the prototype canvas https://claude.ai/artifact/KCuagxsVRtNbn7umfzSmyY (section "Herdr desktop — tkt mod", HerdrMod.dc.html). Branch: tracker `feat/herdr-desktop` (draft) on `main`; delivery **Chain** (slice n on slice n−1, slice 1 on the tracker). Test runner: `go test ./...` (+ `go test -race ./...` for concurrency), `go vet ./...`, `gofmt -l .`; the mod is validated with `herdr-app check <mod folder>`.
Forecast: 6,350–10,620 authored changed lines → 27 slices of ≤ 400 (budget: default 400).

## Specs

- S1. Support both Herdrs: "si tkt soporta los dos herdr." Native Windows is out: "windows nativo: por ahora no darle soporte".
- S2. Architecture: "lo que me gustaria es mantener el core en un solo espacio y luego tener un par de adapters por cada uno." The domain stays free of I/O; each Herdr is an adapter behind one port.
- S3. Adapter selection: commands run inside a terminal use the Herdr they run in (`HC_TERMINAL_ID`/`HC_*` → desktop, `HERDR_ENV`/`HERDR_SOCKET_PATH` → 0.9.5), else the one reachable; when both are reachable and nothing says which, say so instead of guessing. The daemon (detached, no terminal env) follows every reachable Herdr. (From the side-agent note, verified in code: `internal/cli/daemon_start.go:151` Setsid; `internal/herdr/herdr.go:28` socket path only.)
- S4. Desktop UI is native, not a terminal: "pero no quiero que tkt para desktop sea una terminal, quiero que tenga ui nativa de herdr".
- S5. It mirrors the TUI: "basicamente que se parezcan mucho en funcionalidad y diseno pero llevado al nativo"; "importante mantener las funcionalidades iguales, la separacion de las listas igual, los bloques y eso"; "pero me gustaria mantener el mismo formado, el modo board y el modo lista". The prototype (canvas v51) is the visual spec; its component data in `renderVals()` names the screens, wording and behavior (taken from `internal/tui/*.go`).
- S6. Opening: "para el desktop hacer un mod, que se puedea abrir con un keybind". The tab-bar button is deferred: "por ahora lo del boton no, luego lo vemos". The compact panel is deferred: "el tkt mod panel lo podriamos ver luego, una vez tengamos todo implementado".
- S7. Window: "ambas opciones, la ventana separada como opción" — default the floating window inside Herdr (mod overlay kind "window"); option a separate OS window that can move to another monitor (a second herdr-app with its own short HERDR_APP_HOME and a tkt-only layout; verified by spike: "si funciona arrastrarla").
- S8. Prototype decisions the user made (keep): double-click and enter open the actions menu; Start is modal (pickers), never its own screen; details = nav bar with back chevron, breadcrumb "Sprint › KEY", previous/next arrows ("mantén las flechas"), left content + right panel (agent, pull requests, actions, work); clicking outside a popup closes it; sticky section titles with "↑/↓ N more" ("recuerda los scrolls y los sticky"); header controls = segmented Columns | List (Split | Merged), Boards/Repos filter chips with "1/2" badge, Team/Requested toggles; Settings = preferences panel with a section sidebar, grouped cards and working controls.
- S9. gpui-kit constraints (spike, verified): no handlers and no `cx.notify()` inside a `v_virtual_list` renderer; clicks via `on_item_click` (double-click = two clicks on one key within ~400 ms); sticky titles emulated by an overlay chosen from the visible range, re-rendered by `cx.timer`; no `line_clamp`; multiline input via `InputState` rows.
- S11. Testing the mod: "si, vamos con esa forma de probar el mod" — the mod splits pure logic (state, view models, menus, filters, sections, texts; no gpui-kit imports) from thin rendering; the logic is tested with Node's built-in runner (`node --test`, no npm dependencies); Go tests of each `/v1` endpoint write its JSON response as a contract fixture that the mod's JS tests read, so an API change fails a test; CI gains a `node --test` step (its own commit, Risk item 5, independent verifier); `herdr-app check` stays a local check before each mod commit.
- S10. Daemon API changes are additive within `/v1` and documented in `docs/design.md` §3 (CLAUDE.md); tkt stays the only component talking to Jira and GitHub; mutating actions keep the UI confirmation.

## Tasks

| ID | Specs | Slice / branch (base = previous) | Est. lines | Route | Status | Commit |
|---|---|---|---|---|---|---|
| T1 | S2 | 1 `feat/herdr-desktop-1-port` — Herdr port; `internal/cli` uses it; fakes | 170–400 | delegated writer (context backstop) | done (pure refactor: build, vet, gofmt clean; `go test ./...` 888 passed; `-race` cli+herdr 322 passed; differential `--help`/`herdr --help`/`doctor` identical; 391 lines) | slice 1 commit |
| T2a | S1, S2 | 2a `…-2a-core-terminals` — herdrcore: run `hc`, list terminals, labels, events | 250–400 | delegated writer (context backstop) | done (RED: build fail, then stubs failing 4 tests → GREEN 23 tests; vet, gofmt clean; read-only live check 24 panes/3 agents; 737 lines, accepted as one PR) | slice 2a commit |
| T2b | S1, S2 | 2b `…-2b-core-agents` — herdrcore: start agent, prompt, wait, keys | 250–400 | delegated writer (context backstop) | pending | |
| T2c | S1, S2 | 2c `…-2c-core-layout` — herdrcore: spaces/tabs/panes/focus via `herdr-app do` | 250–400 | delegated writer (context backstop) | pending | |
| T3a | S3 | 3a `…-3a-select` — choose Herdr by env or reachability | 100–200 | delegated writer (context backstop) | pending | |
| T3b | S3 | 3b `…-3b-daemon-both` — daemon follows both Herdrs | 140–240 | delegated writer (context backstop) | pending | |
| T4a | S5, S10 | 4a `…-4a-actions-core` — Start/StartChoices/AgentKinds out of Bubble Tea | 200–400 | delegated writer (context backstop) | pending | |
| T4b | S5, S10 | 4b `…-4b-api-snapshot` — API: snapshot with PRs, backlog, team, filters | 250–400 | delegated writer (context backstop) | pending | |
| T4c | S5, S10 | 4c `…-4c-api-start` — API: start, agents, focus, ticket pane | 250–400 | delegated writer (context backstop) | pending | |
| T4d | S5, S10 | 4d `…-4d-api-move-assign` — API: transitions/form, assign, user search | 250–400 | delegated writer (context backstop) | pending | |
| T4e | S5, S10 | 4e `…-4e-api-feedback` — API: feedback, review, clone | 250–400 | delegated writer (context backstop) | pending | |
| T4f | S5, S10 | 4f `…-4f-api-detail-config` — API: details, copy context, config read/write; design.md §3 | 250–400 | delegated writer (context backstop) | pending | |
| T5ci | S11 | 5ci `…-5ci-node-tests` — `node --test` harness for the mod logic + CI step · **Risk: item 5 (CI)** · re-forecast all T5* (S11 adds ~25–35%) before starting | 100–200 | delegated writer (context backstop) + independent verifier | pending | |
| T5a | S4, S6, S9, S11 | 5a `…-5a-mod-shell` — mod skeleton, daemon client, keybind window, header/tabs | 250–400 | delegated writer (context backstop) | pending | |
| T5b | S5, S8 | 5b `…-5b-mod-columns` — Sprint columns and cards | 300–400 | delegated writer (context backstop) | pending | |
| T5c | S5, S8, S9 | 5c `…-5c-mod-list` — list, sections, sticky titles, ↑/↓ N more | 300–400 | delegated writer (context backstop) | pending | |
| T5d | S5 | 5d `…-5d-mod-footer` — preview, legend, status line, sync, loading/empty/not set up | 300–400 | delegated writer (context backstop) | pending | |
| T5e | S5, S8 | 5e `…-5e-mod-prs` — PRs tab, Requested, bots, Repos filter | 300–400 | delegated writer (context backstop) | pending | |
| T5f | S5, S8 | 5f `…-5f-mod-backlog` — Backlog, Split/Merged, Boards filter | 300–400 | delegated writer (context backstop) | pending | |
| T5g | S5, S8 | 5g `…-5g-mod-menus` — ticket/team/PR/backlog menus, one stacked box | 300–400 | delegated writer (context backstop) | pending | |
| T5h | S5, S8 | 5h `…-5h-mod-start` — Start pickers, agent choice, clone, feedback | 300–400 | delegated writer (context backstop) | pending | |
| T5i | S5, S10 | 5i `…-5i-mod-move-assign` — Move, Assign, forms, confirmations | 300–400 | delegated writer (context backstop) | pending | |
| T5j | S5, S8 | 5j `…-5j-mod-details` — details screen | 300–400 | delegated writer (context backstop) | pending | |
| T5k | S5, S8 | 5k `…-5k-mod-settings` — Settings | 300–400 | delegated writer (context backstop) | pending | |
| T6a | S6 | 6a `…-6a-install-mod` — embed the mod; install/uninstall for desktop · **Risk: item 5 (installer writes the user's Herdr config)** | 200–350 | delegated writer (context backstop) + independent verifier | pending | |
| T6b | S6 | 6b `…-6b-install-keys` — keys.json merge with confirmation · **Risk: item 5** | 200–350 | delegated writer (context backstop) + independent verifier | pending | |
| T7a | S7 | 7a `…-7a-window-home` — separate window: seed home, open or focus · **Risk: item 5** | 200–350 | delegated writer (context backstop) + independent verifier | pending | |
| T7b | S7 | 7b `…-7b-window-option` — Settings option, README | 100–200 | delegated writer (context backstop) | pending | |

Native grouping: Chain — never grouped: a stack would merge the draft tracker.

Overrun checkpoint: Before each commit, run `git diff --numstat <base>` and sum additions + deletions, skipping binary rows (`-\t-`) and generated paths (lockfiles, `__snapshots__/`, files marked `linguist-generated` in `.gitattributes`); if the current work unit — slice or single unit — is over 400 real changed lines, stop: hand the accumulated diff to `slice-diff` (budget 400), then re-forecast every slice not yet started with `slice-plan` (budget 400) — a slice that overran invalidates the estimates that assumed its size, not just its own. Slices already merged stay as they are. (Each slice's `<base>` is the previous slice's branch; slice 1's is `feat/herdr-desktop`.)

Open decision: how the separate-window option is chosen (a Settings preference or a second keybind) — ask before T7b.

## Log

- L1 (2026-11-03): "hola, herdr lanzo su app desktop para linux/macos y windows. Analiza que hace falta para lanzar una version compatible de tkt para herdr desktop."
- L2: "windows nativo: por ahora no darle soporte / si tkt soporta los dos herdr. / lo que me gustaria es mantener el core en un solo espacio y luego tener un par de adapters por cada uno. / para el desktop hacer un mod, que se puedea abrir con un keybind y agregar un boton (quizas arriba a la derecha alado del +)"; then "por ahora lo del boton no, luego lo vemos".
- L3: "pero no quiero que tkt para desktop sea una terminal, quiero que tenga ui nativa de herdr"; "me parece bien ir poco a poco, primero con el MVP"; then full parity: "importante mantener las funcionalidades iguales, la separacion de las listas igual, los bloques y eso".
- L4: design iterations on the canvas (v33–v51): columns/list, boards filter, move/assign/form, PR and backlog actions, two-column details, sticky + scroll counts, nav bar, parity audit (30 gaps incorporated, "si, incorpora todo y mantén las flechas"), header controls, preferences-style Settings, interactive Settings.
- L5: spikes: gpui-kit virtual list (sticky emulation works; handler/notify rules); second Herdr window via HERDR_APP_HOME works and moves to another monitor ("si funciona arrastrarla"). Spike files removed.
- L6: "ambas opciones, la ventana separada como opción".
- L7: "si, arranca la fase"; slice plan confirmed with "confirmo el plan, Chain". Branches `feat/herdr-desktop` (tracker) and `feat/herdr-desktop-1-port` created from `main` at cba5b15.
- L8: T1 done by a delegated writer. `internal/herdrport` holds the port types (Worktree, Workspace, Pane, Agent, Subscription, Event) and sentinels (ErrNameTaken, ErrPaneBusy, ErrNotReady, ErrBlocked, ErrNotNamed); `internal/herdr` keeps its wire JSON in private structs and maps errors with `(*RPCError).Is` (same string checks as before). `internal/cli`: `HerdrAPI` = common port, `HerdrPlugins` = 0.9.5-only plugin/config operations reached via `plugins(h)` (`noPlugins{}` fails with `errNoPlugins`). Parent spot check: `go test ./...` 888 passed, `gofmt -l .` clean; RDD off.
- L9: side-agent note: the mod (3,000–4,500 JS lines) would have no automated tests; CI (`.github/workflows/ci.yml`) runs only gofmt, vet, `go test -race`. Recommendation accepted: "si, vamos con esa forma de probar el mod" → S11, new task T5ci before T5a; API tasks T4b–T4f also write the contract fixtures; re-forecast the T5 slices at T5ci.
- L10: T2a done (delegated writer): `internal/herdrcore` — runner (`hc`, injectable), errors (hc stderr JSON `{error:{kind,…}}`, exit 3; kinds → port sentinels), `Terminals/Panes/Agents` via `hc connect` + `terminal.list` (because `hc ls --json` has no labels), labels via `hc label` (nil → `k=`, no source prefix), `Watch` over `hc events --json` (created→pane.created, removed→pane.closed, finished/lost→pane.exited, status→pane.agent_status_changed + pane.agent_detected on first status, missed→resync diff). Gap: workspace.closed needs herdr-app (T2c). Measured 737 lines (over 400); overrun checkpoint → split proposed (2a terminals ~380 / 2a2 events ~360); user: "1 solo PR". Re-forecast T2b/T2c before starting T2b (T2a ran ~2× its estimate).
- Next: re-forecast T2b/T2c, then T2b.
