// tkt's board in Herdr desktop: the daemon's /v1 API, reached through `tkt api` on whichever machine of the
// core runs tkt, so it works whether tkt runs here or on a machine reached over SSH.

import { View, div } from "gpui-kit";
import { v_flex, h_flex, v_virtual_list, Scrollbar, InputState, Input, TextView, VirtualListScrollHandle } from "gpui-base";
import { exec, machines, knownAgents, theme, props, closeOverlay, appHome, readTextFile, writeTextFile } from "herdr";
import * as os from "os";
import * as fs from "fs/promises";
import { apiArgs, parseAnswer, machineOrder, pickMachine, agentKinds, agentRuns, failed, tktOn, outdatedNote, needsUpdate, safeModPath, tktPathFrom } from "./lib/client.js";
import { listRows, stickyAt, moreAround } from "./lib/list.js";
import { columnCards, cardOf, wrapWords, markOf, typeIcon, agentGlyph, cardFlag, EMPTY_COLUMN, withPeach } from "./lib/columns.js";
import { gutterOf, statusTone, prStatusTone, agentWord, prRefs, listColumns, pointsText } from "./lib/rows.js";
import { previewOf, legendFor, problemOf, warningsLine, previewBadge, syncTone } from "./lib/footer.js";
import { prRows, toggled, reposChip, prPreview, prsToggle } from "./lib/prs.js";
import { backlogRows, badges, backlogAgents } from "./lib/backlog.js";
import { ticketMenu, prMenu, withBack, backOf, confirmRemove, menuFooter } from "./lib/menus.js";
import { firstPicker, answer, taskPicker, pickerKey } from "./lib/start.js";
import { moveMenu, formMenu, assignMenu, confirmAssign, missingField } from "./lib/move.js";
import { detailView, neighbors, detailsKey } from "./lib/details.js";
import { SECTIONS, reposPatch, boardsPatch, statusPatch, rootsPatch, statusRows } from "./lib/settings.js";
import { MOD_VERSION } from "./lib/version.js";
import { modKeyFor, withModKey } from "./lib/modkey.js";
import { tabCount, contextText, scopeNotice, routesFor, viewButton } from "./lib/header.js";
import { stripChips, atTop } from "./lib/strip.js";
import { packCache, unpackCache, cachedLabel } from "./lib/cache.js";
import { machineKey, stashed, restored } from "./lib/machines.js";
import { scrolled, menuHeight, MENU_ROW, cardHeight, revealed } from "./lib/scroll.js";
import { direction, tabAfter, stepList, stepColumns, targets, keyOf, boardAction, firstTarget, listKey, sectionAfter } from "./lib/nav.js";

const TABS = [
  { id: "sprint", title: "Sprint", route: "/v1/board?herdr=desktop" },
  { id: "prs", title: "PRs", route: "/v1/prs" },
  { id: "backlog", title: "Backlog", route: "/v1/backlog" },
];
// No event stream reaches a mod, so the shown tab is read again this often.
const POLL = 5000;
const ROW = 28;

function remembered() {
  try {
    return JSON.parse(localStorage.getItem("machine") ?? "undefined");
  } catch {
    return undefined;
  }
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
    this.menu = null;
    this.picker = null;
    this.jobs = [];
    this.details = null;
    this.settings = null;
    this.prompt = null;
    this.form = null;
    this.input = InputState.new({ placeholder: "" });
    this.notice = "";
    this.lastClick = { key: null, at: 0 };
    this.focus = cx.focus_handle();
    this.focus.focus();
    this.scrolls = { rows: VirtualListScrollHandle.new(), "pr-rows": VirtualListScrollHandle.new(), "backlog-rows": VirtualListScrollHandle.new(), menu: VirtualListScrollHandle.new() };
    this.colPanes = {};
    this.panes = { details: { offset: 0, view: 0, content: 0 }, settings: { offset: 0, view: 0, content: 0 } };
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
    return pickMachine(found, remembered());
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

  use(on, cx) {
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
    try {
      localStorage.setItem("machine", JSON.stringify(on));
    } catch {}
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
      const pick = await this.discover();
      if (pick.none && this.outdated.length > 0) throw new Error(outdatedNote(this.outdated));
      if (pick.none) throw new Error("tkt is not installed on this computer or on any machine connected to Herdr");
      if (pick.choose) {
        this.choices = pick.choose;
        throw new Error("tkt is on several machines: choose one");
      }
      this.machine = pick.machine;
      this.pendingUse = true;
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
    const routes = routesFor(TABS, this.tab, { bots: this.bots, refreshBacklog: this.refreshBacklog });
    if (this.tab === "backlog") this.refreshBacklog = false;
    cx.spawn(async (cx) => {
      try {
        const answers = await Promise.all(routes.map(([, route]) => this.call("GET", route)));
        routes.forEach(([id], i) => { this.answers[id] = answers[i]; });
        this.cachedAt = null;
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
          this.use(this.machine, cx);
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

  checkbox(t, on) {
    return h_flex().w(18).h(18).flex_none().rounded(4).border(1).border_color(on ? t.accent : t.muted).bg(on ? t.accent : t.popover)
      .items_center().justify_center().text_size(13).font_semibold().text_color(t.background).child(on ? "✓" : "");
  }

  toggle(id, on, onClick) {
    const t = withPeach(theme());
    return div().id(id).px(10).py(2).rounded(5).border(1).border_color(on ? t.accent : t.border)
      .text_color(on ? t.foreground : t.muted).bg(on ? t.selected : t.chrome).hover((s) => s.bg(t.hover))
      .on_mouse_down("left", (_e, cx) => cx.stop_propagation())
      .on_click((_e, cx) => onClick(cx));
  }

  prControls(t) {
    const view = this.answers.prs?.body;
    if (!view) return div();
    const showBots = this.bots || view.hidden_bots > 0;
    return h_flex().gap(6)
      .child(this.toggle("team-prs", prsToggle(view.team_prs).on, (cx) => this.patchState({ team_prs: !view.team_prs }, cx)).child(prsToggle(view.team_prs).label))
      .when(showBots, (el) => el.child(this.toggle("bots", this.bots, (cx) => { this.bots = !this.bots; this.load(cx); cx.notify(); })
        .child(this.bots ? "Bots" : `Bots ${view.hidden_bots}`)))
      .when((view.repos ?? []).length > 0, (el) => el.child(this.toggle("repos", this.panel === "repos", (cx) => { this.panel = this.panel === "repos" ? null : "repos"; cx.notify(); })
        .child(`${reposChip(view.repos)} ▾`)));
  }

  /** A filter's checkbox panel under the header: each name with its count, a click hides or shows it. */
  filterPanel(t, items, hidden, field) {
    return v_flex().id(`${field}-panel`).absolute().top(44).right(48).w(280).p(6).gap(2).rounded(8).bg(t.popover).border(1).border_color(t.border)
      .on_mouse_down("left", (_e, cx) => cx.stop_propagation())
      .children(items.map((r, i) =>
        h_flex().id(`${field}-${r.name}`).gap(8).px(8).py(4).rounded(5).bg(i === Math.min(this.panelSel ?? 0, items.length - 1) ? t.selected : t.popover).hover((s) => s.bg(t.hover))
          .on_click((_e, cx) => this.patchState({ [field]: toggled(hidden, r.name) }, cx))
          .child(this.checkbox(t, !r.hidden))
          .child(div().flex_1().truncate().text_color(r.hidden ? t.muted : t.foreground).child(r.name))
          .child(div().text_color(t.dim).child(String(r.prs ?? r.tickets ?? "")))));
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
    return this.toggle("team", Boolean(view.team), (cx) => this.toggleTeam(cx)).child("Team");
  }

  /** Turns the team's tickets on or off on the Sprint, then syncs, since they come from Jira. */
  toggleTeam(cx) {
    const view = this.answers.sprint?.body;
    if (!view) return;
    this.attempt(async (cx) => {
      const a = await this.call("PATCH", "/v1/board/state", { team: !view.team });
      if (a.status !== 200) throw new Error(a.body?.message ?? `tkt answered ${a.status}`);
      await this.call("POST", "/v1/sync", {});
      this.notice = view.team ? "only your tickets" : "with the team's tickets · syncing…";
      this.load(cx);
    }, cx);
  }

  boardsChip(t) {
    const boards = this.answers[this.tab]?.body?.boards ?? [];
    if (boards.length === 0) return div();
    const shown = boards.filter((b) => !b.hidden).length;
    return this.toggle("boards", this.panel === "boards", (cx) => { this.panel = this.panel === "boards" ? null : "boards"; cx.notify(); })
      .child(`${shown === boards.length ? "Boards" : `Boards ${shown}/${boards.length}`} ▾`);
  }

  backlogControls(t) {
    const view = this.answers.backlog?.body;
    if (!view) return div();
    const option = (merged, title) => {
      const on = Boolean(view.merged) === merged;
      return div().id(`merge-${merged}`).px(10).py(2).rounded(5).bg(on ? t.selected : t.chrome).text_color(on ? t.foreground : t.muted)
        .on_mouse_down("left", (_e, cx) => cx.stop_propagation())
        .on_click((_e, cx) => this.patchState({ backlog_merged: merged }, cx))
        .child(title);
    };
    return h_flex().gap(6)
      .child(h_flex().p(2).gap(2).rounded(7).border(1).border_color(t.border).child(option(false, "Split")).child(option(true, "Merged")))
      .child(this.boardsChip(t));
  }

  backlogRow(t, r) {
    if (r.kind === "title") {
      const b = badges(r.points);
      const tone = { todo: t.ansi8 ?? t.unknown, doing: t.ansi4 ?? t.working, done: t.ansi2 ?? t.done };
      return h_flex().h(ROW).px(12).gap(8).items_end().pb(3).text_color(t.foreground).bg(this.selected === `group:${r.group}` ? t.selected : t.surface)
        .child(div().text_color(t.dim).child(r.collapsed ? "▸" : "▾"))
        .child(div().font_semibold().child(r.title))
        .when(Boolean(r.board), (el) => el.child(div().px(5).rounded(4).bg(t.chrome).text_color(t.foreground).font_weight(400).child(r.board)))
        .when(Boolean(r.dates), (el) => el.child(div().text_color(t.muted).child(r.dates)))
        .child(div().text_color(t.dim).child(String(r.count)))
        .child(div().flex_1())
        .when(Boolean(b?.unestimated), (el) => el.child(div().text_color(t.dim).child(b.unestimated)))
        .when(Boolean(b), (el) => el.children(b.stages.map(([stage, n]) => div().px(5).rounded(3).bg(tone[stage]).text_color(t.background).child(n))));
    }
    return this.listRow(t, r, { agents: backlogAgents(this.answers) }, this.backlogCols, { backlog: true });
  }

  backlogList(t, view) {
    if (view.no_team_boards) return div().p(16).text_color(t.muted).child("No team boards set: choose them in Settings › Team boards to see their sprints and backlog here.");
    const rows = backlogRows(view);
    this.backlogCols = { ...listColumns(rows.filter((r) => r.kind === "row"), backlogAgents(this.answers)), assignee: true };
    return v_flex().id("backlog").relative().flex_1().min_h_0()
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
      .child(Scrollbar.vertical("backlog-rows").absolute().inset_0());
  }

  prRow(t, r) {
    if (r.kind === "title") {
      return h_flex().h(ROW).px(12).gap(8).items_end().pb(3).font_semibold().text_color(t.foreground).bg(this.selected === `group:${r.group}` ? t.selected : t.surface)
        .child(div().text_color(t.dim).child(r.collapsed ? "▸" : "▾"))
        .child(div().child(r.title))
        .child(div().text_color(t.dim).child(r.needs ? `${r.count} · ${r.needs} need you` : String(r.count)));
    }
    const sel = this.selected === r.key + r.group && !this.inStrip;
    const g = gutterOf({ selected: sel, needsYou: r.needsYou || r.review });
    const pr = r.raw.pr ?? {};
    const ci = { pass: ["✓", t.ansi2], fail: ["✕", t.ansi1], pending: ["…", t.accent] }[pr.checks] ?? ["—", t.dim];
    return h_flex().h(ROW).px(12).gap(10).items_center().bg(sel ? t.selected : t.surface)
      .child(div().w(20).text_color(t[g.tone] ?? t.dim).child(g.text))
      .child(div().w(60).text_color(sel ? t.foreground : t.dim).child(r.key))
      .child(h_flex().w(190).gap(4).child(div().truncate().text_color(t[prStatusTone(r.raw)] ?? t.foreground).child(r.status))
        .when(Boolean(r.approvals), (el) => el.child(div().text_color(t.dim).child(`· ${r.approvals}`))))
      .child(div().w(90).truncate().text_color(t.muted).child(pr.author ?? ""))
      .child(div().w(30).text_color(ci[1] ?? t.dim).child(ci[0]))
      .child(div().flex_1().truncate().text_color(t.foreground).child(r.title));
  }

  prsList(t, view) {
    const rows = prRows(view);
    if (rows.length === 0) return div().p(16).text_color(t.muted).child("No pull requests: none of yours open, and no review requested.");
    const authors = rows.some((r) => r.kind === "row" && r.raw.pr?.author);
    return v_flex().id("prs").relative().flex_1().min_h_0()
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
      .child(Scrollbar.vertical("pr-rows").absolute().inset_0());
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
    if (this.tab === "prs") return prMenu(row.raw, { agent: this.answers.sprint?.body?.agents?.[row.ticketKey] });
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
            this.notice = `copied ${item.value}`;
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
            await this.openStart(item.key);
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
            await this.openStart(item.key);
            break;
          }
          case "fix":
            await this.sendTask({ ticket: item.key, to: "own" });
            break;
          case "feedbackTo":
            await this.openTask({ ticket: item.key }, `ticket=${encodeURIComponent(item.key)}`);
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
    this.prompt = prompt;
    this.input.set_value(prompt.value ?? "");
  }

  answerPrompt(cx) {
    const p = this.prompt;
    const text = this.input.value().trim();
    this.prompt = null;
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
        } else {
          const a = await this.call("GET", `/v1/tickets/${p.key}/users?query=${encodeURIComponent(text)}`);
          if (a.status !== 200) throw new Error(a.body?.message ?? `tkt answered ${a.status}`);
          const users = a.body.users ?? [];
          if (users.length === 0) throw new Error(text.length < 2 ? "type at least two letters" : `nobody in Jira matches ${text}`);
          this.menu = p.kind === "assignUser"
            ? { title: `Assign ${p.key} to`, sel: 0, items: users.map((u) => ({ label: u.name, action: "confirmAssign", key: p.key, who: { to: "user", account_id: u.id, name: u.name } })) }
            : { title: p.field.name, sel: 0, items: users.map((u) => ({ label: u.name, action: "fieldValue", field: p.field, value: { id: u.id, name: u.name } })) };
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
    return v_flex().id("prompt").absolute().top(70).left(0).right(0).items_center()
      .child(v_flex().id("prompt-box").w(420).p(12).gap(8).rounded(10).bg(t.popover).border(1).border_color(t.accent)
        .on_mouse_down("left", (_e, cx) => cx.stop_propagation())
        .child(div().font_semibold().text_color(t.foreground).child(p.title))
        .child(Input.new(this.input))
        .child(h_flex().gap(8).justify_end()
          .child(div().id("prompt-cancel").px(10).py(3).rounded(5).text_color(t.muted).hover((s) => s.bg(t.hover))
            .on_click((_e, cx) => { this.prompt = null; cx.notify(); }).child("Cancel"))
          .child(div().id("prompt-ok").px(10).py(3).rounded(5).bg(t.solid).text_color(t.solid_foreground)
            .on_click((_e, cx) => this.answerPrompt(cx)).child(p.kind === "comment" || p.kind.startsWith("add") ? "Done" : "Search"))));
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
    const a = await this.call("GET", `/v1/tickets/${key}`);
    if (a.status !== 200) {
      this.details = null;
      throw new Error(a.body?.message ?? `tkt answered ${a.status}`);
    }
    this.details = { key, keys, view: detailView(a.body, TABS.find((t) => t.id === this.tab).title), ticket: a.body.ticket };
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

  detailsScreen(t) {
    const d = this.details;
    const v = d.view;
    const { prev, next } = neighbors(d.keys, d.key);
    const button = (id, text, on, onClick) => div().id(id).px(8).py(2).rounded(5).text_color(on ? t.foreground : t.dim).hover((s) => s.bg(t.hover))
      .on_mouse_down("left", (_e, cx) => cx.stop_propagation()).on_click((_e, cx) => { if (on) onClick(cx); }).child(text);
    const nav = h_flex().px(12).py(6).gap(8).items_center().border_b(1).border_color(t.border)
      .child(button("back", "‹ Back", true, (cx) => { this.details = null; cx.notify(); }))
      .child(div().text_color(t.muted).child(v ? v.breadcrumb : `› ${d.key}`))
      .child(div().flex_1())
      .child(button("prev", "◀", Boolean(prev), (cx) => this.goDetails(prev, cx)))
      .child(button("next", "▶", Boolean(next), (cx) => this.goDetails(next, cx)));
    if (!v) return v_flex().flex_1().child(nav).child(div().p(16).text_color(t.muted).child("Loading…"));
    const heading = (text) => div().pt(10).pb(4).font_semibold().text_color(t.foreground).child(text);
    const leftContent = v_flex().p(14).gap(4)
      .child(div().text_size(16).font_semibold().text_color(t.foreground).child(v.title))
      .child(h_flex().gap(14).text_size(12).children(v.fields.map(([k, val], i) => h_flex().id(`f-${i}`).gap(4).child(div().text_color(t.dim).child(k)).child(div().text_color(t.foreground).child(val)))))
      .when(Boolean(v.attention), (el) => el.child(div().text_color(t.blocked).child(`! ${v.attention}`)))
      .child(heading("Description"))
      .child(v.description ? TextView.markdown("desc", v.description) : div().text_color(t.dim).child("—"))
      .when(Boolean(v.acceptance), (el) => el.child(heading("Acceptance criteria")).child(TextView.markdown("acc", v.acceptance)))
      .when(v.subtasks.length > 0, (el) => el.child(heading(`Subtasks · ${v.subtasks.length}`)).children(v.subtasks.map((st) =>
        h_flex().id(`st-${st.key}`).gap(10).child(div().w(96).text_color(t.dim).child(st.key)).child(div().w(110).text_color(t.muted).child(st.status)).child(div().flex_1().truncate().text_color(t.foreground).child(st.summary)))))
      .when(v.comments.length > 0, (el) => el.child(heading(`Comments · ${v.comments.length}`)).children(v.comments.map((c, i) =>
        v_flex().id(`c-${i}`).pb(6).child(div().text_size(12).text_color(t.dim).child(`${c.who} · ${c.when}`)).child(TextView.markdown(`cbody-${i}`, c.body || "")))));
    const left = this.scrollPane("details-left", this.panes.details, leftContent).flex_1().min_w_0().h_full();
    const menu = ticketMenu(d.ticket, { agent: v.agent, attention: v.attention ? [v.attention] : [] });
    const right = v_flex().id("details-right").w(300).min_h_0().overflow_y_scrollbar().p(12).gap(4).border_l(1).border_color(t.border)
      .child(heading("Agent")).child(div().text_color(v.agent ? t[v.agent] ?? t.foreground : t.dim).child(v.agent ? `● ${v.agent}` : "no agent"))
      .child(heading("Pull requests")).children(v.prs.length === 0 ? [div().text_color(t.dim).child("—")] : v.prs.map((p, i) =>
        h_flex().id(`dpr-${i}`).gap(6).child(div().text_color(t[p.tone] ?? t.dim).child(p.mark)).child(div().flex_1().truncate().text_color(t.foreground).child(p.ref))))
      .child(heading("Actions")).children(menu.items.filter((it) => it.action !== "details").map((it, i) =>
        div().id(`dact-${i}`).px(8).py(3).rounded(5).text_color(t.foreground).hover((s) => s.bg(t.hover))
          .on_mouse_down("left", (_e, cx) => cx.stop_propagation()).on_click((_e, cx) => this.act(it, cx)).child(it.label)))
      .child(heading("Work")).children(v.work.length === 0 ? [div().text_color(t.dim).child("no worktree linked")] : v.work.map((w, i) =>
        v_flex().id(`w-${i}`).child(div().text_color(t.foreground).child(w.where)).child(div().text_size(11).text_color(t.dim).truncate().child(w.path))));
    return v_flex().id("details").flex_1().min_h_0().child(nav).child(h_flex().flex_1().min_h_0().items_start().child(left).child(right));
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
    this.settings = { section: this.settings?.section ?? "spaces", data: a.body, statuses: {}, boards: {}, fields: null };
  }

  async patchSettings(change) {
    const a = await this.call("PATCH", "/v1/settings", change);
    if (a.status !== 200) throw new Error(a.body?.message ?? `tkt answered ${a.status}`);
    this.settings.data = a.body;
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
      return controls.length - 1 === (st.sel ?? 0);
    };
    const link = (id, text, onClick, tone) => {
      const on = focusable(onClick);
      return div().id(id).px(6).py(1).rounded(4).bg(on ? t.selected : t.surface).text_color(tone ?? t.accent).hover((s) => s.bg(t.hover))
        .on_mouse_down("left", (_e, cx) => cx.stop_propagation()).on_click((_e, cx) => onClick(cx)).child(on ? `▸ ${text}` : text);
    };
    st.controls = controls;
    const heading = (text) => div().pt(10).pb(4).font_semibold().text_color(t.foreground).child(text);
    const side = v_flex().id("settings-side").w(200).p(8).gap(2).border_r(1).border_color(t.border).children(SECTIONS.map((sec) =>
      div().id(`sec-${sec.id}`).px(10).py(5).rounded(6).bg(st.section === sec.id ? t.selected : t.surface).text_color(st.section === sec.id ? t.foreground : t.muted)
        .hover((s) => s.bg(t.hover)).on_mouse_down("left", (_e, cx) => cx.stop_propagation())
        .on_click((_e, cx) => { st.section = sec.id; st.sel = 0; cx.notify(); }).child(sec.title)));
    let content = v_flex();
    if (st.section === "spaces") {
      content = v_flex().children(d.spaces.map((sp) => v_flex().id(`sp-${sp.key}`).pb(8)
        .child(heading([sp.key, sp.name].filter(Boolean).join(" · ")))
        .children(sp.repos.map((r) => h_flex().id(`repo-${sp.key}-${r.repo}`).gap(10).py(2)
          .child(div().w(260).truncate().text_color(t.foreground).child(r.repo)).child(div().flex_1().truncate().text_color(t.dim).child(r.path))
          .child(link(`rm-${sp.key}-${r.repo}`, "remove", (cx) => { this.menu = { ...confirmRemove(`Remove ${r.repo} from ${sp.key}?`, reposPatch(sp, { remove: r.repo })), sel: 0 }; cx.notify(); }, t.muted))))
        .when(sp.inferred.length > 0, (el) => el.child(div().text_size(12).text_color(t.dim).child(`also seen in its pull requests: ${sp.inferred.join(", ")}`)))
        .child(link(`add-${sp.key}`, "+ Add a repository", (cx) => { this.ask({ kind: "addRepo", title: `Add to ${sp.key}: owner/name, then its folder`, space: sp }); cx.notify(); }))));
    } else if (st.section === "boards") {
      content = v_flex().children(d.spaces.map((sp) => v_flex().id(`bs-${sp.key}`).pb(8).child(heading(sp.key))
        .children((st.boards[sp.key] ?? sp.team_boards).map((b) => {
          const on = sp.team_boards.some((x) => x.id === b.id);
          const toggle = (cx) => {
            if (!on) return this.attempt(() => this.patchSettings(boardsPatch(sp, b.id)), cx);
            this.menu = { ...confirmRemove(`Remove the team board ${b.name} from ${sp.key}?`, boardsPatch(sp, b.id)), sel: 0 };
            cx.notify();
          };
          const lit = focusable(toggle);
          return h_flex().id(`b-${sp.key}-${b.id}`).gap(8).py(2).px(4).rounded(4).bg(lit ? t.selected : t.surface).on_click((_e, cx) => toggle(cx))
            .child(this.checkbox(t, on)).child(div().text_color(t.foreground).child(b.name));
        }))
        .when(!st.boards[sp.key], (el) => el.child(link(`lb-${sp.key}`, "Choose from Jira's boards…", (cx) => this.attempt(async () => { st.boards[sp.key] = (await this.lookup("boards", sp.key)).boards; }, cx))))));
    } else if (st.section === "statuses") {
      content = v_flex().children(d.spaces.map((sp) => {
        const loaded = st.statuses[sp.key];
        const block = v_flex().id(`ss-${sp.key}`).pb(8).child(heading(sp.key));
        if (!loaded) return block.child(link(`ls-${sp.key}`, "Read the statuses from Jira…", (cx) => this.attempt(async () => { st.statuses[sp.key] = await this.lookup("statuses", sp.key); }, cx)));
        return block.children(statusRows(loaded, d.columns).map((r) => h_flex().id(`s-${sp.key}-${r.name}`).gap(10).py(2)
          .child(div().w(180).truncate().text_color(t.foreground).child(r.name))
          .child(link(`sc-${sp.key}-${r.name}`, `${r.column}${r.columnAuto ? "" : " •"}`, (cx) => { this.menu = { title: `${r.name}: column`, sel: 0, items: d.columns.map((c) => ({ label: c.title, action: "settingsPatch", change: statusPatch(sp.key, r.name, { column: c.id }) })) }; cx.notify(); }, t.muted))
          .child(link(`sm-${sp.key}-${r.name}`, `${r.mark}${r.markAuto ? "" : " •"}`, (cx) => { this.menu = { title: `${r.name}: mark`, sel: 0, items: d.marks.map((m) => ({ label: m, action: "settingsPatch", change: statusPatch(sp.key, r.name, { mark: m }) })) }; cx.notify(); }, t.muted))
          .when(r.changed, (el) => el.child(link(`sr-${sp.key}-${r.name}`, "↺ automatic", (cx) => this.attempt(() => this.patchSettings(statusPatch(sp.key, r.name, null)), cx), t.dim)))));
      }));
    } else if (st.section === "clones") {
      content = v_flex().child(heading("Folders tkt looks for clones in; the first one is where new clones go"))
        .children(d.repo_roots.map((root) => h_flex().id(`root-${root}`).gap(10).py(2).child(div().flex_1().text_color(t.foreground).child(root))
          .child(link(`rmr-${root}`, "remove", (cx) => { this.menu = { ...confirmRemove(`Remove the folder ${root}?`, rootsPatch(d.repo_roots, { remove: root })), sel: 0 }; cx.notify(); }, t.muted))))
        .child(link("add-root", "+ Add a folder", (cx) => { this.ask({ kind: "addRoot", title: "A folder for clones, such as ~/src" }); cx.notify(); }));
    } else if (st.section === "board") {
      const pick = (field, title) => (cx) => this.attempt(async () => {
        const names = (await this.lookup("fields")).fields ?? [];
        this.menu = { title, sel: 0, items: [{ label: "— none", action: "settingsPatch", change: { [field]: "" } }, ...names.map((n) => ({ label: n, action: "settingsPatch", change: { [field]: n } }))] };
      }, cx);
      content = v_flex()
        .child(heading("The field a ticket's label comes from")).child(link("label-field", d.label_field || "— none", pick("label_field", "Label field")))
        .child(heading("The story points field")).child(link("points-field", d.points_field || "detected", pick("points_field", "Story points field")));
    } else {
      content = v_flex().child(heading("The machine whose tkt this window shows"))
        .child(div().text_color(t.foreground).child(this.machine === undefined ? "none yet" : this.machine ?? "This computer"))
        .child(link("change-machine", "Change machine…", (cx) => this.pick(cx)));
    }
    const nav = h_flex().px(12).py(6).gap(8).items_center().border_b(1).border_color(t.border)
      .child(link("settings-back", "‹ Back", (cx) => { this.settings = null; cx.notify(); }, t.foreground))
      .child(div().text_color(t.muted).child("Settings"));
    return v_flex().id("settings").flex_1().min_h_0().child(nav)
      .child(h_flex().flex_1().min_h_0().items_start().child(side)
        .child(this.scrollPane("settings-content", this.panes.settings, v_flex().p(14).child(content)).flex_1().h_full()));
  }

  async openStart(key) {
    const a = await this.call("GET", `/v1/tickets/${key}/start?herdr=desktop`);
    if (a.status !== 200) throw new Error(a.body?.message ?? `tkt answered ${a.status}`);
    const p = firstPicker(key, a.body);
    if (p.error) throw new Error(p.error);
    this.choices_ = a.body;
    this.picker = { ...p, checked: new Set(p.rows.filter((r) => r.checked).map((r) => r.value)) };
  }

  async postStart(key, body) {
    const a = await this.call("POST", `/v1/tickets/${key}/start`, body);
    if (a.status === 409 && a.body?.code === "needs_answer" && a.body.needs === "clone") {
      this.picker = { step: "clone", key, body, title: `Start ${key}`, question: "Clone the repositories that have no clone here?", sel: 0, checked: new Set(),
        rows: [{ label: "Clone them", value: true }, { label: "Skip them", value: false }] };
      return;
    }
    if (a.status === 202) {
      this.notice = `starting ${key}: its agent opens in Herdr`;
      this.jobs.push({ id: a.body.id, what: `${key}'s Start` });
    }
    else if (a.status === 200) this.notice = `went to ${key}'s agent`;
    else throw new Error(a.body?.message ?? `tkt answered ${a.status}`);
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
        if (p.step === "clone") await this.postStart(p.key, { ...p.body, clone: row.value });
        else if (p.step === "taskClone") { if (row.value) await this.sendTask({ ...p.body, clone: true }); }
        else if (p.step === "task") {
          if (row.task) await this.sendTask({ ...p.subject, ...row.task });
          else this.picker = { step: "kind", subject: p.subject, title: p.title, question: "Which agent?", checked: new Set(),
            sel: Math.max(row.kinds.indexOf(row.last), 0), rows: row.kinds.map((k) => ({ label: k, hint: k === row.last ? "last used" : "", value: k })) };
        } else if (p.step === "kind") await this.sendTask({ ...p.subject, to: "new", agent: row.value });
        else {
          const next = answer(p, values, this.choices_);
          if (next.post) await this.postStart(p.key, next.post);
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
    return v_flex().id("picker").absolute().top(70).left(0).right(0).items_center()
      .child(v_flex().id("picker-box").w(420).p(12).gap(3).rounded(10).bg(t.popover).border(1).border_color(t.accent)
        .on_mouse_down("left", (_e, cx) => cx.stop_propagation())
        .child(div().font_semibold().text_color(t.foreground).child(p.title))
        .when(Boolean(p.question), (el) => el.child(div().pb(6).text_color(t.muted).child(p.question)))
        .children(p.rows.map((r, i) =>
          h_flex().id(`pick-${i}`).gap(8).px(8).py(4).rounded(5).bg(i === p.sel ? t.selected : t.popover).hover((s) => s.bg(t.hover))
            .on_click((_e, cx) => {
              if (p.multi) {
                if (p.checked.has(r.value)) p.checked.delete(r.value); else p.checked.add(r.value);
                cx.notify();
              } else this.choose([r.value], r, cx);
            })
            .when(Boolean(p.multi), (el) => el.child(this.checkbox(t, p.checked.has(r.value))))
            .child(div().flex_1().truncate().text_color(t.foreground).child(r.label))
            .when(Boolean(r.hint), (el) => el.child(div().text_color(t.dim).child(r.hint)))))
        .child(h_flex().pt(8).gap(8).justify_end()
          .child(div().id("pick-cancel").px(10).py(3).rounded(5).text_color(t.muted).hover((s) => s.bg(t.hover))
            .on_click((_e, cx) => { this.picker = null; cx.notify(); }).child("Cancel"))
          .when(Boolean(p.multi), (el) => el.child(div().id("pick-next").px(10).py(3).rounded(5).bg(t.solid).text_color(t.solid_foreground)
            .on_click((_e, cx) => { if (p.checked.size > 0) this.choose([...p.checked], null, cx); })
            .child("Next")))));
  }

  menuBox(t) {
    const m = this.menu;
    return div().id("menu").absolute().inset_0()
      .child(div().absolute().inset_0().bg(t.background).opacity(0.55))
      .child(v_flex().absolute().inset_0().items_center().justify_center()
        .child(v_flex().id("menu-box").w(420).p(10).gap(2).rounded(10).bg(t.popover).border(1).border_color(t.border)
          .on_mouse_down("left", (_e, cx) => cx.stop_propagation())
          .child(h_flex().gap(6)
            .when(Boolean(m.back), (el) => el.child(div().id("menu-back").text_color(t.dim).hover((s) => s.text_color(t.foreground))
              .on_click((_e, cx) => { this.menu = backOf(this.menu); cx.notify(); }).child("‹")))
            .child(div().font_semibold().text_color(t.foreground).child(m.title)))
          .when(Boolean(m.subtitle), (el) => el.child(div().text_size(12).text_color(t.dim).child(m.subtitle)))
          .when((m.reviewers ?? []).length > 0, (el) => el.child(h_flex().flex_wrap().gap(10).text_size(12)
            .children(m.reviewers.map((r, i) => div().id(`rev-${i}`).text_color(t[r.tone] ?? t.foreground).child(r.text)))))
          .child(div().h(6))
          .child(v_flex().relative().h(menuHeight(m.items.length, this.windowHeight))
            .child(v_virtual_list("menu-items", m.items.length, MENU_ROW, (i) => `${m.title}-${i}`, (range) =>
              m.items.slice(range.start, range.end).map((item, j) => {
                const i = range.start + j;
                return h_flex().h(MENU_ROW).gap(6).px(8).items_center().rounded(5).bg(i === m.sel ? t.selected : t.popover).text_color(t.foreground)
                  .child(div().w(10).text_color(t.accent).child(i === m.sel ? "▸" : ""))
                  .child(div().flex_1().truncate().child(item.label))
                  .when(Boolean(item.keyHint), (el) => el.child(div().text_color(t.accent).child(item.keyHint)));
              })).size_full().track_scroll(this.scrolls.menu).on_item_click((key, cx) => {
                const i = Number(key.slice(key.lastIndexOf("-") + 1));
                if (m.items[i]) this.act(m.items[i], cx);
              }))
            .child(Scrollbar.vertical("menu-items").absolute().inset_0()))
          .child(div().pt(6).text_size(11).text_color(t.dim).child(menuFooter(m.items[m.sel])))));
  }


  key(e, cx) {
    if (!this.prompt && keyOf(e) === "q") e = { ...e, key: "escape", keystroke: "escape", key_char: undefined };
    const k = e.key;
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
      if (a === "back") this.details = null;
      else if (a === "copy") this.act({ action: "copyContext", key: this.details.key, label: "copy" }, cx);
      else if (kk === "enter" && this.details.ticket) this.menu = { ...ticketMenu(this.details.ticket, {}), sel: 0 };
      else if (to !== null) this.panes.details.offset = to;
    } else if (this.settings && !this.prompt) {
      const st = this.settings;
      const kk = keyOf(e);
      const section = sectionAfter(SECTIONS.map((x) => x.id), st.section, kk);
      const to = listKey(st.sel ?? 0, (st.controls ?? []).length, kk);
      if (k === "escape") this.settings = null;
      else if (section) { st.section = section; st.sel = 0; this.panes.settings.offset = 0; }
      else if (to !== null) st.sel = to;
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

  /** The Boards and Repos filters from the keyboard: up and down move, space or enter ticks, a ticks all or none. */
  panelKey(k, cx) {
    const p = this.openPanel();
    const sel = Math.min(this.panelSel ?? 0, Math.max(0, p.items.length - 1));
    if (k === "escape") { this.panel = null; return; }
    const to = listKey(sel, p.items.length, k);
    if (to !== null) { this.panelSel = to; return; }
    const item = p.items[sel];
    if ((k === " " || k === "enter") && item) return this.patchState({ [p.field]: toggled(p.hidden, item.name) }, cx);
    if (k === "a") return this.patchState({ [p.field]: (p.hidden ?? []).length > 0 ? [] : p.items.map((r) => r.name) }, cx);
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
    if (action === "boards" || action === "repos") { this.panel = this.panel === action ? null : action; return; }
    if (action === "bots") { this.bots = !this.bots; return this.load(cx); }
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
        const heights = col.cards.map((c) => cardHeight({ lines: wrapWords(c.summary, 34, 2).length, label: Boolean(body.label_field) }) + 6);
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

  preview(t, board) {
    const p = this.tab === "prs" ? prPreview(prRows(board).find((r) => r.kind === "row" && r.key + r.group === this.selected)) : previewOf(this.selectedRow(board));
    if (!p) return div().px(12).py(6).text_size(12).text_color(t.dim).child("nothing selected");
    const badge = this.tab === "prs" ? null : previewBadge(this.selectedRow(board));
    return v_flex().px(12).py(6).gap(2).border_t(1).border_color(t.border)
      .child(badge ? h_flex().gap(6).items_center()
        .child(div().font_semibold().text_color(t.foreground).child(badge.key))
        .child(div().px(5).rounded(4).text_size(12).bg(t[badge.tone] ?? t.raised).text_color(t.background).child(badge.status))
        .child(div().flex_1().truncate().text_color(t.foreground).child(`· ${badge.summary}`))
        : div().truncate().font_semibold().text_color(t.foreground).child(p.title))
      .child(h_flex().gap(6).text_size(12)
        .when(Boolean(p.label), (el) => el.child(div().px(5).rounded(4).bg(t.chrome).text_color(t.foreground).child(p.label)))
        .child(div().flex_1().truncate().text_color(t.dim).child(p.line)));
  }

  legend(t) {
    const body = this.answers[this.tab]?.body;
    const opts = { strip: (this.answers.sprint?.body?.reviews ?? []).length > 0, bots: body?.hidden_bots ?? 0, merged: Boolean(body?.merged) };
    return h_flex().flex_wrap().px(12).py(4).gap(14).text_size(11).children(legendFor(this.tab, this.view, opts).map(([k, what], i) =>
      h_flex().id(`legend-${i}`).gap(4).child(div().text_color(t.accent).child(k)).child(div().text_color(t.dim).child(what))));
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
    return h_flex().id("strip").px(12).pt(8).gap(8).items_center().text_size(12)
      .child(div().text_color(t.dim).child("Review requested"))
      .children(chips.map((c, i) => {
        const on = this.inStrip && this.stripSel === i;
        return h_flex().id(`chip-${i}`).gap(4).px(8).py(2).rounded(10).border(1).border_color(on ? t.accent : t.ansi5 ?? t.border)
          .hover((s) => s.bg(t.hover)).on_mouse_down("left", (_e, cx) => cx.stop_propagation())
          .on_click((_e, cx) => { this.menu = { ...prMenu(c.row, {}), sel: 0 }; cx.notify(); })
          .child(div().text_color(on ? t.accent : t.peach).child(on ? "▸" : "!")).child(div().text_color(t.foreground).child(c.label));
      }))
      .when(Boolean(more), (el) => el.child(div().text_color(t.dim).child(more)));
  }

  /** The line under the tabs: the tab summed up, and on its right the filter's notice, which a click opens. */
  contextLine(t) {
    const body = this.answers[this.tab]?.body;
    const notice = scopeNotice(this.tab, body);
    return h_flex().px(12).py(3).gap(12).text_size(12).border_b(1).border_color(t.border)
      .child(div().flex_1().truncate().text_color(t.muted).child(contextText(this.tab, body, { loading: this.tab === "backlog" && !body }) || " "))
      .when(Boolean(notice), (el) => el.child(h_flex().id("scope-notice").hover((s) => s.bg(t.hover))
        .on_mouse_down("left", (_e, cx) => cx.stop_propagation())
        .on_click((_e, cx) => { this.panel = this.tab === "prs" ? "repos" : "boards"; cx.notify(); })
        .child(div().text_color(t.dim).child(notice.text)).child(div().text_color(t.accent).child(notice.key)).child(div().text_color(t.dim).child(notice.after))));
  }

  header(t) {
    const close = () => {
      const id = props()?.overlay;
      if (id) closeOverlay(id);
    };
    return h_flex().px(12).py(8).gap(6).items_center().bg(t.chrome).border_b(1).border_color(t.border)
      .child(div().font_semibold().text_color(t.foreground).pr(10).child("tkt"))
      .children(TABS.map((tab, i) => {
        const on = tab.id === this.tab;
        const count = tabCount(tab.id, this.answers[tab.id]?.body);
        return h_flex().id(`tab-${tab.id}`).gap(6).px(8).py(2).rounded(6).border(1).border_color(on ? t.accent : t.border)
          .bg(t.chrome).hover((s) => s.bg(t.hover))
          .on_mouse_down("left", (_e, cx) => cx.stop_propagation())
          .on_click((_e, cx) => this.select(tab.id, cx))
          .child(div().text_color(t.dim).child(String(i + 1)))
          .child(div().text_color(on ? t.foreground : t.muted).when(on, (el) => el.font_semibold()).child(tab.title))
          .when(count !== "", (el) => el.child(div().px(5).rounded(4).text_size(11).bg(on ? t.solid : t.raised).text_color(on ? t.solid_foreground : t.foreground).child(count)));
      }))
      .child(div().flex_1())
      .when(this.tab === "sprint", (el) => el.child(this.viewSwitch(t)).child(this.boardsChip(t)).child(this.teamToggle(t)))
      .when(this.tab === "prs", (el) => el.child(this.prControls(t)))
      .when(this.tab === "backlog", (el) => el.child(this.backlogControls(t)))
      .child(div().id("gear").px(8).py(3).rounded(6).text_color(this.settings ? t.foreground : t.muted).hover((s) => s.bg(t.hover))
        .on_mouse_down("left", (_e, cx) => cx.stop_propagation())
        .on_click((_e, cx) => { if (this.settings) { this.settings = null; cx.notify(); } else this.attempt(() => this.openSettings(), cx); })
        .text_size(18).child("⚙"))
      .child(div().id("close").px(8).py(3).rounded(6).text_color(t.muted).hover((s) => s.bg(t.hover))
        .on_mouse_down("left", (_e, cx) => cx.stop_propagation())
        .on_click(() => close())
        .text_size(16).child("✕"));
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
    const b = viewButton(this.view);
    return div().id("view").px(10).py(2).rounded(6).border(1).border_color(t.border).text_color(t.foreground).hover((s) => s.bg(t.hover))
      .on_mouse_down("left", (_e, cx) => cx.stop_propagation())
      .on_click((_e, cx) => this.setView(b.next, cx))
      .child(b.label);
  }

  card(t, c, labelField) {
    const mark = markOf(c.mark, c.status);
    const flag = cardFlag(c.attention, mark, c.points ? 11 : 14);
    const lines = wrapWords(c.summary, 34, 2);
    const sel = this.selected === c.key && !this.inStrip;
    const icon = typeIcon(c.typeKind);
    return v_flex().id(`card-${c.key}`).h(cardHeight({ lines: lines.length, label: Boolean(labelField) })).p(8).gap(3).rounded(8).border(1)
      .on_mouse_down("left", (_e, cx) => cx.stop_propagation())
      .on_click((_e, cx) => { this.selected = c.key; this.clicked(c.key, () => this.menuFor(this.selectedRow(this.answers.sprint?.body) ?? {}), cx); })
      .border_color(sel ? t.accent : c.attention ? t.blocked : t.border).bg(t.raised).hover((s) => s.border_color(t.accent))
      .child(h_flex().gap(6).items_center()
        .child(h_flex().gap(4).child(div().text_color(t.accent).child(sel ? "▸" : ""))
          .when(c.subtask, (el) => el.child(div().text_color(t.ansi6 ?? t.dim).child("↳")))
          .child(div().text_color(sel ? t.foreground : t.dim).when(sel, (el) => el.font_semibold()).child(c.key)))
        .when(Boolean(c.points), (el) => el.child(div().text_color(t.dim).child(c.points)))
        .child(div().flex_1())
        .when(Boolean(flag), (el) => el.child(div().text_color(c.attention ? t.blocked : t[mark?.tone] ?? t.muted).child(flag)))
        .when(Boolean(c.agent), (el) => el.child(div().text_color(t[c.agent] ?? t.unknown).child(agentGlyph(c.agent)))))
      .children(lines.map((l, i) => div().id(`card-${c.key}-l${i}`).font_semibold().text_color(c.team ? t.muted : t.foreground).child(l)))
      .child(h_flex().gap(6).text_size(12).children(c.prs.length === 0 && !c.finished ? [div().text_color(t.dim).child("—")] :
        c.prs.map((p, i) => h_flex().id(`card-${c.key}-pr${i}`).gap(3).child(div().text_color(t.muted).child(p.ref)).child(div().text_color(t[p.tone] ?? t.dim).child(p.mark))))
        .when(Boolean(c.finished), (el) => el.child(div().text_color(t.dim).child(c.finished))))
      .when(Boolean(labelField), (el) => el.child(h_flex().h(18).text_size(12)
        .when(Boolean(c.label), (row) => row.child(div().px(5).rounded(4).bg(t.chrome).text_color(t.foreground).child(c.label)))))
      .child(h_flex().gap(5).text_size(12)
        .when(Boolean(icon), (el) => el.child(div().text_color(t[icon.tone] ?? t.dim).child(icon.glyph)))
        .child(div().flex_1().text_color(t.dim).truncate().child([c.info, c.who].filter(Boolean).join(" · "))));
  }

  /** The dim header over a list's rows, naming the columns it shows, as the TUI's. */
  listHeader(t, heads, cols, opts = {}) {
    const cell = (w, text) => div().w(w).truncate().child(text);
    return h_flex().h(ROW - 6).px(12).gap(10).items_center().text_size(12).text_color(t.dim).border_b(1).border_color(t.border)
      .child(cell(20, "")).child(cell(opts.prs ? 60 : 110, heads[0])).child(cell(opts.prs ? 190 : 130, heads[1]))
      .when(Boolean(cols.points), (el) => el.child(cell(36, "SP")))
      .when(Boolean(opts.backlog), (el) => el.child(cell(150, "BOARD")))
      .when(Boolean(cols.assignee), (el) => el.child(cell(90, opts.prs ? "AUTHOR" : "ASSIGNEE")))
      .when(Boolean(cols.agent), (el) => el.child(cell(96, "AGENT")))
      .when(Boolean(heads[2]), (el) => el.child(cell(opts.prs ? 30 : 120, heads[2])))
      .child(div().flex_1().child("TITLE"));
  }

  listRow(t, r, board, cols = listColumns([], board.agents), opts = {}) {
    if (r.kind === "title") {
      return h_flex().h(ROW).px(12).items_end().pb(3).font_semibold().text_color(t.foreground)
        .child(div().child(r.title)).child(div().pl(8).text_color(t.dim).child(String(r.count)));
    }
    const c = cardOf(r, board.agents);
    const tk = r.ticket ?? {};
    const sel = this.selected === r.key && !this.inStrip;
    const g = gutterOf({ selected: sel, attention: c.attention, mark: markOf(c.mark, c.status) });
    const icon = opts.backlog ? typeIcon(c.typeKind) : null;
    return h_flex().h(ROW).px(12).gap(10).items_center().bg(sel ? t.selected : t.surface)
      .child(div().w(20).text_color(t[g.tone] ?? t.dim).child(g.text))
      .child(h_flex().w(110).gap(4)
        .when(Boolean(icon), (el) => el.child(div().text_color(t[icon?.tone] ?? t.dim).child(icon?.glyph ?? "")))
        .when(c.subtask, (el) => el.child(div().text_color(t.ansi6 ?? t.dim).child("↳")))
        .child(div().text_color(sel ? t.foreground : t.dim).child(c.key)))
      .child(div().w(130).truncate().text_color(t[statusTone(tk.category)] ?? t.muted).child(c.status))
      .when(Boolean(cols.points), (el) => el.child(div().w(36).text_color(t.dim).child(pointsText(tk.points))))
      .when(Boolean(opts.backlog), (el) => el.child(div().w(150).child(tk.board ? div().px(5).rounded(4).bg(t.chrome).text_color(t.foreground).truncate().child(tk.board) : div())))
      .when(Boolean(cols.assignee), (el) => el.child(div().w(90).truncate().text_color(t.muted).child(c.who)))
      .when(Boolean(cols.agent), (el) => el.child(div().w(96).text_color(t[c.agent] ?? t.dim).child(agentWord(c.agent))))
      .when(!opts.backlog, (el) => el.child(div().w(120).truncate().text_color(t.muted).child(prRefs(tk.prs))))
      .child(div().flex_1().truncate().text_color(c.team ? t.muted : t.foreground).child(c.summary));
  }


  list(t, board) {
    const rows = listRows(board.sections);
    const cols = listColumns(rows.filter((r) => r.kind === "row"), board.agents);
    const sticky = stickyAt(rows, this.shownRange);
    const more = moreAround(rows, this.shownRange);
    const hint = (text) => div().absolute().right(16).px(8).py(1).rounded(10).bg(t.raised).text_size(11).text_color(t.dim).child(text);
    return v_flex().flex_1().min_h_0().child(this.listHeader(t, ["KEY", "STATUS", "PR / CI"], cols)).child(v_flex().id("list").relative().flex_1().min_h_0()
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
      .when(Boolean(sticky), (el) => el.child(h_flex().absolute().top(0).left(0).right(0).h(ROW).px(12).items_end().pb(3)
        .bg(t.surface).border_b(1).border_color(t.border).font_semibold().text_color(t.foreground)
        .child(div().child(sticky.title)).child(div().pl(8).text_color(t.dim).child(String(sticky.count)))))
      .when(more.above > 0 && !sticky, (el) => el.child(hint(`↑ ${more.above} more`).top(6)))
      .when(more.above > 0 && Boolean(sticky), (el) => el.child(hint(`↑ ${more.above} more`).top(ROW + 6)))
      .when(more.below > 0, (el) => el.child(hint(`↓ ${more.below} more`).bottom(6))));
  }

  columns(t, board) {
    return h_flex().id("columns").flex_1().min_h_0().gap(10).p(10).items_start().children(
      columnCards(board).map((col) =>
        v_flex().id(`col-${col.category}`).flex_1().min_w(220).h_full().min_h_0().gap(6).p(6).rounded(8).border(1)
          .border_color(!this.inStrip && col.cards.some((c) => c.key === this.selected) ? t.accent : t.border)
          .child(h_flex().px(4).pb(4).border_b(1).border_color(t.border)
            .child(div().font_semibold().text_color(t.foreground).child(col.title))
            .child(div().flex_1())
            .child(div().text_color(t.dim).child([String(col.count), col.points].filter(Boolean).join(" · "))))
          .child(col.cards.length === 0 ? div().px(4).text_color(t.dim).child(EMPTY_COLUMN)
            : this.scrollPane(`col-${col.category}-cards`, this.colPane(col.category),
              v_flex().gap(6).children(col.cards.map((c) => this.card(t, c, board.label_field)))).flex_1())));
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

  status(t) {
    const where = this.machine === undefined ? "looking for tkt…" : `tkt on ${this.machine ?? "this computer"} ▾`;
    return h_flex().px(12).py(5).gap(12).bg(t.chrome).border_t(1).border_color(t.border).text_size(12)
      .child(div().id("machine").px(4).rounded(4).text_color(t.dim).hover((s) => s.bg(t.hover).text_color(t.foreground))
        .on_mouse_down("left", (_e, cx) => cx.stop_propagation())
        .on_click((_e, cx) => this.pick(cx))
        .child(where))
      .child(div().flex_1().truncate().text_color(this.error ? t.blocked : t.dim).child(this.error ? problemOf(this.error, this.machine) : this.notice))
      .child(((label) => div().text_color(t[syncTone(label)] ?? t.dim).child(label))(this.tab === "backlog" && this.answers.backlog?.body?.error ? `! ${this.answers.backlog.body.error}` : this.cachedAt ? cachedLabel(this.cachedAt) : this.answers[this.tab]?.body?.sync_label ?? ""))
      .child(div().id("sync").px(6).rounded(4).text_color(t.muted).hover((s) => s.bg(t.hover).text_color(t.foreground))
        .on_mouse_down("left", (_e, cx) => cx.stop_propagation())
        .on_click((_e, cx) => this.sync(cx))
        .child("↻"));
  }

  render(_cx) {
    const t = withPeach(theme());
    return v_flex().id("root").relative().size_full().bg(t.surface).text_size(13)
      .key_context("TktMod").track_focus(this.focus).on_key_down((e, cx) => this.key(e, cx))
      .on_action("tkt::NextTab", (_e, cx) => this.key({ key: "tab", keystroke: "tab", modifiers: {} }, cx))
      .on_action("tkt::PrevTab", (_e, cx) => this.key({ key: "tab", keystroke: "shift-tab", modifiers: { shift: true } }, cx))
      .on_size_change((size) => { this.windowHeight = size.height; })
      .on_mouse_down("left", (_e, cx) => { this.focus.focus(); if (this.panel || this.menu || this.picker) { this.panel = null; this.menu = null; this.picker = null; cx.notify(); } })
      .child(this.header(t))
      .when(!this.choices && !this.details && !this.settings, (el) => el.child(this.contextLine(t)))
      .when(Boolean(warningsLine(this.answers.sprint?.body?.warnings)), (el) => el.child(div().px(12).py(3).truncate().text_size(12).text_color(t.working).child(warningsLine(this.answers.sprint.body.warnings))))
      .child(v_flex().id("body").flex_1().min_h_0().child(this.body(t)))
      .when((this.tab === "sprint" || this.tab === "prs") && !this.choices && !this.details && !this.settings, (el) => el.child(this.preview(t, this.answers[this.tab]?.body)))
      .child(this.legend(t))
      .child(this.status(t))
      .when(Boolean(this.openPanel()), (el) => { const p = this.openPanel(); return el.child(this.filterPanel(t, p.items, p.hidden, p.field)); })
      .when(Boolean(this.menu), (el) => el.child(this.menuBox(t)))
      .when(Boolean(this.picker), (el) => el.child(this.pickerBox(t)))
      .when(Boolean(this.prompt), (el) => el.child(this.promptBox(t)));
  }
}
