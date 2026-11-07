import { test } from "node:test";
import assert from "node:assert/strict";
import { fixture } from "./fixtures.js";
import { previewOf, legendFor, problemOf } from "../mod/lib/footer.js";

test("the preview of a selected ticket says what the TUI's preview says", () => {
  const board = fixture("board.json");
  const row = board.columns[1].rows[0];
  assert.deepEqual(previewOf(row), {
    title: "DEMO-1 In Progress · Filter accounts",
    line: "3 points · Ada Lovelace · CI failing · example-org/api#12 · f sends the failure to the agent",
    label: "",
  });
  assert.equal(previewOf(undefined), null);
});

test("the legend lists what clicks and keys do on the shown screen", () => {
  assert.ok(legendFor("sprint", "columns").some(([k]) => k === "double-click"));
  assert.ok(legendFor("backlog", "list").some(([k, what]) => k === "click" && what === "select"));
});

test("a tkt that is not set up says how to set it up", () => {
  assert.equal(problemOf("tkt is not configured yet: run `tkt init`", "wsl-dev"), "tkt isn't set up on wsl-dev: run `tkt init` in a terminal there");
  assert.equal(problemOf("tkt is not configured yet: run `tkt init`", null), "tkt isn't set up on this computer: run `tkt init` in a terminal");
  assert.equal(problemOf("the tkt daemon did not answer: timeout", null), "the tkt daemon did not answer: timeout");
});

test("the legend names z density on the Sprint's columns only, as the TUI's", () => {
  assert.ok(legendFor("sprint", "columns").some(([k, what]) => k === "z" && what === "density"));
  assert.ok(!legendFor("sprint", "list").some(([k]) => k === "z"));
});

test("the bottom bar names the five keys that matter on each tab, ? listing the rest", async () => {
  const { topKeys } = await import("../mod/lib/footer.js");
  assert.deepEqual(topKeys("sprint", "columns").map(([k]) => k), ["enter", "i", "f", "v", "?"]);
  assert.deepEqual(topKeys("prs", "list").map(([k]) => k), ["enter", "p", "f", "t", "?"]);
  assert.deepEqual(topKeys("backlog", "list").map(([k]) => k), ["enter", "i", "o", "v", "?"]);
  assert.equal(topKeys("sprint", "list").at(-1)[1], "all keys");
});

test("a notice shows for six seconds, an error until it is gone", async () => {
  const { noticeShown } = await import("../mod/lib/footer.js");
  assert.equal(noticeShown("✓ saved", 1000, 6999), true);
  assert.equal(noticeShown("✓ saved", 1000, 7001), false);
  assert.equal(noticeShown("", 1000, 1001), false);
});

test("a notice that reports a failure stays until another replaces it", async () => {
  const { noticeShown } = await import("../mod/lib/footer.js");
  assert.equal(noticeShown("could not: Jira answered 502", 1000, 60000), true);
  assert.equal(noticeShown("! cached · 10:30", 1000, 60000), true);
});
