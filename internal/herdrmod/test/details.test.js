import { test } from "node:test";
import assert from "node:assert/strict";
import { detailView, neighbors } from "../mod/lib/details.js";

const detail = {
  ticket: { key: "DEMO-1", type: "Story", summary: "Filter accounts", status: "In Progress", points: 3, assignee: "Ada Lovelace", label: "Web", board: "Core",
    url: "https://example.atlassian.net/browse/DEMO-1",
    prs: [{ repo: "example-org/api", number: 12, title: "Filter accounts", url: "u12", state: "open", checks: "fail" }],
    subtasks: [{ key: "DEMO-4", summary: "Write the query", status: "To Do" }] },
  description: "Filter the **accounts**.",
  acceptance_criteria: "- by region",
  comments: [{ author: "Alan Turing", created: "2026-10-20T12:00:00Z", body: "Looks good" }],
  attention: ["CI failing"], agent: "working",
  work: [{ key: "DEMO-1", repo: "example-org/api", branch: "feature/DEMO-1-filter", path: "/src/api-DEMO-1" }],
};

test("the details say what the TUI's details say", () => {
  const v = detailView(detail, "Sprint");
  assert.equal(v.breadcrumb, "Sprint › DEMO-1");
  assert.equal(v.title, "Filter accounts");
  assert.deepEqual(v.fields, [["Status", "In Progress"], ["Type", "Story"], ["Points", "3"], ["Assignee", "Ada Lovelace"], ["Label", "Web"], ["Board", "Core"]]);
  assert.equal(v.attention, "CI failing");
  assert.deepEqual(v.subtasks, [{ key: "DEMO-4", status: "To Do", summary: "Write the query" }]);
  assert.deepEqual(v.prs, [{ ref: "example-org/api#12", title: "Filter accounts", url: "u12", mark: "✕", tone: "blocked" }]);
  assert.deepEqual(v.work, [{ where: "example-org/api · feature/DEMO-1-filter", path: "/src/api-DEMO-1" }]);
  assert.equal(v.comments[0].who, "Alan Turing");
});

test("the arrows go to the previous and next ticket of the list it was opened from", () => {
  assert.deepEqual(neighbors(["A-1", "A-2", "A-3"], "A-2"), { prev: "A-1", next: "A-3" });
  assert.deepEqual(neighbors(["A-1", "A-2"], "A-1"), { prev: null, next: "A-2" });
  assert.deepEqual(neighbors([], "A-1"), { prev: null, next: null });
});

test("the details say where the ticket sits in the list it was opened from, and its status's column", async () => {
  const { positionOf, detailView: view } = await import("../mod/lib/details.js");
  assert.equal(positionOf(["A-1", "A-2", "A-3"], "A-2"), "2 of 3");
  assert.equal(positionOf(["A-1"], "B-9"), "");
  assert.equal(view({ ticket: { ...detail.ticket, category: "in_progress" } }, "Sprint").category, "in_progress");
});
