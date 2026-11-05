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

test("the Backlog shows each ticket's agent, from the agents the board knows, as the TUI's AGENT column does", async () => {
  const { backlogAgents } = await import("../mod/lib/backlog.js");
  const { listColumns, agentWord } = await import("../mod/lib/rows.js");
  const { cardOf } = await import("../mod/lib/columns.js");
  const agents = backlogAgents({ sprint: { status: 200, body: { agents: { "DEMO-40": "working" } } } });
  assert.deepEqual(agents, { "DEMO-40": "working" });
  assert.equal(listColumns([{ ticket: {} }], agents).agent, true);
  assert.equal(agentWord(cardOf({ key: "DEMO-40", ticket: {} }, agents).agent), "● working");
  assert.deepEqual(backlogAgents({}), {});
});
