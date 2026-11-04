import { test } from "node:test";
import assert from "node:assert/strict";
import { keyOf, boardAction } from "../mod/lib/nav.js";

const ev = (keystroke, key_char, mods = {}) => ({ keystroke, key_char, modifiers: { shift: false, control: false, alt: false, platform: false, ...mods } });

test("a key reads as the character it types, so B, O, P, R and , match the TUI's bindings", () => {
  assert.equal(keyOf(ev("shift-b", "B")), "B");
  assert.equal(keyOf(ev(",", ",")), ",");
  assert.equal(keyOf(ev("space", " ")), " ");
  assert.equal(keyOf(ev("down", undefined)), "down");
  assert.equal(keyOf(ev("shift-tab", undefined)), "shift-tab");
  assert.equal(keyOf(ev("tab", "\t")), "tab");
  assert.equal(keyOf(ev("cmd-j", "j", { platform: true })), "cmd-j");
});

test("each board key does what the TUI's does on that tab", () => {
  const on = (k, tab) => boardAction(k, tab);
  assert.equal(on("enter", "sprint"), "menu");
  assert.equal(on(" ", "backlog"), "menu");
  assert.equal(on("i", "prs"), "details");
  assert.equal(on("o", "sprint"), "openJira");
  assert.equal(on("O", "sprint"), "copyJira");
  assert.equal(on("p", "sprint"), "openPR");
  assert.equal(on("P", "prs"), "copyPR");
  assert.equal(on("g", "backlog"), "refresh");
  assert.equal(on(",", "sprint"), "settings");
  assert.equal(on("t", "sprint"), "team");
  assert.equal(on("t", "prs"), "team");
  assert.equal(on("t", "backlog"), null);
  assert.equal(on("f", "sprint"), "feedback");
  assert.equal(on("f", "backlog"), null);
  assert.equal(on("B", "sprint"), "boards");
  assert.equal(on("B", "prs"), null);
  assert.equal(on("R", "prs"), "repos");
  assert.equal(on("R", "sprint"), null);
  assert.equal(on("b", "prs"), "bots");
  assert.equal(on("q", "sprint"), "close");
  assert.equal(on("escape", "sprint"), "close");
  assert.equal(on("x", "sprint"), null);
});

test("in a picker, up and down move, space ticks a repository, enter goes on as the TUI's pickers do", async () => {
  const { pickerKey } = await import("../mod/lib/start.js");
  const multi = { multi: true, sel: 1, rows: [{ value: "a" }, { value: "b" }, { value: "c" }], checked: new Set(["a"]) };
  assert.deepEqual(pickerKey(multi, "down"), { kind: "move", sel: 2 });
  assert.deepEqual(pickerKey({ ...multi, sel: 2 }, "down"), { kind: "move", sel: 2 });
  assert.deepEqual(pickerKey(multi, "up"), { kind: "move", sel: 0 });
  assert.deepEqual(pickerKey(multi, " "), { kind: "toggle", value: "b" });
  assert.deepEqual(pickerKey(multi, "enter"), { kind: "next" });
  assert.equal(pickerKey({ ...multi, checked: new Set() }, "enter"), null);
  const single = { multi: false, sel: 0, rows: [{ value: "claude" }, { value: "codex" }] };
  assert.deepEqual(pickerKey(single, "enter"), { kind: "choose", index: 0 });
  assert.equal(pickerKey(single, " "), null);
});

test("on the details screen esc, i, left and h go back, and c copies the ticket, as the TUI's details do", async () => {
  const { detailsKey } = await import("../mod/lib/details.js");
  for (const k of ["escape", "i", "left", "h"]) assert.equal(detailsKey(k), "back", k);
  assert.equal(detailsKey("c"), "copy");
  assert.equal(detailsKey("x"), null);
});
