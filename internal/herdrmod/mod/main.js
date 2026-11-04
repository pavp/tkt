// tkt's board in Herdr desktop: the daemon's /v1 API, reached through `tkt api` on whichever machine of the
// core runs tkt, so it works whether tkt runs here or on a machine reached over SSH.

import { View, div } from "gpui-kit";
import { v_flex, h_flex } from "gpui-base";
import { exec, machines, theme, props, closeOverlay } from "herdr";
import { apiArgs, parseAnswer, machineOrder } from "./lib/client.js";
import { listRows } from "./lib/list.js";

const TABS = [
  { id: "sprint", title: "Sprint", route: "/v1/board" },
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
    this.load(cx);
    cx.timer.every(POLL, (cx) => this.load(cx));
  }

  async run(args, on) {
    return parseAnswer(await exec("tkt", args, on ? { on } : {}));
  }

  /** One daemon request on the machine tkt was found on, looking for it first when none is known yet. */
  async call(method, path, body) {
    if (this.machine === undefined) {
      let reason = "tkt was not found on any machine of Herdr";
      for (const on of machineOrder(machines(), remembered())) {
        try {
          await this.run(apiArgs("GET", "/health"), on);
          this.machine = on;
          try {
            localStorage.setItem("machine", JSON.stringify(on));
          } catch {}
          break;
        } catch (e) {
          reason = e.message;
        }
      }
      if (this.machine === undefined) throw new Error(reason);
    }
    try {
      return await this.run(apiArgs(method, path, body), this.machine);
    } catch (e) {
      this.machine = undefined;
      throw e;
    }
  }

  load(cx) {
    const tab = TABS.find((t) => t.id === this.tab);
    cx.spawn(async (cx) => {
      try {
        this.answers[tab.id] = await this.call("GET", tab.route);
        this.error = null;
      } catch (e) {
        this.error = e.message;
      }
      cx.notify();
    });
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
      .child(div().id("close").px(8).py(3).rounded(6).text_color(t.muted).hover((s) => s.bg(t.hover))
        .on_mouse_down("left", (_e, cx) => cx.stop_propagation())
        .on_click(() => close())
        .child("✕"));
  }

  body(t) {
    const answer = this.answers[this.tab];
    if (!answer) return div().p(16).text_color(t.muted).child(this.error ? "" : "Loading…");
    if (answer.status === 503) return div().p(16).text_color(t.muted).child("tkt is syncing the board for the first time…");
    if (answer.status !== 200) return div().p(16).text_color(t.blocked).child(answer.body?.message ?? `tkt answered ${answer.status}`);
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
    const where = this.machine === undefined ? "looking for tkt…" : `tkt on ${this.machine ?? "this computer"}`;
    return h_flex().px(12).py(5).gap(12).bg(t.chrome).border_t(1).border_color(t.border).text_size(12)
      .child(div().text_color(t.dim).child(where))
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
