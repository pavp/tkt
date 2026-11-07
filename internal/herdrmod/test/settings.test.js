import { test } from "node:test";
import assert from "node:assert/strict";
import { fixture } from "./fixtures.js";
import { SECTIONS, reposPatch, boardsPatch, statusPatch, rootsPatch, statusRows } from "../mod/lib/settings.js";

test("Settings has the TUI's sections, the mod's Colors, and the machine the window uses", () => {
  assert.deepEqual(SECTIONS.map((s) => s.title), ["Spaces & repositories", "Team boards", "Statuses", "Local clones", "Board", "Colors", "Machine"]);
});

test("a repository is added to or removed from its space as the whole list", () => {
  const space = fixture("settings.json").spaces[0];
  assert.deepEqual(reposPatch(space, { add: { repo: "example-org/api", path: "~/src/api" } }), { spaces: { DEMO: { repos: [{ repo: "example-org/web", path: "~/src/web" }, { repo: "example-org/api", path: "~/src/api" }] } } });
  assert.deepEqual(reposPatch(space, { remove: "example-org/web" }), { spaces: { DEMO: { repos: [] } } });
});

test("a team board is turned on or off as the whole list of ids", () => {
  const space = fixture("settings.json").spaces[0];
  assert.deepEqual(boardsPatch(space, 9), { spaces: { DEMO: { team_boards: [7, 9] } } });
  assert.deepEqual(boardsPatch(space, 7), { spaces: { DEMO: { team_boards: [] } } });
});

test("a status gets a column or mark, or goes back to automatic", () => {
  assert.deepEqual(statusPatch("DEMO", "Testing", { column: "done" }), { spaces: { DEMO: { statuses: { Testing: { column: "done" } } } } });
  assert.deepEqual(statusPatch("DEMO", "Testing", null), { spaces: { DEMO: { statuses: { Testing: null } } } });
  const rows = statusRows(fixture("settings-statuses.json"), fixture("settings.json").columns);
  assert.deepEqual(rows[2], { name: "Testing", column: "In Review", columnAuto: false, mark: "diamond", markAuto: true, changed: true });
});

test("a clones folder is added or removed", () => {
  assert.deepEqual(rootsPatch(["~/src"], { add: "~/code" }), { repo_roots: ["~/src", "~/code"] });
  assert.deepEqual(rootsPatch(["~/src", "~/code"], { remove: "~/src" }), { repo_roots: ["~/code"] });
});

test("the Statuses board puts each status in its column, saying whether you set it or it is automatic", async () => {
  const { statusBoard } = await import("../mod/lib/settings.js");
  const view = fixture("settings-statuses.json");
  const columns = fixture("settings.json").columns;
  const board = statusBoard(view, columns);
  assert.deepEqual(board.map((c) => c.id), columns.map((c) => c.id));
  const all = board.flatMap((c) => c.statuses.map((s) => ({ ...s, column: c.id })));
  assert.equal(all.length, view.statuses.length);
  for (const s of view.statuses) {
    const got = all.find((x) => x.name === s.name);
    assert.equal(got.column, s.column);
    assert.equal(got.manual, !s.column_auto || !s.mark_auto);
    assert.equal(got.mark, s.mark);
  }
});

test("a status moves to the column beside its own, and stops at the board's ends", async () => {
  const { nextColumn } = await import("../mod/lib/settings.js");
  const cols = [{ id: "todo" }, { id: "in_progress" }, { id: "review" }, { id: "done" }];
  assert.equal(nextColumn(cols, "in_progress", 1), "review");
  assert.equal(nextColumn(cols, "in_progress", -1), "todo");
  assert.equal(nextColumn(cols, "todo", -1), null);
  assert.equal(nextColumn(cols, "done", 1), null);
});

test("the mark picker offers every mark with its icon, none first", async () => {
  const { markChoices } = await import("../mod/lib/settings.js");
  const marks = markChoices(["none", "diamond", "blocked", "clock", "flag", "star", "triangle"]);
  assert.deepEqual(marks.map((m) => m.glyph), ["—", "◇", "⊘", "◷", "⚑", "★", "▲"]);
  assert.equal(marks[0].name, "none");
});

test("a status drag starts once the pointer travels a few pixels, so a click stays a click", async () => {
  const { dragStarted } = await import("../mod/lib/settings.js");
  const press = { x: 100, y: 200 };
  assert.equal(dragStarted(press, { x: 102, y: 201 }), false);
  assert.equal(dragStarted(press, { x: 105, y: 200 }), true);
  assert.equal(dragStarted(press, { x: 100, y: 194 }), true);
  assert.equal(dragStarted(null, { x: 300, y: 300 }), false);
});

test("the column under the pointer is read from where it is across the row of equal columns, none in a gap", async () => {
  const { laneAt } = await import("../mod/lib/settings.js");
  const row = { width: 430, count: 4, gap: 10 };
  assert.equal(laneAt(0, row), 0);
  assert.equal(laneAt(99, row), 0);
  assert.equal(laneAt(105, row), -1, "the gap between two columns is no column");
  assert.equal(laneAt(111, row), 1);
  assert.equal(laneAt(429, row), 3);
  assert.equal(laneAt(-5, row), -1);
  assert.equal(laneAt(431, row), -1);
});
