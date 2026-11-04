import { test } from "node:test";
import assert from "node:assert/strict";
import { typeIcon, agentGlyph, cardFlag, EMPTY_COLUMN } from "../mod/lib/columns.js";

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
