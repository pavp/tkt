import { test } from "node:test";
import assert from "node:assert/strict";
import { direction, tabAfter, stepList, stepColumns } from "../mod/lib/nav.js";

test("arrows, wasd and hjkl move as the TUI's do", () => {
  for (const [keys, want] of [[["down", "s", "j"], [0, 1]], [["up", "w", "k"], [0, -1]], [["right", "d", "l"], [1, 0]], [["left", "a", "h"], [-1, 0]]]) {
    for (const k of keys) assert.deepEqual(direction(k), want, k);
  }
  assert.equal(direction("x"), null);
});

test("1, 2 and 3 pick a tab, tab and shift-tab cycle, n and r go to PRs and back", () => {
  assert.equal(tabAfter("1", "prs"), "sprint");
  assert.equal(tabAfter("2", "sprint"), "prs");
  assert.equal(tabAfter("3", "sprint"), "backlog");
  assert.equal(tabAfter("tab", "backlog"), "sprint");
  assert.equal(tabAfter("shift-tab", "sprint"), "backlog");
  assert.equal(tabAfter("n", "sprint"), "prs");
  assert.equal(tabAfter("r", "prs"), "sprint");
  assert.equal(tabAfter("r", "backlog"), "prs");
  assert.equal(tabAfter("x", "sprint"), null);
});

test("in a list, up and down step through the rows and stop at either end; nothing selected starts at the first", () => {
  const ids = ["A", "B", "C"];
  assert.equal(stepList(ids, null, 1), "A");
  assert.equal(stepList(ids, "A", 1), "B");
  assert.equal(stepList(ids, "C", 1), "C");
  assert.equal(stepList(ids, "A", -1), "A");
  assert.equal(stepList(ids, "gone", 1), "A");
  assert.equal(stepList([], null, 1), null);
});

test("in columns, left and right skip empty columns and keep the row as far as the column reaches", () => {
  const cols = [["A1", "A2", "A3"], [], ["C1"], ["D1", "D2"]];
  assert.equal(stepColumns(cols, null, 0, 1), "A1");
  assert.equal(stepColumns(cols, "A1", 0, 1), "A2");
  assert.equal(stepColumns(cols, "A3", 0, 1), "A3");
  assert.equal(stepColumns(cols, "A3", 1, 0), "C1");
  assert.equal(stepColumns(cols, "C1", 1, 0), "D1");
  assert.equal(stepColumns(cols, "D2", 1, 0), "D2");
  assert.equal(stepColumns(cols, "D2", -1, 0), "C1");
  assert.equal(stepColumns(cols, "A2", -1, 0), "A2");
});

test("the selectable items follow the tab and view: cards by column, list rows, PR rows by group, backlog rows", async () => {
  const { targets } = await import("../mod/lib/nav.js");
  const board = {
    columns: [{ title: "To Do", category: "todo", rows: [{ key: "A" }] }, { title: "Done", category: "done", rows: [{ key: "B" }] }],
    sections: [{ title: "My tickets", count: 2, rows: [{ key: "A" }, { key: "B" }] }],
  };
  assert.deepEqual(targets("sprint", "columns", board), { cols: [["A"], ["B"]] });
  assert.deepEqual(targets("sprint", "list", board), { ids: ["A", "B"], at: [1, 2], list: "rows" });
  const prs = { sections: [{ group: "mine:web", title: "web", rows: [{ key: "#1" }] }, { group: "review:web", title: "web", collapsed: true, rows: [{ key: "#2" }] }] };
  assert.deepEqual(targets("prs", "columns", prs), { ids: ["group:mine:web", "#1mine:web", "group:review:web"], at: [0, 1, 2], list: "pr-rows" });
  const backlog = { groups: [{ group: "sprint:1", title: "S1", rows: [{ key: "C" }] }] };
  assert.deepEqual(targets("backlog", "columns", backlog), { ids: ["group:sprint:1", "C"], at: [0, 1], list: "backlog-rows" });
});

test("the board opens with its first ticket selected, as the TUI's does", async () => {
  const { firstTarget } = await import("../mod/lib/nav.js");
  assert.equal(firstTarget({ cols: [[], ["B1"], ["C1"]] }), "B1");
  assert.equal(firstTarget({ ids: ["A", "B"] }), "A");
  assert.equal(firstTarget({ ids: ["group:mine:web", "#1mine:web"] }), "#1mine:web");
  assert.equal(firstTarget({ ids: [] }), null);
});

test("a list of options moves with up and down and stops at its ends", async () => {
  const { listKey } = await import("../mod/lib/nav.js");
  assert.equal(listKey(0, 3, "down"), 1);
  assert.equal(listKey(2, 3, "j"), 2);
  assert.equal(listKey(1, 3, "up"), 0);
  assert.equal(listKey(0, 3, "k"), 0);
  assert.equal(listKey(0, 3, "x"), null);
  assert.equal(listKey(0, 0, "down"), 0);
});

test("Settings' sections follow tab and right, shift-tab and left back, as the TUI's", async () => {
  const { sectionAfter } = await import("../mod/lib/nav.js");
  const ids = ["spaces", "boards", "statuses"];
  assert.equal(sectionAfter(ids, "spaces", "tab"), "boards");
  assert.equal(sectionAfter(ids, "statuses", "right"), "spaces");
  assert.equal(sectionAfter(ids, "spaces", "shift-tab"), "statuses");
  assert.equal(sectionAfter(ids, "boards", "left"), "spaces");
  assert.equal(sectionAfter(ids, "boards", "x"), null);
});

test("z switches the Sprint's cards between detailed and compact, as the TUI's density", async () => {
  const { boardAction } = await import("../mod/lib/nav.js");
  assert.equal(boardAction("z", "sprint"), "density");
  assert.equal(boardAction("z", "prs"), null);
  assert.equal(boardAction("z", "backlog"), null);
});
