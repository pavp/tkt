import { test } from "node:test";
import assert from "node:assert/strict";
import { fixture } from "./fixtures.js";
import { firstPicker, answer, taskPicker, startingView, startingKey, startingJob, startingNotice, startingToast, resumable } from "../mod/lib/start.js";

test("a ticket with worktrees asks which, as the TUI's Open picker", () => {
  const p = firstPicker("DEMO-7", fixture("start-worktrees.json"));
  assert.equal(p.title, "Open DEMO-7");
  assert.equal(p.question, "Which worktree?");
  assert.deepEqual(p.rows.map((r) => [r.label, r.hint]), [["feature/DEMO-7-a", "● idle"], ["feature/DEMO-7-b", "no agent"]]);
  assert.deepEqual(answer(p, ["/src/web-DEMO-7-a"], fixture("start-worktrees.json")), { post: { herdr: "desktop", worktree: "/src/web-DEMO-7-a" } });
  const next = answer(p, ["/src/web-DEMO-7-b"], fixture("start-worktrees.json"));
  assert.equal(next.picker.question, "Which agent?");
  assert.equal(next.picker.rows[next.picker.sel].label, "opencode");
  assert.deepEqual(answer(next.picker, ["claude"], fixture("start-worktrees.json")), { post: { herdr: "desktop", worktree: "/src/web-DEMO-7-b", agent: "claude" } });
});

test("a ticket without worktrees asks the repositories, then the agent", () => {
  const choices = fixture("start-repos.json");
  const p = firstPicker("DEMO-7", choices);
  assert.equal(p.title, "Start DEMO-7");
  assert.equal(p.multi, true);
  assert.deepEqual(p.rows.map((r) => [r.label, r.hint, r.checked]), [["example-org/web", "last used", true], ["example-org/api", "", false]]);
  const next = answer(p, ["example-org/web"], choices);
  assert.deepEqual(answer(next.picker, ["claude"], choices), { post: { herdr: "desktop", repos: ["example-org/web"], agent: "claude" } });
});

test("with no agent to start, the picker says why", () => {
  assert.equal(firstPicker("DEMO-7", fixture("start-no-agent.json")).error, "no agent Herdr desktop can start is installed; install one of: pi, claude");
});

test("feedback goes to an open agent, or to a new one of a kind", () => {
  const p = taskPicker(fixture("task-agents.json"));
  assert.equal(p.title, "Send DEMO-1's feedback to");
  assert.deepEqual(p.rows.map((r) => r.label), ["claude · api", "claude-3 · web", "A new agent ▸"]);
  assert.deepEqual(p.rows[0].task, { to: "open", pane: "t1" });
  assert.equal(p.rows[2].kinds.length, 2);
});

test("a Start with nothing to choose but the agent asks for it, or starts at once, as the TUI's Start does", async () => {
  const { firstPicker } = await import("../mod/lib/start.js");
  const asks = firstPicker("DEMO-1", { opens: true, worktrees: [], repos: [], agents: ["claude", "pi"], last_agent: "pi" });
  assert.equal(asks.step, "agent");
  assert.equal(asks.question, "Which agent?");
  assert.deepEqual(asks.rows.map((r) => r.value), ["claude", "pi"]);
  assert.equal(asks.sel, 1);
  assert.deepEqual(firstPicker("DEMO-1", { opens: true, worktrees: [], repos: [], agents: ["claude"] }), { post: { herdr: "desktop", agent: "claude" } });
  assert.deepEqual(firstPicker("DEMO-1", { opens: false, worktrees: [], repos: [], agents: ["claude", "pi"] }), { post: { herdr: "desktop" } });
});

test("a running Start reads as a spinner, its step and the time it has taken, in the modal and the footer", () => {
  const s = { key: "DEMO-7", since: 1000 };
  const v = startingView(s, 1000 + 72_000);
  assert.equal(v.title, "Starting DEMO-7");
  assert.equal(v.line, "Preparing its worktree…");
  assert.equal(v.elapsed, "1:12");
  assert.equal(v.footer, "Starting DEMO-7 · preparing its worktree… 1:12");
  assert.equal(v.tone, "accent");
  assert.equal(v.spinning, true);
  assert.deepEqual(v.keys, [["esc", "keep working · it runs on"]]);
});

test("a failed Start keeps its modal open with tkt's message until it is closed", () => {
  const v = startingView({ key: "DEMO-7", since: 0, failed: "could not create the worktree of DEMO-7 in /src/web: boom" }, 5000);
  assert.equal(v.title, "DEMO-7's Start failed");
  assert.equal(v.line, "could not create the worktree of DEMO-7 in /src/web: boom");
  assert.deepEqual(v.keys, [["enter", "close"]]);
});

test("the Start modal takes every key: esc hides a running one, enter or esc closes a failed one", () => {
  const running = { key: "DEMO-7", since: 0 };
  assert.equal(startingKey(running, "escape"), "hide");
  for (const k of ["enter", "down", "s", "tab"]) assert.equal(startingKey(running, k), null);
  const failed = { ...running, failed: "boom" };
  assert.equal(startingKey(failed, "escape"), "close");
  assert.equal(startingKey(failed, "enter"), "close");
  assert.equal(startingKey(failed, "down"), null);
});

test("a Start's job ends running, failed with its message, or done", () => {
  assert.deepEqual(startingJob({ status: 200, body: { state: "running" } }), { state: "running" });
  assert.deepEqual(startingJob({ status: 200, body: { state: "failed", message: "boom" } }), { state: "failed", message: "boom" });
  assert.deepEqual(startingJob({ status: 200, body: { state: "done" } }), { state: "done" });
  assert.deepEqual(startingJob({ status: 404, body: { message: "no such job" } }), { state: "gone" });
  assert.deepEqual(startingJob({ status: 500, body: { message: "boom" } }), { state: "failed", message: "boom" });
  assert.deepEqual(startingJob({ status: 0 }), { state: "running" });
});

test("how a hidden Start ended stays in the footer in color: green when its agent opened, red when it failed", () => {
  assert.deepEqual(startingNotice("DEMO-7", { state: "done" }), { text: "started DEMO-7: its agent is in Herdr", tone: "ok" });
  assert.deepEqual(startingNotice("DEMO-7", { state: "failed", message: "boom" }), { text: "DEMO-7's Start failed: boom", tone: "error" });
});

test("a hidden Start that ends posts a Herdr notification: green when its agent opened, red with tkt's message when it failed", () => {
  assert.deepEqual(startingToast("DEMO-7", { state: "done" }), { title: "Started DEMO-7", body: "Its agent is in Herdr.", level: "success" });
  assert.deepEqual(startingToast("DEMO-7", { state: "failed", message: "boom" }), { title: "DEMO-7's Start failed", body: "boom", level: "error" });
});

test("a Start saved before the mod closed is followed again on the same machine while tkt still keeps its job", () => {
  const pending = { key: "DEMO-7", id: "j1", since: 1000, machine: "wsl-dev" };
  assert.equal(resumable(pending, "wsl-dev", 1000 + 60_000), true);
  assert.equal(resumable(pending, "this computer", 1000 + 60_000), false);
  assert.equal(resumable(pending, "wsl-dev", 1000 + 16 * 60_000), false);
  assert.equal(resumable(null, "wsl-dev", 1000), false);
});
