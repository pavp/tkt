import { test } from "node:test";
import assert from "node:assert/strict";
import { stripChips, atTop } from "../mod/lib/strip.js";

const reviews = Array.from({ length: 8 }, (_, i) => ({ label: `#${i + 1}`, row: { key: `#${i + 1}` } }));

test("the strip shows the review requests that fit, then '+N more · press 2', as the TUI's strip does", () => {
  assert.deepEqual(stripChips(reviews, 6).chips.map((c) => c.label), ["#1", "#2", "#3", "#4", "#5", "#6"]);
  assert.equal(stripChips(reviews, 6).more, "+2 more · press 2");
  assert.equal(stripChips(reviews.slice(0, 3), 6).more, "");
  assert.deepEqual(stripChips(undefined, 6), { chips: [], more: "" });
});

test("up from the first ticket of a list or the top card of a column reaches the strip", () => {
  assert.equal(atTop({ ids: ["A", "B"] }, "A"), true);
  assert.equal(atTop({ ids: ["A", "B"] }, "B"), false);
  assert.equal(atTop({ ids: ["A", "B"] }, null), true);
  assert.equal(atTop({ cols: [["A", "B"], ["C"]] }, "C"), true);
  assert.equal(atTop({ cols: [["A", "B"], ["C"]] }, "B"), false);
});
