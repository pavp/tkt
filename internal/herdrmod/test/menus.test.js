import { test } from "node:test";
import assert from "node:assert/strict";
import { fixture } from "./fixtures.js";
import { ticketMenu, prMenu, menuFooter } from "../mod/lib/menus.js";

const labels = (m) => m.items.map((i) => i.label);

test("a ticket of yours offers what the TUI's menu offers, feedback first when it needs you", () => {
  const board = fixture("board.json");
  const row = board.columns[1].rows[0];
  const m = ticketMenu(row.ticket, { agent: board.agents[row.key] });
  assert.equal(m.title, "Actions for DEMO-1");
  assert.equal(m.subtitle, "In Progress");
  assert.deepEqual(labels(m), [
    "Go to the agent in Herdr",
    "Show details",
    "Open in Jira ↗",
    "Open #12 in GitHub ↗",
    "Send the CI failure to the agent",
    "Send its feedback to another agent ▸",
    "Move to… ▸",
    "Assign to… ▸",
    "Copy the key DEMO-1",
    "Copy the ticket context (.ticket.md)",
  ]);
  assert.equal(m.items[0].action, "start");
  assert.equal(m.items[2].url, row.ticket.url);
  assert.equal(menuFooter(m.items[2]), "enter run · c copy link · esc close", "c copies the link of the item that opens it, so no row repeats it");
});

test("without an agent, a ticket starts one and its feedback goes to an agent picked", () => {
  const row = fixture("board.json").columns[1].rows[0];
  const m = ticketMenu(row.ticket, {});
  assert.equal(m.items[0].label, "Start · open the agent");
  assert.ok(labels(m).includes("Send the CI failure to an agent ▸"));
});

test("a team ticket offers details, Jira, assign and copies", () => {
  const t = { key: "DEMO-8", status: "To Do", team: true, url: "https://example.atlassian.net/browse/DEMO-8" };
  const m = ticketMenu(t, {});
  assert.equal(m.subtitle, "To Do · unassigned");
  assert.deepEqual(labels(m), ["Show details", "Open in Jira ↗", "Assign to… ▸", "Copy the key DEMO-8"]);
});

test("a backlog ticket not yours asks before starting", () => {
  const t = { key: "DEMO-40", status: "To Do", assignee: "Grace Hopper", board: "Web", url: "u" };
  const m = ticketMenu(t, { backlog: true, me: "Ada Lovelace" });
  assert.equal(m.subtitle, "To Do · Grace Hopper · Web");
  assert.equal(m.items[0].action, "notYours");
});

test("someone's pull request opens and reviews, c copying its link", () => {
  const view = fixture("prs.json");
  const review = view.sections[2].rows[0];
  const m = prMenu(review, {});
  assert.equal(m.title, "Pull request #15");
  assert.equal(m.subtitle, "example-org/api · grace · review requested");
  assert.deepEqual(labels(m), ["Open in GitHub ↗", "Review with ▸"]);
});

test("one of your pull requests acts on it, then on its ticket", () => {
  const mine = fixture("prs.json").sections[0].rows[0];
  const m = prMenu(mine, { agent: "working" });
  assert.equal(m.title, "Pull request #12");
  assert.deepEqual(labels(m).slice(0, 5), ["Open #12 in GitHub ↗", "Send the CI failure to the agent", "Send its feedback to another agent ▸", "Review with ▸", "Go to the agent in Herdr"]);
});

test("a backlog ticket offers its board's current sprint, asking first, unless it is already in one", async () => {
  const { sprintMoveItem } = await import("../mod/lib/menus.js");
  const backlog = {
    groups: [
      { group: "sprint:Sprint 12", title: "Sprint 12", sprints: [11, 21], active: true, rows: [{ key: "DEMO-20" }] },
      { group: "backlog:", title: "Backlog", rows: [{ key: "DEMO-40" }] },
    ],
    activeSprints: [{ id: 11, name: "Sprint 12", board: "Web" }, { id: 31, name: "Ops 3", board: "Ops" }, { id: 32, name: "Ops 4", board: "Ops" }],
  };
  const one = sprintMoveItem({ key: "DEMO-40", board: "Web" }, backlog);
  assert.equal(one.label, "Move to current sprint");
  assert.equal(one.menu.title, "Move DEMO-40 to Sprint 12?");
  assert.deepEqual(one.menu.items.map((i) => i.label), ["Yes, move it", "Cancel"]);
  assert.deepEqual([one.menu.items[0].action, one.menu.items[0].sprint], ["moveToSprint", 11]);
  const several = sprintMoveItem({ key: "DEMO-41", board: "Ops" }, backlog);
  assert.equal(several.label, "Move to current sprint ▸");
  assert.equal(several.menu.title, "Move DEMO-41 to which sprint?");
  assert.deepEqual(several.menu.items.map((i) => i.menu.title), ["Move DEMO-41 to Ops 3?", "Move DEMO-41 to Ops 4?"]);
  assert.equal(sprintMoveItem({ key: "DEMO-20", board: "Web" }, backlog), null);
  assert.equal(sprintMoveItem({ key: "DEMO-40", board: "Core" }, backlog), null);
});

test("a menu of one yes and Cancel is a confirmation, drawn as a dialog", async () => {
  const { isConfirm } = await import("../mod/lib/menus.js");
  assert.equal(isConfirm({ title: "Move DEMO-1 to Done?", items: [{ label: "Yes, move it", action: "transition" }, { label: "Cancel", action: "cancel" }] }), true);
  assert.equal(isConfirm({ title: "Move DEMO-1 to", items: [{ label: "Done", action: "submenu" }, { label: "Cancel", action: "cancel" }] }), false, "a choice is not a yes");
  assert.equal(isConfirm({ title: "Actions", items: [{ label: "Show details", action: "details" }] }), false);
});

test("a menu item's trailing ▸ or ↗ goes to the right, and items group by what they do", async () => {
  const { splitTail, itemGroup } = await import("../mod/lib/menus.js");
  assert.deepEqual(splitTail("Move to… ▸"), { label: "Move to…", tail: "▸" });
  assert.deepEqual(splitTail("Open in Jira ↗"), { label: "Open in Jira", tail: "↗" });
  assert.deepEqual(splitTail("Copy the key DEMO-1"), { label: "Copy the key DEMO-1", tail: "" });
  assert.equal(itemGroup({ action: "open" }), itemGroup({ action: "submenu", menu: {} }));
  assert.notEqual(itemGroup({ action: "move" }), itemGroup({ action: "copy" }));
  assert.equal(itemGroup({ action: "copyContext" }), itemGroup({ action: "copy" }));
});

test("the menu's key line reads as keys and what they do", async () => {
  const { footerKeys } = await import("../mod/lib/menus.js");
  assert.deepEqual(footerKeys("enter run · c copy link · esc close"), [["enter", "run"], ["c", "copy link"], ["esc", "close"]]);
});

test("a ticket's pull requests submenu names each one by number and title", () => {
  const t = { key: "DEMO-5", status: "In Progress", url: "u", prs: [{ number: 12, title: "Add the filter", url: "a" }, { number: 13, title: "Fix the date range", url: "b" }] };
  const m = ticketMenu(t, {});
  const prs = m.items.find((i) => i.label === "Pull requests (2) ▸");
  assert.deepEqual(prs.menu.items.map((i) => i.label), ["#12 Add the filter", "#13 Fix the date range"]);
  assert.deepEqual(ticketMenu({ ...t, prs: [{ number: 12, url: "a" }, { number: 13, url: "b" }] }, {}).items.find((i) => i.action === "submenu").menu.items.map((i) => i.label), ["#12", "#13"], "a pull request without a title keeps its number");
});
