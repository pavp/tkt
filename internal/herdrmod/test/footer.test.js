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
