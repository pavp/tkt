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
