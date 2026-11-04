import { test } from "node:test";
import assert from "node:assert/strict";
import { fixture } from "./fixtures.js";
import { backlogRows, badges } from "../mod/lib/backlog.js";

test("the Backlog shows each sprint's title, dates and points, then its tickets unless folded", () => {
  const rows = backlogRows(fixture("backlog.json"));
  const titles = rows.filter((r) => r.kind === "title");
  assert.deepEqual(titles.map((r) => [r.title, r.board, r.dates, r.collapsed]), [
    ["Sprint 12", "Web", "Oct 26 – Nov 6 · active", false],
    ["Sprint 12", "Core", "from Nov 6", false],
    ["Backlog", "", "", true],
  ]);
  assert.deepEqual(rows.filter((r) => r.kind === "row").map((r) => r.key), ["DEMO-20", "DEMO-21", "DEMO-30"]);
  assert.equal(rows[1].card.points, "5pt");
});

test("a group's points are its three stage badges, after how many have no estimate", () => {
  assert.deepEqual(badges({ todo: 0, doing: 5, done: 0, unestimated: 1 }), { unestimated: "1 unestimated", stages: [["todo", "0"], ["doing", "5"], ["done", "0"]] });
  assert.deepEqual(badges({ todo: 2, doing: 0, done: 0, unestimated: 0 }), { unestimated: "", stages: [["todo", "2"], ["doing", "0"], ["done", "0"]] });
  assert.equal(badges(undefined), null);
});
