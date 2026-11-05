import { test } from "node:test";
import assert from "node:assert/strict";
import { modKeyFor, withModKey } from "../mod/lib/modkey.js";

test("the key that opens tkt is cmd-j on macOS and ctrl-shift-j where cmd is the Windows or Super key", () => {
  assert.equal(modKeyFor("darwin"), "cmd-j");
  assert.equal(modKeyFor("win32"), "ctrl-shift-j");
  assert.equal(modKeyFor("linux"), "ctrl-shift-j");
});

test("keys.json gains the key only when nothing opens tkt yet and the key is free, keeping every other binding", () => {
  const added = withModKey('{"global":{"ctrl-shift-t":"tab.new"}}', "ctrl-shift-j");
  assert.equal(added.status, "added");
  assert.deepEqual(JSON.parse(added.text), { global: { "ctrl-shift-t": "tab.new", "ctrl-shift-j": "mod.open:tkt" } });
  assert.equal(withModKey(null, "ctrl-shift-j").status, "added");
  assert.deepEqual(JSON.parse(withModKey(null, "ctrl-shift-j").text), { global: { "ctrl-shift-j": "mod.open:tkt" } });
  assert.equal(withModKey('{"global":{"cmd-j":"mod.open:tkt"}}', "ctrl-shift-j").status, "present");
  assert.deepEqual(withModKey('{"global":{"ctrl-shift-j":"space.next"}}', "ctrl-shift-j"), { status: "taken", by: "space.next" });
  assert.equal(withModKey("{ not json", "ctrl-shift-j").status, "unreadable");
});
