// tkt's board in Herdr desktop: the daemon's /v1 API, reached through `tkt api` on whichever machine of the
// core runs tkt, so it works whether tkt runs here or on a machine reached over SSH.

import { View, div } from "gpui-kit";
import { v_flex, h_flex } from "gpui-base";
import { exec, machines, knownAgents, theme, props, closeOverlay } from "herdr";
import { apiArgs, parseAnswer, machineOrder, pickMachine, agentKinds, failed } from "./lib/client.js";
import { listRows } from "./lib/list.js";
import { columnCards, wrapWords } from "./lib/columns.js";

const TABS = [
  { id: "sprint", title: "Sprint", route: "/v1/board?herdr=desktop" },
  { id: "prs", title: "PRs", route: "/v1/prs" },
  { id: "backlog", title: "Backlog", route: "/v1/backlog" },
];
// No event stream reaches a mod, so the shown tab is read again this often.
const POLL = 5000;

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
    cx.spawn(async (cx) => {
      try {
        this.answers[tab.id] = await this.call("GET", tab.route);
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
    return v_flex().id(`card-${c.key}`).p(8).gap(3).rounded(8).border(1)
      .border_color(c.attention ? t.blocked : t.border).bg(t.raised).hover((s) => s.border_color(t.accent))
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

  columns(t, board) {
    return h_flex().id("columns").flex_1().gap(10).p(10).items_start().children(
      columnCards(board).map((col) =>
        v_flex().id(`col-${col.category}`).flex_1().min_w(220).h_full().gap(6)
          .child(h_flex().px(4).pb(4).border_b(1).border_color(t.border)
            .child(div().font_semibold().text_color(t.foreground).child(col.title))
            .child(div().flex_1())
            .child(div().text_color(t.dim).child([String(col.count), col.points].filter(Boolean).join(" · "))))
          .child(v_flex().id(`col-${col.category}-cards`).flex_1().overflow_y_scrollbar().gap(6)
            .children(col.cards.map((c) => this.card(t, c))))));
  }

  body(t) {
    if (this.choices) return this.chooser(t);
    const answer = this.answers[this.tab];
    if (!answer) return div().p(16).text_color(t.muted).child(this.error ? "" : "Loading…");
    if (answer.status === 503) return div().p(16).text_color(t.muted).child("tkt is syncing the board for the first time…");
    if (answer.status !== 200) return div().p(16).text_color(t.blocked).child(answer.body?.message ?? `tkt answered ${answer.status}`);
    if (this.tab === "sprint" && this.view === "columns") return this.columns(t, answer.body);
    if (this.tab !== "sprint") return div().p(16).text_color(t.muted).child(`${(answer.body.sections ?? answer.body.groups ?? []).length} groups`);
    return v_flex().id("rows").flex_1().overflow_y_scrollbar().px(12).py(8).gap(2).children(
      listRows(answer.body.sections).map((r, i) =>
        r.kind === "title"
          ? div().id(`r-${i}`).pt(10).pb(4).font_semibold().text_color(t.foreground).child(`${r.title} · ${r.count}`)
          : h_flex().id(`r-${i}`).gap(10).px(6).py(3).rounded(4).hover((s) => s.bg(t.hover))
              .child(div().w(90).text_color(t.accent).child(r.key))
              .child(div().flex_1().truncate().text_color(t.foreground).child(r.ticket?.summary ?? ""))
              .child(div().text_color(t.dim).child(r.ticket?.status ?? ""))));
  }

  status(t) {
    const where = this.machine === undefined ? "looking for tkt…" : `tkt on ${this.machine ?? "this computer"} ▾`;
    return h_flex().px(12).py(5).gap(12).bg(t.chrome).border_t(1).border_color(t.border).text_size(12)
      .child(div().id("machine").px(4).rounded(4).text_color(t.dim).hover((s) => s.bg(t.hover).text_color(t.foreground))
        .on_mouse_down("left", (_e, cx) => cx.stop_propagation())
        .on_click((_e, cx) => this.pick(cx))
        .child(where))
      .child(div().flex_1().truncate().text_color(this.error ? t.blocked : t.dim).child(this.error ?? ""));
  }

  render(_cx) {
    const t = theme();
    return v_flex().id("root").size_full().bg(t.surface).text_size(13)
      .child(this.header(t))
      .child(this.body(t))
      .child(this.status(t));
  }
}
