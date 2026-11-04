import { test } from "node:test";
import assert from "node:assert/strict";
import { ticketMenu, prMenu, menuFooter, reviewersOf } from "../mod/lib/menus.js";
import { previewBadge, legendFor, syncTone } from "../mod/lib/footer.js";

test("menu items name the board key that runs them, as the TUI's menus do", () => {
  const t = { key: "DEMO-1", status: "In Progress", url: "https://example.atlassian.net/browse/DEMO-1", prs: [{ number: 12, url: "https://github.com/example-org/api/pull/12" }] };
  const keys = Object.fromEntries(ticketMenu(t).items.filter((i) => i.keyHint).map((i) => [i.label, i.keyHint]));
  assert.deepEqual(keys, { "Show details": "i", "Open in Jira ↗": "o", "Open #12 in GitHub ↗": "p" });
  const two = ticketMenu({ ...t, prs: [{ number: 1 }, { number: 2 }] }).items.find((i) => i.label === "Pull requests (2) ▸");
  assert.equal(two.keyHint, "p");
});

test("the menu's footer offers c to copy the link of an item that opens one", () => {
  assert.equal(menuFooter({ action: "open", url: "https://x" }), "enter run · c copy link · esc close");
  assert.equal(menuFooter({ action: "move" }), "enter run · esc close");
});

test("a pull request's menu names who approved it and who asked for changes, as the TUI's", () => {
  assert.deepEqual(reviewersOf({ approved_by: ["ana", "sam"], changes_requested_by: ["luis"] }), [
    { text: "✓ ana", tone: "ansi2" }, { text: "✓ sam", tone: "ansi2" }, { text: "✗ luis", tone: "ansi3" },
  ]);
  assert.deepEqual(reviewersOf({}), []);
  const m = prMenu({ key: "#5", pr: { number: 5, repo: "example-org/web", approved_by: ["ana"] }, review: true });
  assert.deepEqual(m.reviewers, [{ text: "✓ ana", tone: "ansi2" }]);
});

test("the preview's title shows the status as a badge on its column's color, with its mark's icon", () => {
  const row = { key: "DEMO-1", ticket: { status: "Ready for Testing", category: "review", mark: "diamond", summary: "Filter accounts" } };
  assert.deepEqual(previewBadge(row), { key: "DEMO-1", status: "◇ Ready for Testing", tone: "ansi5", summary: "Filter accounts" });
  assert.equal(previewBadge({ key: "X" }), null);
});

test("the legend lists the keys that work on the tab in view, as the TUI's", () => {
  const sprint = legendFor("sprint", "columns", { strip: true });
  assert.deepEqual(sprint[0], ["←↑↓→ wasd", "move · ↑ to reviews"]);
  for (const k of ["f", "i", "o", "p", "O P", "tab 1-3", "v", "t", "g", ","]) assert.ok(sprint.some(([key]) => key === k), k);
  assert.deepEqual(legendFor("sprint", "list", {})[0], ["↑↓ ws", "move"]);
  const backlog = legendFor("backlog", "list", { merged: true });
  assert.ok(backlog.some(([k, what]) => k === "v" && what === "split"));
  assert.ok(!backlog.some(([k]) => k === "f" || k === "t"));
  assert.ok(legendFor("prs", "list", { bots: 3 }).some(([k]) => k === "b"));
});

test("a sync label that warns ('! cached …') shows in the warning color, the others dimmed", () => {
  assert.equal(syncTone("! cached · 10:30"), "ansi3");
  assert.equal(syncTone("✓ synced 3 min ago"), "dim");
});
