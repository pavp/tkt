// tkt's board in Herdr desktop: the daemon's /v1 API, reached through `tkt api` on whichever machine of the
// core runs tkt, so it works whether tkt runs here or on a machine reached over SSH.

import { View, div } from "gpui-kit";
import { v_flex, h_flex, v_virtual_list, Scrollbar } from "gpui-base";
import { exec, machines, knownAgents, theme, props, closeOverlay } from "herdr";
import { apiArgs, parseAnswer, machineOrder, pickMachine, agentKinds, failed } from "./lib/client.js";
import { listRows, stickyAt, moreAround } from "./lib/list.js";
import { columnCards, cardOf, wrapWords } from "./lib/columns.js";
import { previewOf, legendFor, problemOf } from "./lib/footer.js";
import { prRows, toggled, reposChip } from "./lib/prs.js";

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
    this.error = null;
    this.machine = undefined;
    this.choices = null;
    this.failures = 0;
    this.found = [];
    this.view = "columns";
    this.range = { start: 0, end: 0 };
    this.shownRange = { start: 0, end: 0 };
    this.selected = null;
    this.bots = false;
    this.reposOpen = false;
    // The list learns what is on screen while it lays out, where it may not ask for a render; the pinned title and
    // the "N more" hints follow on this tick instead.
    cx.timer.every(150, (cx) => {
      if (this.range.start !== this.shownRange.start || this.range.end !== this.shownRange.end) {
        this.shownRange = { ...this.range };
        cx.notify();
      }
    });
    this.loadView(cx);
    this.load(cx);
    cx.timer.every(POLL, (cx) => this.load(cx));
  }

  async run(args, on) {
    return parseAnswer(await exec("tkt", args, on ? { on } : {}));
  }

  /** The machines tkt is installed on, asked with `tkt --version`, which starts no daemon. */
  async discover() {
    const found = [];
    for (const on of machineOrder(machines(), remembered())) {
      try {
        const r = await exec("tkt", ["--version"], on ? { on } : {});
        if (r.code === 0) found.push(on);
      } catch {}
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
    this.machine = on;
    this.choices = null;
    try {
      localStorage.setItem("machine", JSON.stringify(on));
    } catch {}
    cx.spawn(async () => {
      try {
        const known = await knownAgents(on ? { on } : {});
        await this.call("POST", "/v1/herdr/agents", { agents: agentKinds(known) });
      } catch {}
    });
    this.load(cx);
  }

  /** One daemon request on the machine whose tkt is used; with none chosen yet it looks, and asks when tkt is on
   *  several. */
  async call(method, path, body) {
    if (this.machine === undefined) {
      const pick = await this.discover();
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

  load(cx) {
    const tab = TABS.find((t) => t.id === this.tab);
    const route = tab.id === "prs" && this.bots ? `${tab.route}?bots=true` : tab.route;
    cx.spawn(async (cx) => {
      try {
        this.answers[tab.id] = await this.call("GET", route);
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

  toggle(id, on, onClick) {
    const t = theme();
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
      .child(this.toggle("team-prs", view.team_prs, (cx) => this.patchState({ team_prs: !view.team_prs }, cx)).child("Team"))
      .when(showBots, (el) => el.child(this.toggle("bots", this.bots, (cx) => { this.bots = !this.bots; this.load(cx); cx.notify(); })
        .child(this.bots ? "Bots" : `Bots ${view.hidden_bots}`)))
      .when((view.repos ?? []).length > 0, (el) => el.child(this.toggle("repos", this.reposOpen, (cx) => { this.reposOpen = !this.reposOpen; cx.notify(); })
        .child(`${reposChip(view.repos)} ▾`)));
  }

  reposPanel(t) {
    const view = this.answers.prs?.body;
    return v_flex().id("repos-panel").absolute().top(44).right(48).w(280).p(6).gap(2).rounded(8).bg(t.popover).border(1).border_color(t.border)
      .on_mouse_down("left", (_e, cx) => cx.stop_propagation())
      .children((view?.repos ?? []).map((r) =>
        h_flex().id(`repo-${r.name}`).gap(8).px(8).py(4).rounded(5).hover((s) => s.bg(t.hover))
          .on_click((_e, cx) => this.patchState({ hidden_repos: toggled(view.hidden_repos, r.name) }, cx))
          .child(div().w(14).text_color(t.accent).child(r.hidden ? "☐" : "☑"))
          .child(div().flex_1().truncate().text_color(r.hidden ? t.muted : t.foreground).child(r.name))
          .child(div().text_color(t.dim).child(String(r.prs)))));
  }

  prRow(t, r) {
    if (r.kind === "title") {
      return h_flex().h(ROW).px(12).gap(8).items_end().pb(3).font_semibold().text_color(t.foreground)
        .child(div().text_color(t.dim).child(r.collapsed ? "▸" : "▾"))
        .child(div().child(r.title))
        .child(div().text_color(t.dim).child(r.needs ? `${r.count} · ${r.needs} need you` : String(r.count)));
    }
    const sel = this.selected === r.key + r.group;
    return h_flex().h(ROW).px(12).gap(10).items_center().bg(sel ? t.selected : t.surface)
      .child(div().w(60).text_color(t.dim).child(r.key))
      .child(div().w(96).text_color(t.dim).child(r.ticketKey))
      .child(div().w(140).truncate().text_color(r.needsYou ? t.blocked : t.muted).child(r.needsYou ? `! ${r.status}` : r.status))
      .child(div().w(90).truncate().text_color(t.muted).child(r.who))
      .child(div().flex_1().truncate().text_color(t.foreground).child(r.title));
  }

  prsList(t, view) {
    const rows = prRows(view);
    if (rows.length === 0) return div().p(16).text_color(t.muted).child("No pull requests: none of yours open, and no review requested.");
    return v_flex().id("prs").relative().flex_1().min_h_0()
      .child(v_virtual_list("pr-rows", rows.length, ROW, (i) => `${i}`, (range) =>
        rows.slice(range.start, range.end).map((r) => this.prRow(t, r))).size_full().on_item_click((key, cx) => {
        const r = rows[Number(key)];
        if (!r) return;
        if (r.kind === "title") this.patchState({ collapsed: toggled(view.collapsed, r.group) }, cx);
        else {
          this.selected = r.key + r.group;
          cx.notify();
        }
      }))
      .child(Scrollbar.vertical("pr-rows").absolute().inset_0());
  }

  sync(cx) {
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
    const p = previewOf(this.selectedRow(board));
    if (!p) return div().px(12).py(6).text_size(12).text_color(t.dim).child("nothing selected");
    return v_flex().px(12).py(6).gap(2).border_t(1).border_color(t.border)
      .child(div().truncate().font_semibold().text_color(t.foreground).child(p.title))
      .child(h_flex().gap(6).text_size(12)
        .when(Boolean(p.label), (el) => el.child(div().px(4).rounded(3).bg(t.raised).text_color(t.accent).child(p.label)))
        .child(div().flex_1().truncate().text_color(t.dim).child(p.line)));
  }

  legend(t) {
    return h_flex().px(12).py(4).gap(14).text_size(11).children(legendFor(this.tab, this.view).map(([k, what], i) =>
      h_flex().id(`legend-${i}`).gap(4).child(div().text_color(t.accent).child(k)).child(div().text_color(t.dim).child(what))));
  }

  select(id, cx) {
    this.tab = id;
    this.load(cx);
    cx.notify();
  }

  header(t) {
    const close = () => {
      const id = props()?.overlay;
      if (id) closeOverlay(id);
    };
    return h_flex().px(12).py(8).gap(6).items_center().bg(t.chrome).border_b(1).border_color(t.border)
      .child(div().font_semibold().text_color(t.foreground).pr(10).child("tkt"))
      .children(TABS.map((tab) => {
        const on = tab.id === this.tab;
        return div().id(`tab-${tab.id}`).px(10).py(3).rounded(6)
          .bg(on ? t.selected : t.chrome).text_color(on ? t.foreground : t.muted)
          .hover((s) => s.bg(t.hover))
          .on_mouse_down("left", (_e, cx) => cx.stop_propagation())
          .on_click((_e, cx) => this.select(tab.id, cx))
          .child(tab.title);
      }))
      .child(div().flex_1())
      .when(this.tab === "sprint", (el) => el.child(this.viewSwitch(t)))
      .when(this.tab === "prs", (el) => el.child(this.prControls(t)))
      .child(div().id("close").px(8).py(3).rounded(6).text_color(t.muted).hover((s) => s.bg(t.hover))
        .on_mouse_down("left", (_e, cx) => cx.stop_propagation())
        .on_click(() => close())
        .child("✕"));
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
    const option = (id, title) => {
      const on = this.view === id;
      return div().id(`view-${id}`).px(10).py(2).rounded(5)
        .bg(on ? t.selected : t.chrome).text_color(on ? t.foreground : t.muted)
        .on_mouse_down("left", (_e, cx) => cx.stop_propagation())
        .on_click((_e, cx) => this.setView(id, cx))
        .child(title);
    };
    return h_flex().p(2).gap(2).rounded(7).border(1).border_color(t.border)
      .child(option("columns", "◫ Columns")).child(option("list", "≣ List"));
  }

  card(t, c) {
    const flag = c.attention ? `! ${c.attention}` : c.mark ? `${c.mark} ${c.status}` : "";
    const lines = wrapWords(c.summary, 34, 2);
    const sel = this.selected === c.key;
    return v_flex().id(`card-${c.key}`).p(8).gap(3).rounded(8).border(1)
      .on_mouse_down("left", (_e, cx) => cx.stop_propagation())
      .on_click((_e, cx) => { this.selected = c.key; cx.notify(); })
      .border_color(sel ? t.accent : c.attention ? t.blocked : t.border).bg(t.raised).hover((s) => s.border_color(t.accent))
      .child(h_flex().gap(6).items_center()
        .child(div().text_color(t.dim).child(c.subtask ? `↳ ${c.key}` : c.key))
        .when(Boolean(c.points), (el) => el.child(div().text_color(t.dim).child(c.points)))
        .child(div().flex_1())
        .when(Boolean(flag), (el) => el.child(div().text_color(c.attention ? t.blocked : t.muted).child(flag)))
        .when(Boolean(c.agent), (el) => el.child(div().text_color(t[c.agent] ?? t.unknown).child("●"))))
      .children(lines.map((l, i) => div().id(`card-${c.key}-l${i}`).font_semibold().text_color(c.team ? t.muted : t.foreground).child(l)))
      .child(h_flex().gap(6).text_size(12).children(c.prs.length === 0 ? [div().text_color(t.dim).child("—")] :
        c.prs.map((p, i) => h_flex().id(`card-${c.key}-pr${i}`).gap(3).child(div().text_color(t.muted).child(p.ref)).child(div().text_color(t[p.tone] ?? t.dim).child(p.mark)))))
      .when(Boolean(c.label), (el) => el.child(div().text_size(12).text_color(t.accent).child(c.label)))
      .child(div().text_size(12).text_color(t.dim).truncate().child([c.info, c.who].filter(Boolean).join(" · ")));
  }

  listRow(t, r, board) {
    if (r.kind === "title") {
      return h_flex().h(ROW).px(12).items_end().pb(3).font_semibold().text_color(t.foreground)
        .child(div().child(r.title)).child(div().pl(8).text_color(t.dim).child(String(r.count)));
    }
    const c = cardOf(r, board.agents);
    const sel = this.selected === r.key;
    const pr = c.prs[0];
    return h_flex().h(ROW).px(12).gap(10).items_center().bg(sel ? t.selected : t.surface)
      .child(div().w(96).text_color(sel ? t.foreground : t.dim).child(c.subtask ? `↳ ${c.key}` : c.key))
      .child(div().w(110).truncate().text_color(c.attention ? t.blocked : t.muted).child(c.attention ? `! ${c.attention}` : c.status))
      .child(div().w(40).text_color(t.dim).child(c.points))
      .child(div().w(80).truncate().text_color(t.muted).child(c.who))
      .child(div().w(16).text_color(t[c.agent] ?? t.dim).child(c.agent ? "●" : ""))
      .child(div().w(60).text_color(pr ? t[pr.tone] ?? t.dim : t.dim).child(pr ? `${pr.ref} ${pr.mark}` : "—"))
      .child(div().flex_1().truncate().text_color(c.team ? t.muted : t.foreground).child(c.summary));
  }

  list(t, board) {
    const rows = listRows(board.sections);
    const sticky = stickyAt(rows, this.shownRange);
    const more = moreAround(rows, this.shownRange);
    const hint = (text) => div().absolute().right(16).px(8).py(1).rounded(10).bg(t.raised).text_size(11).text_color(t.dim).child(text);
    return v_flex().id("list").relative().flex_1().min_h_0()
      .child(v_virtual_list("rows", rows.length, ROW, (i) => `${rows[i].kind}-${rows[i].key ?? rows[i].title}-${i}`, (range) => {
        this.range = { start: range.start, end: range.end };
        return rows.slice(range.start, range.end).map((r) => this.listRow(t, r, board));
      }).size_full().on_item_click((key, cx) => {
        const [kind, ...rest] = key.split("-");
        if (kind === "row") {
          this.selected = rest.slice(0, -1).join("-");
          cx.notify();
        }
      }))
      .child(Scrollbar.vertical("rows").absolute().inset_0())
      .when(Boolean(sticky), (el) => el.child(h_flex().absolute().top(0).left(0).right(0).h(ROW).px(12).items_end().pb(3)
        .bg(t.surface).border_b(1).border_color(t.border).font_semibold().text_color(t.foreground)
        .child(div().child(sticky.title)).child(div().pl(8).text_color(t.dim).child(String(sticky.count)))))
      .when(more.above > 0 && !sticky, (el) => el.child(hint(`↑ ${more.above} more`).top(6)))
      .when(more.above > 0 && Boolean(sticky), (el) => el.child(hint(`↑ ${more.above} more`).top(ROW + 6)))
      .when(more.below > 0, (el) => el.child(hint(`↓ ${more.below} more`).bottom(6)));
  }

  columns(t, board) {
    return h_flex().id("columns").flex_1().min_h_0().gap(10).p(10).items_start().children(
      columnCards(board).map((col) =>
        v_flex().id(`col-${col.category}`).flex_1().min_w(220).h_full().min_h_0().gap(6)
          .child(h_flex().px(4).pb(4).border_b(1).border_color(t.border)
            .child(div().font_semibold().text_color(t.foreground).child(col.title))
            .child(div().flex_1())
            .child(div().text_color(t.dim).child([String(col.count), col.points].filter(Boolean).join(" · "))))
          .child(v_flex().id(`col-${col.category}-cards`).flex_1().min_h_0().overflow_y_scrollbar().gap(6)
            .children(col.cards.map((c) => this.card(t, c))))));
  }

  body(t) {
    if (this.choices) return this.chooser(t);
    const answer = this.answers[this.tab];
    if (!answer) return div().p(16).text_color(this.error ? t.blocked : t.muted).child(this.error ? problemOf(this.error, this.machine) : "Loading…");
    if (answer.status === 503) return div().p(16).text_color(t.muted).child("tkt is syncing the board for the first time…");
    if (answer.status !== 200) return div().p(16).text_color(t.blocked).child(problemOf(answer.body?.message ?? `tkt answered ${answer.status}`, this.machine));
    if (this.tab === "sprint" && (answer.body.sections ?? []).length === 0) return div().p(16).text_color(t.muted).child("No tickets on the board: nothing assigned to you in an open sprint.");
    if (this.tab === "sprint" && this.view === "columns") return this.columns(t, answer.body);
    if (this.tab === "sprint") return this.list(t, answer.body);
    if (this.tab === "prs") return this.prsList(t, answer.body);
    return div().p(16).text_color(t.muted).child(`${(answer.body.sections ?? answer.body.groups ?? []).length} groups`);
  }

  status(t) {
    const where = this.machine === undefined ? "looking for tkt…" : `tkt on ${this.machine ?? "this computer"} ▾`;
    return h_flex().px(12).py(5).gap(12).bg(t.chrome).border_t(1).border_color(t.border).text_size(12)
      .child(div().id("machine").px(4).rounded(4).text_color(t.dim).hover((s) => s.bg(t.hover).text_color(t.foreground))
        .on_mouse_down("left", (_e, cx) => cx.stop_propagation())
        .on_click((_e, cx) => this.pick(cx))
        .child(where))
      .child(div().flex_1().truncate().text_color(this.error ? t.blocked : t.dim).child(this.error ? problemOf(this.error, this.machine) : ""))
      .child(div().text_color(t.dim).child(this.answers.sprint?.body?.sync_label ?? ""))
      .child(div().id("sync").px(6).rounded(4).text_color(t.muted).hover((s) => s.bg(t.hover).text_color(t.foreground))
        .on_mouse_down("left", (_e, cx) => cx.stop_propagation())
        .on_click((_e, cx) => this.sync(cx))
        .child("↻"));
  }

  render(_cx) {
    const t = theme();
    return v_flex().id("root").relative().size_full().bg(t.surface).text_size(13)
      .on_mouse_down("left", (_e, cx) => { if (this.reposOpen) { this.reposOpen = false; cx.notify(); } })
      .child(this.header(t))
      .child(v_flex().id("body").flex_1().min_h_0().child(this.body(t)))
      .when(this.tab === "sprint" && !this.choices, (el) => el.child(this.preview(t, this.answers.sprint?.body)))
      .child(this.legend(t))
      .child(this.status(t))
      .when(this.tab === "prs" && Boolean(this.reposOpen), (el) => el.child(this.reposPanel(t)));
  }
}
