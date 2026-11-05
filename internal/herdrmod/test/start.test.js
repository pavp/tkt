import { test } from "node:test";
import assert from "node:assert/strict";
import { fixture } from "./fixtures.js";
import { firstPicker, answer, taskPicker } from "../mod/lib/start.js";

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
