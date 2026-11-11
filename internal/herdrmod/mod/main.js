// tkt's board in Herdr desktop: the daemon's /v1 API, reached through `tkt api` on whichever machine of the
// core runs tkt, so it works whether tkt runs here or on a machine reached over SSH.

import { View, div, svg } from "gpui-kit";
import { v_flex, h_flex, v_virtual_list, Scrollbar, InputState, Input, TextView, VirtualListScrollHandle, Popup } from "gpui-base";
import { notify, exec, machines, shared as sharedValue, setShared, knownAgents, theme, props, closeOverlay, appHome, readTextFile, writeTextFile, settings, fonts } from "herdr";
import { tktTheme, scrim, monoFont, sourceColors, contrast, ensure, mix, CATPPUCCIN } from "./lib/theme.js";
import * as os from "os";
import * as fs from "fs/promises";
import { apiArgs, parseAnswer, machineOrder, pickMachine, agentKinds, agentRuns, failed, tktOn, outdatedNote, needsUpdate, safeModPath, tktPathFrom, shared, chosenMachine } from "./lib/client.js";
import { listRows, stickyAt, moreAround } from "./lib/list.js";
import { columnCards, cardOf, wrapWords, markOf, typeIcon, agentGlyph, cardFlag, EMPTY_COLUMN } from "./lib/columns.js";
import { gutterOf, statusTone, prStatusTone, agentLabel, prRefs, listColumns, pointsText } from "./lib/rows.js";
import { previewOf, legendFor, problemOf, warningsLine, previewBadge, syncTone, topKeys, noticeShown } from "./lib/footer.js";
import { prRows, toggled, reposChip, prPreview } from "./lib/prs.js";
import { backlogRows, badges, backlogAgents } from "./lib/backlog.js";
import { ticketMenu, prMenu, detailsMenu, withBack, backOf, confirmRemove, menuFooter, isConfirm, splitTail, itemGroup, footerKeys, prsSubmenu } from "./lib/menus.js";
import { firstPicker, answer, taskPicker, pickerKey, startingView, startingKey, startingJob, startingNotice, startingToast, resumable } from "./lib/start.js";
import { moveMenu, formMenu, assignMenu, confirmAssign, missingField } from "./lib/move.js";
import { detailView, neighbors, detailsKey, positionOf, prSummary, isBot } from "./lib/details.js";
import { SECTIONS, reposPatch, boardsPatch, statusPatch, rootsPatch, palettePatch, languagePatch, statusBoard, markChoices, dragStarted, laneAt, statusMove, grabKey } from "./lib/settings.js";
import { MOD_VERSION } from "./lib/version.js";
import { modKeyFor, withModKey } from "./lib/modkey.js";
import { tabCount, contextText, scopeNotice, routesFor, viewButton } from "./lib/header.js";
import { stripChips, atTop } from "./lib/strip.js";
import { packCache, unpackCache, cachedLabel, keptAnswer, pollAfter } from "./lib/cache.js";
import { machineKey, stashed, restored } from "./lib/machines.js";
import { scrolled, menuHeight, MENU_ROW, cardHeight, cardsBelow, revealed, keepVisible, rowsBelow, rowTop } from "./lib/scroll.js";
import { direction, tabAfter, stepList, stepColumns, targets, keyOf, boardAction, firstTarget, listKey, settingsNav } from "./lib/nav.js";
import { peopleQuery, peopleStatus, peopleItems, SEARCH_WAIT } from "./lib/people.js";
import { filterDraft, filterRows, filterKey } from "./lib/filterpanel.js";
import { completing, suggestions, accepted, ownersOf } from "./lib/complete.js";
import { matching } from "./lib/match.js";

const isPeople = (p) => p?.kind === "assignUser" || p?.kind === "fieldUser";
const completes = (p) => p?.kind === "addRepo" || p?.kind === "addRoot";
const listed = (p) => isPeople(p) || p?.kind === "choose" || completes(p);

const TABS = [
  { id: "sprint", title: "Sprint", route: "/v1/board?herdr=desktop" },
  { id: "prs", title: "PRs", route: "/v1/prs" },
  { id: "backlog", title: "Backlog", route: "/v1/backlog" },
];
// No event stream reaches a mod, so the shown tab is read again this often.
const POLL = 5000;
const ROW = 32;

/** The machine chosen in any copy of the mod: Herdr gives each copy (each layout, each block) its own storage. */
function remembered() {
  let local;
  try {
    local = JSON.parse(localStorage.getItem("machine") ?? "undefined");
  } catch {}
  return chosenMachine(sharedValue("tkt.machine"), local);
}

/** The Start the mod waits on, kept so a mod closed before it ended can tell how it did when opened again. */
function loadPending() {
  try { return JSON.parse(localStorage.getItem("pendingStart") ?? "null"); } catch { return null; }
}

function savePending(p) {
  try {
    if (p) localStorage.setItem("pendingStart", JSON.stringify(p));
    else localStorage.removeItem("pendingStart");
  } catch {}
}

export default class Tkt extends View {
  init(_props, cx) {
    this.tab = "sprint";
    this.answers = {};
    this.cachedAt = null;
    this.states = {};
    try {
      localStorage.removeItem("board");
    } catch {}
    const cached = unpackCache(localStorage.getItem(`board:${machineKey(remembered())}`), remembered());
    if (cached) {
      this.tab = cached.tab;
      this.answers = cached.answers;
      this.cachedAt = cached.at;
    }
    this.error = null;
    this.machine = undefined;
    this.choices = null;
    this.failures = 0;
    this.found = [];
    this.lookOnce = shared(() => this.discover());
    this.menu = null;
    this.picker = null;
    this.jobs = [];
    this.starting = null;
    this.details = null;
    this.settings = null;
    this.prompt = null;
    this.known = { repos: {}, folders: {}, pending: new Set(), errors: {} };
    this.form = null;
    this.input = InputState.new({ placeholder: "" });
    this.input.on("change", (_e, cx) => this.promptTyped(cx));
    this.input.on("submit", (_e, cx) => this.submitPrompt(cx));
    this.notice = "";
    this.lastClick = { key: null, at: 0 };
    this.focus = cx.focus_handle();
    this.focus.focus();
    this.scrolls = { rows: VirtualListScrollHandle.new(), "pr-rows": VirtualListScrollHandle.new(), "backlog-rows": VirtualListScrollHandle.new(), menu: VirtualListScrollHandle.new() };
    this.colPanes = {};
    this.panes = { details: { offset: 0, view: 0, content: 0 }, detailsRight: { offset: 0, view: 0, content: 0 }, settings: { offset: 0, view: 0, content: 0 } };
    this.windowHeight = 0;
    // GPUI moves the focus with tab; bound in the mod's own key context, tab switches tabs as in the TUI.
    cx.bind_keys([{ keystroke: "tab", action: "tkt::NextTab", context: "TktMod" }, { keystroke: "shift-tab", action: "tkt::PrevTab", context: "TktMod" }]);
    this.outdated = [];
    this.view = "columns";
    this.range = { start: 0, end: 0 };
    this.shownRange = { start: 0, end: 0 };
    this.selected = this.answers[this.tab]?.body ? firstTarget(targets(this.tab, "columns", this.answers[this.tab].body)) : null;
    this.inStrip = false;
    this.stripSel = 0;
    this.bots = false;
    this.panel = null;
    this.refreshBacklog = false;
    // The list learns what is on screen while it lays out, where it may not ask for a render; the pinned title and
    // the "N more" hints follow on this tick instead.
    cx.timer.every(150, (cx) => {
      if (this.range.start !== this.shownRange.start || this.range.end !== this.shownRange.end) {
        this.shownRange = { ...this.range };
        cx.notify();
      }
    });
    this.loadView(cx);
    this.offerModKey(cx);
    this.load(cx);
    cx.timer.every(POLL, (cx) => this.load(cx));
  }

  /** tkt's path on machine on: Herdr runs programs with a plain PATH, which may miss where tkt is installed
   *  (Homebrew on Linux adds itself in the interactive shell's rc), so the user's own shell is asked too. */
  async tkt(on) {
    const key = on ?? "";
    if (!this.tktPaths) this.tktPaths = {};
    if (!this.tktPaths[key]) {
      let path = null;
      try {
        const r = await exec("sh", ["-c", 'command -v tkt || "${SHELL:-sh}" -ic "command -v tkt" 2>/dev/null'], on ? { on } : {});
        path = tktPathFrom(r.stdout);
      } catch {}
      this.tktPaths[key] = path ?? "tkt";
    }
    return this.tktPaths[key];
  }

  async run(args, on) {
    return parseAnswer(await exec(await this.tkt(on), args, on ? { on } : {}));
  }

  /** The machines whose tkt the mod can use, asked with `tkt api --help`, which starts no daemon; a tkt too old
   *  for `tkt api` is set apart so the person can be told to update it. */
  async discover() {
    const found = [];
    this.outdated = [];
    for (const on of machineOrder(machines(), remembered())) {
      let r = null;
      try {
        r = await exec(await this.tkt(on), ["api", "--help"], on ? { on } : {});
      } catch {}
      const state = tktOn(r);
      if (state === "ready") found.push(on);
      if (state === "old") this.outdated.push(on);
    }
    this.found = found;
    const pick = pickMachine(found, remembered(), this.misses ?? 0);
    this.misses = pick.wait !== undefined ? (this.misses ?? 0) + 1 : 0;
    return pick;
  }

  /** Opens the list of machines with tkt, looked for again, so the person can switch. */
  pick(cx) {
    cx.spawn(async (cx) => {
      await this.discover();
      this.choices = this.found.length > 0 ? this.found : null;
      if (!this.choices) this.error = "tkt is not installed on this computer or on any machine connected to Herdr";
      cx.notify();
    });
  }

  /** The machines tkt runs on, as a popup over the bottom bar's machine button. */
  openMachines(cx) {
    if (this.machineMenu) { this.machineMenu = null; cx.notify(); return; }
    cx.spawn(async (cx) => {
      await this.discover();
      const items = this.found ?? [];
      if (items.length === 0) this.error = "tkt is not installed on this computer or on any machine connected to Herdr";
      else this.machineMenu = { items, sel: Math.max(0, items.findIndex((on) => machineKey(on) === machineKey(this.machine))) };
      cx.notify();
    });
  }

  machineBox(t) {
    const m = this.machineMenu;
    return v_flex().id("machines").my(4).w(260).p(6).gap(1).rounded(10).bg(t.popover).border(1).border_color(t.border).shadow_lg()
      .on_mouse_down("left", (_e, cx) => cx.stop_propagation())
      .child(div().px(8).pt(4).pb(6).text_size(11).font_semibold().text_color(t.muted).child("Show tkt on"))
      .children(m.items.map((on, i) => {
        const current = machineKey(on) === machineKey(this.machine);
        const sel = i === m.sel;
        return h_flex().id(`machine-${i}`).h(30).px(8).gap(8).items_center().rounded(6).bg(sel ? mix(t.accent, t.popover, 0.12) : t.popover).hover((h) => h.bg(t.hover))
          .on_click((_e, cx) => { this.machineMenu = null; this.use(on, cx); cx.notify(); })
          .child(div().flex_1().text_size(13).text_color(t.foreground).child(on ?? "This computer"))
          .when(current, (el) => el.child(div().text_size(12).text_color(t.accent).child("✓")));
      }));
  }

  /** Shows tkt on machine on; keep saves it as the choice of every copy, which a machine used only because the
   *  chosen one stopped answering does not. */
  use(on, cx, keep = true) {
    const switched = this.machine !== undefined && machineKey(this.machine) !== machineKey(on);
    if (switched) {
      this.states[machineKey(this.machine)] = stashed(this);
      Object.assign(this, restored(this.states, on, unpackCache(localStorage.getItem(`board:${machineKey(on)}`), on)));
      this.details = null;
      this.menu = null;
      this.panel = null;
      this.cacheSaved = 0;
    }
    this.machine = on;
    if (switched) this.loadView(cx);
    this.choices = null;
    this.misses = 0;
    if (keep) {
      setShared("tkt.machine", { on: on ?? null });
      try {
        localStorage.setItem("machine", JSON.stringify(on ?? null));
      } catch {}
    }
    cx.spawn(async (cx) => {
      try {
        const h = await this.call("GET", "/health");
        const v = h.body?.version;
        if (needsUpdate(MOD_VERSION, v)) {
          try {
            await this.updateFrom(on);
            this.notice = `updated the tkt mod to ${v}, as tkt on ${on ?? "this computer"}`;
          } catch (e) {
            this.notice = `the tkt mod (${MOD_VERSION}) differs from tkt on ${on ?? "this computer"} (${v}) and could not update: ${e.message}`;
          }
          cx.notify();
        }
      } catch {}
      try {
        const st = await this.call("GET", "/v1/settings");
        if (st.status === 200 && st.body?.palette !== this.palette) {
          this.palette = st.body.palette;
          cx.notify();
        }
      } catch {}
      try {
        const known = await knownAgents(on ? { on } : {});
        const runs = agentRuns(known);
        await this.call("POST", "/v1/herdr/agents", Object.keys(runs).length > 0 ? { agents: agentKinds(known), runs } : { agents: agentKinds(known) });
      } catch {}
    });
    this.load(cx);
  }

  /** One daemon request on the machine whose tkt is used; with none chosen yet it looks, and asks when tkt is on
   *  several. */
  async call(method, path, body) {
    if (this.machine === undefined) {
      const pick = await this.lookOnce();
      if (pick.none && this.outdated.length > 0) throw new Error(outdatedNote(this.outdated));
      if (pick.none) throw new Error("tkt is not installed on this computer or on any machine connected to Herdr");
      if (pick.wait !== undefined) throw new Error(`tkt on ${pick.wait ?? "this computer"} is not answering yet; trying again`);
      if (pick.choose) {
        this.choices = pick.choose;
        throw new Error("tkt is on several machines: choose one");
      }
      if (this.machine === undefined) {
        const chosen = remembered();
        this.machine = pick.machine;
        this.pendingUse = true;
        this.keepPick = chosen === undefined || chosen === pick.machine;
      }
    }
    try {
      const answer = await this.run(apiArgs(method, path, body), this.machine);
      this.failures = 0;
      return answer;
    } catch (e) {
      const next = failed(this.failures ?? 0);
      this.failures = next.failures;
      if (next.lookAgain) this.machine = undefined;
      throw e;
    }
  }

  /** Asks how the Starts and tasks answered 202 ended, since no event stream reaches a mod. */
  async followJobs() {
    for (const job of [...this.jobs]) {
      const a = await this.call("GET", `/v1/jobs/${job.id}`);
      if (a.status === 200 && a.body.state === "running") continue;
      this.jobs = this.jobs.filter((j) => j !== job);
      if (a.status === 200 && a.body.state === "failed") this.notice = `${job.what} failed: ${a.body.message}`;
      else if (a.status === 200) this.notice = `${job.what} finished`;
    }
  }

  load(cx) {
    this.resumeStart(cx);
    const routes = routesFor(TABS, this.tab, { bots: this.bots, refreshBacklog: this.refreshBacklog });
    if (this.tab === "backlog") this.refreshBacklog = false;
    cx.spawn(async (cx) => {
      try {
        const answers = await Promise.all(routes.map(([, route]) => this.call("GET", route)));
        routes.forEach(([id], i) => { this.answers[id] = keptAnswer(this.answers[id], answers[i]); });
        if (routes.every(([id], i) => this.answers[id] === answers[i])) this.cachedAt = null;
        if (Date.now() - (this.cacheSaved ?? 0) > 60000) {
          this.cacheSaved = Date.now();
          try {
            localStorage.setItem(`board:${machineKey(this.machine)}`, packCache(this.machine, this.answers, this.cacheSaved, this.tab));
          } catch {}
        }
        const body = this.answers[this.tab]?.body;
        if (body && !this.selected) this.selected = firstTarget(targets(this.tab, this.view, body));
        if (this.jobs.length > 0) await this.followJobs();
        this.error = null;
        if (this.pendingUse) {
          this.pendingUse = false;
          this.use(this.machine, cx, this.keepPick ?? true);
        }
        const soon = pollAfter(answers[routes.findIndex(([id]) => id === this.tab)]);
        if (soon && !this.soonLoad) {
          this.soonLoad = true;
          cx.timer.after(soon, (cx) => { this.soonLoad = false; this.load(cx); });
        }
      } catch (e) {
        this.error = e.message;
      }
      cx.notify();
    });
  }

  loadView(cx) {
    cx.spawn(async (cx) => {
      try {
        const a = await this.call("GET", "/v1/board/state");
        if (a.status === 200 && a.body?.view) this.view = a.body.view;
      } catch {}
      cx.notify();
    });
  }

  /** Columns or list, saved as the board's view, which the TUI reads when it opens. */
  setView(view, cx) {
    this.view = view;
    cx.spawn(async () => {
      try {
        await this.call("PATCH", "/v1/board/state", { view });
      } catch {}
    });
    cx.notify();
  }

  /** Changes the board's saved view (shared with the TUI) and reads the shown tab again. */
  patchState(change, cx) {
    cx.spawn(async (cx) => {
      try {
        await this.call("PATCH", "/v1/board/state", change);
      } catch (e) {
        this.error = e.message;
      }
      this.load(cx);
      cx.notify();
    });
  }

  get notice() {
    return noticeShown(this._notice, this._noticeAt ?? 0, Date.now()) ? this._notice : "";
  }

  set notice(text) {
    this._notice = text;
    this._noticeAt = Date.now();
  }

  theme() {
    return tktTheme(theme(), this.palette);
  }

  icon(name, size = 16) {
    return svg(`mods/tkt/icons/${name}.svg`).w(size).h(size).flex_none();
  }

  iconButton(t, id, name, onClick) {
    return h_flex().id(id).w(32).h(32).flex_none().items_center().justify_center().rounded(7).text_color(t.muted).hover((s) => s.bg(t.hover).text_color(t.foreground))
      .on_mouse_down("left", (_e, cx) => cx.stop_propagation()).on_click((_e, cx) => onClick(cx)).child(this.icon(name));
  }

  /** Options side by side in one rounded box, the chosen one raised, as the header's switches. */
  segment(t, id, options) {
    return h_flex().id(id).p(3).gap(2).rounded(8).border(1).border_color(t.border).bg(t.surface).children(options.map((o, i) =>
      h_flex().id(`${id}-${i}`).h(26).px(10).items_center().rounded(5).text_size(12).font_weight(500).bg(o.on ? t.pill : t.surface)
        .text_color(o.on ? t.foreground : t.muted).hover((s) => s.text_color(t.foreground))
        .on_mouse_down("left", (_e, cx) => cx.stop_propagation()).on_click((_e, cx) => { if (!o.on) o.click(cx); }).child(o.label)));
  }

  /** A filter's button: the funnel, its name, and how many it shows when it hides some. */
  filterButton(t, id, label, on, onClick) {
    return h_flex().id(id).h(32).px(10).gap(6).items_center().rounded(8).border(1).border_color(on ? t.accent : t.border).text_size(12).font_weight(500)
      .text_color(t.muted).hover((s) => s.text_color(t.foreground)).on_mouse_down("left", (_e, cx) => cx.stop_propagation()).on_click((_e, cx) => onClick(cx))
      .child(this.icon("filter", 13)).child(label);
  }

  /** el in the terminal's font, for ticket keys, PR numbers and paths. */
  mono(el) {
    if (this.monoFamily === undefined) {
      try { this.monoFamily = monoFont(settings(), fonts()); } catch { this.monoFamily = ""; }
    }
    return this.monoFamily ? el.font_family(this.monoFamily) : el;
  }

  checkbox(t, on) {
    return h_flex().w(18).h(18).flex_none().rounded(4).border(1).border_color(on ? t.accent : t.muted).bg(on ? t.accent : t.popover)
      .items_center().justify_center().text_size(13).font_semibold().text_color(t.background).child(on ? "✓" : "");
  }

  toggle(id, on, onClick) {
    const t = this.theme();
    return div().id(id).px(10).py(2).rounded(5).border(1).border_color(on ? t.accent : t.border)
      .text_color(on ? t.foreground : t.muted).bg(on ? t.selected : t.chrome).hover((s) => s.bg(t.hover))
      .on_mouse_down("left", (_e, cx) => cx.stop_propagation())
      .on_click((_e, cx) => onClick(cx));
  }

  prControls(t) {
    const view = this.answers.prs?.body;
    if (!view) return div();
    const showBots = this.bots || view.hidden_bots > 0;
    return h_flex().gap(8).items_center()
      .child(this.segment(t, "team-prs", [
        { label: "Requested", on: !view.team_prs, click: (cx) => this.patchState({ team_prs: false }, cx) },
        { label: "Team", on: Boolean(view.team_prs), click: (cx) => this.patchState({ team_prs: true }, cx) }]))
      .when(showBots, (el) => el.child(h_flex().id("bots").h(32).px(10).items_center().rounded(8).border(1).border_color(this.bots ? t.accent : t.border)
        .text_size(12).font_weight(500).text_color(this.bots ? t.foreground : t.muted).hover((s) => s.text_color(t.foreground))
        .on_mouse_down("left", (_e, cx) => cx.stop_propagation()).on_click((_e, cx) => { this.bots = !this.bots; this.load(cx); cx.notify(); })
        .child(this.bots ? "Bots shown" : `Bots ${view.hidden_bots} hidden`)))
      .when((view.repos ?? []).length > 0, (el) => el.child(this.filterButton(t, "repos", reposChip(view.repos), this.panel === "repos", (cx) => { this.togglePanel("repos"); cx.notify(); })));
  }

  /** Opens a filter with a draft of what it hides, or closes it, dropping the draft. */
  togglePanel(name) {
    this.panel = this.panel === name ? null : name;
    this.panelDraft = this.panel ? filterDraft(this.openPanel()?.hidden) : null;
  }

  /** Saves the draft of the open filter, when it differs, and closes it. */
  applyPanel(cx) {
    const p = this.openPanel();
    const d = this.panelDraft;
    this.togglePanel(this.panel);
    const same = (p?.hidden ?? []).length === d.hidden.length && d.hidden.every((n) => (p?.hidden ?? []).includes(n));
    if (p && !same) this.patchState({ [p.field]: d.hidden }, cx);
  }

  /** A filter's panel under the header: typed text narrows it, ticks change a draft, Apply saves it. */
  filterPanel(t) {
    const p = this.openPanel();
    const d = this.panelDraft ?? filterDraft(p.hidden);
    const rows = filterRows(p.items, d);
    const noun = this.panel === "repos" ? "repositories" : "boards";
    const button = (id, label, primary, click) => h_flex().id(id).h(26).px(12).items_center().rounded(6).text_size(12)
      .when(primary, (el) => el.bg(t.accent).font_semibold().text_color(ensure(t.chrome, t.accent, 4.5)))
      .when(!primary, (el) => el.border(1).border_color(t.border).text_color(t.foreground).hover((s) => s.bg(t.hover)))
      .on_click((_e, cx) => { click(cx); cx.notify(); }).child(label);
    return v_flex().id(`${p.field}-panel`).absolute().top(44).right(48).w(300).p(6).gap(2).rounded(8).bg(t.popover).border(1).border_color(t.border)
      .on_mouse_down("left", (_e, cx) => cx.stop_propagation())
      .child(h_flex().h(28).mb(4).px(8).gap(6).items_center().rounded(6).border(1).border_color(d.query ? t.accent : t.border).bg(t.surface).text_size(12)
        .child(div().text_color(t.dim).child("⌕"))
        .child(d.query ? div().flex_1().min_w_0().truncate().text_color(t.foreground).child(`${d.query}▏`) : div().flex_1().text_color(t.dim).child(`Type to filter ${noun}`)))
      .when(rows.length === 0, (el) => el.child(div().px(8).py(6).text_size(12).text_color(t.muted).child(`No ${noun} match “${d.query}”`)))
      .children(rows.map((r, i) =>
        h_flex().id(`${p.field}-${r.name}`).gap(8).px(8).py(4).rounded(5).bg(i === d.sel ? t.selected : t.popover).hover((s) => s.bg(t.hover))
          .on_click((_e, cx) => { this.panelDraft = filterKey({ ...d, sel: i }, p.items, " ").draft; cx.notify(); })
          .child(this.checkbox(t, !r.hidden))
          .child(div().flex_1().truncate().text_color(r.hidden ? t.muted : t.foreground).child(r.name))
          .child(div().text_color(t.dim).child(String(r.prs ?? r.tickets ?? "")))))
      .child(h_flex().mt(4).pt(6).px(2).gap(6).items_center().border_t(1).border_color(t.border)
        .child(div().flex_1().min_w_0().truncate().text_size(11).text_color(t.muted).child("space tick · ctrl-a all or none"))
        .child(button("panel-cancel", "Cancel", false, () => this.togglePanel(this.panel)))
        .child(button("panel-apply", "Apply", true, (cx) => this.applyPanel(cx))));
  }

  openPanel() {
    const view = this.answers[this.tab]?.body;
    if (!this.panel || !view) return null;
    if (this.panel === "repos") return { items: view.repos ?? [], hidden: view.hidden_repos, field: "hidden_repos" };
    return { items: view.boards ?? [], hidden: view.hidden_boards, field: "hidden_boards" };
  }

  /** The Sprint's Team toggle: the team's tickets come with a sync, so the change asks for one, as the TUI's t does. */
  teamToggle(t) {
    const view = this.answers.sprint?.body;
    if (!view) return div();
    return this.segment(t, "team", [{ label: "Mine", on: !view.team, click: (cx) => this.toggleTeam(cx) }, { label: "Team", on: Boolean(view.team), click: (cx) => this.toggleTeam(cx) }]);
  }

  /** Turns the team's tickets on or off on the Sprint, then syncs, since they come from Jira. */
  toggleTeam(cx) {
    const view = this.answers.sprint?.body;
    if (!view) return;
    this.attempt(async (cx) => {
      const a = await this.call("PATCH", "/v1/board/state", { team: !view.team });
      if (a.status !== 200) throw new Error(a.body?.message ?? `tkt answered ${a.status}`);
      // tkt keeps the last team tickets while the team is off, so only turning it on needs them read again.
      if (!view.team) await this.call("POST", "/v1/sync", {});
      this.notice = view.team ? "only your tickets" : "with the team's tickets · syncing…";
      this.cacheSaved = 0;
      this.load(cx);
    }, cx);
  }

  boardsChip(t) {
    const boards = this.answers[this.tab]?.body?.boards ?? [];
    if (boards.length === 0) return div();
    const shown = boards.filter((b) => !b.hidden).length;
    return this.filterButton(t, "boards", shown === boards.length ? "Boards" : `Boards ${shown}/${boards.length}`, this.panel === "boards", (cx) => { this.togglePanel("boards"); cx.notify(); });
  }

  backlogControls(t) {
    const view = this.answers.backlog?.body;
    if (!view) return div();
    return h_flex().gap(8).items_center()
      .child(this.segment(t, "merge", [
        { label: "Split", on: !view.merged, click: (cx) => this.patchState({ backlog_merged: false }, cx) },
        { label: "Merged", on: Boolean(view.merged), click: (cx) => this.patchState({ backlog_merged: true }, cx) }]))
      .child(this.boardsChip(t));
  }

  backlogRow(t, r) {
    if (r.kind === "title") {
      const b = badges(r.points);
      const tone = { todo: t[statusTone("todo")] ?? t.muted, doing: t[statusTone("in_progress")] ?? t.ansi4, done: t[statusTone("done")] ?? t.ansi2 };
      const chip = mix(t.ansi4, t.surface, 0.14);
      return this.sectionTitle(t, r, { fold: Boolean(r.collapsed), sel: this.selected === `group:${r.group}`,
        after: [...(r.board ? [div().px(8).rounded(4).bg(chip).text_size(11).text_color(ensure(mix(t.ansi4, t.foreground, 0.45), chip, 4.5)).child(r.board)] : []),
          ...(r.dates ? [div().text_size(11).text_color(t.muted).child(r.dates)] : [])],
        extra: [div().flex_1(), ...(b?.unestimated ? [div().text_size(11).text_color(t.muted).child(b.unestimated)] : []),
          ...(b ? b.stages.map(([stage, n]) => h_flex().gap(5).items_center().child(div().w(7).h(7).rounded(4).bg(tone[stage])).child(this.mono(div().text_size(11).text_color(t.muted)).child(n))) : [])] });
    }
    return this.listRow(t, r, { agents: backlogAgents(this.answers) }, this.backlogCols, { backlog: true });
  }

  backlogList(t, view) {
    if (view.no_team_boards) return div().p(16).text_color(t.muted).child("No team boards set: choose them in Settings › Team boards to see their sprints and backlog here.");
    const rows = backlogRows(view);
    this.backlogCols = { ...listColumns(rows.filter((r) => r.kind === "row"), backlogAgents(this.answers)), assignee: true };
    return this.tableBox(t, v_flex().id("backlog").relative().flex_1().min_h_0()
      .child(this.listHeader(t, ["KEY", "STATUS", ""], this.backlogCols, { backlog: true }))
      .child(v_virtual_list("backlog-rows", rows.length, ROW, (i) => `${i}`, (range) =>
        rows.slice(range.start, range.end).map((r) => this.backlogRow(t, r))).size_full().track_scroll(this.scrolls["backlog-rows"]).on_item_click((key, cx) => {
        const r = rows[Number(key)];
        if (!r) return;
        if (r.kind === "title") this.patchState({ collapsed: toggled(view.collapsed, r.group) }, cx);
        else {
          this.selected = r.key;
          this.clicked(r.key, () => this.menuFor(r), cx);
        }
      }))
      .child(Scrollbar.vertical("backlog-rows").absolute().inset_0()));
  }

  prRow(t, r) {
    if (r.kind === "title") {
      return this.sectionTitle(t, r, { fold: Boolean(r.collapsed), sel: this.selected === `group:${r.group}`,
        extra: r.needs ? [div().text_size(11).text_color(ensure(t.ansi3, t.surface, 4.5)).child(`${r.needs} need you`)] : [] });
    }
    const sel = this.selected === r.key + r.group && !this.inStrip;
    const g = gutterOf({ selected: sel, needsYou: r.needsYou || r.review });
    const pr = r.raw.pr ?? {};
    const ci = { pass: ["✓", t.ansi2], fail: ["✕", ensure(t.ansi1, t.surface, 4.5)], pending: ["…", t.muted] }[pr.checks] ?? ["—", t.dim];
    const tone = t[prStatusTone(r.raw)] ?? t.foreground;
    return this.rowBox(t, sel)
      .child(div().w(20).flex_none().text_size(12).font_semibold().text_color(t[g.tone] ?? t.muted).child(g.text))
      .child(this.mono(div().w(44).flex_none().text_size(11).text_color(sel ? t.accent : t.muted)).child(r.key))
      .child(h_flex().w(160).flex_none().gap(4).child(div().truncate().text_size(12).text_color(ensure(tone, t.surface, 4.5)).child(r.status))
        .when(Boolean(r.approvals), (el) => el.child(div().text_size(12).text_color(t.muted).child(`· ${r.approvals}`))))
      .child(div().w(140).flex_none().truncate().text_size(12).text_color(t.muted).child(pr.author ?? ""))
      .child(div().w(24).flex_none().font_semibold().text_color(ci[1] ?? t.dim).child(ci[0]))
      .child(div().flex_1().min_w_0().truncate().text_size(13).text_color(pr.draft ? t.muted : t.foreground).child(r.title));
  }

  prsList(t, view) {
    const rows = prRows(view);
    if (rows.length === 0) return div().p(16).text_color(t.muted).child("No pull requests: none of yours open, and no review requested.");
    const authors = rows.some((r) => r.kind === "row" && r.raw.pr?.author);
    return this.tableBox(t, v_flex().id("prs").relative().flex_1().min_h_0()
      .child(this.listHeader(t, ["PR", "STATUS", "CI"], { assignee: authors }, { prs: true }))
      .child(v_virtual_list("pr-rows", rows.length, ROW, (i) => `${i}`, (range) =>
        rows.slice(range.start, range.end).map((r) => this.prRow(t, r))).size_full().track_scroll(this.scrolls["pr-rows"]).on_item_click((key, cx) => {
        const r = rows[Number(key)];
        if (!r) return;
        if (r.kind === "title") this.patchState({ collapsed: toggled(view.collapsed, r.group) }, cx);
        else {
          this.selected = r.key + r.group;
          this.clicked(this.selected, () => this.menuFor(r), cx);
        }
      }))
      .child(Scrollbar.vertical("pr-rows").absolute().inset_0()));
  }

  /** A click selects; a second one on the same item within 400 ms opens its menu (S9: no native double-click). */
  clicked(key, open, cx) {
    const now = Date.now();
    const double = this.lastClick.key === key && now - this.lastClick.at < 400;
    this.lastClick = { key, at: now };
    if (double) this.menu = { ...open(), sel: 0 };
    cx.notify();
  }

  menuFor(row) {
    if (this.tab === "prs") return prMenu(row.raw, { agent: this.answers.sprint?.body?.agents?.[row.ticketKey || row.raw.feedback_key] });
    const agents = this.answers.sprint?.body?.agents ?? {};
    if (this.tab === "backlog") return ticketMenu(row.ticket, { backlog: true, me: this.answers.backlog?.body?.me, agent: agents[row.key], groups: this.answers.backlog?.body?.groups, activeSprints: this.answers.backlog?.body?.active_sprints, prLines: row.card?.pull_requests });
    return ticketMenu(row.ticket, { agent: agents[row.key], attention: row.card?.attention, prLines: row.card?.pull_requests });
  }

  async local(script, arg) {
    const r = await exec("sh", ["-c", script, "sh", arg]);
    if (r.code !== 0) throw new Error((r.stderr || "it did not run").trim());
  }

  /** Offers once to bind the key that opens tkt in this window's keys.json, when no key opens it yet: install
   *  binds it only where tkt runs, and Herdr desktop's window may run on another machine (such as Windows). */
  offerModKey(cx) {
    if (localStorage.getItem("modKeyOffer") === "declined") return;
    cx.spawn(async (cx) => {
      try {
        const path = `${appHome()}/keys.json`;
        const before = await readTextFile(path);
        const keyName = modKeyFor(os.platform());
        const r = withModKey(before, keyName);
        if (r.status !== "added" || this.menu) return;
        this.menu = {
          title: `Open tkt with ${keyName}?`,
          subtitle: "keys.json gets this one binding; Herdr then stops adding its own new default keys to that file.",
          sel: 0,
          items: [
            { label: `Yes, bind ${keyName}`, action: "bindModKey", path, before, after: r.text, keyName },
            { label: "No thanks", action: "declineModKey", keyName },
          ],
        };
        cx.notify();
      } catch {}
    });
  }

  /** Runs a menu item; the screens Start, Move, Assign, details and feedback open come with their own tasks. */
  act(item, cx) {
    const from = this.menu;
    this.menu = null;
    cx.spawn(async (cx) => {
      try {
        switch (item.action) {
          case "open":
            await this.local('open "$1" 2>/dev/null || xdg-open "$1"', item.url);
            this.notice = "opened ↗";
            break;
          case "copy":
            await this.local('printf %s "$1" | (pbcopy 2>/dev/null || wl-copy 2>/dev/null || xclip -selection clipboard)', item.value);
            this.notice = `copied ${item.what ?? item.value}`;
            break;
          case "copyContext": {
            const a = await this.call("GET", `/v1/tickets/${item.key}/context`);
            if (a.status !== 200) throw new Error(a.body?.message ?? `tkt answered ${a.status}`);
            await this.local('printf %s "$1" | (pbcopy 2>/dev/null || wl-copy 2>/dev/null || xclip -selection clipboard)', a.body.text);
            this.notice = `copied the context of ${item.key}`;
            break;
          }
          case "moveToSprint": {
            const a = await this.call("POST", `/v1/tickets/${item.key}/sprint`, { sprint: item.sprint });
            if (a.status !== 200) throw new Error(a.body?.message ?? `tkt answered ${a.status}`);
            this.notice = `moved ${item.key} to ${item.name}`;
            this.refreshBacklog = true;
            this.load(cx);
            break;
          }
          case "bindModKey":
            await writeTextFile(item.path, item.after, item.before);
            this.notice = `${item.keyName} opens tkt`;
            break;
          case "declineModKey":
            localStorage.setItem("modKeyOffer", "declined");
            this.notice = `no key bound; bind ${item.keyName} to mod.open:tkt in keys.json any time`;
            break;
          case "settingsPatch":
            await this.patchSettings(item.change);
            break;
          case "details":
            await this.openDetails(item.key);
            break;
          case "start":
            await this.openStart(item.key, cx);
            break;
          case "notYours":
            this.menu = { title: `${item.key} is not yours`, subtitle: "", sel: 0, items: [
              { label: "Assign it to me, then start", action: "assignMeStart", key: item.key },
              { label: "Start without assigning", action: "start", key: item.key },
              { label: "Cancel", action: "cancel" },
            ] };
            break;
          case "assignMeStart": {
            const a = await this.call("PUT", `/v1/tickets/${item.key}/assignee`, { to: "me" });
            if (a.status !== 200) throw new Error(a.body?.message ?? `tkt answered ${a.status}`);
            await this.openStart(item.key, cx);
            break;
          }
          case "fix":
            await this.sendTask(item.pr ? { feedback: item.pr, to: "own" } : { ticket: item.key, to: "own" });
            break;
          case "feedbackTo":
            if (item.pr) await this.openTask({ feedback: item.pr }, `feedback=true&repo=${encodeURIComponent(item.pr.repo)}&number=${item.pr.number}`);
            else await this.openTask({ ticket: item.key }, `ticket=${encodeURIComponent(item.key)}`);
            break;
          case "review":
            await this.openTask({ review: { repo: item.pr.repo, number: item.pr.number } }, `repo=${encodeURIComponent(item.pr.repo)}&number=${item.pr.number}`);
            break;
          case "cancel":
            this.form = null;
            break;
          case "submenu":
            this.menu = { ...item.menu, sel: 0 };
            break;
          case "move": {
            const a = await this.call("GET", `/v1/tickets/${item.key}/transitions`);
            if (a.status !== 200) throw new Error(a.body?.message ?? `tkt answered ${a.status}`);
            this.menu = { ...moveMenu(item.key, a.body), sel: 0 };
            break;
          }
          case "form":
            this.form = { key: item.key ?? this.formKey(item), move: item.move, values: {}, comment: "" };
            this.menu = { ...formMenu(this.form.key, item.move, this.form), sel: 0 };
            break;
          case "fieldValue":
            if (item.value) this.form.values[item.field.id] = item.value;
            else delete this.form.values[item.field.id];
            this.menu = { ...formMenu(this.form.key, this.form.move, this.form), sel: 0 };
            break;
          case "comment":
            this.ask({ kind: "comment", title: "Comment", value: this.form.comment });
            break;
          case "fieldUser":
            this.ask({ kind: "fieldUser", title: `${item.field.name}: search someone`, field: item.field, key: this.form.key });
            break;
          case "transition": {
            const missing = missingField(item.move, item.values);
            if (missing) {
              this.notice = missing;
              this.menu = { ...formMenu(this.form.key, this.form.move, this.form), sel: 0 };
              break;
            }
            const values = Object.fromEntries(Object.entries(item.values).map(([id, v]) => [id, v.id]));
            const a = await this.call("POST", `/v1/tickets/${item.key}/transitions`, { id: item.move.id, values, comment: item.comment });
            if (a.status !== 200) throw new Error(a.body?.message ?? `tkt answered ${a.status}`);
            this.form = null;
            this.notice = `moved ${item.key} to ${item.move.to}`;
            this.load(cx);
            break;
          }
          case "assignMenu":
            this.menu = { ...assignMenu(item.key, item.now), sel: 0 };
            break;
          case "confirmAssign":
            this.menu = { ...confirmAssign(item.key, item.who), sel: 0 };
            break;
          case "searchUser":
            this.ask({ kind: "assignUser", title: `Assign ${item.key} to`, key: item.key });
            break;
          case "assign": {
            const a = await this.call("PUT", `/v1/tickets/${item.key}/assignee`, item.who);
            if (a.status !== 200) throw new Error(a.body?.message ?? `tkt answered ${a.status}`);
            this.notice = `assigned ${item.key} to ${item.who.to === "me" ? "you" : item.who.to === "nobody" ? "nobody" : item.who.name}`;
            this.load(cx);
            break;
          }
          default:
            this.notice = `${item.label.replace(" ▸", "")}: not in the Herdr desktop window yet`;
        }
      } catch (e) {
        this.notice = `could not: ${e.message}`;
      }
      this.menu = withBack(this.menu, from);
      cx.notify();
    });
    cx.notify();
  }

  formKey() {
    return this.form?.key;
  }

  /** Opens the text prompt: a comment, or a name to search people by. */
  ask(prompt) {
    this.prompt = { ...prompt, sel: 0, seq: 0 };
    this.input.set_value(prompt.value ?? "");
    this.input.focus();
  }

  /** Searches Jira for the people a search prompt names, once typing pauses; a later key drops an earlier answer. */
  promptTyped(cx) {
    const p = this.prompt;
    if (p && !isPeople(p)) {
      p.sel = 0;
      if (completes(p)) this.fetchCompletion(completing(p.kind, this.input.value()), cx);
      cx.notify();
      return;
    }
    if (!isPeople(p)) return;
    p.text = this.input.value();
    p.error = null;
    const seq = ++p.seq;
    const q = peopleQuery(p.text);
    if (!q) {
      p.users = undefined;
      p.searching = false;
      cx.notify();
      return;
    }
    p.searching = true;
    cx.notify();
    const current = () => this.prompt === p && p.seq === seq;
    cx.spawn(async (cx) => {
      await cx.sleep(SEARCH_WAIT);
      if (!current()) return;
      try {
        const a = await this.call("GET", `/v1/tickets/${p.key}/users?query=${encodeURIComponent(q)}`);
        if (!current()) return;
        if (a.status !== 200) throw new Error(a.body?.message ?? `tkt answered ${a.status}`);
        p.users = a.body.users ?? [];
        p.sel = 0;
      } catch (e) {
        if (!current()) return;
        p.error = e.message;
      }
      p.searching = false;
      cx.notify();
    });
  }

  /** Enter in the prompt: a search picks the highlighted person, any other prompt is answered. */
  submitPrompt(cx) {
    const p = this.prompt;
    if (!p) return;
    if (!isPeople(p) && p.kind !== "choose") return this.answerPrompt(cx);
    this.promptRows(p)[p.sel]?.pick(cx);
  }

  /** The rows a prompt lists under its text: people found, the choices that match, or the completions of the text. */
  promptRows(p) {
    const text = this.input.value();
    const pick = (item) => (cx) => { this.closePrompt(); this.act(item, cx); };
    if (isPeople(p)) return peopleItems(p, p.users ?? []).map((item) => ({ label: item.label, pick: pick(item) }));
    if (p.kind === "choose") return matching(p.items, text, (i) => i.label).map((item) => ({ label: item.label, pick: pick(item) }));
    if (!completes(p)) return [];
    const c = completing(p.kind, text);
    return (suggestions(c, { ...this.known, owners: ownersOf(this.settings?.data?.spaces) }) ?? []).map((name) => ({ label: name, pick: (cx) => {
      this.input.set_value(accepted(c, name));
      this.input.focus();
      this.promptTyped(cx);
    } }));
  }

  /** The line under a completion prompt while its list is missing, failed or empty. */
  completionStatus(p, rows) {
    const text = this.input.value();
    const c = completing(p.kind, text);
    if (!c) return p.kind === "addRepo" && /\s/.test(text.trim()) ? "The clone's folder starts with ~ or /" : null;
    const key = c.source === "repos" ? `repos:${c.owner}` : `folders:${c.dir}`;
    if (this.known.errors[key]) return `Could not list: ${this.known.errors[key]}`;
    if (this.known.pending.has(key)) return c.source === "repos" ? `Listing ${c.owner}'s repositories…` : `Listing ${c.dir}…`;
    return rows.length === 0 && c.text ? `Nothing matches “${c.text}”` : null;
  }

  /** Asks tkt once for the repositories or folders a completion needs, keeping them for the next prompts. */
  fetchCompletion(c, cx) {
    if (!c || c.source === "owners") return;
    const key = c.source === "repos" ? `repos:${c.owner}` : `folders:${c.dir}`;
    const k = this.known;
    if ((c.source === "repos" ? k.repos[c.owner] : k.folders[c.dir]) || k.pending.has(key)) return;
    k.pending.add(key);
    k.errors[key] = null;
    cx.spawn(async (cx) => {
      try {
        const path = c.source === "repos" ? `/v1/settings/repos?owner=${encodeURIComponent(c.owner)}` : `/v1/settings/folders?path=${encodeURIComponent(c.dir)}`;
        const a = await this.call("GET", path);
        if (a.status !== 200) throw new Error(a.body?.message ?? `tkt answered ${a.status}`);
        if (c.source === "repos") k.repos[c.owner] = a.body.repos ?? [];
        else k.folders[c.dir] = a.body.folders ?? [];
      } catch (e) {
        k.errors[key] = e.message;
      }
      k.pending.delete(key);
      cx.notify();
    });
  }

  closePrompt() {
    this.prompt = null;
    this.focus.focus();
  }

  answerPrompt(cx) {
    const p = this.prompt;
    const text = this.input.value().trim();
    this.closePrompt();
    cx.spawn(async (cx) => {
      try {
        if (p.kind === "addRepo") {
          const [repo, ...path] = text.split(/\s+/);
          if (!repo || path.length === 0) throw new Error("give owner/name, then the folder of its clone");
          await this.patchSettings(reposPatch(p.space, { add: { repo, path: path.join(" ") } }));
        } else if (p.kind === "addRoot") {
          if (!text) throw new Error("give a folder");
          await this.patchSettings(rootsPatch(this.settings.data.repo_roots, { add: text }));
        } else if (p.kind === "comment") {
          this.form.comment = text;
          this.menu = { ...formMenu(this.form.key, this.form.move, this.form), sel: 0 };
        }
      } catch (e) {
        this.notice = `could not: ${e.message}`;
      }
      cx.notify();
    });
    cx.notify();
  }

  promptBox(t) {
    const p = this.prompt;
    const field = div().rounded(8).border(1).border_color(t.accent).bg(t.surface).p(2).child(Input.new(this.input));
    const rows = this.promptRows(p);
    const from = Math.max(0, Math.min(p.sel - 3, rows.length - 8));
    const status = isPeople(p) ? peopleStatus(p)
      : p.kind === "choose" ? (rows.length === 0 ? `Nothing matches “${this.input.value().trim()}”` : null)
        : completes(p) ? this.completionStatus(p, rows) : null;
    const buttons = h_flex().gap(8).justify_end()
      .child(h_flex().id("prompt-cancel").h(30).px(14).items_center().rounded(7).border(1).border_color(t.border).text_size(13).text_color(t.foreground)
        .on_click((_e, cx) => { this.closePrompt(); cx.notify(); }).child("Cancel"))
      .child(h_flex().id("prompt-ok").h(30).px(16).items_center().rounded(7).bg(t.accent).text_size(13).font_semibold().text_color(ensure(t.chrome, t.accent, 4.5))
        .on_click((_e, cx) => this.answerPrompt(cx)).child("Done"));
    const keys = completes(p) ? [["↑↓", "choose"], ["tab", "complete"], ["enter", "save"], ["esc", "cancel"]] : [["↑↓", "choose"], ["enter", "pick"], ["esc", "cancel"]];
    return this.dialog(t, "prompt", 440, v_flex().gap(10).px(16).pt(16).pb(12)
      .child(div().text_size(14).font_semibold().text_color(t.foreground).child(p.title))
      .child(field)
      .when(rows.length > 0, (el) => el.child(v_flex().gap(1).children(rows.slice(from, from + 8).map((row, j) => {
        const i = from + j;
        const on = i === p.sel;
        return h_flex().id(`prompt-row-${i}`).h(MENU_ROW).gap(8).pl(6).pr(10).items_center().rounded(7).border(1).border_color(on ? mix(t.accent, t.popover, 0.45) : t.popover)
          .bg(on ? mix(t.accent, t.popover, 0.12) : t.popover).hover((h) => h.bg(on ? mix(t.accent, t.popover, 0.12) : t.hover))
          .on_click((_e, cx) => { p.sel = i; row.pick(cx); cx.notify(); })
          .child(div().w(10).text_size(11).text_color(t.accent).child(on ? "▸" : ""))
          .child(div().flex_1().min_w_0().truncate().text_size(13).text_color(t.foreground).child(row.label));
      })).when(rows.length > 8, (el) => el.child(div().px(6).text_size(11).text_color(t.dim).child(`${rows.length} found · ↑↓ for more`)))))
      .when(Boolean(status), (el) => el.child(div().px(6).text_size(12).text_color(p.error || /^Could not/.test(status) ? ensure(t.ansi1, t.popover, 4.5) : t.muted).child(status)))
      .when(!listed(p) || completes(p), (el) => el.child(buttons))
      .when(listed(p), (el) => el.child(div().pt(9).border_t(1).border_color(t.border).child(this.keyLine(t, keys)))));
  }

  /** The keys of the shown tab's tickets, in its order, for the details' arrows. */
  shownKeys() {
    const body = this.answers[this.tab]?.body;
    if (this.tab === "prs") return [...new Set(prRows(body).filter((r) => r.kind === "row" && r.ticketKey).map((r) => r.ticketKey))];
    if (this.tab === "backlog") return backlogRows(body).filter((r) => r.kind === "row").map((r) => r.key);
    return listRows(body?.sections).filter((r) => r.kind === "row").map((r) => r.key);
  }

  async openDetails(key) {
    const keys = this.details?.keys ?? this.shownKeys();
    this.details = { key, keys, view: null };
    this.panes.details.offset = 0;
    this.panes.detailsRight.offset = 0;
    const a = await this.call("GET", `/v1/tickets/${key}`);
    if (a.status !== 200) {
      this.details = null;
      throw new Error(a.body?.message ?? `tkt answered ${a.status}`);
    }
    const known = this.boardRow(key)?.ticket ?? {};
    const ticket = { ...known, ...Object.fromEntries(Object.entries(a.body.ticket ?? {}).filter(([, v]) => v !== "" && v !== null && v !== undefined && v !== 0)) };
    this.details = { key, keys, view: detailView({ ...a.body, ticket }, TABS.find((t) => t.id === this.tab).title), ticket: a.body.ticket };
  }

  goDetails(key, cx) {
    cx.spawn(async (cx) => {
      try {
        await this.openDetails(key);
      } catch (e) {
        this.notice = `could not: ${e.message}`;
      }
      cx.notify();
    });
  }

  /** A pane that scrolls by an offset, so keys can scroll it as the wheel does; GPUI scrolls only virtual lists
   *  from code. */
  scrollPane(id, pane, content) {
    return v_flex().id(id).min_h_0().overflow_hidden()
      .on_size_change((size) => { pane.view = size.height; })
      .on_scroll_wheel((e, cx) => {
        const max = Math.max(0, pane.content - pane.view);
        pane.offset = Math.max(0, Math.min(max, pane.offset - e.delta.y));
        cx.notify();
      })
      .child(v_flex().id(`${id}-content`).mt(-pane.offset).on_size_change((size) => { pane.content = size.height; }).child(content));
  }

  /** The type kind of a ticket the board shows, for its icon; empty when the board does not show it. */
  kindOf(key) {
    return this.boardRow(key)?.card?.type_kind ?? "";
  }

  /** The board's row of a ticket, whose card and ticket fill what the details leave out. */
  boardRow(key) {
    const rows = [...(this.answers.sprint?.body?.columns ?? []).flatMap((c) => c.rows ?? []), ...(this.answers.backlog?.body?.groups ?? []).flatMap((g) => g.rows ?? [])];
    return rows.find((r) => r.key === key) ?? null;
  }

  detailsScreen(t) {
    const d = this.details;
    const v = d.view;
    const { prev, next } = neighbors(d.keys, d.key);
    const step = (id, icon, on, onClick) => h_flex().id(id).w(26).h(24).items_center().justify_center().rounded(5).bg(on ? t.pill : t.surface).text_size(11)
      .text_color(on ? t.foreground : t.dim).on_mouse_down("left", (_e, cx) => cx.stop_propagation()).on_click((_e, cx) => { if (on) onClick(cx); }).child(icon);
    const nav = h_flex().h(44).px(14).gap(10).items_center().border_b(1).border_color(t.border)
      .child(h_flex().id("back").h(28).pl(6).pr(10).gap(6).items_center().rounded(7).border(1).border_color(t.border).text_size(12).font_weight(500).text_color(t.foreground)
        .hover((s) => s.bg(t.hover)).on_mouse_down("left", (_e, cx) => cx.stop_propagation()).on_click((_e, cx) => { this.details = null; cx.notify(); }).child("‹").child("Back"))
      .child(h_flex().gap(6).items_center().text_size(12).text_color(t.muted).child((v?.breadcrumb ?? "").split(" › ")[0] || "").child(div().text_color(t.dim).child("›"))
        .child(this.mono(div().text_size(11).text_color(t.foreground)).child(d.key)))
      .child(div().flex_1())
      .child(div().text_size(11).text_color(t.muted).child(positionOf(d.keys, d.key)))
      .child(h_flex().p(2).gap(2).rounded(7).border(1).border_color(t.border).child(step("prev", "◀", Boolean(prev), (cx) => this.goDetails(prev, cx))).child(step("next", "▶", Boolean(next), (cx) => this.goDetails(next, cx))));
    const bar = (w, h) => div().w(w).h(h).rounded(4).bg(t.pill);
    if (!v) return v_flex().flex_1().child(nav).child(h_flex().flex_1().items_start()
      .child(v_flex().flex_1().gap(14).px(28).py(24).child(bar(420, 18)).child(bar(300, 12)).child(div().h(14)).child(bar(560, 11)).child(bar(520, 11)).child(bar(420, 11))
        .child(div().pt(8).text_size(12).text_color(t.muted).child(`Loading ${d.key}…`)))
      .child(div().w(320).h_full().bg(t.well).border_l(1).border_color(t.border)));
    const h2 = (text, count) => h_flex().pt(10).pb(2).gap(8).items_center().child(div().text_size(12).font_semibold().text_color(t.foreground).child(text))
      .when(count !== undefined, (el) => el.child(div().px(7).rounded(9).bg(t.pill).text_size(11).font_semibold().text_color(t.muted).child(String(count))));
    const icon = typeIcon(this.kindOf(d.key));
    const body = ensure(mix(t.foreground, t.muted, 0.25), t.raised, 4.5);
    const fieldOf = (k) => v.fields.find(([x]) => x === k)?.[1] ?? "";
    const initialsOf = (who) => (who ?? "").split(/\s+/).filter(Boolean).slice(0, 2).map((w) => w[0]).join("").toUpperCase();
    const avatar = (who, size) => h_flex().w(size).h(size).flex_none().rounded(size / 2).bg(mix(t.foreground, t.raised, 0.12)).items_center().justify_center()
      .text_size(size > 20 ? 10 : 8).font_semibold().text_color(t.foreground).child(initialsOf(who));
    const copyButton = (id, value, what) => h_flex().id(id).ml_auto().h(24).px(8).gap(5).items_center().rounded(6).border(1).border_color(t.raised).text_size(11).text_color(t.muted)
      .hover((h) => h.border_color(t.border).bg(t.surface).text_color(t.foreground)).on_mouse_down("left", (_e, cx) => cx.stop_propagation())
      .on_click((_e, cx) => this.act({ action: "copy", value, what }, cx)).child(this.icon("copy", 12)).child("Copy");
    const card = (id, title, text) => v_flex().id(id).max_w(680).rounded(10).border(1).border_color(t.border).bg(t.raised).overflow_hidden()
      .child(h_flex().h(38).pl(14).pr(8).items_center().border_b(1).border_color(t.border).child(div().text_size(12).font_semibold().text_color(t.foreground).child(title))
        .child(copyButton(`${id}-copy`, text, `the ${title.toLowerCase()}`)))
      .child(div().px(14).pt(10).pb(12).text_size(13).text_color(body).child(TextView.markdown(`${id}-text`, text)));
    const tag = (id, children) => h_flex().id(id).h(24).px(9).gap(6).items_center().rounded(6).bg(t.pill).text_size(12).text_color(t.foreground).children(children);
    const tone = t[statusTone(v.category)] ?? t.muted;
    const statusBg = mix(tone, t.surface, 0.14);
    const who = fieldOf("Assignee");
    const board = fieldOf("Board") || fieldOf("Label");
    const boardBg = mix(t.ansi4, t.surface, 0.16);
    const prs = prSummary(v.prs);
    const leftContent = v_flex().px(28).pt(22).pb(24).gap(14).max_w(760)
      .child(v_flex().gap(10)
        .child(h_flex().gap(8).items_center()
          .when(Boolean(icon), (el) => el.child(div().text_size(11).text_color(t[icon.tone] ?? t.muted).child(icon.glyph)))
          .child(this.mono(div().text_size(12).text_color(t.muted)).child(d.key))
          .when(Boolean(fieldOf("Status")), (el) => el.child(h_flex().h(20).px(9).gap(6).items_center().rounded(10).bg(statusBg)
            .child(div().w(6).h(6).rounded(3).bg(tone)).child(div().text_size(11).font_semibold().text_color(ensure(tone, statusBg, 4.5)).child(fieldOf("Status"))))))
        .child(div().max_w(680).text_size(21).font_semibold().text_color(t.foreground).child(v.title))
        .child(h_flex().flex_wrap().gap(6)
          .when(Boolean(fieldOf("Type")), (el) => el.child(tag("chip-type", [...(icon ? [div().text_size(11).text_color(t[icon.tone] ?? t.muted).child(icon.glyph)] : []), fieldOf("Type")])))
          .when(Boolean(fieldOf("Points")), (el) => el.child(tag("chip-points", [this.mono(div()).child(`${fieldOf("Points")} pt`)])))
          .child(tag("chip-who", who && who !== "unassigned" ? [avatar(who, 18), who] : [div().text_color(t.muted).child("unassigned")]))
          .when(Boolean(board), (el) => el.child(h_flex().id("chip-board").h(24).px(9).items_center().rounded(6).bg(boardBg).text_size(12).text_color(ensure(mix(t.ansi4, t.foreground, 0.45), boardBg, 4.5)).child(board)))
          .when(Boolean(prs), (el) => el.child(tag("chip-prs", [div().text_color(t.muted).child(prs.split(" · ")[0]), ...(prs.includes(" · ") ? [prs.split(" · ")[1]] : [])])))))
      .when(Boolean(v.attention), (el) => el.child(h_flex().max_w(680).px(12).py(9).gap(10).items_center().rounded(8).border(1).border_color(mix(t.ansi1, t.surface, 0.35)).bg(mix(t.ansi1, t.surface, 0.09))
        .child(div().text_size(12).font_semibold().text_color(ensure(t.ansi1, mix(t.ansi1, t.surface, 0.09), 4.5)).child(`! ${v.attention}`))))
      .child(v.description ? card("desc", "Description", v.description) : v_flex().max_w(680).px(14).py(12).rounded(10).border(1).border_color(t.border).text_size(13).text_color(t.muted).child("No description"))
      .when(Boolean(v.acceptance), (el) => el.child(card("acc", "Acceptance criteria", v.acceptance)))
      .when(v.subtasks.length > 0, (el) => el.child(h2("Subtasks", v.subtasks.length)).child(v_flex().max_w(680).rounded(10).border(1).border_color(t.border).overflow_hidden()
        .children(v.subtasks.map((st, i) => h_flex().id(`st-${st.key}`).h(34).px(12).gap(10).items_center().bg(t.raised).when(i > 0, (row) => row.border_t(1).border_color(t.border))
          .child(this.mono(div().w(84).flex_none().text_size(11).text_color(t.muted)).child(st.key))
          .child(div().w(130).flex_none().truncate().text_size(12).text_color(t.foreground).child(st.status))
          .child(div().flex_1().min_w_0().truncate().text_size(13).text_color(t.foreground).child(st.summary))))))
      .child(h2("Comments", v.comments.length))
      .child(v.comments.length === 0 ? div().text_size(13).text_color(t.muted).child("No comments yet") : v_flex().max_w(680).gap(12).children(v.comments.map((c, i) => {
        const bot = isBot(c.who);
        return h_flex().id(`c-${i}`).gap(12).items_start()
          .child(avatar(c.who, 28))
          .child(v_flex().flex_1().min_w_0().rounded(10).border(1).border_color(t.border).bg(bot ? t.surface : t.raised).overflow_hidden()
            .child(h_flex().h(34).pl(12).pr(8).gap(8).items_center().border_b(1).border_color(t.border)
              .child(div().min_w_0().truncate().text_size(12).font_weight(bot ? 500 : 600).text_color(bot ? t.muted : t.foreground).child(c.who))
              .when(bot, (el) => el.child(div().px(5).rounded(4).border(1).border_color(t.border).text_size(10).text_color(t.muted).child("bot")))
              .child(div().flex_none().text_size(11).text_color(t.muted).child(c.when))
              .child(copyButton(`c-${i}-copy`, c.body || "", `${c.who}'s comment`)))
            .child(div().px(12).pt(9).pb(11).text_size(13).text_color(bot ? t.muted : body).child(TextView.markdown(`cbody-${i}`, c.body || ""))));
      })));
    const left = this.scrollPane("details-left", this.panes.details, leftContent).flex_1().min_w_0().h_full();
    const menu = detailsMenu(d);
    const prUrls = new Set(v.prs.map((p) => p.url));
    const items = menu.items.filter((it) => it.action !== "details" && !(it.action === "open" && prUrls.has(it.url)) && !(it.action === "submenu" && it.label.startsWith("Pull requests")));
    const primary = items.find((it) => it.action === "start");
    const group = (it) => it.action === "copy" || it.action === "copyContext" ? 3 : it.action === "open" || it.action === "submenu" ? 1 : 2;
    const rest = items.filter((it) => it !== primary).sort((a, b) => group(a) - group(b));
    const prItems = v.prs.slice(0, 3).map((p) => ({ action: "open", url: p.url, label: p.ref }));
    const moreItem = v.prs.length > 3 ? { action: "submenu", label: "more", menu: prsSubmenu(d.key, v.prs.map((p) => ({ number: Number(p.ref.split("#").pop()), label: `#${p.ref.split("#").pop()} ${p.title ?? ""}`.trim(), url: p.url }))) } : null;
    d.actions = [...(primary ? [primary] : []), ...rest, ...prItems, ...(moreItem ? [moreItem] : [])];
    d.focusY = this.detailsFocusY(d, prItems.length, Boolean(moreItem), Boolean(primary), rest.map(group));
    const lit = (it) => Boolean(d.actFocus) && d.actions[d.actSel ?? 0] === it;
    const h3 = (text) => div().pb(6).text_size(12).font_semibold().text_color(t.muted).child(text);
    const labelName = this.answers.sprint?.body?.label_field || "Label";
    const valueOf = (k) => v.fields.find(([x]) => x === k)?.[1] ?? "";
    const more = valueOf("Board") && valueOf("Label") ? [[labelName, valueOf("Label")]] : [];
    const actions = v_flex().id("dsec-acts").gap(4).on_size_change((size) => { d.hActs = size.height; }).child(h3("Actions"))
      .when(Boolean(primary), (el) => el.child(h_flex().id("dact-primary").h(32).justify_center().items_center().rounded(7).bg(t.accent).text_size(13).font_semibold()
        .border(2).border_color(primary && lit(primary) ? mix(t.accent, t.foreground, 0.4) : t.accent)
        .text_color(ensure(t.chrome, t.accent, 4.5)).on_mouse_down("left", (_e, cx) => cx.stop_propagation()).on_click((_e, cx) => this.act(primary, cx)).child(primary.label)))
      .children(rest.flatMap((it, i) => [...(i > 0 && group(it) !== group(rest[i - 1]) ? [div().id(`dact-sep-${i}`).h(1).my(4).bg(t.border)] : []),
        h_flex().id(`dact-${i}`).h(28).px(8).items_center().rounded(6).text_size(12).text_color(t.foreground).hover((s) => s.bg(t.hover))
          .border(1).border_color(lit(it) ? mix(t.accent, t.well, 0.45) : t.well).bg(lit(it) ? mix(t.accent, t.well, 0.12) : t.well).child(div().w(10).text_size(11).text_color(t.accent).child(lit(it) ? "▸" : ""))
          .on_mouse_down("left", (_e, cx) => cx.stop_propagation()).on_click((_e, cx) => this.act(it, cx)).child(it.label)]));
    const pullRequests = v_flex().id("dsec-prs").gap(6).on_size_change((size) => { d.hPrs = size.height; }).child(h3(v.prs.length > 0 ? `Pull requests · ${v.prs.length}` : "Pull requests"))
      .child(v.prs.length === 0 ? div().text_size(12).text_color(t.muted).child("none linked") : v_flex().id("dpr-box").on_size_change((size) => { d.hPrBox = size.height; }).rounded(8).border(1).border_color(t.border).bg(t.raised).overflow_hidden()
        .children(v.prs.slice(0, 3).map((p, i) => h_flex().id(`dpr-${i}`).h(32).px(10).gap(8).items_center().min_w_0().hover((h) => h.bg(t.hover))
          .when(lit(prItems[i]), (el) => el.bg(mix(t.accent, t.raised, 0.14)))
          .when(i > 0, (el) => el.border_t(1).border_color(t.border))
          .on_mouse_down("left", (_e, cx) => cx.stop_propagation()).on_click((_e, cx) => this.act({ action: "open", url: p.url, label: p.ref }, cx))
          .child(div().w(12).flex_none().font_semibold().text_size(11).text_color(p.mark === "✓" ? t.ansi2 : p.mark === "✕" ? ensure(t.ansi1, t.raised, 4.5) : t.muted).child(["✓", "✕", "…", "—"].includes(p.mark) ? p.mark : "·"))
          .child(this.mono(div().flex_none().text_size(11).text_color(t.muted)).child(`#${p.ref.split("#").pop()}`))
          .child(div().flex_1().min_w_0().truncate().text_size(12).text_color(["✓", "✕", "…", "—"].includes(p.mark) ? t.foreground : t.muted).child(p.title ?? ""))
          .when(!["✓", "✕", "…", "—"].includes(p.mark), (el) => el.child(div().flex_none().text_size(11).text_color(t.muted).child(p.mark)))))
        .when(v.prs.length > 3, (el) => el.child(h_flex().id("dpr-more").h(30).px(10).items_center().border_t(1).border_color(t.border).text_size(12)
          .text_color(ensure(mix(t.accent, t.foreground, 0.3), t.raised, 4.5)).hover((h) => h.bg(t.hover))
          .on_mouse_down("left", (_e, cx) => cx.stop_propagation())
          .when(lit(moreItem), (el) => el.bg(mix(t.accent, t.raised, 0.14)))
          .on_click((_e, cx) => this.act(moreItem, cx))
          .child(`+${v.prs.length - 3} more ▸`))));
    const agentRow = h_flex().id("d-agent").h(32).px(10).gap(8).items_center().rounded(8).border(1).border_color(t.border).bg(t.raised)
      .child(v.agent ? this.agentDot(t, v.agent) : div().w(8).h(8).rounded(4).border(1).border_color(t.dim))
      .child(div().flex_1().min_w_0().truncate().text_size(12).text_color(v.agent ? t.foreground : t.muted).child(v.agent ? agentLabel(v.agent) : "no agent"))
      .when(Boolean(v.agent && primary), (el) => el.hover((h) => h.bg(t.hover)).on_mouse_down("left", (_e, cx) => cx.stop_propagation()).on_click((_e, cx) => this.act(primary, cx))
        .child(div().flex_none().text_size(11).text_color(ensure(mix(t.accent, t.foreground, 0.3), t.raised, 4.5)).child("Go to it ›")));
    const work = v_flex().gap(8).child(h3("Agent and work")).child(agentRow)
      .children(v.work.length === 0 ? [div().text_size(12).text_color(t.muted).child("no worktree linked")] : v.work.map((w, i) =>
        v_flex().id(`w-${i}`).gap(3).child(this.mono(div().text_size(11).text_color(t.foreground).truncate()).child(w.where)).child(this.mono(div().text_size(11).text_color(t.muted).truncate()).child(w.path))));
    const rightContent = v_flex().px(18).py(22).gap(22).child(actions).child(pullRequests).child(work)
      .when(more.length > 0, (el) => el.child(v_flex().gap(2).child(h3("More")).children(more.map(([k, val], i) => h_flex().id(`dm-${i}`).min_h(24).gap(10).items_center()
        .child(div().w(76).flex_none().text_size(12).text_color(t.muted).child(k)).child(div().flex_1().min_w_0().truncate().text_size(12).text_color(t.foreground).child(val))))));
    const right = this.scrollPane("details-right", this.panes.detailsRight, rightContent).w(320).flex_none().h_full().bg(t.well).border_l(1).border_color(t.border);
    return v_flex().id("details").flex_1().min_h_0().child(nav).child(h_flex().flex_1().min_h_0().items_start().child(left).child(right));
  }

  /** Scrolls the details' right column to the item the keyboard is on; the last one shows the column's end. */
  showFocused() {
    const d = this.details;
    const pane = this.panes.detailsRight;
    const sel = d.actSel ?? 0;
    const y = d.focusY?.[sel];
    if (sel === (d.actions?.length ?? 0) - 1) pane.offset = Math.max(0, pane.content - pane.view);
    else if (y) pane.offset = keepVisible(pane, y[0], y[1]);
  }

  /** Where each item the keyboard reaches in the details' right column sits, as [top, height]: the actions from their
   *  fixed row heights, then the pull requests below the measured Actions section. */
  detailsFocusY(d, prs, more, primary, groups) {
    const out = [];
    let y = 22 + 22 + 4;
    if (primary) { out.push([y, 32]); y += 36; }
    groups.forEach((g, i) => {
      if (i > 0 && g !== groups[i - 1]) y += 13;
      out.push([y, 28]);
      y += 32;
    });
    const rowsTop = 22 + (d.hActs ?? 0) + 22 + Math.max(0, (d.hPrs ?? 0) - (d.hPrBox ?? 0));
    for (let i = 0; i < prs; i++) out.push([rowsTop + i * 33, 32]);
    if (more) out.push([rowsTop + prs * 33, 30]);
    return out;
  }

  /** Writes the mod the tkt on machine on carries over this one; Herdr reloads the mod when its files change. */
  async updateFrom(on) {
    const r = await exec(await this.tkt(on), ["herdr", "export-mod", "--json"], on ? { on } : {});
    if (r.code !== 0) throw new Error((r.stderr || "tkt herdr export-mod failed").trim());
    const { files } = JSON.parse(r.stdout);
    const outside = Object.keys(files).find((path) => !safeModPath(path));
    if (outside !== undefined) throw new Error(`tkt on ${on ?? "this computer"} sent a file outside the mod's folder (${outside}); nothing was written`);
    const dir = `${appHome()}/mods/tkt`;
    for (const [path, text] of Object.entries(files)) {
      const target = `${dir}/${path}`;
      await fs.mkdir(target.slice(0, target.lastIndexOf("/")), { recursive: true });
      await fs.writeFile(target, text);
    }
  }

  async openSettings() {
    const a = await this.call("GET", "/v1/settings");
    if (a.status !== 200) throw new Error(a.body?.message ?? `tkt answered ${a.status}`);
    this.settings = { section: this.settings?.section ?? "spaces", zone: this.settings?.zone ?? "menu", data: a.body, statuses: {}, statusFailed: {}, fields: null };
    if (this.settings.section === "statuses") await this.readStatuses();
  }

  /** Reads the shown space's statuses from Jira once, so the Statuses board appears without asking. */
  async readStatuses() {
    const st = this.settings;
    const space = st?.data.spaces.find((x) => x.key === st.statusSpace) ?? st?.data.spaces[0];
    if (!space || st.statuses[space.key] || st.statusReading === space.key) return;
    st.statusReading = space.key;
    st.statusFailed[space.key] = false;
    try {
      st.statuses[space.key] = await this.lookup("statuses", space.key);
    } catch (e) {
      st.statusFailed[space.key] = true;
      throw e;
    } finally {
      st.statusReading = null;
    }
  }

  async patchSettings(change) {
    const a = await this.call("PATCH", "/v1/settings", change);
    if (a.status !== 200) throw new Error(a.body?.message ?? `tkt answered ${a.status}`);
    this.settings.data = a.body;
    this.palette = a.body.palette;
    this.settings.statuses = {};
    this.notice = "✓ saved to config.toml · the board refreshes with it";
  }

  /** Runs fn in a task, telling its error, then renders. */
  /** Runs fn in a task, handing it the task's cx: a cx from the click that started it is gone once fn awaits. */
  attempt(fn, cx) {
    cx.spawn(async (cx) => {
      try {
        await fn(cx);
      } catch (e) {
        this.notice = `could not: ${e.message}`;
      }
      cx.notify();
    });
  }

  async lookup(kind, space) {
    const q = space ? `?space=${encodeURIComponent(space)}` : "";
    const a = await this.call("GET", `/v1/settings/${kind}${q}`);
    if (a.status !== 200) throw new Error(a.body?.message ?? `tkt answered ${a.status}`);
    return a.body;
  }

  settingsScreen(t) {
    const st = this.settings;
    const d = st.data;
    // Every control registers in the order it is drawn, so up, down and enter reach them as the TUI's rows.
    const controls = [];
    const focusable = (onClick) => {
      controls.push(onClick);
      return st.zone !== "menu" && controls.length - 1 === (st.sel ?? 0);
    };
    const press = (el, onClick) => el.on_mouse_down("left", (_e, cx) => cx.stop_propagation()).on_click((_e, cx) => onClick(cx));
    const button = (id, text, onClick, kind = "plain") => {
      const on = focusable(onClick);
      const danger = kind === "danger" && on;
      const el = h_flex().id(id).flex_none().h(kind === "add" ? 28 : 26).px(10).items_center().rounded(kind === "add" ? 7 : 6).text_size(12).font_weight(500)
        .border(1).border_color(on ? (danger ? mix(t.ansi1, t.surface, 0.45) : t.accent) : t.border)
        .text_color(danger ? ensure(t.ansi1, t.surface, 4.5) : kind === "add" || kind === "value" ? ensure(mix(t.accent, t.foreground, 0.3), t.surface, 4.5) : t.foreground)
        .bg(on ? mix(t.accent, t.surface, 0.1) : t.surface).hover((s) => s.border_color(t.accent));
      return press(el, onClick).child(text);
    };
    st.controls = controls;
    const intro = (title, text) => v_flex().gap(4).pb(18).child(div().text_size(18).font_semibold().text_color(t.foreground).child(title)).child(div().text_size(13).text_color(t.muted).child(text));
    const spaceTitle = (sp) => h_flex().gap(8).items_end().pb(8).child(this.mono(div().text_size(12).font_weight(500).text_color(t.foreground)).child(sp.key))
      .when(Boolean(sp.name), (el) => el.child(div().text_size(13).font_semibold().text_color(t.foreground).child(sp.name)));
    const table = (rows) => v_flex().rounded(10).border(1).border_color(t.border).overflow_hidden().children(rows);
    const tableRow = (id, i, children) => h_flex().id(id).h(44).pl(14).pr(12).gap(14).items_center().bg(t.raised).when(i > 0, (el) => el.border_t(1).border_color(t.border)).children(children);
    const side = v_flex().id("settings-side").w(220).flex_none().h_full().px(10).py(12).gap(2).bg(t.well).border_r(1).border_color(t.border).children(SECTIONS.map((sec) => {
      const on = st.section === sec.id;
      const lit = on && st.zone === "menu";
      return press(h_flex().id(`sec-${sec.id}`).h(32).px(12).items_center().rounded(7).border(1).border_color(lit ? t.accent : t.well).bg(on ? t.pill : t.well).text_size(13).font_weight(on ? 600 : 500)
        .text_color(on ? t.foreground : t.muted).hover((s) => s.text_color(t.foreground)), (cx) => { st.section = sec.id; st.zone = "menu"; st.sel = 0; st.chipFocus = false; st.drag = null; st.press = null; st.grab = null; if (sec.id === "statuses") this.attempt(() => this.readStatuses(), cx); cx.notify(); }).child(sec.title);
    }));
    let content = v_flex();
    let below = 0;
    if (st.section === "spaces") {
      content = v_flex().child(intro("Spaces & repositories", "The repositories each Jira space works in. tkt finds their clones and pull requests through them."))
        .children(d.spaces.map((sp) => v_flex().id(`sp-${sp.key}`).gap(8).pb(22).child(spaceTitle(sp))
          .when(sp.repos.length > 0, (el) => el.child(table(sp.repos.map((r, i) => tableRow(`repo-${sp.key}-${r.repo}`, i, [
            this.mono(div().w(260).flex_none().truncate().text_size(12).text_color(t.foreground)).child(r.repo),
            this.mono(div().flex_1().min_w_0().truncate().text_size(11).text_color(r.path ? t.muted : t.dim)).child(r.path || "no local clone"),
            button(`rm-${sp.key}-${r.repo}`, "Remove", (cx) => { this.menu = { ...confirmRemove(`Remove ${r.repo} from ${sp.key}?`, reposPatch(sp, { remove: r.repo })), sel: 0 }; cx.notify(); }, "danger")])))))
          .when(sp.inferred.length > 0, (el) => el.child(h_flex().gap(4).text_size(12).text_color(t.muted).child("Also seen in its pull requests:").child(this.mono(div().text_size(11).text_color(t.foreground)).child(sp.inferred.join(", ")))))
          .child(h_flex().child(button(`add-${sp.key}`, "+ Add a repository", (cx) => { this.ask({ kind: "addRepo", title: `Add to ${sp.key}: owner/name, then its folder`, space: sp }); cx.notify(); }, "add")))));
    } else if (st.section === "boards") {
      content = v_flex().child(intro("Team boards", "The scrum boards whose sprints the Team view and the Backlog show."))
        .children(d.spaces.map((sp) => v_flex().id(`bs-${sp.key}`).gap(8).pb(22).child(spaceTitle(sp))
          .children(sp.team_boards.map((b) => {
            const on = sp.team_boards.some((x) => x.id === b.id);
            const toggle = (cx) => {
              if (!on) return this.attempt(() => this.patchSettings(boardsPatch(sp, b.id)), cx);
              this.menu = { ...confirmRemove(`Remove the team board ${b.name} from ${sp.key}?`, boardsPatch(sp, b.id)), sel: 0 };
              cx.notify();
            };
            const lit = focusable(toggle);
            return press(h_flex().id(`b-${sp.key}-${b.id}`).h(34).gap(10).px(10).items_center().rounded(7).border(1).border_color(lit ? t.accent : t.surface)
              .bg(lit ? mix(t.accent, t.surface, 0.1) : t.surface), toggle).child(this.checkbox(t, on)).child(div().text_size(13).text_color(t.foreground).child(b.name));
          }))
          .child(h_flex().child(button(`lb-${sp.key}`, "+ Add a team board…", (cx) => this.attempt(async () => {
            const boards = (await this.lookup("boards", sp.key)).boards ?? [];
            const items = boards.filter((b) => !sp.team_boards.some((x) => x.id === b.id)).map((b) => ({ label: b.name, action: "settingsPatch", change: boardsPatch(sp, b.id) }));
            this.ask({ kind: "choose", title: `Add a team board to ${sp.key}`, items });
          }, cx), "add")))));
    } else if (st.section === "statuses") {
      const space = d.spaces.find((x) => x.key === st.statusSpace) ?? d.spaces[0];
      const loaded = space ? st.statuses[space.key] : null;
      st.chipAt = {};
      const colTitle = (id) => d.columns.find((c) => c.id === id)?.title ?? id;
      const hint = st.grab ? (st.grab.to !== st.grab.from ? `Space drops ${st.grab.name} in ${colTitle(st.grab.to)}, saved for ${space?.key}; esc cancels.` : `← → choose a column for ${st.grab.name}; esc cancels.`)
        : st.drag ? (st.dropTarget && st.dropTarget !== st.drag.column ? `Release to move ${st.drag.name} to ${colTitle(st.dropTarget)}, saved for ${space?.key}.` : `Drag ${st.drag.name} onto another column; release elsewhere to cancel.`) : null;
      const head = h_flex().id("status-head").gap(16).items_end().pb(16).on_size_change((size) => { st.headH = size.height; })
        .child(v_flex().flex_1().min_w_0().gap(4).child(div().text_size(18).font_semibold().text_color(t.foreground).child("Statuses"))
          .child(div().text_size(13).truncate().text_color(hint ? t.foreground : t.muted).child(hint ?? "Which column of the board each Jira status falls in, and the mark its tickets show.")))
        .when(Boolean(loaded), (el) => el.child(h_flex().flex_none().gap(14).items_center().text_size(12).text_color(t.muted)
          .child(h_flex().gap(6).items_center().child(div().w(6).h(6).rounded(3).bg(t.accent)).child("set by you"))
          .child(div().child("auto: from Jira's category"))))
        .when(d.spaces.length > 1, (el) => el.child(this.segment(t, "status-space", d.spaces.map((x) => ({ label: x.key, on: x.key === space?.key, click: (cx) => { st.statusSpace = x.key; st.sel = 0; st.grab = null; this.attempt(() => this.readStatuses(), cx); cx.notify(); } })))));
      if (!space) content = head;
      else if (!loaded && !st.statusFailed?.[space.key]) content = v_flex().child(head).child(div().text_size(13).text_color(t.muted).child("Reading the statuses from Jira…"));
      else if (!loaded) content = v_flex().child(head).child(h_flex().child(button(`ls-${space.key}`, "Read the statuses from Jira again", (cx) => this.attempt(() => this.readStatuses(), cx), "add")));
      else {
        const columns = statusBoard(loaded, d.columns).map((col) => {
          const tone = t[statusTone(col.id)] ?? t.muted;
          const moving = st.drag ?? st.grab;
          const from = st.drag ? st.drag.column : st.grab?.from;
          const target = Boolean(moving) && (st.drag ? st.dropTarget : st.grab.to) === col.id && from !== col.id;
          return v_flex().id(`scol-${col.id}`).flex_1().min_w(150).rounded(10).pt(2).bg(tone).child(v_flex().flex_1().rounded(9).border(target ? 2 : 1)
            .border_color(target ? t.accent : t.border).bg(target ? mix(t.accent, t.well, 0.06) : t.well).overflow_hidden()
            .child(h_flex().id(`scolh-${col.id}`).px(12).pt(10).pb(8).gap(8).items_center().on_size_change((size) => { st.colHeadH = size.height; })
              .child(div().w(8).h(8).rounded(4).bg(tone))
              .child(div().text_size(12).font_semibold().text_color(t.foreground).child(col.title))
              .child(div().px(7).rounded(9).bg(t.pill).text_size(11).font_semibold().text_color(t.muted).child(String(col.statuses.length))))
            .child(v_flex().gap(6).px(8).pb(10).children(col.statuses.map((stt, row) => {
              const open = (cx) => { st.markPicker = { space: space.key, name: stt.name, mark: stt.mark, markAuto: stt.markAuto, sel: Math.max(0, d.marks.indexOf(stt.mark)) }; cx.notify(); };
              const idx = controls.length;
              const on = focusable(open);
              st.chipAt[idx] = { space: space.key, name: stt.name, column: col.id, row };
              if (st.focusStatus === stt.name) { st.sel = idx; st.focusStatus = null; }
              const m = markOf(stt.mark, stt.name);
              const held = st.grab?.name === stt.name;
              const picking = st.markPicker?.space === space.key && st.markPicker?.name === stt.name;
              const chip = h_flex().id(`chip-${stt.name}`).h(34).px(10).gap(8).items_center().rounded(7).border(1).border_color(on || held ? t.accent : t.border).bg(held ? mix(t.accent, t.raised, 0.16) : t.raised)
                .when(held && st.grab.to !== st.grab.from, (el) => el.opacity(0.35))
                .when(st.drag?.name === stt.name, (el) => el.opacity(0.35)).hover((h) => h.border_color(t.accent)).cursor_grab()
                .on_mouse_down("left", (e, cx) => {
                  cx.stop_propagation();
                  st.press = { space: space.key, name: stt.name, column: col.id, x: e.position.x, y: e.position.y, grabX: e.local_position?.x ?? 12, grabY: e.local_position?.y ?? 17, width: e.bounds?.width ?? 180, glyph: m ? m.glyph : "·", tone: m ? t[m.tone] ?? t.muted : t.dim };
                  st.dragged = false; st.sel = idx; st.chipFocus = true;
                })
                .on_click((_e, cx) => { if (!st.dragged) open(cx); })
                .child(div().w(14).flex_none().text_size(12).text_color(m ? t[m.tone] ?? t.muted : t.dim).child(m ? m.glyph : "·"))
                .child(div().flex_1().min_w_0().truncate().text_size(13).text_color(t.foreground).child(stt.name))
                .child(stt.manual ? div().w(6).h(6).flex_none().rounded(3).bg(t.accent) : div().flex_none().text_size(10).text_color(t.dim).child("auto"));
              return Popup.new(`mark-popup-${stt.name}`, chip).w_full().anchor("top_left").when(picking, (el) => el.content(this.markPickerBox(t)));
            }))
              .when(target, (el) => el.child(h_flex().id(`drop-${col.id}`).h(34).px(10).items_center().justify_center().rounded(7).border(1).border_color(t.accent)
                .bg(mix(t.accent, t.well, 0.14)).text_size(12).text_color(ensure(mix(t.accent, t.foreground, 0.3), t.well, 4.5)).child(`Drop ${moving.name} here`)))));
        });
        const lanes = statusBoard(loaded, d.columns);
        // The page's py(24), the column's pt(2) and its border sit above each column's title.
        st.rows = { top: 24 + (st.headH ?? 0) + 3 + (st.colHeadH ?? 0), row: 34, gap: 6 };
        below = rowsBelow(lanes.map((l) => l.statuses.length), st.rows, this.panes.settings);
        content = v_flex().child(head).child(h_flex().id("status-lanes").gap(10).items_start().children(columns)
          .on_mouse_move((e) => { const i = laneAt(e.local_position.x, { width: e.bounds.width, count: lanes.length, gap: 10 }); st.overLane = i >= 0 ? lanes[i].id : null; })
          .on_mouse_up("left", (_e, cx) => {
            const dr = st.drag, to = st.dropTarget;
            st.drag = null; st.press = null; st.dropTarget = null;
            if (dr && to && to !== dr.column) this.attempt(() => this.moveStatus(dr.space, dr.name, to), cx); else cx.notify();
          }));
      }
    } else if (st.section === "clones") {
      content = v_flex().gap(8).child(intro("Local clones", "Folders tkt looks for clones in; the first one is where new clones go."))
        .when(d.repo_roots.length > 0, (el) => el.child(table(d.repo_roots.map((root, i) => tableRow(`root-${root}`, i, [
          this.mono(div().flex_1().min_w_0().truncate().text_size(12).text_color(t.foreground)).child(root),
          ...(i === 0 ? [div().text_size(11).text_color(t.muted).child("new clones go here")] : []),
          button(`rmr-${root}`, "Remove", (cx) => { this.menu = { ...confirmRemove(`Remove the folder ${root}?`, rootsPatch(d.repo_roots, { remove: root })), sel: 0 }; cx.notify(); }, "danger")])))))
        .child(h_flex().child(button("add-root", "+ Add a folder", (cx) => { this.ask({ kind: "addRoot", title: "A folder for clones, such as ~/src" }); cx.notify(); }, "add")));
    } else if (st.section === "board") {
      const pick = (field, title) => (cx) => this.attempt(async () => {
        const names = (await this.lookup("fields")).fields ?? [];
        this.ask({ kind: "choose", title: `${title}: type part of its name`,
          items: [{ label: "— none", action: "settingsPatch", change: { [field]: "" } }, ...names.map((n) => ({ label: n, action: "settingsPatch", change: { [field]: n } }))] });
      }, cx);
      const setting = (title, text, control) => h_flex().gap(14).items_center().py(12).border_b(1).border_color(t.border)
        .child(v_flex().flex_1().gap(3).child(div().text_size(13).font_semibold().text_color(t.foreground).child(title)).child(div().text_size(12).text_color(t.muted).child(text))).child(control);
      content = v_flex().child(intro("Board", "Fields of your Jira site that the board reads."))
        .child(setting("Ticket label", "The field a ticket's label comes from, shown on cards and in the details.", button("label-field", `${d.label_field || "— none"} ▾`, pick("label_field", "Label field"), "value")))
        .child(setting("Story points", "The story points field; detected when unset.", button("points-field", `${d.points_field || "detected"} ▾`, pick("points_field", "Story points field"), "value")));
    } else if (st.section === "agents") {
      const language = d.language ?? "en";
      const option = (id, title, sub) => this.choice(t, focusable, `language-${id}`, language === id, title, sub, (cx) => this.attempt(() => this.patchSettings(languagePatch(id)), cx));
      content = v_flex().w(420).gap(8).child(intro("Agents", "The language the agents tkt starts talk to you in. Their code, commits and pull requests follow each repository's own conventions."))
        .child(option("en", "English", "Agents talk to you in English."))
        .child(option("es", "Español", "Agents talk to you in Spanish."));
    } else if (st.section === "colors") {
      content = this.colorsSection(t, d, focusable);
    } else {
      content = v_flex().child(intro("Machine", "The machine whose tkt this window shows."))
        .child(h_flex().gap(14).items_center().py(12).border_b(1).border_color(t.border)
          .child(div().flex_1().text_size(13).text_color(t.foreground).child(this.machine === undefined ? "none yet" : this.machine ?? "This computer"))
          .child(button("change-machine", "Change machine…", (cx) => this.pick(cx), "value")));
    }
    const back = (cx) => { this.settings = null; cx.notify(); };
    const backOn = focusable(back);
    const nav = h_flex().h(44).px(14).gap(10).items_center().border_b(1).border_color(t.border)
      .child(press(h_flex().id("settings-back").h(28).pl(6).pr(10).gap(6).items_center().rounded(7).border(1).border_color(backOn ? t.accent : t.border)
        .text_size(12).font_weight(500).text_color(t.foreground).hover((s) => s.bg(t.hover)), back).child("‹").child("Board"))
      .child(div().text_size(14).font_semibold().text_color(t.foreground).child("Settings"))
      .child(div().flex_1())
      .child(div().text_size(11).text_color(t.muted).child("Changes are saved to your config.toml"));
    return v_flex().id("settings").flex_1().min_h_0().child(nav)
      .child(h_flex().flex_1().min_h_0().items_start().child(side)
        .child(v_flex().relative().flex_1().h_full().min_h_0().min_w_0()
          .child(this.scrollPane("settings-content", this.panes.settings, v_flex().px(32).py(24).child(content)).flex_1())
          .when(below > 0, (el) => el.child(h_flex().absolute().bottom(12).left(0).right(0).justify_center()
            .child(div().px(10).py(1).rounded(10).border(1).border_color(t.border).bg(t.pill).shadow_md().text_size(11).text_color(t.muted).child(`↓ ${below} more`))))));
  }

  /** One of a setting's choices: a radio card that saves it when clicked or entered. */
  choice(t, focusable, id, on, title, sub, choose) {
    const lit = focusable(choose);
    return h_flex().id(id).gap(12).p(12).rounded(9).border(1).border_color(on || lit ? t.accent : t.border).bg(on ? t.selected : t.raised)
      .hover((s) => s.border_color(t.accent)).on_mouse_down("left", (_e, cx) => cx.stop_propagation()).on_click((_e, cx) => choose(cx))
      .child(h_flex().w(16).h(16).flex_none().rounded(8).border(2).border_color(on ? t.accent : t.dim).items_center().justify_center()
        .child(div().w(8).h(8).rounded(4).bg(on ? t.accent : t.raised)))
      .child(v_flex().flex_1().min_w_0().gap(3).child(div().font_semibold().text_color(t.foreground).child(title)).child(div().text_size(12).text_color(t.muted).child(sub)));
  }

  /** Settings › Colors: the palette, and how each color the palette gives reads once tkt raises it. */
  colorsSection(t, d, focusable) {
    const palette = d.palette ?? "herdr";
    const source = sourceColors(theme(), palette);
    const option = (id, title, sub) => this.choice(t, focusable, `palette-${id}`, palette === id, title, sub, (cx) => this.attempt(() => this.patchSettings(palettePatch(id)), cx));
    const ratio = (c, min, note) => {
      const r = contrast(c, t.raised);
      const ok = r >= min;
      const pill = div().flex_none().px(6).rounded(4).text_size(11).text_color(ok ? t.ansi2 : t.ansi1).bg(t.surface).child(`${r.toFixed(1)}:1${ok ? "" : " ✕"}`);
      return note ? v_flex().flex_none().items_end().child(pill).child(div().text_size(10).text_color(t.muted).child(note)) : pill;
    };
    const roles = [["Secondary text", "muted", 4.5, "Ana López · 3 pts"], ["Faint text", "dim", 4.5, "accounts-web · Product"], ["In Progress", "ansi4", 3, "● In Progress"],
      ["In Review", "ansi5", 3, "● In Review"], ["Done", "ansi2", 3, "#49 approved"], ["Blocked", "ansi1", 4.5, "Blocked"]];
    const cell = (color, min, sample, note) => h_flex().flex_1().min_w_0().gap(6).items_center()
      .child(div().flex_none().px(8).py(3).rounded(5).bg(t.raised).text_size(12).font_weight(500).text_color(color).child(sample))
      .child(div().flex_1()).child(ratio(color, min, note));
    const heading = (text) => div().pb(8).font_semibold().text_size(15).text_color(t.foreground).child(text);
    const left = v_flex().w(300).flex_none().gap(8).pr(24).border_r(1).border_color(t.border)
      .child(heading("Palette"))
      .child(option("herdr", "Herdr theme", "Column and PR colors come from the terminal colors of your Herdr theme, so tkt changes with it."))
      .child(option("tkt", "tkt · Catppuccin", "Catppuccin Mocha in dark (Latte in light), as the TUI uses, with its grays raised to read at 4.5:1. Backgrounds and agent colors still follow Herdr."))
      .child(div().pt(16).child(heading("Legibility")))
      .child(div().text_size(12).text_color(t.foreground).child("Whatever the palette, tkt never draws text below 4.5:1 contrast, nor status marks below 3:1. A color the theme makes too faint is darkened or lightened until it passes; its hue stays."))
      .child(div().pt(6).text_size(12).text_color(t.muted).child("Backgrounds always come from the Herdr theme, so tkt matches the window it lives in."));
    const right = v_flex().flex_1().min_w_0().gap(10).pl(20)
      .child(h_flex().gap(10).items_end().child(div().font_semibold().text_size(15).text_color(t.foreground).child("On the current theme")).child(div().text_size(12).text_color(t.muted).child("on a card")))
      .child(v_flex().rounded(10).border(1).border_color(t.border)
        .child(h_flex().px(12).py(8).gap(10).bg(t.chrome).text_size(11).font_semibold().text_color(t.muted)
          .child(div().w(96).child("Used for")).child(div().flex_1().child("The palette's color")).child(div().flex_1().child("What tkt draws")))
        .children(roles.map(([label, key, min, sample]) => {
          const from = source[key] ?? t[key];
          return h_flex().id(`role-${key}`).px(12).py(8).gap(10).items_center().border_t(1).border_color(t.border)
            .child(div().w(96).text_size(12).text_color(t.foreground).child(label))
            .child(cell(from, min, sample)).child(cell(t[key], min, sample, from === t[key] ? "" : "adjusted"));
        })))
      .child(h_flex().pt(10).gap(10).items_end().child(div().font_semibold().text_color(t.foreground).child("Light palette")).child(div().text_size(12).text_color(t.muted).child("Catppuccin Latte · defined, designed later")))
      .child(h_flex().gap(14).children(Object.entries(CATPPUCCIN.latte).map(([k, c]) =>
        v_flex().id(`latte-${k}`).gap(4).items_center().child(div().w(28).h(28).rounded(7).border(1).border_color(t.border).bg(c)).child(this.mono(div().text_size(10).text_color(t.muted)).child(c)))));
    return h_flex().items_start().child(left).child(right);
  }

  /** Puts a space's status in another column, then reads the statuses again and keeps it selected. */
  async moveStatus(space, name, column) {
    await this.patchSettings(statusPatch(space, name, { column }));
    this.settings.statuses[space] = await this.lookup("statuses", space);
    this.settings.focusStatus = name;
  }

  saveMark(change, cx) {
    const st = this.settings;
    const p = st.markPicker;
    st.markPicker = null;
    this.attempt(async () => {
      await this.patchSettings(statusPatch(p.space, p.name, change));
      st.statuses[p.space] = await this.lookup("statuses", p.space);
      st.focusStatus = p.name;
    }, cx);
  }

  /** Arrows over the Statuses board, and space to pick a status up and drop it; false for keys it leaves to Settings. */
  statusesKey(kk, cx) {
    const st = this.settings;
    if (st.section !== "statuses") return false;
    if (st.grab) {
      const g = st.grab;
      const r = grabKey(statusBoard(st.statuses[g.space], st.data.columns), g, kk);
      st.grab = r.grab ?? null;
      if (r.drop && g.to !== g.from) this.attempt(() => this.moveStatus(g.space, g.name, g.to), cx);
      cx.notify();
      return true;
    }
    const chip = st.chipAt?.[st.sel ?? 0];
    if (!chip) return false;
    st.chipFocus = true;
    if (kk === " ") {
      st.grab = { space: chip.space, name: chip.name, from: chip.column, to: chip.column };
      cx.notify();
      return true;
    }
    const to = statusMove(statusBoard(st.statuses[chip.space], st.data.columns), { column: chip.column, name: chip.name }, kk);
    if (!to || ((kk === "left" || kk === "h") && to.column === chip.column && to.name === chip.name)) return false;
    const idx = Object.keys(st.chipAt).find((i) => st.chipAt[i].name === to.name);
    if (idx !== undefined) {
      st.sel = Number(idx);
      if (st.rows) this.panes.settings.offset = keepVisible(this.panes.settings, rowTop(st.rows, st.chipAt[idx].row), st.rows.row);
    }
    cx.notify();
    return true;
  }

  markPickerKey(kk, k, cx) {
    const p = this.settings.markPicker;
    const marks = markChoices(this.settings.data.marks);
    const step = { left: -1, h: -1, right: 1, l: 1, up: -4, k: -4, down: 4, j: 4 }[kk];
    if (k === "escape") this.settings.markPicker = null;
    else if (step) p.sel = Math.max(0, Math.min(marks.length - 1, p.sel + step));
    else if ((kk === "enter" || kk === " ") && marks[p.sel]) this.saveMark({ mark: marks[p.sel].name }, cx);
  }

  /** The marks a status can take, each with its icon, the card's flag as it would look, and back to automatic. */
  markPickerBox(t) {
    const st = this.settings;
    const p = st.markPicker;
    const marks = markChoices(st.data.marks);
    const chosen = marks[p.sel];
    const mk = markOf(chosen?.name, p.name);
    return v_flex().id("marks").my(4).w(340).rounded(12).bg(t.popover).border(1).border_color(t.border).shadow_xl()
      .on_mouse_down("left", (_e, cx) => cx.stop_propagation()).child(v_flex().gap(12).p(14)
      .child(v_flex().gap(3).child(div().text_size(14).font_semibold().text_color(t.foreground).child(`Mark for ${p.name}`))
        .child(div().text_size(12).text_color(t.muted).child(`${p.space} tickets in this status show it on their card`)))
      .child(h_flex().flex_wrap().gap(6).children(marks.map((m, i) => {
        const on = i === p.sel;
        const tone = markOf(m.name, p.name);
        return v_flex().id(`mark-${m.name}`).w(70).h(52).gap(3).items_center().justify_center().rounded(8).border(1).border_color(on ? t.accent : t.border)
          .bg(on ? mix(t.accent, t.popover, 0.12) : t.raised).hover((h) => h.border_color(t.accent))
          .on_click((_e, cx) => this.saveMark({ mark: m.name }, cx))
          .child(div().text_size(18).text_color(tone ? t[tone.tone] ?? t.muted : t.dim).child(m.glyph))
          .child(div().text_size(10).text_color(t.muted).child(m.name));
      })))
      .child(h_flex().gap(8).px(10).py(8).items_center().rounded(8).bg(t.surface).border(1).border_color(t.border)
        .child(div().text_size(11).text_color(t.muted).child("On a card")).child(div().flex_1())
        .child(h_flex().gap(4).px(7).rounded(5).bg(t.pill).text_size(10).font_semibold().text_color(t.muted)
          .when(Boolean(mk), (el) => el.child(div().text_color(t[mk.tone] ?? t.muted).child(mk.glyph))).child(p.name)))
      .when(!p.markAuto, (el) => el.child(h_flex().id("mark-auto").h(26).px(8).items_center().rounded(6).text_size(12).text_color(t.foreground).hover((h) => h.bg(t.hover))
        .on_click((_e, cx) => this.saveMark(null, cx)).child("↺ Back to automatic (column and mark)")))
      .child(div().pt(9).border_t(1).border_color(t.border).child(this.keyLine(t, [["← →", "choose"], ["enter", "save"], ["esc", "cancel"]]))));
  }

  async openStart(key, cx) {
    const a = await this.call("GET", `/v1/tickets/${key}/start?herdr=desktop`);
    if (a.status !== 200) throw new Error(a.body?.message ?? `tkt answered ${a.status}`);
    const p = firstPicker(key, a.body);
    if (p.error) throw new Error(p.error);
    if (p.post) return this.postStart(key, p.post, cx);
    this.choices_ = a.body;
    this.picker = { ...p, checked: new Set(p.rows.filter((r) => r.checked).map((r) => r.value)) };
  }

  async postStart(key, body, cx) {
    const a = await this.call("POST", `/v1/tickets/${key}/start`, body);
    if (a.status === 409 && a.body?.code === "needs_answer" && a.body.needs === "clone") {
      this.picker = { step: "clone", key, body, title: `Start ${key}`, question: "Clone the repositories that have no clone here?", sel: 0, checked: new Set(),
        rows: [{ label: "Clone them", value: true }, { label: "Skip them", value: false }] };
      return;
    }
    if (a.status === 202) {
      this.starting = { key, id: a.body.id, since: Date.now() };
      savePending({ key, id: a.body.id, since: this.starting.since, machine: machineKey(this.machine) });
      this.followStart(cx);
    }
    else if (a.status === 200) this.notice = `went to ${key}'s agent`;
    else throw new Error(a.body?.message ?? `tkt answered ${a.status}`);
  }

  /** Redraws the Start the mod waits on and asks its job how it ended, until it ends; a failure the modal shows stays
   *  until closed, one it no longer shows goes to the footer. */
  followStart(cx) {
    const s = this.starting;
    if (!s || s.failed) return;
    cx.timer.after(250, (cx) => cx.spawn(async (cx) => {
      if (this.starting !== s) return;
      if (Date.now() - (s.polled ?? 0) >= 2000) {
        s.polled = Date.now();
        let job = { state: "running" };
        try { job = startingJob(await this.call("GET", `/v1/jobs/${s.id}`)); } catch {}
        if (this.starting !== s) return;
        if (job.state !== "running") savePending(null);
        if (job.state === "gone") this.starting = null;
        else if (job.state === "failed" && !s.hidden) s.failed = job.message;
        else if (job.state !== "running") {
          this.starting = null;
          this.startNotice = startingNotice(s.key, job);
          this.notice = this.startNotice.text;
          notify(startingToast(s.key, job));
        }
      }
      cx.notify();
      this.followStart(cx);
    }));
  }

  /** Follows again, hidden, a Start the mod was closed on, once tkt's machine is known, so how it ended is not lost. */
  resumeStart(cx) {
    if (this.starting || this.machine === undefined) return;
    const pending = loadPending();
    if (!resumable(pending, machineKey(this.machine), Date.now())) {
      if (pending) savePending(null);
      return;
    }
    this.starting = { key: pending.key, id: pending.id, since: pending.since, hidden: true };
    this.followStart(cx);
  }

  /** The Start the mod waits on, over the dimmed board, which it blocks until hidden or, after a failure, closed. */
  startingBox(t) {
    const v = startingView(this.starting, Date.now());
    return div().id("starting-block").absolute().inset_0().on_mouse_down("left", (_e, cx) => cx.stop_propagation())
      .child(this.dialog(t, "starting", 400, v_flex().gap(12).px(18).py(16)
        .child(div().text_size(14).font_semibold().text_color(t.foreground).child(v.title))
        .child(this.starting.failed
          ? div().text_size(12).text_color(ensure(t.ansi1, t.popover, 4.5)).child(v.line)
          : h_flex().gap(8).items_center().text_size(13)
            .child(this.icon("spinner", 15).text_color(t.accent).spin(800))
            .child(div().flex_1().text_color(t.foreground).child(v.line))
            .child(this.mono(div().flex_none().text_size(12).text_color(t.muted)).child(v.elapsed)))
        .child(div().pt(9).border_t(1).border_color(t.border).child(this.keyLine(t, v.keys)))));
  }

  async openTask(subject, query) {
    const a = await this.call("GET", `/v1/tasks/agents?herdr=desktop&${query}`);
    if (a.status !== 200) throw new Error(a.body?.message ?? `tkt answered ${a.status}`);
    this.picker = { ...taskPicker(a.body), subject, checked: new Set() };
  }

  async sendTask(body) {
    const a = await this.call("POST", "/v1/tasks", { herdr: "desktop", ...body });
    if (a.status === 409 && a.body?.code === "needs_answer" && a.body.needs === "clone") {
      this.picker = { step: "taskClone", body, title: "Clone the repository?", question: `${a.body.repo ?? "It"} has no clone: clone it into ${a.body.into ?? "your clones folder"}?`, sel: 0, checked: new Set(),
        rows: [{ label: "Clone it", value: true }, { label: "Cancel", value: false }] };
      return;
    }
    if (a.status === 202) this.jobs.push({ id: a.body.id, what: "the task" });
    if (a.status === 200 || a.status === 202) this.notice = "sent to the agent";
    else throw new Error(a.body?.message ?? `tkt answered ${a.status}`);
  }

  /** One row of the open picker was chosen (or, on a multi picker, its Next pressed with values). */
  choose(values, row, cx) {
    const p = this.picker;
    this.picker = null;
    cx.spawn(async (cx) => {
      try {
        if (p.step === "clone") await this.postStart(p.key, { ...p.body, clone: row.value }, cx);
        else if (p.step === "taskClone") { if (row.value) await this.sendTask({ ...p.body, clone: true }); }
        else if (p.step === "task") {
          if (row.task) await this.sendTask({ ...p.subject, ...row.task });
          else this.picker = { step: "kind", subject: p.subject, title: p.title, question: "Which agent?", checked: new Set(),
            sel: Math.max(row.kinds.indexOf(row.last), 0), rows: row.kinds.map((k) => ({ label: k, hint: k === row.last ? "last used" : "", value: k })) };
        } else if (p.step === "kind") await this.sendTask({ ...p.subject, to: "new", agent: row.value });
        else {
          const next = answer(p, values, this.choices_);
          if (next.post) await this.postStart(p.key, next.post, cx);
          else this.picker = { ...next.picker, checked: new Set() };
        }
      } catch (e) {
        this.notice = `could not: ${e.message}`;
      }
      cx.notify();
    });
    cx.notify();
  }

  pickerBox(t) {
    const p = this.picker;
    return this.dialog(t, "picker", 440, v_flex()
      .child(v_flex().gap(4).px(16).pt(14).pb(12).child(div().text_size(14).font_semibold().text_color(t.foreground).child(p.title))
        .when(Boolean(p.question), (el) => el.child(div().text_size(12).text_color(t.muted).child(p.question))))
      .child(div().h(1).bg(t.border))
      .child(v_flex().p(6).gap(1).children(p.rows.map((r, i) => {
        const on = i === p.sel;
        return h_flex().id(`pick-${i}`).h(32).gap(8).pl(6).pr(10).items_center().rounded(7).border(1).border_color(on ? mix(t.accent, t.popover, 0.45) : t.popover)
          .bg(on ? mix(t.accent, t.popover, 0.12) : t.popover).hover((s) => s.bg(t.hover))
          .on_click((_e, cx) => {
            if (p.multi) {
              if (p.checked.has(r.value)) p.checked.delete(r.value); else p.checked.add(r.value);
              cx.notify();
            } else this.choose([r.value], r, cx);
          })
          .child(div().w(10).text_size(11).text_color(t.accent).child(on ? "▸" : ""))
          .when(Boolean(p.multi), (el) => el.child(this.checkbox(t, p.checked.has(r.value))))
          .child(div().flex_1().min_w_0().truncate().text_size(13).text_color(t.foreground).child(r.label))
          .when(Boolean(r.hint), (el) => el.child(div().text_size(12).text_color(/^● /.test(r.hint) ? t.working ?? t.muted : t.muted).child(r.hint)));
      })))
      .when(Boolean(p.multi), (el) => el.child(h_flex().gap(8).justify_end().px(14).pb(12)
        .child(h_flex().id("pick-cancel").h(30).px(14).items_center().rounded(7).border(1).border_color(t.border).text_size(13).text_color(t.foreground)
          .on_click((_e, cx) => { this.picker = null; cx.notify(); }).child("Cancel"))
        .child(h_flex().id("pick-next").h(30).px(16).items_center().rounded(7).bg(t.accent).text_size(13).font_semibold().text_color(ensure(t.chrome, t.accent, 4.5))
          .on_click((_e, cx) => { if (p.checked.size > 0) this.choose([...p.checked], null, cx); }).child("Next"))))
      .child(div().px(16).py(9).border_t(1).border_color(t.border)
        .child(this.keyLine(t, p.multi ? [["space", "tick"], ["enter", "next"], ["esc", "cancel"]] : [["enter", "choose"], ["esc", "cancel"]]))));
  }

  /** A centered box over the dimmed window, as every menu, picker, prompt and confirmation. */
  dialog(t, id, width, content) {
    return div().id(id).absolute().inset_0()
      .child(div().absolute().inset_0().bg(scrim(t.surface)))
      .child(v_flex().absolute().inset_0().items_center().justify_center()
        .child(v_flex().id(`${id}-box`).w(width).rounded(12).bg(t.popover).border(1).border_color(t.border).shadow_xl()
          .on_mouse_down("left", (_e, cx) => cx.stop_propagation()).child(content)));
  }

  keyLine(t, pairs) {
    return h_flex().gap(12).items_center().children(pairs.map(([k, what], i) => h_flex().id(`mk-${i}`).gap(5).items_center().text_size(11).text_color(t.muted).child(this.keyCap(t, k)).child(what)));
  }

  menuBox(t) {
    const m = this.menu;
    if (isConfirm(m)) return this.confirmBox(t, m);
    const header = v_flex().gap(4).px(16).pt(14).pb(12)
      .child(h_flex().gap(8).items_center()
        .when(Boolean(m.back), (el) => el.child(h_flex().id("menu-back").w(22).h(22).items_center().justify_center().rounded(5).bg(t.pill).text_size(12).text_color(t.foreground)
          .on_click((_e, cx) => { this.menu = backOf(this.menu); cx.notify(); }).child("‹")))
        .child(div().text_size(14).font_semibold().text_color(t.foreground).child(m.title)))
      .when(Boolean(m.subtitle), (el) => el.child(div().text_size(12).text_color(t.muted).child(m.subtitle)))
      .when((m.reviewers ?? []).length > 0, (el) => el.child(h_flex().flex_wrap().gap(10).text_size(12)
        .children(m.reviewers.map((r, i) => div().id(`rev-${i}`).text_color(t[r.tone] ?? t.foreground).child(r.text)))));
    const row = (item, i, inPlain = false) => {
      const { label, tail } = splitTail(item.label);
      const on = i === m.sel;
      const primary = item.action === "fix" || item.action === "transition";
      const split = !inPlain && i > 0 && itemGroup(item) !== itemGroup(m.items[i - 1]);
      return h_flex().id(`mi-${i}`).h(MENU_ROW).gap(8).pl(6).pr(10).items_center().rounded(7).border(1).border_color(on ? mix(t.accent, t.popover, 0.45) : t.popover)
        .bg(on ? mix(t.accent, t.popover, 0.12) : t.popover)
        .relative().when(split, (el) => el.child(div().absolute().top(0).left(8).right(8).h(1).bg(t.border)))
        .child(div().w(10).text_size(11).text_color(t.accent).child(on ? "▸" : ""))
        .child(div().flex_1().min_w_0().truncate().text_size(13).text_color(primary ? ensure(mix(t.accent, t.foreground, 0.3), t.popover, 4.5) : t.foreground).when(primary, (el) => el.font_semibold()).child(label))
        .when(Boolean(item.keyHint), (el) => el.child(this.keyCap(t, item.keyHint)))
        .when(Boolean(tail), (el) => el.child(div().text_size(12).text_color(t.muted).child(tail)));
    };
    const plain = m.items.length <= 14;
    const list = plain ? v_flex().gap(1).children(m.items.flatMap((item, i) => [
      ...(i > 0 && itemGroup(item) !== itemGroup(m.items[i - 1]) ? [div().id(`msep-${i}`).h(1).mx(8).my(5).bg(t.border)] : []),
      row(item, i, true).on_click((_e, cx) => this.act(item, cx))]))
      : v_flex().relative().h(menuHeight(m.items.length, this.windowHeight))
      .child(v_virtual_list("menu-items", m.items.length, MENU_ROW, (i) => `${m.title}-${i}`, (range) =>
        m.items.slice(range.start, range.end).map((item, j) => row(item, range.start + j))).size_full().track_scroll(this.scrolls.menu).on_item_click((key, cx) => {
        const i = Number(key.slice(key.lastIndexOf("-") + 1));
        if (m.items[i]) this.act(m.items[i], cx);
      }))
      .child(Scrollbar.vertical("menu-items").absolute().inset_0());
    return this.dialog(t, "menu", 440, v_flex().child(header).child(div().h(1).bg(t.border)).child(div().p(6).child(list))
      .child(div().px(16).py(9).border_t(1).border_color(t.border).child(this.keyLine(t, footerKeys(menuFooter(m.items[m.sel]))))));
  }

  /** A yes-or-Cancel menu as a dialog: the question, what it changes, and the two buttons; enter runs the one selected. */
  confirmBox(t, m) {
    const [yes, no] = m.items;
    const danger = /remove/i.test(yes.label);
    const yesColor = danger ? t.ansi1 : t.accent;
    const row = (yes.key && this.boardRow(yes.key)) || (!yes.key ? this.selectedRow(this.answers.sprint?.body) : null);
    const move = yes.action === "transition" && yes.move;
    const aboutTicket = ["transition", "assign", "moveToSprint"].includes(yes.action);
    const body = m.subtitle || (aboutTicket ? row?.ticket?.summary : yes.action === "settingsPatch" ? "Only tkt's configuration changes: nothing in Jira, GitHub or your clones." : "");
    const button = (item, i, solid) => {
      const on = m.sel === i;
      return h_flex().id(`confirm-${i}`).h(32).px(14).items_center().rounded(7).text_size(13).font_weight(solid ? 600 : 500)
        .border(on ? 2 : 1).border_color(on ? (solid ? mix(yesColor, t.foreground, 0.35) : t.accent) : solid ? yesColor : t.border)
        .bg(solid ? yesColor : t.popover).text_color(solid ? ensure(t.chrome, yesColor, 4.5) : t.foreground)
        .on_click((_e, cx) => this.act(item, cx)).child(item.label);
    };
    return this.dialog(t, "menu", 440, v_flex().gap(16).px(18).pt(18).pb(14)
      .child(v_flex().gap(6).child(div().text_size(15).font_semibold().text_color(t.foreground).child(m.title))
        .when(Boolean(body), (el) => el.child(div().text_size(13).text_color(t.foreground).child(body))))
      .when(Boolean(move), (el) => el.child(h_flex().gap(10).px(12).py(10).items_center().rounded(8).bg(t.surface).border(1).border_color(t.border).text_size(12)
        .when(Boolean(row?.ticket?.status), (r) => r.child(div().text_color(t.muted).child(row.ticket.status)).child(div().text_color(t.muted).child("→")))
        .child(div().font_weight(500).text_color(t.foreground).child(move.to)).child(div().flex_1()).child(div().text_size(11).text_color(t.muted).child("writes to Jira"))))
      .child(h_flex().gap(8).justify_end().child(button(no, 1, false)).child(button(yes, 0, true)))
      .child(this.keyLine(t, [["enter", m.sel === 0 ? splitTail(yes.label).label.replace(/^Yes, /, "") : "cancel"], ["↑↓", "choose"], ["esc", "cancel"]])));
  }


  key(e, cx) {
    if (!this.prompt && !this.panel && keyOf(e) === "q") e = { ...e, key: "escape", keystroke: "escape", key_char: undefined };
    const k = e.key;
    if (this.starting && !this.starting.hidden) {
      const what = startingKey(this.starting, k);
      if (what === "close") this.starting = null;
      if (what === "hide") this.starting.hidden = true;
      cx.notify();
      return;
    }
    if (this.keysOpen) { this.keysOpen = false; cx.notify(); return; }
    if (this.machineMenu) {
      const m = this.machineMenu;
      if (k === "escape" || k === "m") this.machineMenu = null;
      else if (k === "down" || k === "up") m.sel = Math.max(0, Math.min(m.items.length - 1, m.sel + (k === "down" ? 1 : -1)));
      else if (k === "enter") { this.machineMenu = null; this.use(m.items[m.sel], cx); }
      cx.notify();
      return;
    }
    if (this.prompt) {
      const p = this.prompt;
      if (k === "escape") this.closePrompt();
      else if (k === "enter") return this.submitPrompt(cx);
      else if (k === "tab" && completes(p)) this.promptRows(p)[p.sel]?.pick(cx);
      else if (listed(p) && (k === "down" || k === "up")) p.sel = Math.max(0, Math.min(this.promptRows(p).length - 1, p.sel + (k === "down" ? 1 : -1)));
      cx.notify();
      return;
    }
    if (this.menu) {
      if (k === "escape") this.menu = backOf(this.menu);
      if (k === "down") this.menu.sel = Math.min(this.menu.items.length - 1, this.menu.sel + 1);
      if (k === "up") this.menu.sel = Math.max(0, this.menu.sel - 1);
      if (this.menu && (k === "down" || k === "up")) this.scrolls.menu.scroll_to_item(this.menu.sel);
      if (k === "enter") return this.act(this.menu.items[this.menu.sel], cx);
      const item = this.menu?.items[this.menu.sel];
      if (keyOf(e) === "c" && item?.action === "open" && item.url) return this.act({ action: "copy", value: item.url, label: "copy" }, cx);
    } else if (this.picker) {
      this.pickerKeyDown(keyOf(e), cx);
    } else if (this.details && !this.prompt) {
      const kk = keyOf(e);
      const a = detailsKey(kk);
      const to = scrolled(this.panes.details, kk);
      const acts = this.details.actions ?? [];
      if (this.details.actFocus) {
        if (k === "escape" || kk === "left" || kk === "h") this.details.actFocus = false;
        else if (listKey(this.details.actSel ?? 0, acts.length, kk) !== null) { this.details.actSel = listKey(this.details.actSel ?? 0, acts.length, kk); this.showFocused(); }
        else if ((kk === "enter" || kk === " ") && acts[this.details.actSel ?? 0]) this.act(acts[this.details.actSel ?? 0], cx);
      } else if (a === "actions" && acts.length > 0) { this.details.actFocus = true; this.details.actSel = 0; this.showFocused(); }
      else if (a === "back") this.details = null;
      else if (a === "copy") this.act({ action: "copyContext", key: this.details.key, label: "copy" }, cx);
      else if (kk === "enter" && this.details.ticket) this.menu = { ...detailsMenu(this.details), sel: 0 };
      else if (to !== null) this.panes.details.offset = to;
    } else if (this.settings && !this.prompt && this.settings.markPicker) {
      this.markPickerKey(keyOf(e), k, cx);
    } else if (this.settings && !this.prompt && this.settings.zone !== "menu" && this.statusesKey(k === "escape" ? "escape" : keyOf(e), cx)) {
      // handled by the Statuses board
    } else if (this.settings && !this.prompt) {
      const st = this.settings;
      const kk = keyOf(e);
      const nav = settingsNav({ zone: st.zone ?? "menu", section: st.section, sel: st.sel ?? 0 }, SECTIONS.map((x) => x.id), (st.controls ?? []).length, kk);
      if (k === "escape") this.settings = null;
      else if (nav && nav.section !== st.section) {
        Object.assign(st, nav, { chipFocus: false, drag: null, press: null, grab: null });
        this.panes.settings.offset = 0;
        if (nav.section === "statuses") this.attempt(() => this.readStatuses(), cx);
      } else if (nav) Object.assign(st, nav, { chipFocus: nav.zone === "content" && Boolean(st.chipAt?.[nav.sel]) });
      else if ((kk === "enter" || kk === " ") && st.controls?.[st.sel ?? 0]) st.controls[st.sel ?? 0](cx);
      else {
        const off = scrolled(this.panes.settings, kk);
        if (off !== null) this.panes.settings.offset = off;
      }
    } else if (this.panel && this.openPanel()) {
      this.panelKey(keyOf(e), cx);
    } else if (!this.prompt && !this.choices && !this.details && !this.settings && !this.picker && !this.panel) {
      this.boardKey(e, cx);
    }
    cx.notify();
  }

  /** The Boards and Repos filters from the keyboard: typing narrows, up and down move, space ticks, ctrl-a all or none, enter applies, esc cancels. */
  panelKey(k, cx) {
    const p = this.openPanel();
    const { draft, done } = filterKey(this.panelDraft ?? filterDraft(p.hidden), p.items, k);
    this.panelDraft = draft;
    if (done === "apply") this.applyPanel(cx);
    else if (done === "cancel") this.togglePanel(this.panel);
  }

  pickerKeyDown(k, cx) {
    const p = this.picker;
    if (k === "escape") { this.picker = null; return; }
    const r = pickerKey(p, k);
    if (r?.kind === "move") p.sel = r.sel;
    if (r?.kind === "toggle") { if (p.checked.has(r.value)) p.checked.delete(r.value); else p.checked.add(r.value); }
    if (r?.kind === "next") this.choose([...p.checked], null, cx);
    if (r?.kind === "choose") this.choose([p.rows[r.index].value], p.rows[r.index], cx);
  }

  /** The selected row of the tab in view, with its ticket and, on the PRs tab, its pull request. */
  current() {
    const body = this.answers[this.tab]?.body;
    if (!this.selected || !body) return null;
    if (this.tab === "prs") {
      const row = prRows(body).find((r) => r.kind === "row" && r.key + r.group === this.selected);
      return row ? { row, key: row.ticketKey, ticket: row.raw.ticket, pr: row.raw.pr } : null;
    }
    const row = this.tab === "backlog" ? backlogRows(body).find((r) => r.kind === "row" && r.key === this.selected) : this.selectedRow(body);
    return row ? { row, key: row.key, ticket: row.ticket, pr: null } : null;
  }

  /** What a board key does to the selection, as the TUI's keys do. */
  runKey(action, cx) {
    const close = () => { const id = props()?.overlay; if (id) closeOverlay(id); };
    if (action === "close") return close();
    if (action === "refresh") return this.sync(cx);
    if (action === "settings") return this.attempt(() => this.openSettings(), cx);
    if (action === "machines") return this.openMachines(cx);
    if (action === "boards" || action === "repos") return this.togglePanel(action);
    if (action === "bots") { this.bots = !this.bots; return this.load(cx); }
    if (action === "keys") { this.keysOpen = !this.keysOpen; return; }
    if (action === "density") {
      if (this.view !== "columns") return;
      this.compact = !this.compact;
      this.notice = this.compact ? "compact cards · z for details" : "detailed cards · z for compact";
      return;
    }
    if (action === "team") {
      if (this.tab === "prs") return this.patchState({ team_prs: !this.answers.prs?.body?.team_prs }, cx);
      return this.toggleTeam(cx);
    }
    if (action === "menu" && this.selected?.startsWith("group:")) {
      const body = this.answers[this.tab]?.body;
      return this.patchState({ collapsed: toggled(body?.collapsed, this.selected.slice("group:".length)) }, cx);
    }
    if (this.inStrip && action === "menu") {
      const chip = stripChips(this.answers.sprint?.body?.reviews, 6).chips[this.stripSel];
      if (chip) this.menu = { ...prMenu(chip.row, {}), sel: 0 };
      return;
    }
    const c = this.current();
    if (!c) return;
    const t = c.ticket ?? {};
    switch (action) {
      case "menu":
        this.menu = { ...this.menuFor(c.row), sel: 0 };
        return;
      case "details":
        if (c.key) this.goDetails(c.key, cx);
        return;
      case "openJira":
      case "copyJira":
        if (t.url) this.act(action === "openJira" ? { action: "open", url: t.url } : { action: "copy", value: t.url, label: "copy" }, cx);
        return;
      case "openPR":
      case "copyPR": {
        const prs = t.prs ?? [];
        if (!c.pr && prs.length > 1) {
          const item = this.menuFor(c.row).items.find((i) => i.label.startsWith("Pull requests ("));
          if (item) this.menu = { ...item.menu, sel: 0 };
          return;
        }
        const url = c.pr?.url ?? prs[0]?.url ?? "";
        if (!url) { this.notice = `${c.key} has no pull request yet`; return; }
        this.act(action === "openPR" ? { action: "open", url } : { action: "copy", value: url, label: "copy" }, cx);
        return;
      }
      case "feedback":
        if (t.team) { this.notice = `${c.key} is ${t.assignee || "nobody"}'s ticket`; return; }
        this.act({ action: this.answers.sprint?.body?.agents?.[c.key] ? "fix" : "feedbackTo", key: c.key, label: "feedback" }, cx);
        return;
    }
  }

  /** The board's keys, as the TUI's: tabs, the view, and moving the selection. */
  boardKey(e, cx) {
    const k = keyOf(e);
    const tab = tabAfter(k, this.tab);
    if (tab) return this.select(tab, cx);
    const action = boardAction(k, this.tab);
    if (action) return this.runKey(action, cx);
    if (k === "v") {
      if (this.tab === "backlog") return this.patchState({ backlog_merged: !this.answers.backlog?.body?.merged }, cx);
      if (this.tab === "sprint") return this.setView(this.view === "columns" ? "list" : "columns", cx);
      return;
    }
    const d = direction(k);
    const body = this.answers[this.tab]?.body;
    if (!d || !body) return;
    const at = targets(this.tab, this.view, body);
    const chips = this.tab === "sprint" ? stripChips(body.reviews, 6).chips : [];
    if (this.inStrip) {
      if (d[1] > 0 || chips.length === 0) this.inStrip = false;
      else this.stripSel = Math.max(0, Math.min(chips.length - 1, this.stripSel + d[0]));
      return;
    }
    if (d[1] < 0 && chips.length > 0 && atTop(at, this.selected)) {
      this.inStrip = true;
      this.stripSel = 0;
      return;
    }
    if (at.cols) {
      this.selected = stepColumns(at.cols, this.selected, d[0], d[1]);
      const ci = at.cols.findIndex((c) => c.includes(this.selected));
      const col = columnCards(body)[ci];
      if (col) {
        const heights = col.cards.map((c) => cardHeight({ lines: wrapWords(c.summary, 34, this.compact ? 1 : 2).length, label: Boolean(body.label_field), compact: this.compact }) + (this.compact ? 6 : 8));
        const i = at.cols[ci].indexOf(this.selected);
        const pane = this.colPane(col.category);
        pane.offset = revealed(pane, heights.slice(0, i).reduce((a, b) => a + b, 0), heights[i]);
      }
      return;
    }
    if (d[1] === 0) return;
    this.selected = stepList(at.ids, this.selected, d[1]);
    const i = at.ids.indexOf(this.selected);
    if (i >= 0) this.scrolls[at.list].scroll_to_item(at.at[i], "center");
  }

  sync(cx) {
    if (this.tab === "backlog") {
      this.refreshBacklog = true;
      this.load(cx);
      return;
    }
    cx.spawn(async (cx) => {
      try {
        await this.call("POST", "/v1/sync", {});
      } catch (e) {
        this.error = e.message;
      }
      this.load(cx);
      cx.notify();
    });
  }

  selectedRow(board) {
    if (!this.selected || !board) return undefined;
    for (const group of [...(board.columns ?? []), ...(board.sections ?? [])]) {
      const row = group.rows.find((r) => r.key === this.selected);
      if (row) return row;
    }
    return undefined;
  }

  keyCap(t, k) {
    return this.mono(div().px(5).rounded(4).border(1).border_color(t.border).text_size(10).text_color(t.foreground)).child(k);
  }

  /** The bottom bar: the selection and the sync on top; under it the selection's details, or a notice or an error,
   *  and the keys that matter on the tab. */
  footer(t) {
    const body = this.answers[this.tab]?.body;
    const board = !this.choices && !this.details && !this.settings;
    const row = board && this.tab === "sprint" ? this.selectedRow(body) : board && this.tab === "backlog" ? this.boardRow(this.selected) : null;
    const badge = row ? previewBadge(row) : null;
    const p = row ? previewOf(row) : board && this.tab === "prs" ? prPreview(prRows(body).find((r) => r.kind === "row" && r.key + r.group === this.selected)) : null;
    const where = this.machine === undefined ? "looking for tkt…" : `tkt on ${this.machine ?? "this computer"}`;
    const sync = this.tab === "backlog" && this.answers.backlog?.body?.error ? `! ${this.answers.backlog.body.error}` : this.cachedAt ? cachedLabel(this.cachedAt) : this.answers[this.tab]?.body?.sync_label ?? "";
    const warn = sync.startsWith("!");
    const title = (text) => div().flex_1().min_w_0().truncate().text_size(13).font_weight(500).text_color(t.foreground).child(text);
    const detailKeys = this.details?.actFocus ? [["↑↓", "choose"], ["enter", "run"], ["←", "back to the text"]] : [["esc", "back"], ["→", "actions"], ["c", "copy as markdown"]];
    const keys = this.settings
      ? (this.settings.section === "statuses" && this.settings.statuses?.[(this.settings.data.spaces.find((x) => x.key === this.settings.statusSpace) ?? this.settings.data.spaces[0])?.key]
        ? (this.settings.grab ? [["← →", "choose a column"], ["space", "drop"], ["esc", "cancel"]] : [["arrows", "select"], ["space", "pick up"], ["enter", "mark"], ["esc", "back to the board"]])
        : [["↑↓", "move"], ["enter", "choose"], ["esc", "back to the board"]])
      : this.details ? detailKeys : topKeys(this.tab);
    const line1 = h_flex().gap(10).items_center()
      .when(Boolean(this.details) && !this.settings, (el) => el.child(title(`Details of ${this.details.key}`)))
      .when(Boolean(badge), (el) => el.child(div().flex_none().px(8).py(1).rounded(5).bg(t.pill).text_size(11).font_semibold().text_color(t.muted).child(badge.status))
        .child(this.mono(div().flex_none().text_size(12).text_color(t.muted)).child(badge.key)).child(title(badge.summary)))
      .when(!badge && Boolean(p), (el) => el.child(title(p.title)))
      .when(!badge && !p && (!this.details || this.settings), (el) => el.child(div().flex_1().text_size(12).text_color(t.muted).child(board ? "nothing selected" : "")))
      .when(Boolean(sync), (el) => el.child(h_flex().flex_none().gap(6).items_center().text_size(11).text_color(warn ? ensure(t.ansi3, t.chrome, 4.5) : t.muted)
        .child(div().w(6).h(6).rounded(3).bg(warn ? t.ansi3 : t.ansi2)).child(sync.replace(/^✓ /, ""))))
      .child(h_flex().id("sync").w(22).h(22).flex_none().items_center().justify_center().rounded(5).text_color(t.muted).hover((s) => s.bg(t.hover).text_color(t.foreground))
        .on_mouse_down("left", (_e, cx) => cx.stop_propagation()).on_click((_e, cx) => this.sync(cx)).child(this.icon("refresh", 13)))
      .child(div().w(1).h(14).flex_none().bg(t.border))
      .child(Popup.new("machine-popup", h_flex().id("machine").flex_none().gap(5).px(6).h(22).items_center().rounded(5).text_size(11).text_color(t.muted).hover((s) => s.bg(t.hover).text_color(t.foreground))
        .on_mouse_down("left", (_e, cx) => cx.stop_propagation()).on_click((_e, cx) => this.openMachines(cx)).child(where).child("▾"))
        .anchor("top_right").when(Boolean(this.machineMenu), (el) => el.content(this.machineBox(t))));
    const error = this.error ? problemOf(this.error, this.machine) : "";
    const running = this.starting?.hidden ? startingView(this.starting, Date.now()) : null;
    const notice = running ? running.footer : this.notice;
    // The tone holds only while the footer still shows the Start's own line, not a later notice.
    const tone = running?.tone ?? (this.startNotice?.text === notice ? this.startNotice.tone : null);
    const toneColor = { accent: t.accent, ok: ensure(t.ansi2, t.chrome, 4.5), error: ensure(t.ansi1, t.chrome, 4.5) }[tone];
    const label = mix(t.ansi4, t.chrome, 0.14);
    const line2 = h_flex().gap(8).items_center()
      .child(error ? div().flex_1().min_w_0().truncate().text_size(12).text_color(ensure(t.ansi1, t.chrome, 4.5)).child(error)
        : notice && toneColor ? h_flex().flex_1().min_w_0().gap(6).items_center()
          .child(running ? this.icon("spinner", 12).text_color(toneColor).spin(800) : div().w(6).h(6).flex_none().rounded(3).bg(toneColor))
          .child(div().flex_1().min_w_0().truncate().text_size(12).font_weight(500).text_color(toneColor).child(notice))
        : notice ? div().flex_1().min_w_0().truncate().text_size(12).text_color(t.foreground).child(notice)
          : h_flex().flex_1().min_w_0().gap(8).items_center()
            .when(Boolean(p?.label), (el) => el.child(div().flex_none().px(8).rounded(4).bg(label).text_size(11).text_color(ensure(mix(t.ansi4, t.foreground, 0.45), label, 4.5)).child(p.label)))
            .child(div().flex_1().min_w_0().truncate().text_size(12).text_color(t.muted).child(p?.line ?? "")))
      .children(keys.map(([k, what], i) => h_flex().id(`key-${i}`).flex_none().gap(5).items_center().text_size(11).text_color(t.muted).child(this.keyCap(t, k)).child(what)));
    return v_flex().px(16).pt(9).pb(10).gap(6).bg(t.chrome).border_t(1).border_color(t.border).child(line1).child(line2);
  }

  /** Every key the tab answers, as the legend lists them; any key closes it. */
  keysBox(t) {
    const body = this.answers[this.tab]?.body;
    const opts = { strip: (this.answers.sprint?.body?.reviews ?? []).length > 0, bots: body?.hidden_bots ?? 0, merged: Boolean(body?.merged) };
    return div().id("keys").absolute().inset_0()
      .child(div().absolute().inset_0().bg(scrim(t.surface)))
      .child(v_flex().absolute().inset_0().items_center().justify_center()
        .child(v_flex().id("keys-box").w(520).p(16).gap(10).rounded(12).bg(t.popover).border(1).border_color(t.border).shadow_lg()
          .on_mouse_down("left", (_e, cx) => { cx.stop_propagation(); this.keysOpen = false; cx.notify(); })
          .child(div().text_size(14).font_semibold().text_color(t.foreground).child("Keys"))
          .child(h_flex().flex_wrap().gap_x(18).gap_y(8).children(legendFor(this.tab, this.view, opts).map(([k, what], i) =>
            h_flex().id(`all-key-${i}`).w(220).gap(8).items_center().text_size(12).text_color(t.muted).child(this.keyCap(t, k)).child(what))))
          .child(div().text_size(11).text_color(t.muted).child("any key closes"))));
  }

  select(id, cx) {
    this.tab = id;
    this.cacheSaved = 0;
    this.inStrip = false;
    this.selected = null;
    this.load(cx);
    cx.notify();
  }

  /** The review requests over the Sprint's tickets, each chip opening the PRs tab's menu for it. */
  strip(t, board) {
    const { chips, more } = stripChips(board?.reviews, 6);
    if (chips.length === 0) return div();
    const amber = mix(t.ansi3, t.raised, 0.05);
    return h_flex().id("strip").px(18).pt(12).gap(8).items_center()
      .child(div().text_size(11).font_weight(500).text_color(t.muted).child("Review requested"))
      .children(chips.map((c, i) => {
        const on = this.inStrip && this.stripSel === i;
        return h_flex().id(`chip-${i}`).h(26).gap(6).px(10).items_center().rounded(13).border(1).border_color(on ? t.accent : mix(t.ansi3, t.raised, 0.28)).bg(amber)
          .hover((s) => s.border_color(t.accent)).on_mouse_down("left", (_e, cx) => cx.stop_propagation())
          .on_click((_e, cx) => { this.menu = { ...prMenu(c.row, {}), sel: 0 }; cx.notify(); })
          .child(div().text_size(12).font_semibold().text_color(on ? t.accent : ensure(t.ansi3, amber, 4.5)).child(on ? "▸" : "!"))
          .child(this.mono(div().text_size(11).text_color(ensure(t.foreground, amber, 7))).child(c.label));
      }))
      .when(Boolean(more), (el) => el.child(div().text_size(11).text_color(t.muted).child(more)));
  }

  /** The line under the tabs: the tab summed up, and on its right the filter's notice, which a click opens. */
  contextLine(t) {
    const body = this.answers[this.tab]?.body;
    const notice = scopeNotice(this.tab, body);
    const text = contextText(this.tab, body, { loading: this.tab === "backlog" && !body }) || " ";
    const parts = this.tab === "sprint"
      ? text.split(/ {3,}/).flatMap((sprint, i) => {
        const words = sprint.split(" · ");
        const head = words.length > 1 ? words.slice(0, -1).join(" · ") : sprint;
        return [...(i > 0 ? [div().w(14)] : []), div().text_size(13).font_semibold().text_color(t.foreground).child(head), ...(words.length > 1 ? [div().text_size(12).text_color(t.muted).child(`· ${words.at(-1)}`)] : [])];
      })
      : text.split(" · ").flatMap((w, i) => [...(i > 0 ? [div().text_size(12).text_color(t.dim).child("·")] : []),
        div().text_size(12).text_color(/needs? you/.test(w) ? ensure(t.ansi3, t.surface, 4.5) : i === 0 ? t.foreground : t.muted).child(w)]);
    return h_flex().px(18).pt(12).gap(8).items_center()
      .child(h_flex().flex_1().min_w_0().gap(8).items_center().children(parts))
      .when(Boolean(notice), (el) => el.child(h_flex().id("scope-notice").gap(5).items_center().text_size(11).text_color(t.muted).hover((s) => s.text_color(t.foreground))
        .on_mouse_down("left", (_e, cx) => cx.stop_propagation())
        .on_click((_e, cx) => { this.togglePanel(this.tab === "prs" ? "repos" : "boards"); cx.notify(); })
        .child(notice.text.replace(/ · $/, " ·")).child(this.keyCap(t, notice.key)).child(notice.after.trim())));
  }

  header(t) {
    const close = () => {
      const id = props()?.overlay;
      if (id) closeOverlay(id);
    };
    const tabs = h_flex().p(3).gap(2).rounded(8).border(1).border_color(t.border).bg(t.surface).children(TABS.map((tab, i) => {
      const on = tab.id === this.tab;
      const count = tabCount(tab.id, this.answers[tab.id]?.body);
      return h_flex().id(`tab-${tab.id}`).h(28).px(10).gap(7).items_center().rounded(5).bg(on ? t.pill : t.surface).hover((s) => s.bg(t.hover))
        .on_mouse_down("left", (_e, cx) => cx.stop_propagation())
        .on_click((_e, cx) => this.select(tab.id, cx))
        .child(this.mono(div().text_size(10).text_color(t.muted)).child(String(i + 1)))
        .child(div().text_size(13).text_color(on ? t.foreground : t.muted).font_weight(on ? 600 : 500).child(tab.title))
        .when(count !== "", (el) => el.child(div().px(6).rounded(8).text_size(10).font_semibold().bg(on ? t.accent : t.pill).text_color(on ? ensure(t.chrome, t.accent, 4.5) : t.foreground).child(count)));
    }));
    return h_flex().h(52).px(14).gap(12).items_center().bg(t.chrome).border_b(1).border_color(t.border)
      .child(h_flex().gap(8).items_center().mr(6)
        .child(h_flex().w(20).h(20).rounded(5).bg(t.accent).items_center().justify_center().text_size(12).font_semibold().text_color(ensure(t.chrome, t.accent, 4.5)).child("t"))
        .child(div().text_size(14).font_semibold().text_color(t.foreground).child("tkt")))
      .child(tabs)
      .child(div().flex_1())
      .when(this.tab === "sprint", (el) => el.child(this.viewSwitch(t)).child(this.teamToggle(t)).child(this.boardsChip(t)))
      .when(this.tab === "prs", (el) => el.child(this.prControls(t)))
      .when(this.tab === "backlog", (el) => el.child(this.backlogControls(t)))
      .child(this.iconButton(t, "gear", "gear", (cx) => { if (this.settings) { this.settings = null; cx.notify(); } else this.attempt(() => this.openSettings(), cx); }))
      .child(this.iconButton(t, "close", "close", () => close()));
  }

  chooser(t) {
    return v_flex().p(16).gap(8).text_color(t.foreground)
      .child(div().child("tkt is installed on these machines. Which one's board should this window show?"))
      .children(this.choices.map((on) =>
        div().id(`m-${on ?? "here"}`).px(10).py(6).rounded(6).bg(t.raised).hover((s) => s.bg(t.hover))
          .on_mouse_down("left", (_e, cx) => cx.stop_propagation())
          .on_click((_e, cx) => this.use(on, cx))
          .child(`${on ?? "This computer"}${on === this.machine ? "  ✓" : ""}`)))
      .when(this.machine !== undefined, (el) => el.child(
        div().id("m-cancel").px(10).py(6).text_color(t.muted).hover((s) => s.text_color(t.foreground))
          .on_mouse_down("left", (_e, cx) => cx.stop_propagation())
          .on_click((_e, cx) => { this.choices = null; cx.notify(); })
          .child("Keep the current one")));
  }

  viewSwitch(t) {
    return this.segment(t, "view", [{ label: "Columns", on: this.view === "columns", click: (cx) => this.setView("columns", cx) }, { label: "List", on: this.view === "list", click: (cx) => this.setView("list", cx) }]);
  }

  card(t, c, labelField) {
    const mark = markOf(c.mark, c.status);
    const flag = cardFlag(c.attention, mark, c.points ? 11 : 14);
    const compact = Boolean(this.compact);
    const lines = wrapWords(c.summary, 34, compact ? 1 : 2);
    const sel = this.selected === c.key && !this.inStrip;
    const icon = typeIcon(c.typeKind);
    const alarm = Boolean(c.attention) || c.mark === "blocked";
    const flagBg = alarm ? mix(t.ansi1, t.raised, 0.16) : t.pill;
    const ci = { "✓": t.ansi2, "✕": t.ansi1 };
    const top = h_flex().h(18).gap(6).items_center()
      .child(this.mono(div().text_size(11).text_color(sel ? t.accent : t.muted)).when(sel, (el) => el.font_semibold()).child([sel ? "▸" : "", c.subtask ? "↳" : "", c.key].filter(Boolean).join(" ")))
      .when(Boolean(c.points), (el) => el.child(this.mono(div().px(5).rounded(4).bg(t.pill).text_size(10).text_color(t.muted)).child(c.points)))
      .child(div().flex_1())
      .when(Boolean(flag), (el) => el.child(div().px(6).rounded(5).bg(flagBg).text_size(10).font_semibold().text_color(alarm ? ensure(t.ansi1, flagBg, 4.5) : t.muted).child(flag)))
      .when(Boolean(c.agent), (el) => el.child(this.agentDot(t, c.agent)));
    const card = v_flex().id(`card-${c.key}`).h(cardHeight({ lines: lines.length, label: Boolean(labelField), compact })).px(11).py(compact ? 8 : 10).gap(compact ? 5 : 7)
      .relative().rounded(8).border(1).border_color(t.card).bg(t.card).shadow_sm().hover((s) => s.border_color(mix(t.foreground, t.card, 0.18)))
      .on_mouse_down("left", (_e, cx) => cx.stop_propagation())
      .on_click((_e, cx) => { this.selected = c.key; this.clicked(c.key, () => this.menuFor(this.selectedRow(this.answers.sprint?.body) ?? {}), cx); })
      .child(top)
      .children(lines.map((l, i) => div().id(`card-${c.key}-l${i}`).h(20).truncate().text_size(13).font_weight(500).text_color(t.foreground).child(l)))
      .when(!c.team, (el) => el.child(this.icon("corner", 14).absolute().top(0).right(0).text_color(t.accent)))
      .when(sel, (el) => el.child(div().absolute().top(-4).left(-4).right(-4).bottom(-4).rounded(11).border(1.5).border_color(t.accent)));
    if (compact) return card;
    const label = mix(t.ansi4, t.raised, 0.14);
    return card
      .child(h_flex().h(20).gap(6).items_center().text_size(11).children(c.prs.length === 0 && !c.finished ? [div().text_color(t.muted).child("—")] :
        c.prs.map((p, i) => h_flex().id(`card-${c.key}-pr${i}`).gap(5).px(7).rounded(5).bg(t.pill)
          .child(this.mono(div().text_color(t.muted)).child(p.ref)).child(div().font_semibold().text_color(ci[p.mark] ?? t.muted).child(p.mark))))
        .when(Boolean(c.finished), (el) => el.child(div().text_color(t.muted).child(c.finished))))
      .when(Boolean(labelField), (el) => el.child(h_flex().h(18).items_center()
        .when(Boolean(c.label), (row) => row.child(div().px(8).rounded(4).bg(label).text_size(11).text_color(ensure(mix(t.ansi4, t.foreground, 0.45), label, 4.5)).child(c.label)))))
      .child(h_flex().h(16).gap(6).items_center().text_size(11)
        .when(Boolean(icon), (el) => el.child(div().text_size(11).text_color(t[icon.tone] ?? t.muted).child(icon.glyph)))
        .child(div().flex_1().min_w_0().truncate().text_color(t.muted).child(c.info))
        .when(Boolean(c.who), (el) => el.child(div().flex_none().text_color(t.foreground).child(c.who))));
  }

  /** An agent's state as Herdr colors it: a dot, hollow when idle, ringed while it works or needs you. */
  agentDot(t, status) {
    const c = t[status] ?? t.unknown;
    const dot = status === "idle" ? div().w(8).h(8).rounded(4).border(2).border_color(c) : div().w(8).h(8).rounded(4).bg(c);
    if (status !== "working" && status !== "blocked") return dot;
    return h_flex().w(14).h(14).rounded(7).bg(mix(c, t.raised, 0.25)).items_center().justify_center().child(dot);
  }

  /** The dim header over a list's rows, naming the columns it shows, as the TUI's. */
  listHeader(t, heads, cols, opts = {}) {
    const cell = (w, text) => div().w(w).flex_none().truncate().child(text);
    return h_flex().h(ROW - 2).px(12).gap(10).items_center().text_size(10).font_semibold().text_color(t.muted).border_b(1).border_color(t.border)
      .child(cell(20, "")).child(cell(opts.prs ? 44 : 90, heads[0])).child(cell(opts.prs ? 160 : 118, heads[1]))
      .when(Boolean(cols.points), (el) => el.child(cell(24, "SP")))
      .when(Boolean(opts.backlog), (el) => el.child(cell(118, "BOARD")))
      .when(Boolean(cols.assignee), (el) => el.child(cell(opts.prs ? 140 : 76, opts.prs ? "AUTHOR" : "ASSIGNEE")))
      .when(Boolean(cols.agent), (el) => el.child(cell(88, "AGENT")))
      .when(Boolean(heads[2]), (el) => el.child(cell(opts.prs ? 24 : 96, heads[2])))
      .child(div().flex_1().child("TITLE"));
  }

  /** A list row's box: the selected one tinted and outlined in the accent. */
  rowBox(t, sel) {
    return h_flex().h(ROW).px(12).gap(10).items_center().rounded(6).border(1).border_color(sel ? t.accent : t.surface).bg(sel ? mix(t.accent, t.surface, 0.1) : t.surface);
  }

  /** A section's title in a list: its name, its count, and what follows (the board, dates, points). */
  sectionTitle(t, r, opts = {}) {
    return h_flex().h(ROW).px(12).gap(8).items_center().rounded(6).bg(opts.sel ? mix(t.accent, t.surface, 0.1) : t.surface)
      .when(opts.fold !== undefined, (el) => el.child(div().w(12).text_size(10).text_color(t.muted).child(opts.fold ? "▸" : "▾")))
      .child(div().text_size(12).font_semibold().text_color(t.foreground).child(r.title))
      .children(opts.after ?? [])
      .child(div().px(7).rounded(9).bg(t.pill).text_size(11).font_semibold().text_color(t.muted).child(String(r.count)))
      .children(opts.extra ?? []);
  }

  /** A PR/CI cell: each reference with its mark, ✓ green and ✕ red. */
  refsCell(t, prs, w) {
    const words = prRefs(prs).split(" ");
    return h_flex().w(w).flex_none().gap(4).items_center().overflow_hidden().children(words.map((wd, i) =>
      this.mono(div().id(`ref-${i}`).text_size(11).text_color(wd === "✓" ? t.ansi2 : wd === "✕" ? ensure(t.ansi1, t.surface, 4.5) : t.muted)).child(wd)));
  }

  listRow(t, r, board, cols = listColumns([], board.agents), opts = {}) {
    if (r.kind === "title") return this.sectionTitle(t, r);
    const c = cardOf(r, board.agents);
    const tk = r.ticket ?? {};
    const sel = this.selected === r.key && !this.inStrip;
    const g = gutterOf({ selected: sel, attention: c.attention, mark: markOf(c.mark, c.status) });
    const icon = opts.backlog ? typeIcon(c.typeKind) : null;
    const blocked = c.mark === "blocked";
    const chip = mix(t.ansi4, t.surface, 0.14);
    return this.rowBox(t, sel).when(tk.category === "done", (el) => el.opacity(0.72))
      .child(div().w(20).flex_none().text_size(12).font_semibold().text_color(t[g.tone] ?? t.muted).child(g.text))
      .child(h_flex().w(90).flex_none().gap(5).items_center()
        .when(Boolean(icon), (el) => el.child(div().text_size(10).text_color(t[icon?.tone] ?? t.muted).child(icon?.glyph ?? "")))
        .when(c.subtask, (el) => el.child(div().text_color(t.muted).child("↳")))
        .child(this.mono(div().text_size(11).text_color(sel ? t.accent : t.muted)).child(c.key)))
      .child(h_flex().w(118).flex_none().gap(6).items_center()
        .child(div().w(7).h(7).flex_none().rounded(4).bg(t[statusTone(tk.category)] ?? t.muted))
        .child(div().truncate().text_size(12).text_color(blocked ? ensure(t.ansi1, t.surface, 4.5) : t.foreground).when(blocked, (el) => el.font_semibold()).child(c.status)))
      .when(Boolean(cols.points), (el) => el.child(this.mono(div().w(24).flex_none().text_size(11).text_color(t.muted)).child(pointsText(tk.points))))
      .when(Boolean(opts.backlog), (el) => el.child(h_flex().w(118).flex_none()
        .when(Boolean(tk.board), (row) => row.child(div().px(8).rounded(4).bg(chip).truncate().text_size(11).text_color(ensure(mix(t.ansi4, t.foreground, 0.45), chip, 4.5)).child(tk.board)))))
      .when(Boolean(cols.assignee), (el) => el.child(div().w(76).flex_none().truncate().text_size(12).text_color(c.who === "unassigned" ? t.dim : t.muted).child(c.who)))
      .when(Boolean(cols.agent), (el) => el.child(h_flex().w(88).flex_none().gap(6).items_center()
        .child(c.agent ? this.agentDot(t, c.agent) : div().text_color(t.dim).child("—"))
        .child(div().text_size(12).text_color(t.muted).child(agentLabel(c.agent)))))
      .when(!opts.backlog, (el) => el.child(this.refsCell(t, tk.prs, 96)))
      .child(div().flex_1().min_w_0().truncate().text_size(13).text_color(c.team ? t.muted : t.foreground).child(c.summary));
  }


  list(t, board) {
    const rows = listRows(board.sections);
    const cols = listColumns(rows.filter((r) => r.kind === "row"), board.agents);
    const sticky = stickyAt(rows, this.shownRange);
    const more = moreAround(rows, this.shownRange);
    const hint = (text) => div().absolute().right(16).px(10).py(1).rounded(10).border(1).border_color(t.border).bg(t.pill).shadow_md().text_size(11).text_color(t.muted).child(text);
    return this.tableBox(t, v_flex().flex_1().min_h_0().child(this.listHeader(t, ["KEY", "STATUS", "PR / CI"], cols)).child(v_flex().id("list").relative().flex_1().min_h_0()
      .child(v_virtual_list("rows", rows.length, ROW, (i) => `${rows[i].kind}-${rows[i].key ?? rows[i].title}-${i}`, (range) => {
        this.range = { start: range.start, end: range.end };
        return rows.slice(range.start, range.end).map((r) => this.listRow(t, r, board, cols));
      }).size_full().track_scroll(this.scrolls.rows).on_item_click((key, cx) => {
        const [kind, ...rest] = key.split("-");
        if (kind === "row") {
          this.selected = rest.slice(0, -1).join("-");
          const row = rows.find((r) => r.kind === "row" && r.key === this.selected);
          this.clicked(this.selected, () => this.menuFor(row), cx);
        }
      }))
      .child(Scrollbar.vertical("rows").absolute().inset_0())
      .when(Boolean(sticky), (el) => el.child(div().absolute().top(0).left(0).right(0).bg(t.surface).border_b(1).border_color(t.border).child(this.sectionTitle(t, sticky))))
      .when(more.above > 0 && !sticky, (el) => el.child(hint(`↑ ${more.above} more`).top(6)))
      .when(more.above > 0 && Boolean(sticky), (el) => el.child(hint(`↑ ${more.above} more`).top(ROW + 6)))
      .when(more.below > 0, (el) => el.child(hint(`↓ ${more.below} more`).bottom(6)))));
  }

  /** A list in its rounded box, as the design's tables. */
  tableBox(t, content) {
    return v_flex().flex_1().min_h_0().p(14).child(v_flex().flex_1().min_h_0().rounded(10).border(1).border_color(t.border).overflow_hidden().px(6).child(content));
  }

  columns(t, board) {
    const gap = this.compact ? 6 : 8;
    const anySel = !this.inStrip && columnCards(board).some((col) => col.cards.some((c) => c.key === this.selected));
    return h_flex().id("columns").flex_1().min_h_0().gap(10).p(14).items_start().children(
      columnCards(board).map((col) => {
        const tone = t[statusTone(col.category)] ?? t.muted;
        const selIn = !this.inStrip && col.cards.some((c) => c.key === this.selected);
        const pane = this.colPane(col.category);
        const heights = col.cards.map((c) => cardHeight({ lines: wrapWords(c.summary, 34, this.compact ? 1 : 2).length, label: Boolean(board.label_field), compact: this.compact }));
        const below = cardsBelow(heights, gap, pane);
        return v_flex().id(`col-${col.category}`).flex_1().min_w(220).h_full().min_h_0().rounded(10).pt(2).bg(tone).child(v_flex().flex_1().min_h_0().rounded(9).border(1).overflow_hidden()
          .border_color(t.border).bg(t.well)
          .child(h_flex().px(12).pt(10).pb(8).gap(8).items_center().border_b(1).border_color(selIn ? mix(t.accent, t.well, 0.6) : t.well)
            .child(div().w(8).h(8).rounded(4).bg(tone))
            .child(div().text_size(12).font_semibold().text_color(selIn || !anySel ? t.foreground : t.muted).child(col.title))
            .child(div().px(7).rounded(9).bg(t.pill).text_size(11).font_semibold().text_color(t.muted).child(String(col.count)))
            .child(div().flex_1())
            .when(Boolean(col.points), (el) => el.child(this.mono(div().text_size(11).text_color(t.muted)).child(col.points))))
          .child(col.cards.length === 0 ? h_flex().py(28).justify_center().text_size(12).text_color(t.muted).child(EMPTY_COLUMN)
            : v_flex().relative().flex_1().min_h_0().px(2).pb(4)
              .child(this.scrollPane(`col-${col.category}-cards`, pane,
                v_flex().gap(gap).p(6).children(col.cards.map((c) => this.card(t, c, board.label_field)))).flex_1())
              .when(below > 0, (el) => el.child(h_flex().absolute().bottom(10).left(0).right(0).justify_center()
                .child(div().px(10).py(1).rounded(10).border(1).border_color(t.border).bg(t.pill).shadow_md().text_size(11).text_color(t.muted).child(`↓ ${below} more`))))));
      }));
  }

  /** The Sprint's columns while it loads: their titles, and cards shaped as the real ones. */
  skeletonColumns(t) {
    const cols = [["todo", "To Do"], ["in_progress", "In Progress"], ["review", "In Review"], ["done", "Done"]];
    const bar = (w, h) => div().w(w).h(h).rounded(4).bg(t.pill);
    const ghost = (i) => v_flex().id(`ghost-${i}`).gap(9).px(11).py(12).rounded(8).border(1).border_color(t.border).bg(t.raised)
      .child(bar(70, 9)).child(bar(170, 11)).child(bar(120, 11));
    return h_flex().id("columns").flex_1().min_h_0().gap(10).p(14).items_start().children(cols.map(([cat, title], i) =>
      v_flex().id(`col-${cat}`).flex_1().min_w(220).h_full().rounded(10).pt(2).bg(t[statusTone(cat)] ?? t.muted).child(v_flex().flex_1().rounded(9).border(1).border_color(t.border).bg(t.well).overflow_hidden()
        .child(h_flex().px(12).pt(10).pb(8).gap(8).items_center()
          .child(div().w(8).h(8).rounded(4).bg(t[statusTone(cat)] ?? t.muted))
          .child(div().text_size(12).font_semibold().text_color(t.foreground).child(title))
          .child(div().px(7).rounded(9).bg(t.pill).text_size(11).text_color(t.muted).child("…")))
        .child(v_flex().gap(8).px(8).child(ghost(i * 2)).child(ghost(i * 2 + 1))))));
  }

  /** What the last sync could not read, on an amber strip as the board's other notices. */
  warningsStrip(t, text) {
    const y = t.ansi3;
    const bg = mix(y, t.surface, 0.06);
    return h_flex().mx(14).mt(10).px(12).py(7).gap(10).items_center().rounded(8).border(1).border_color(mix(y, t.surface, 0.28)).bg(bg)
      .child(h_flex().w(16).h(16).flex_none().rounded(8).bg(y).items_center().justify_center().text_size(11).font_semibold().text_color(ensure(t.surface, y, 4.5)).child("!"))
      .child(div().flex_1().min_w_0().truncate().text_size(12).text_color(ensure(t.foreground, bg, 7)).child(text.replace(/^! /, "")))
      .child(div().flex_none().text_size(11).text_color(t.muted).child("showing what tkt could read"));
  }

  colPane(category) {
    if (!this.colPanes[category]) this.colPanes[category] = { offset: 0, view: 0, content: 0 };
    return this.colPanes[category];
  }

  body(t) {
    if (this.choices) return this.chooser(t);
    if (this.settings) return this.settingsScreen(t);
    if (this.details) return this.detailsScreen(t);
    const answer = this.answers[this.tab];
    if (!answer && !this.error && this.tab === "sprint" && this.view === "columns") return this.skeletonColumns(t);
    if (!answer) return div().p(16).text_color(this.error ? t.blocked : t.muted).child(this.error ? problemOf(this.error, this.machine) : "Loading…");
    if (answer.status === 503) return div().p(16).text_color(t.muted).child("tkt is syncing the board for the first time…");
    if (answer.status !== 200) return div().p(16).text_color(t.blocked).child(problemOf(answer.body?.message ?? `tkt answered ${answer.status}`, this.machine));
    if (this.tab === "sprint" && (answer.body.sections ?? []).length === 0) return div().p(16).text_color(t.muted).child("No tickets on the board: nothing assigned to you in an open sprint.");
    if (this.tab === "sprint") return v_flex().flex_1().min_h_0().child(this.strip(t, answer.body))
      .child(this.view === "columns" ? this.columns(t, answer.body) : this.list(t, answer.body));
    if (this.tab === "prs") return this.prsList(t, answer.body);
    if (this.tab === "backlog") return this.backlogList(t, answer.body);
    return div().p(16).text_color(t.muted).child(`${(answer.body.sections ?? answer.body.groups ?? []).length} groups`);
  }

  /** Runs after the status lanes' own move handler, so `overLane` holds the column under the pointer for this move only. */
  trackStatusDrag(e, cx) {
    const st = this.settings;
    if (!st) return;
    const over = st.overLane ?? null;
    st.overLane = null;
    if (!st.press) return;
    if (!st.drag && !dragStarted(st.press, e.position)) return;
    st.drag = st.press;
    // Set as the drag starts: the chip's click fires before the lanes' mouse-up, so a release over it must already know.
    st.dragged = true;
    st.dropTarget = over;
    st.ghost = { x: e.local_position.x - st.drag.grabX, y: e.local_position.y - st.drag.grabY };
    cx.notify();
  }

  statusGhost(t) {
    const dr = this.settings?.drag;
    const g = this.settings?.ghost;
    if (!dr || !g) return null;
    return h_flex().id("status-ghost").absolute().left(g.x).top(g.y).w(dr.width).h(34).px(10).gap(8).items_center().rounded(7)
      .border(1).border_color(t.accent).bg(t.raised).shadow_lg()
      .child(div().w(14).flex_none().text_size(12).text_color(dr.tone).child(dr.glyph))
      .child(div().flex_1().min_w_0().truncate().text_size(13).text_color(t.foreground).child(dr.name));
  }

  render(_cx) {
    const t = this.theme();
    return v_flex().id("root").relative().size_full().bg(t.surface).text_size(13)
      .key_context("TktMod").track_focus(this.focus).on_key_down((e, cx) => this.key(e, cx))
      .on_action("tkt::NextTab", (_e, cx) => this.key({ key: "tab", keystroke: "tab", modifiers: {} }, cx))
      .on_action("tkt::PrevTab", (_e, cx) => this.key({ key: "tab", keystroke: "shift-tab", modifiers: { shift: true } }, cx))
      .on_size_change((size) => { this.windowHeight = size.height; })
      .on_mouse_move((e, cx) => this.trackStatusDrag(e, cx))
      .on_mouse_up("left", (_e, cx) => { if (this.settings?.press || this.settings?.drag) { this.settings.press = null; this.settings.drag = null; this.settings.dropTarget = null; cx.notify(); } })
      .on_mouse_down("left", (_e, cx) => { this.focus.focus(); if (this.settings?.drag || this.settings?.press) { this.settings.drag = null; this.settings.press = null; this.settings.dropTarget = null; cx.notify(); } if (this.machineMenu) { this.machineMenu = null; cx.notify(); } if (this.panel || this.menu || this.picker) { this.panel = null; this.menu = null; this.picker = null; cx.notify(); } if (this.settings?.markPicker) { this.settings.markPicker = null; cx.notify(); } })
      .child(this.header(t))
      .when(!this.choices && !this.details && !this.settings, (el) => el.child(this.contextLine(t)))
      .when(Boolean(warningsLine(this.answers.sprint?.body?.warnings)), (el) => el.child(this.warningsStrip(t, warningsLine(this.answers.sprint.body.warnings))))
      .child(v_flex().id("body").flex_1().min_h_0().child(this.body(t)))
      .child(this.footer(t))
      .when(Boolean(this.openPanel()), (el) => el.child(this.filterPanel(t)))
      .when(Boolean(this.menu), (el) => el.child(this.menuBox(t)))
      .when(Boolean(this.picker), (el) => el.child(this.pickerBox(t)))
      .when(Boolean(this.prompt), (el) => el.child(this.promptBox(t)))
      .when(Boolean(this.keysOpen), (el) => el.child(this.keysBox(t)))
      .when(Boolean(this.starting && !this.starting.hidden), (el) => el.child(this.startingBox(t)))
      .when(Boolean(this.settings?.drag && this.settings?.ghost), (el) => el.child(this.statusGhost(t)));
  }
}
