import { test } from "node:test";
import assert from "node:assert/strict";
import { machineKey, stashed, restored } from "../mod/lib/machines.js";
import { packCache, unpackCache } from "../mod/lib/cache.js";

test("each machine keeps its own tab, selection and board: switching away and back finds them as they were", () => {
  const local = { tab: "prs", selected: "#12mine:web", inStrip: true, stripSel: 2, answers: { prs: { status: 200, body: {} } }, cachedAt: null };
  const states = { [machineKey(null)]: stashed(local) };
  assert.deepEqual(restored(states, null, null), { tab: "prs", selected: "#12mine:web", inStrip: false, stripSel: 0, answers: { prs: { status: 200, body: {} } }, cachedAt: null });
  assert.deepEqual(restored(states, "wsl-dev", null), { tab: "sprint", selected: null, inStrip: false, stripSel: 0, answers: {}, cachedAt: null });
  assert.equal(machineKey(null), "local");
  assert.equal(machineKey("wsl-dev"), "wsl-dev");
});

test("a machine seen in an earlier window starts from its kept copy and its tab", () => {
  const cached = unpackCache(packCache("wsl-dev", { sprint: { status: 200, body: { columns: [] } } }, 5, "backlog"), "wsl-dev");
  assert.equal(cached.tab, "backlog");
  assert.deepEqual(restored({}, "wsl-dev", cached), { tab: "backlog", selected: null, inStrip: false, stripSel: 0, answers: { sprint: { status: 200, body: { columns: [] } } }, cachedAt: 5 });
});
