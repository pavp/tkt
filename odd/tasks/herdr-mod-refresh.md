# Herdr mod design refresh

Objective: implement the design approved on the prototype canvas (https://claude.ai/artifact/KCuagxsVRtNbn7umfzSmyY, row "tkt mod — design refresh proposal") in the Herdr desktop mod, dark mode first.
Branches: `feat/herdr-mod-refresh-<n>-<slug>`, one per task. Delivery: Stacked (each slice merges into `main` in order; no release until the last one). Runners: `node --test 'internal/herdrmod/test/*.test.js'`, `herdr-app check`, `go test ./...`.

## Specs

- **S1** Implement the approved canvas design: "el diseno de tkt mod herdr me parece muy plain, creo que le falta trabajo en diseno".
- **S2** Each slice is checked against the design: "recuerda siempre verificar contra el diseno qu quede realmente igual".
- **S3** Theme and legibility: "hay manera de integrarlo con los temas de herdr pero tambien tener uno propio?" · "Me gustaira que tkt respete mucho la legibilidad que por ejemplo a veces no presta los temas de herdr" · "me parece bien usar el tema captuchino e incluso mejorarlo" · "por ahora solo nos enfocamos en el modo oscuro, pero podemos dejar definid ala paleta de colores para el claro". Backgrounds always come from Herdr's theme; column and PR colors come from the Herdr theme's ANSI colors or from the tkt palette (Catppuccin Mocha, Latte defined for light); text never drops below 4.5:1 contrast and marks below 3:1, keeping their hue. The palette is chosen in Settings › Colors.
- **S4** Agent states: "que sean los mismos que los de herdr". The dot takes Herdr's exact color (idle a hollow ring); the word, where shown, is neutral.
- **S5** Fewer colors (proposal approved: "si"), with: "los iconos de diferencia el tipo de ticket si que me gustaria que mantuviera colores de diferenciacion, en review requested quizas si falta algun color mantener el ambar o amarillo que estaba pero de manera sutil." · "quita el rojo de los tickets en blocked, con tener solo la etiqueta blocked en rojo es suficiente".
- **S6** Cards: "prefiero que sigas el diseno del original pero mejorado" · product "dejalo en una sola fila" · "el titulo puede tener max 2 lineas" · "la persona asignada puede ir pegada a la derecha".
- **S7** Bottom bar: "me gustaria mantner la barra de abajo que va mostrando detalles del ticket seleccionado"; sync, refresh and the machine picker stay at the bottom, as in the original.
- **S8** Header: "veo que pusiste un boton de tema? quitalo deja el de settings y la X para cerrar".
- **S9** Lists and board keep the mod's behavior: "la lista de sprint no tiene la misma funcionalidad que el board" · "tambien revisa sprint board por si se te paso algo".
- **S10** Details: "los detalles de type, story poimnts y esos que estan en la izqueirda lo apsas al menu de la derecha".
- **S11** Menus, confirmations and Settings in the approved style ("si me gusta").
- **S12** Statuses: "en settings statuses me gustaria mejorarlo, que tal si hacer un board? y poder moverlos entre columnas?" · marks: "la manera de setear los iconos a cada status".
- **S13** TUI parity, as a follow-up feature: "recuerda mantener paridad con el tui en lo posible, si lo implementas en el mod llevalo al tui tambien".

## Tasks

| ID | Specs | Slice | Route | Status | Commit / PR |
|---|---|---|---|---|---|
| T1 | S2 S3 S4 | theme-core: `lib/theme.js` (palettes, mix, contrast guard), tests, `main.js` uses it, mono font helper | inline, no Writer reason | done: 118/118 node tests, herdr-app check passed, new tests failed before lib/theme.js existed; screenshot of the PRs tab rendered with the theme | branch feat/herdr-mod-refresh-1-theme-core |
| T2 | S2 S3 | palette-setting: daemon `palette` setting (optional, additive), Settings › Colors | inline, no Writer reason | pending | |
| T3 | S2 S5 S6 S9 | board-cards: card anatomy, columns, compact, empty, overflow, loading, warnings strip | inline, no Writer reason | pending | |
| T4 | S2 S7 S8 S9 | board-chrome: header, context line, filter notice, review strip, bottom bar, `?` keys | inline, no Writer reason | pending | |
| T5 | S2 S5 S9 | lists: Sprint list, PRs, Backlog | inline, no Writer reason | pending | |
| T6 | S2 S10 | details: Details column, grouped actions, loading | inline, no Writer reason | pending | |
| T7 | S2 S11 | menus: grouped menus, confirm dialogs, pickers, prompt | inline, no Writer reason | pending | |
| T8 | S2 S11 | settings-sections: sidebar and sections | inline, no Writer reason | pending | |
| T9 | S2 S12 | statuses-board: status board, keyboard and press-release moves, mark picker | inline, no Writer reason | pending | |

## Log

- **L1** (2026-11-06) The user's request: "el diseno de tkt mod herdr me parece muy plain, creo que le falta trabajo en diseno. Que propones? primero texto".
- **L2** (2026-11-07) Slice plan (budget 400 authored changed lines): forecast 1460–2475 lines over 9 slices, each under 400 at its high end: T1 220–350, T2 100–190, T3 190–320, T4 180–320, T5 140–275, T6 120–210, T7 160–300, T8 100–180, T9 170–290. Dependencies are linear (every slice edits `main.js`). The narrow panel is out of scope: the mod has none yet. User: "si, Stacked. recuerda siempre verificar contra el diseno qu quede realmente igual". Native grouping: once the last slice's PR is open, group with `gh stack link <bottom-pr> … <top-pr>`; merging any PR then takes every unmerged PR below it; every branch lives in one repository; read back each PR's base and diff and report that, and `gh stack unstack <stack>` dissolves the stack before any base can be corrected.
- **L3** (2026-11-07) API limits found in `gpui-kit.d.ts` that adjust the design: `font_family`, `opacity`, `on_mouse_down`/`on_mouse_up` exist; shadows are only fixed presets with no color, so the selection ring and the agent halo are drawn with borders and a backing circle; there is no rotation, so the lifted status chip only gains a shadow and a border.
- **L5** (2026-11-07) T1: `withPeach` replaced by `tktTheme(theme(), palette)` (palette defaults to Herdr's until T2 adds the setting); ticket keys and PR numbers use the terminal font (`settings().font_family`). Visual check: the mod renders with the new theme; T1 changes no layout, so the per-artboard comparison starts at T3. 209 changed lines.
- **L4** Checkpoint: before each commit, run `git diff --numstat main` (or the previous slice's branch) and sum additions + deletions; over 400 real changed lines, stop and re-plan with `slice-diff` / `slice-plan`.
