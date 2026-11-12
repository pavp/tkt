import { test } from "node:test";
import assert from "node:assert/strict";
import { packCache, unpackCache, cachedLabel } from "../mod/lib/cache.js";

const ok = (body) => ({ status: 200, body });

test("the board's last copy is kept per machine, so the window opens on it as the TUI opens on its cache", () => {
  const raw = packCache("wsl-dev", { sprint: ok({ columns: [] }), prs: ok({ sections: [] }), backlog: ok({ groups: [] }) }, Date.UTC(2026, 10, 4, 10, 30));
  const back = unpackCache(raw, "wsl-dev");
  assert.deepEqual(back.answers, { sprint: ok({ columns: [] }), prs: ok({ sections: [] }) });
  assert.equal(back.at, Date.UTC(2026, 10, 4, 10, 30));
  assert.equal(unpackCache(raw, null), null);
  assert.equal(unpackCache("not json", "wsl-dev"), null);
  assert.equal(unpackCache(null, "wsl-dev"), null);
});

test("an answer that is not a 200 is not kept", () => {
  const raw = packCache(null, { sprint: { status: 503, body: {} }, prs: ok({ sections: [] }) }, 0);
  assert.deepEqual(unpackCache(raw, null).answers, { prs: ok({ sections: [] }) });
});

test("while the window shows its last copy, the sync label says so as the TUI's does", () => {
  const at = new Date(2026, 10, 4, 10, 30).getTime();
  assert.equal(cachedLabel(at), "! cached · 10:30");
});

test("an answer saying the daemon is still syncing never replaces a board already shown", async () => {
  const { keptAnswer } = await import("../mod/lib/cache.js");
  const good = { status: 200, body: { columns: [] } };
  const syncing = { status: 503, body: { message: "the first sync has not finished" } };
  assert.equal(keptAnswer(good, syncing), good, "the cached board stays on screen and in the next save");
  assert.equal(keptAnswer(undefined, syncing), syncing, "with nothing shown, the screen says it syncs");
  assert.equal(keptAnswer(good, { status: 500, body: {} }).status, 500, "a real failure still shows");
  const fresh = { status: 200, body: { columns: [1] } };
  assert.equal(keptAnswer(good, fresh), fresh);
});

test("while the daemon syncs, the board is asked again sooner than the usual poll", async () => {
  const { pollAfter } = await import("../mod/lib/cache.js");
  assert.equal(pollAfter({ status: 200, body: { syncing: true } }), 1500);
  assert.equal(pollAfter({ status: 200, body: { partial: true } }), 1500);
  assert.equal(pollAfter({ status: 503, body: {} }), 1500, "the first sync is still running");
  assert.equal(pollAfter({ status: 200, body: {} }), null);
  assert.equal(pollAfter(undefined), null);
});

test("the window opens on the view it kept, so a list never shows as columns while tkt answers", async () => {
  const { keptView } = await import("../mod/lib/cache.js");
  assert.equal(keptView("list"), "list");
  assert.equal(keptView("columns"), "columns");
  assert.equal(keptView(null), "columns");
  assert.equal(keptView("grid"), "columns");
});
