import { test } from "node:test";
import assert from "node:assert/strict";
import { tabCount, contextText, scopeNotice } from "../mod/lib/header.js";

const board = {
  sprints: ["DEMO · Sprint 12 · 3d left", "OPS · Ops 3 · overdue"],
  boards: [{ name: "Web", tickets: 2, hidden: false }, { name: "Core", tickets: 1, hidden: true }],
  sections: [{ title: "My tickets", rows: [{ key: "A" }, { key: "A-1", parent: "A" }, { key: "B" }] }, { title: "Team · Web", rows: [{ key: "C" }] }],
};
const prs = {
  sections: [
    { group: "mine:web", count: 2, rows: [{ key: "#1", needs_you: true }, { key: "#2" }] },
    { group: "review:web", count: 1, rows: [{ key: "#3", review: true }] },
  ],
  repos: [{ name: "example-org/web" }, { name: "example-org/api" }],
  hidden_repos: ["example-org/api"],
};
const backlog = {
  boards: [{ name: "Web", hidden: false }, { name: "Core", hidden: false }],
  groups: [{ group: "sprint:11", count: 4 }, { group: "sprint:21", count: 2 }, { group: "backlog:", count: 9 }],
};

test("each tab counts what it shows, as the TUI's tabs do; the Backlog says – until it loads", () => {
  assert.equal(tabCount("sprint", board), "3");
  assert.equal(tabCount("prs", prs), "3");
  assert.equal(tabCount("backlog", backlog), "15");
  assert.equal(tabCount("backlog", undefined), "–");
  assert.equal(tabCount("sprint", undefined), "");
});

test("the context line sums the tab up in the TUI's words", () => {
  assert.equal(contextText("sprint", board), "DEMO · Sprint 12 · 3d left   OPS · Ops 3 · overdue");
  assert.equal(contextText("prs", prs), "2 open · 1 needs you · 1 review request");
  assert.equal(contextText("prs", { sections: [{ group: "mine:web", count: 1, rows: [{ key: "#1" }] }] }), "1 open · 0 review requests");
  assert.equal(contextText("backlog", backlog), "2 boards · 2 sprints · 9 in backlog");
  assert.equal(contextText("backlog", undefined, { loading: true }), "loading backlog…");
});

test("the filter's notice says how much it hides, and which key changes it", () => {
  assert.deepEqual(scopeNotice("sprint", board), { text: "Showing 1 of 2 boards · ", key: "B", after: " change" });
  assert.deepEqual(scopeNotice("prs", prs), { text: "Showing 1 of 2 repositories · ", key: "R", after: " change" });
  assert.equal(scopeNotice("backlog", backlog), null);
});

test("the Sprint and PRs tabs load together, as both counts show on the header; the Backlog only when shown", async () => {
  const { routesFor } = await import("../mod/lib/header.js");
  const tabs = [{ id: "sprint", route: "/v1/board?herdr=desktop" }, { id: "prs", route: "/v1/prs" }, { id: "backlog", route: "/v1/backlog" }];
  assert.deepEqual(routesFor(tabs, "sprint", {}), [["sprint", "/v1/board?herdr=desktop"], ["prs", "/v1/prs"]]);
  assert.deepEqual(routesFor(tabs, "prs", { bots: true }), [["prs", "/v1/prs?bots=true"], ["sprint", "/v1/board?herdr=desktop"]]);
  assert.deepEqual(routesFor(tabs, "backlog", { refreshBacklog: true }), [["backlog", "/v1/backlog?refresh=true"], ["sprint", "/v1/board?herdr=desktop"], ["prs", "/v1/prs"]]);
});

test("the view switch is one button whose icon names the view in use, as the TUI's", async () => {
  const { viewButton } = await import("../mod/lib/header.js");
  assert.deepEqual(viewButton("columns"), { label: "◫ View", next: "list" });
  assert.deepEqual(viewButton("list"), { label: "≣ View", next: "columns" });
});
