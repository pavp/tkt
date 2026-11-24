import { test } from "node:test";
import assert from "node:assert/strict";
import { fixture } from "./fixtures.js";
import { prRows, toggled, reposChip } from "../mod/lib/prs.js";

test("the PRs list shows each group's title, then its pull requests, as /v1/prs lays them out", () => {
  const view = fixture("prs.json");
  const rows = prRows(view);
  assert.deepEqual(rows.filter((r) => r.kind === "title").map((r) => r.title), view.sections.map((s) => s.title));
  const first = rows[1];
  assert.equal(first.key, "#12");
  assert.equal(first.ticketKey, "DEMO-1");
  assert.equal(first.status, "CI failing");
  assert.equal(first.needsYou, true);
  const review = rows.find((r) => r.key === "#15");
  assert.equal(review.review, true);
  assert.equal(review.who, "grace");
});

test("a collapsed group shows only its title", () => {
  const view = fixture("prs.json");
  view.sections[0].collapsed = true;
  const rows = prRows(view);
  assert.equal(rows[0].collapsed, true);
  assert.equal(rows[1].kind, "title");
});

test("toggling a name adds it to a list or takes it out", () => {
  assert.deepEqual(toggled(["a"], "b"), ["a", "b"]);
  assert.deepEqual(toggled(["a", "b"], "a"), ["b"]);
  assert.deepEqual(toggled(undefined, "a"), ["a"]);
});

test("the Repos chip counts the repositories shown", () => {
  assert.equal(reposChip(fixture("prs.json").repos), "Repos");
  assert.equal(reposChip(fixture("prs-hidden-repo.json").repos), "Repos 2/3");
});

test("a pull request's preview says what the TUI's PRs preview says", async () => {
  const { prPreview } = await import("../mod/lib/prs.js");
  const rows = prRows(fixture("prs.json"));
  const mine = rows.find((r) => r.key === "#12");
  assert.deepEqual(prPreview(mine), { title: "#12 · DEMO-1 · Filter accounts", line: "CI failing in example-org/api · CI failing ✕ · enter for actions" });
  const review = rows.find((r) => r.key === "#15");
  assert.deepEqual(prPreview(review), { title: "#15 · Rate limit the API", line: "review requested by grace in example-org/api · CI not reported · enter for actions" });
  assert.equal(prPreview(undefined), null);
});

test("a stack shows as one row titled with its size, name and status, then its pull requests from the bottom", () => {
  const rows = prRows(fixture("prs-stack.json"));
  const at = rows.findIndex((r) => r.kind === "stack");
  const stack = rows[at];
  assert.deepEqual({ group: stack.group, title: stack.title, status: stack.status, count: stack.count, collapsed: stack.collapsed, tone: stack.tone },
    { group: "stack:example-org/web#39", title: "Stack of 3 · feat/DEMO-2-export · 2 here", status: "CI failing", count: 2, collapsed: false, tone: "ansi1" });
  assert.deepEqual(rows.slice(at + 1, at + 3).map((r) => [r.key, r.after, r.on, r.inStack]), [["#40", 0, "feat/DEMO-2-export-0-shape", true], ["#41", 40, "", true]]);
  assert.ok(!rows.some((r) => r.kind === "stack" && r.count < 2), "a pull request alone in its group gets no stack title");
  assert.equal(rows[at + 3].inStack, false);
});

test("a folded stack keeps its title and hides its pull requests", () => {
  const rows = prRows(fixture("prs-stack-folded.json"));
  const at = rows.findIndex((r) => r.kind === "stack");
  assert.equal(rows[at].collapsed, true);
  assert.equal(rows[at + 1].key, "#30");
});

test("a stacked pull request's menu copies its stack's URLs", async () => {
  const { prMenu } = await import("../mod/lib/menus.js");
  const view = fixture("prs-stack.json");
  const raw = view.sections.flatMap((s) => s.rows).find((r) => r.key === "#41");
  const item = prMenu(raw).items.find((i) => i.label === "Copy the stack's 3 URLs");
  assert.deepEqual(item && [item.action, item.value, item.what], ["copy", "https://github.com/example-org/web/pull/39\nhttps://github.com/example-org/web/pull/40\nhttps://github.com/example-org/web/pull/41", "the stack's 3 URLs"]);
  const shape = view.sections.flatMap((s) => s.rows).find((r) => r.key === "#39");
  assert.ok(prMenu(shape).items.some((i) => i.label === "Copy the stack's 3 URLs"), "a review request copies the whole stack too");
  const alone = view.sections.flatMap((s) => s.rows).find((r) => r.key === "#30");
  assert.ok(!prMenu(alone).items.some((i) => i.label.includes("stack")));
});

test("the arrows stop on a stack's title, which enter folds as a group's", async () => {
  const { targets } = await import("../mod/lib/nav.js");
  const ids = targets("prs", "list", fixture("prs-stack.json")).ids;
  const at = ids.indexOf("group:stack:example-org/web#39");
  assert.deepEqual(ids.slice(at, at + 3), ["group:stack:example-org/web#39", "#40mine:example-org/web", "#41mine:example-org/web"]);
});

test("a stack's title holds its URLs, and its preview says how to fold and copy it", async () => {
  const { prPreview } = await import("../mod/lib/prs.js");
  const stack = prRows(fixture("prs-stack.json")).find((r) => r.kind === "stack");
  assert.equal(stack.urls.length, 3);
  assert.deepEqual(prPreview(stack), { title: "Stack of 3 · feat/DEMO-2-export · 2 here", line: "CI failing · enter to fold · P copies the stack's 3 URLs" });
});

test("a stack's rows say where they sit in its block, and a lone part of a stack says which", () => {
  const rows = prRows(fixture("prs-stack.json"));
  const at = rows.findIndex((r) => r.kind === "stack");
  assert.deepEqual(rows.slice(at, at + 4).map((r) => r.block ?? ""), ["top", "mid", "bottom", ""]);
  const lone = rows.find((r) => r.key === "#39");
  assert.deepEqual([lone.block ?? "", lone.stackNote], ["", "1 of 3"]);
  const folded = prRows(fixture("prs-stack-folded.json")).find((r) => r.kind === "stack");
  assert.equal(folded.block, "only");
});
