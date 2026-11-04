import { test } from "node:test";
import assert from "node:assert/strict";
import { fixture } from "./fixtures.js";
import { listRows } from "../mod/lib/list.js";

test("the list shows each section's title, then its rows, in the board's order", () => {
  for (const name of ["board.json", "board-team.json"]) {
    const board = fixture(name);
    const rows = listRows(board.sections);
    const titles = rows.filter((r) => r.kind === "title").map((r) => r.title);
    assert.deepEqual(titles, board.sections.map((s) => s.title), name);
    for (const s of board.sections) {
      const at = rows.findIndex((r) => r.kind === "title" && r.title === s.title);
      const keys = rows.slice(at + 1, at + 1 + s.rows.length).map((r) => r.key);
      assert.deepEqual(keys, s.rows.map((r) => r.key), `${name}: ${s.title}`);
    }
  }
});

test("an empty board has no rows", () => {
  assert.deepEqual(listRows([]), []);
  assert.deepEqual(listRows(undefined), []);
});

test("the sticky title is the section of the first row shown, once its own title scrolled away", async () => {
  const { stickyAt, moreAround } = await import("../mod/lib/list.js");
  const rows = listRows(fixture("board-team.json").sections);
  assert.equal(stickyAt(rows, { start: 0, end: 3 }), null);
  const second = rows.findIndex((r, i) => i > 0 && r.kind === "title");
  assert.equal(stickyAt(rows, { start: 2, end: 4 })?.title, "My tickets");
  assert.equal(stickyAt(rows, { start: second, end: second + 2 }), null);
  assert.equal(stickyAt(rows, { start: second + 1, end: second + 2 })?.title, rows[second].title);
  const tickets = rows.filter((r) => r.kind === "row").length;
  assert.deepEqual(moreAround(rows, { start: 0, end: rows.length }), { above: 0, below: 0 });
  assert.deepEqual(moreAround(rows, { start: 2, end: 3 }), { above: 1, below: tickets - 2 });
});
