import { test } from "node:test";
import assert from "node:assert/strict";
import { fixture } from "./fixtures.js";
import { moveMenu, formMenu, assignMenu, confirmAssign, missingField } from "../mod/lib/move.js";

const labels = (m) => m.items.map((i) => i.label);

test("Move lists Jira's transitions as the TUI's Move to… menu", () => {
  const m = moveMenu("DEMO-123", fixture("transitions.json"));
  assert.equal(m.title, "Move DEMO-123 to");
  assert.deepEqual(labels(m), ["In Review", "Done · in Jira ↗", "Blocked"]);
  assert.equal(m.items[0].action, "submenu");
  assert.deepEqual(labels(m.items[0].menu), ["Yes, move it", "Cancel"]);
  assert.equal(m.items[0].menu.title, "Move DEMO-123 to In Review?");
  assert.equal(m.items[1].action, "open");
  assert.equal(m.items[2].action, "form");
});

test("a move with fields is a form: each field, a comment, then the move", () => {
  const move = fixture("transitions.json").moves[2];
  const f = formMenu("DEMO-123", move, { values: {}, comment: "" });
  assert.equal(f.title, "Move DEMO-123 to Blocked");
  assert.deepEqual(labels(f), ["Reason: —", "Owner: —", "Comment: —", "Move DEMO-123 to Blocked now", "Cancel"]);
  assert.equal(missingField(move, {}), "choose Reason first");
  assert.equal(missingField(move, { customfield_10300: { id: "9001", value: "Waiting on a decision" } }), "");
  const chosen = formMenu("DEMO-123", move, { values: { customfield_10300: { id: "9001", value: "Waiting on a decision" } }, comment: "soon" });
  assert.deepEqual(labels(chosen).slice(0, 3), ["Reason: Waiting on a decision", "Owner: —", "Comment: soon"]);
  assert.deepEqual(labels(f.items[0].menu), ["— none", "Waiting on a decision"]);
});

test("Assign offers you, unassigning and a search, then asks", () => {
  assert.deepEqual(labels(assignMenu("DEMO-1", "")), ["Me", "Search someone…"]);
  const m = assignMenu("DEMO-1", "Ada Lovelace");
  assert.equal(m.title, "Assign DEMO-1 · now Ada Lovelace");
  assert.deepEqual(labels(m), ["Me", "Unassign", "Search someone…"]);
  assert.equal(confirmAssign("DEMO-1", { to: "me" }).title, "Assign DEMO-1 to you?");
  assert.equal(confirmAssign("DEMO-1", { to: "nobody" }).title, "Leave DEMO-1 unassigned?");
  const u = confirmAssign("DEMO-1", { to: "user", account_id: "acc-alan", name: "Alan Turing" });
  assert.equal(u.title, "Assign DEMO-1 to Alan Turing?");
  assert.deepEqual(labels(u), ["Yes, assign it", "Cancel"]);
});
