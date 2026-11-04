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
