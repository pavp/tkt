import { test } from "node:test";
import assert from "node:assert/strict";
import { ticketMenu, prsSubmenu, withBack, backOf, confirmRemove } from "../mod/lib/menus.js";
import { prsToggle } from "../mod/lib/prs.js";
import { warningsLine } from "../mod/lib/footer.js";

const lines = [
  { number: 9, label: "#9 CI failing · api", url: "https://github.com/example-org/api/pull/9" },
  { number: 3, label: "#3 merged · web", url: "https://github.com/example-org/web/pull/3" },
];

test("a ticket's Pull requests (N) lists each pull request once: enter opens it, c copies its link", () => {
  const t = { key: "DEMO-1", status: "In Progress", prs: [{ number: 9 }, { number: 3 }] };
  const item = ticketMenu(t, { prLines: lines }).items.find((i) => i.label === "Pull requests (2) ▸");
  assert.equal(item.action, "submenu");
  assert.deepEqual(item.menu, prsSubmenu("DEMO-1", lines));
  assert.equal(item.menu.title, "Pull requests of DEMO-1");
  assert.deepEqual(item.menu.items, [
    { label: "#9 CI failing · api", action: "open", url: "https://github.com/example-org/api/pull/9" },
    { label: "#3 merged · web", action: "open", url: "https://github.com/example-org/web/pull/3" },
  ]);
});

test("a submenu remembers the menu it came from, so esc goes back to it and a refreshed menu keeps that way back", () => {
  const top = { title: "Actions for DEMO-1", items: [] };
  const move = withBack({ title: "Move DEMO-1 to…", items: [] }, top);
  assert.equal(backOf(move), top);
  const refreshed = withBack({ title: "Move DEMO-1 to…", items: [{ label: "x" }] }, move);
  assert.equal(backOf(refreshed), top);
  assert.equal(backOf(top), null);
  assert.equal(withBack(null, top), null);
  assert.equal(backOf(withBack(top, null)), null);
});

test("removing something in Settings asks first, in the TUI's words", () => {
  const change = { repo_roots: [] };
  assert.deepEqual(confirmRemove("Remove example-org/web from DEMO?", change), {
    title: "Remove example-org/web from DEMO?",
    items: [{ label: "Yes, remove it", action: "settingsPatch", change }, { label: "Cancel", action: "cancel" }],
  });
});

test("the PRs tab's toggle is Requested, lit while it narrows the list to yours and your review requests", () => {
  assert.deepEqual(prsToggle(false), { label: "Requested", on: true });
  assert.deepEqual(prsToggle(true), { label: "Requested", on: false });
});

test("the sync's warnings show as one line, as the TUI's header does", () => {
  assert.equal(warningsLine(["team board X: filter unreadable", "GitHub slow"]), "! team board X: filter unreadable · GitHub slow");
  assert.equal(warningsLine([]), "");
  assert.equal(warningsLine(undefined), "");
});
