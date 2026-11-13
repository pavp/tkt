import { test } from "node:test";
import assert from "node:assert/strict";
import { typeIcon, agentGlyph, cardFlag, cardChips, chipRows, EMPTY_COLUMN } from "../mod/lib/columns.js";

test("a card's type icon and color are the TUI's: bug red dot, epic mauve diamond, story green, task blue, subtask teal", () => {
  assert.deepEqual(typeIcon("bug"), { glyph: "●", tone: "ansi1" });
  assert.deepEqual(typeIcon("epic"), { glyph: "◆", tone: "ansi5" });
  assert.deepEqual(typeIcon("story"), { glyph: "■", tone: "ansi2" });
  assert.deepEqual(typeIcon("task"), { glyph: "■", tone: "ansi4" });
  assert.deepEqual(typeIcon("subtask"), { glyph: "■", tone: "ansi6" });
  assert.deepEqual(typeIcon("other"), { glyph: "■", tone: "dim" });
  assert.equal(typeIcon(""), null);
});

test("an idle agent is a hollow dot, the others a filled one, as Herdr and the TUI draw them", () => {
  assert.equal(agentGlyph("idle"), "○");
  assert.equal(agentGlyph("working"), "●");
  assert.equal(agentGlyph("blocked"), "●");
  assert.equal(agentGlyph("done"), "●");
});

test("a card that needs you keeps its status mark's icon after the reason, as the TUI's", () => {
  const mark = { glyph: "◇", word: "◇ Ready for Testing", tone: "ansi4" };
  assert.equal(cardFlag("CI failing", mark), "! CI failing ◇");
  assert.equal(cardFlag("CI failing", null), "! CI failing");
  assert.equal(cardFlag("", mark), "◇ Ready for Testing");
  assert.equal(cardFlag("", null), "");
  assert.equal(EMPTY_COLUMN, "— nothing here");
});

test("a long status mark on a card falls back to its icon, as the TUI's narrow cards do", () => {
  const long = { glyph: "◇", word: "◇ Ready for Testing", tone: "ansi4" };
  assert.equal(cardFlag("", long, 12), "◇");
  assert.equal(cardFlag("", { glyph: "⊘", word: "⊘ Blocked", tone: "ansi3" }, 12), "⊘ Blocked");
});

test("a card's chips are its open pull requests, then how many finished", () => {
  const prs = [{ ref: "#887", mark: "✓", tone: "done" }];
  assert.deepEqual(cardChips(prs, ""), prs);
  assert.deepEqual(cardChips(prs, "1 merged · 2 closed"), [...prs, { text: "1 merged · 2 closed" }]);
  assert.deepEqual(cardChips([], ""), []);
});

test("chips pack into the pixel rows a card fits, so many of them never spill out of the card", () => {
  const narrow = ["#887", "#888", "#889", "#890"].map((ref) => ({ ref, mark: "✓" }));
  const five = [...narrow, { ref: "#891", mark: "✓" }];
  assert.deepEqual(chipRows(narrow, 180).map((row) => row.length), [3, 1], "a card at its narrowest width holds three");
  assert.deepEqual(chipRows(narrow, 211).map((row) => row.length), [3, 1], "the reported card, 4 pull requests: one row of three");
  assert.deepEqual(chipRows(five, 211).map((row) => row.length), [3, 2]);
  assert.deepEqual(chipRows(narrow, 240).map((row) => row.length), [4], "a wider card holds them all in one row");
  assert.deepEqual(chipRows(five, 300).map((row) => row.length), [5]);
});

test("how many finished pull requests fit beside the last chips decides its row", () => {
  const chips = cardChips([{ ref: "#890", mark: "✓" }, { ref: "#891", mark: "✓" }], "7 merged");
  assert.deepEqual(chipRows(chips, 211).map((row) => row.map((c) => c.ref ?? c.text)), [["#890", "#891", "7 merged"]]);
  assert.deepEqual(chipRows(chips, 120).map((row) => row.map((c) => c.ref ?? c.text)), [["#890", "#891"], ["7 merged"]]);
});

test("a chip wider than a card's row keeps a row of its own rather than being dropped", () => {
  const long = [{ ref: "#1234567890123456789", mark: "✓" }, { ref: "#1", mark: "✓" }];
  assert.deepEqual(chipRows(long, 180).map((row) => row.map((c) => c.ref)), [["#1234567890123456789"], ["#1"]]);
  assert.deepEqual(chipRows([], 180), []);
});
