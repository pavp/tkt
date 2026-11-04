// tkt's board in Herdr desktop: the daemon's /v1 API, reached through `tkt api` on whichever machine of the
// core runs tkt, so it works whether tkt runs here or on a machine reached over SSH.

import { View, div } from "gpui-kit";
import { v_flex, h_flex, v_virtual_list, Scrollbar, InputState, Input } from "gpui-base";
import { exec, machines, knownAgents, theme, props, closeOverlay } from "herdr";
import { apiArgs, parseAnswer, machineOrder, pickMachine, agentKinds, failed, tktOn, outdatedNote } from "./lib/client.js";
import { listRows, stickyAt, moreAround } from "./lib/list.js";
import { columnCards, cardOf, wrapWords } from "./lib/columns.js";
import { previewOf, legendFor, problemOf } from "./lib/footer.js";
import { prRows, toggled, reposChip } from "./lib/prs.js";
import { backlogRows, badges } from "./lib/backlog.js";
import { ticketMenu, prMenu } from "./lib/menus.js";
import { firstPicker, answer, taskPicker } from "./lib/start.js";
import { moveMenu, formMenu, assignMenu, confirmAssign, missingField } from "./lib/move.js";

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
    this.menu = null;
    this.picker = null;
    this.prompt = null;
    this.form = null;
    this.input = InputState.new({ placeholder: "" });
    this.notice = "";
    this.lastClick = { key: null, at: 0 };
    this.focus = cx.focus_handle();
    this.outdated = [];
    this.view = "columns";
    this.range = { start: 0, end: 0 };
    this.shownRange = { start: 0, end: 0 };
    this.selected = null;
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
    this.load(cx);
    cx.timer.every(POLL, (cx) => this.load(cx));
  }

  async run(args, on) {
    return parseAnswer(await exec("tkt", args, on ? { on } : {}));
  }

  /** The machines whose tkt the mod can use, asked with `tkt api --help`, which starts no daemon; a tkt too old
   *  for `tkt api` is set apart so the person can be told to update it. */
  async discover() {
    const found = [];
    this.outdated = [];
    for (const on of machineOrder(machines(), remembered())) {
      let r = null;
      try {
        r = await exec("tkt", ["api", "--help"], on ? { on } : {});
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

  load(cx) {
    const tab = TABS.find((t) => t.id === this.tab);
    let route = tab.id === "prs" && this.bots ? `${tab.route}?bots=true` : tab.route;
    if (tab.id === "backlog" && this.refreshBacklog) {
      route = `${tab.route}?refresh=true`;
      this.refreshBacklog = false;
    }
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
      .when((view.repos ?? []).length > 0, (el) => el.child(this.toggle("repos", this.panel === "repos", (cx) => { this.panel = this.panel === "repos" ? null : "repos"; cx.notify(); })
        .child(`${reposChip(view.repos)} ▾`)));
  }

  /** A filter's checkbox panel under the header: each name with its count, a click hides or shows it. */
  filterPanel(t, items, hidden, field) {
    return v_flex().id(`${field}-panel`).absolute().top(44).right(48).w(280).p(6).gap(2).rounded(8).bg(t.popover).border(1).border_color(t.border)
      .on_mouse_down("left", (_e, cx) => cx.stop_propagation())
      .children(items.map((r) =>
        h_flex().id(`${field}-${r.name}`).gap(8).px(8).py(4).rounded(5).hover((s) => s.bg(t.hover))
          .on_click((_e, cx) => this.patchState({ [field]: toggled(hidden, r.name) }, cx))
          .child(div().w(14).text_color(t.accent).child(r.hidden ? "☐" : "☑"))
          .child(div().flex_1().truncate().text_color(r.hidden ? t.muted : t.foreground).child(r.name))
          .child(div().text_color(t.dim).child(String(r.prs ?? r.tickets ?? "")))));
  }

  openPanel() {
    const view = this.answers[this.tab]?.body;
    if (!this.panel || !view) return null;
    if (this.panel === "repos") return { items: view.repos ?? [], hidden: view.hidden_repos, field: "hidden_repos" };
    return { items: view.boards ?? [], hidden: view.hidden_boards, field: "hidden_boards" };
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
    return h_flex().gap(6).child(this.boardsChip(t))
      .child(h_flex().p(2).gap(2).rounded(7).border(1).border_color(t.border).child(option(false, "Split")).child(option(true, "Merged")));
  }

  backlogRow(t, r) {
    if (r.kind === "title") {
      const b = badges(r.points);
      const tone = { todo: t.unknown, doing: t.working, done: t.done };
      return h_flex().h(ROW).px(12).gap(8).items_end().pb(3).text_color(t.foreground)
        .child(div().text_color(t.dim).child(r.collapsed ? "▸" : "▾"))
        .child(div().font_semibold().child(r.title))
        .when(Boolean(r.board), (el) => el.child(div().text_color(t.muted).child(r.board)))
        .when(Boolean(r.dates), (el) => el.child(div().text_color(t.dim).child(r.dates)))
        .child(div().text_color(t.dim).child(String(r.count)))
        .child(div().flex_1())
        .when(Boolean(b?.unestimated), (el) => el.child(div().text_color(t.dim).child(b.unestimated)))
        .when(Boolean(b), (el) => el.children(b.stages.map(([stage, n]) => div().px(5).rounded(3).bg(tone[stage]).text_color(t.background).child(n))));
    }
    return this.listRow(t, r, { agents: {} });
  }

  backlogList(t, view) {
    if (view.no_team_boards) return div().p(16).text_color(t.muted).child("No team boards set: choose them in Settings › Team boards to see their sprints and backlog here.");
    const rows = backlogRows(view);
    return v_flex().id("backlog").relative().flex_1().min_h_0()
      .child(v_virtual_list("backlog-rows", rows.length, ROW, (i) => `${i}`, (range) =>
        rows.slice(range.start, range.end).map((r) => this.backlogRow(t, r))).size_full().on_item_click((key, cx) => {
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
    if (this.tab === "backlog") return ticketMenu(row.ticket, { backlog: true, me: this.answers.backlog?.body?.me, agent: agents[row.key] });
    return ticketMenu(row.ticket, { agent: agents[row.key], attention: row.card?.attention });
  }

  async local(script, arg) {
    const r = await exec("sh", ["-c", script, "sh", arg]);
    if (r.code !== 0) throw new Error((r.stderr || "it did not run").trim());
  }

  /** Runs a menu item; the screens Start, Move, Assign, details and feedback open come with their own tasks. */
  act(item, cx) {
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
        if (p.kind === "comment") {
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
            .on_click((_e, cx) => this.answerPrompt(cx)).child(p.kind === "comment" ? "Done" : "Search"))));
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
    if (a.status === 202) this.notice = `starting ${key}: its agent opens in Herdr`;
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
            .when(Boolean(p.multi), (el) => el.child(div().w(14).text_color(t.accent).child(p.checked.has(r.value) ? "☑" : "☐")))
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
    return v_flex().id("menu").absolute().top(70).left(0).right(0).items_center()
      .child(v_flex().id("menu-box").w(380).p(10).gap(2).rounded(10).bg(t.popover).border(1).border_color(t.accent)
        .on_mouse_down("left", (_e, cx) => cx.stop_propagation())
        .child(div().font_semibold().text_color(t.foreground).child(m.title))
        .when(Boolean(m.subtitle), (el) => el.child(div().pb(6).text_size(12).text_color(t.dim).child(m.subtitle)))
        .children(m.items.map((item, i) =>
          div().id(`menu-${i}`).px(8).py(4).rounded(5).bg(i === m.sel ? t.selected : t.popover).text_color(t.foreground)
            .hover((s) => s.bg(t.hover))
            .on_click((_e, cx) => this.act(item, cx))
            .child(item.label))));
  }

  key(e, cx) {
    const k = e.key;
    if (this.menu) {
      if (k === "escape") this.menu = null;
      if (k === "down") this.menu.sel = Math.min(this.menu.items.length - 1, this.menu.sel + 1);
      if (k === "up") this.menu.sel = Math.max(0, this.menu.sel - 1);
      if (k === "enter") return this.act(this.menu.items[this.menu.sel], cx);
    } else if (this.picker && k === "escape") {
      this.picker = null;
    } else if (k === "escape" && this.panel) {
      this.panel = null;
    }
    cx.notify();
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
      .when(this.tab === "backlog", (el) => el.child(this.backlogControls(t)))
      .when(this.tab === "sprint", (el) => el.child(this.boardsChip(t)))
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
      .on_click((_e, cx) => { this.selected = c.key; this.clicked(c.key, () => this.menuFor(this.selectedRow(this.answers.sprint?.body) ?? {}), cx); })
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
      .child(div().text_color(t.dim).child(this.tab === "backlog" ? (this.answers.backlog?.body?.error ? `! ${this.answers.backlog.body.error}` : "") : this.answers.sprint?.body?.sync_label ?? ""))
      .child(div().id("sync").px(6).rounded(4).text_color(t.muted).hover((s) => s.bg(t.hover).text_color(t.foreground))
        .on_mouse_down("left", (_e, cx) => cx.stop_propagation())
        .on_click((_e, cx) => this.sync(cx))
        .child("↻"));
  }

  render(_cx) {
    const t = theme();
    return v_flex().id("root").relative().size_full().bg(t.surface).text_size(13)
      .track_focus(this.focus).on_key_down((e, cx) => this.key(e, cx))
      .on_mouse_down("left", (_e, cx) => { this.focus.focus(); if (this.panel || this.menu || this.picker) { this.panel = null; this.menu = null; this.picker = null; cx.notify(); } })
      .child(this.header(t))
      .child(v_flex().id("body").flex_1().min_h_0().child(this.body(t)))
      .when(this.tab === "sprint" && !this.choices, (el) => el.child(this.preview(t, this.answers.sprint?.body)))
      .child(this.legend(t))
      .child(this.status(t))
      .when(Boolean(this.openPanel()), (el) => { const p = this.openPanel(); return el.child(this.filterPanel(t, p.items, p.hidden, p.field)); })
      .when(Boolean(this.menu), (el) => el.child(this.menuBox(t)))
      .when(Boolean(this.picker), (el) => el.child(this.pickerBox(t)))
      .when(Boolean(this.prompt), (el) => el.child(this.promptBox(t)));
  }
}
