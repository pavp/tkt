import { test } from "node:test";
import assert from "node:assert/strict";
import { fixture } from "./fixtures.js";
import { columnCards, cardOf, wrapWords } from "../mod/lib/columns.js";

test("the columns are the board's, each titled with its count and points", () => {
  const cols = columnCards(fixture("board.json"));
  assert.deepEqual(cols.map((c) => [c.title, c.count, c.points]), [["To Do", 2, "1 pts"], ["In Progress", 1, "3 pts"], ["In Review", 0, ""], ["Done", 1, ""]]);
});

test("a card says what the TUI's card says", () => {
  const [todo, doing] = columnCards(fixture("board.json"));
  const bug = todo.cards.find((c) => c.key === "DEMO-2");
  assert.equal(bug.points, "1pt");
  assert.equal(bug.who, "Ada L.");
  assert.equal(bug.typeKind, "bug");
  const sub = todo.cards.find((c) => c.key === "DEMO-4");
  assert.equal(sub.subtask, true);
  assert.equal(sub.info, "of DEMO-1");
  const story = doing.cards[0];
  assert.equal(story.agent, "working");
  assert.equal(story.attention, "CI failing");
  assert.equal(story.info, "0/1 subtasks · 1 to do · Story");
  assert.deepEqual(story.prs, [{ ref: "#12", mark: "✕", tone: "blocked" }]);
});

test("a summary wraps on words into at most the lines it has", () => {
  assert.deepEqual(wrapWords("Filter accounts by region and plan", 16, 2), ["Filter accounts", "by region and…"]);
  assert.deepEqual(wrapWords("Short", 16, 2), ["Short"]);
  assert.deepEqual(wrapWords("", 16, 2), []);
});

test("a status mark shows as the TUI's icon before the status, never its name", async () => {
  const { markOf } = await import("../mod/lib/columns.js");
  assert.deepEqual(markOf("diamond", "Ready for Testing"), { glyph: "◇", word: "◇ Ready for Testing", tone: "ansi4" });
  assert.deepEqual(markOf("blocked", "Blocked"), { glyph: "⊘", word: "⊘ Blocked", tone: "peach" });
  assert.equal(markOf("none", "To Do"), null);
  assert.equal(markOf("", "To Do"), null);
  assert.equal(markOf("unknown", "To Do"), null);
});

test("a card's pull requests end with its finished ones, as the TUI words them", () => {
  const c = cardOf({ key: "DEMO-9", ticket: { key: "DEMO-9", prs: [{ number: 1, state: "merged" }] }, card: { finished: "#1 merged" } }, {});
  assert.deepEqual(c.prs, []);
  assert.equal(c.finished, "#1 merged");
});

test("the TUI's peach stays the color for blocked marks and what needs you", async () => {
  const { PEACH } = await import("../mod/lib/columns.js");
  assert.equal(PEACH, "#fab387");
});
