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
