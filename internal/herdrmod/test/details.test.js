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
  assert.deepEqual(v.prs, [{ ref: "example-org/api#12", title: "Filter accounts", url: "u12", mark: "✕", tone: "blocked", depth: 0, number: 12, status: "", after: 0, updated: "" }]);
  assert.deepEqual(v.stackUrls, []);
  assert.deepEqual(v.work, [{ where: "example-org/api · feature/DEMO-1-filter", path: "/src/api-DEMO-1" }]);
  assert.equal(v.comments[0].who, "Alan Turing");
});

test("the pull requests follow the daemon's stacks, each stack's URLs from its bottom to its top", () => {
  const pr = (number, url, extra = {}) => ({ repo: "example-org/web", number, title: `PR ${number}`, url, state: "open", checks: "pass", ...extra });
  const v = detailView({ ...detail, pr_stacks: [
    { name: "refactor/module", status: "CI failing", prs: [
      { ...pr(1, "u1"), depth: 0, status: "Ready to merge", updated_at: "2026-11-23T10:00:00Z" },
      { ...pr(2, "u2"), depth: 1, after: 1, status: "Ready to merge" },
      { ...pr(3, "u3", { checks: "fail" }), depth: 2, after: 2, status: "CI failing" }] },
    { name: "fix/x", status: "Approved", prs: [{ ...pr(9, "u9"), depth: 0, status: "Approved" }] }] }, "Sprint");
  assert.deepEqual(v.prs.map((p) => [p.ref, p.depth]), [["example-org/web#1", 0], ["example-org/web#2", 1], ["example-org/web#3", 2], ["example-org/web#9", 0]]);
  assert.deepEqual(v.stackUrls, ["u1\nu2\nu3"], "a pull request alone is no stack");
  assert.deepEqual(v.stacks.map((s) => [s.name, s.status, s.tone, s.prs.length]), [["refactor/module", "CI failing", "ansi1", 3], ["fix/x", "Approved", "ansi2", 1]]);
  assert.deepEqual(v.stacks[0].prs.map((p) => [p.number, p.status, p.after, p.updated]), [[1, "Ready to merge", 0, "2026-11-23T10:00:00Z"], [2, "Ready to merge", 1, ""], [3, "CI failing", 2, ""]]);
});

test("without the daemon's stacks, each pull request is a stack of its own", () => {
  const v = detailView(detail, "Sprint");
  assert.deepEqual(v.stacks.map((s) => s.prs.map((p) => p.number)), [[12]]);
});

test("a pull request's age reads in minutes, hours, days, then weeks", async () => {
  const { age } = await import("../mod/lib/details.js");
  const now = Date.parse("2026-11-24T12:00:00Z");
  assert.equal(age("2026-11-24T11:58:00Z", now), "2m");
  assert.equal(age("2026-11-23T22:00:00Z", now), "14h");
  assert.equal(age("2026-11-20T12:00:00Z", now), "4d");
  assert.equal(age("2026-10-20T12:00:00Z", now), "5w");
  assert.equal(age("", now), "");
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

test("→ moves into the details' actions, as Tab does", async () => {
  const { detailsKey } = await import("../mod/lib/details.js");
  assert.equal(detailsKey("right"), "actions");
  assert.equal(detailsKey("l"), "actions");
  assert.equal(detailsKey("tab"), "actions");
  assert.equal(detailsKey("left"), "back");
});


test("the header sums a ticket's pull requests up by their checks, else by how many merged", async () => {
  const { prSummary } = await import("../mod/lib/details.js");
  assert.equal(prSummary([]), "");
  assert.equal(prSummary([{ mark: "✓" }, { mark: "✓" }]), "2 PRs · ✓ checks passing");
  assert.equal(prSummary([{ mark: "✓" }, { mark: "✕" }]), "2 PRs · ✕ checks failing", "a failure wins");
  assert.equal(prSummary([{ mark: "…" }]), "1 PR · … checks running");
  assert.equal(prSummary([{ mark: "merged" }, { mark: "merged" }, { mark: "closed" }]), "3 PRs · 2 merged");
});

test("comments from bots and automations are told apart by their author", async () => {
  const { isBot } = await import("../mod/lib/details.js");
  for (const who of ["Automation for Jira", "ScriptRunner for Jira", "dependabot[bot]", "Release Bot"]) assert.equal(isBot(who), true, who);
  for (const who of ["Ada Lovelace", "Robert Botha", ""]) assert.equal(isBot(who), false, who);
});

test("the details' pull requests show three entries, a stack as its title then its rows unless folded", async () => {
  const { prEntries } = await import("../mod/lib/details.js");
  const pr = (n) => ({ ref: `o/web#${n}`, url: `u${n}`, number: n });
  const stacks = [{ name: "feat/a", prs: [pr(1), pr(2)] }, { prs: [pr(5)] }, { prs: [pr(6)] }, { prs: [pr(7)] }];
  const open = prEntries(stacks, {});
  assert.deepEqual(open.entries.map((e) => [e.kind, e.pr?.number ?? e.key, e.h]),
    [["stack", "o/web#1", 30], ["row", 1, 46], ["row", 2, 46], ["pr", 5, 32], ["pr", 6, 32]]);
  assert.deepEqual(open.more.map((p) => p.number), [7]);
  const folded = prEntries(stacks, { "o/web#1": true });
  assert.deepEqual(folded.entries.map((e) => [e.kind, e.folded ?? null]), [["stack", true], ["pr", null], ["pr", null]]);
});
