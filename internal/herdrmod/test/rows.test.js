import { test } from "node:test";
import assert from "node:assert/strict";
import { gutterOf, statusTone, prStatusTone, agentWord, prRefs, listColumns, pointsText } from "../mod/lib/rows.js";

test("a row's gutter says it is selected, needs you, or carries a status mark, as the TUI's list does", () => {
  const mark = { glyph: "◇", tone: "ansi4" };
  assert.deepEqual(gutterOf({ selected: true, attention: "CI failing", mark }), { text: "▸", tone: "accent" });
  assert.deepEqual(gutterOf({ attention: "CI failing", mark }), { text: "!◇", tone: "ansi3" });
  assert.deepEqual(gutterOf({ attention: "CI failing" }), { text: "!", tone: "ansi3" });
  assert.deepEqual(gutterOf({ mark }), { text: "◇", tone: "ansi4" });
  assert.deepEqual(gutterOf({ needsYou: true }), { text: "!", tone: "ansi3" });
  assert.deepEqual(gutterOf({}), { text: "", tone: "dim" });
});

test("a ticket's status takes its column's color, a pull request's where it stands, as the TUI's", () => {
  assert.equal(statusTone("todo"), "dim");
  assert.equal(statusTone("in_progress"), "ansi4");
  assert.equal(statusTone("review"), "ansi5");
  assert.equal(statusTone("done"), "ansi2");
  assert.equal(prStatusTone({ review: true }), "ansi5");
  assert.equal(prStatusTone({ needs_you: true, pr: {} }), "ansi3");
  assert.equal(prStatusTone({ pr: { draft: true } }), "dim");
  assert.equal(prStatusTone({ pr: { review: "approved" } }), "ansi2");
  assert.equal(prStatusTone({ pr: { merge: "ready" } }), "ansi2");
  assert.equal(prStatusTone({ pr: { review: "review_required" } }), "ansi5");
  assert.equal(prStatusTone({ pr: { checks: "pending" } }), "accent");
  assert.equal(prStatusTone({ pr: {} }), "foreground");
});

test("the AGENT column names the agent's state in words, as the TUI's", () => {
  assert.equal(agentWord("working"), "● working");
  assert.equal(agentWord("blocked"), "● needs you");
  assert.equal(agentWord("done"), "● done");
  assert.equal(agentWord("idle"), "○ idle");
  assert.equal(agentWord(""), "");
});

test("the PR / CI cell lists every open pull request, then the finished ones, as the TUI's", () => {
  assert.equal(prRefs([]), "—");
  assert.equal(prRefs([{ number: 1, state: "open", checks: "pass" }, { number: 2, state: "open", checks: "fail" }]), "#1 ✓ #2 ✕");
  assert.equal(prRefs([{ number: 1, state: "open", checks: "pending" }, { number: 3, state: "merged" }]), "#1 … #3 merged");
  assert.equal(prRefs([{ number: 3, state: "merged" }, { number: 4, state: "merged" }, { number: 5, state: "closed" }]), "2 merged · 1 closed");
});

test("SP and AGENT show only when some row has them, ASSIGNEE whenever tickets show (as the TUI's), and points read as numbers", () => {
  const rows = [{ ticket: { points: 3 } }, { ticket: { team: true, assignee: "Alan Turing" } }];
  assert.deepEqual(listColumns(rows, { "DEMO-1": "working" }), { points: true, assignee: true, agent: true });
  assert.deepEqual(listColumns([{ ticket: {} }], {}), { points: false, assignee: true, agent: false });
  assert.deepEqual(listColumns([], {}), { points: false, assignee: false, agent: false });
  assert.equal(pointsText(3), "3");
  assert.equal(pointsText(0.5), "0.5");
  assert.equal(pointsText(0), "");
});
