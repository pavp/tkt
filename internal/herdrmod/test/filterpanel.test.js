import { test } from "node:test";
import assert from "node:assert/strict";
import { filterDraft, filterRows, filterKey } from "../mod/lib/filterpanel.js";

const items = [{ name: "example-org/web", prs: 3 }, { name: "example-org/api", prs: 1 }, { name: "other/website", prs: 2 }];

test("typing narrows the Repos filter to the names that match, starts first", () => {
  let d = filterDraft(["example-org/api"]);
  for (const k of ["w", "e", "b"]) d = filterKey(d, items, k).draft;
  assert.equal(d.query, "web");
  assert.deepEqual(filterRows(items, d).map((r) => [r.name, r.hidden]), [["example-org/web", false], ["other/website", false]]);
  d = filterKey(d, items, "backspace").draft;
  assert.equal(d.query, "we");
});

test("space ticks in the draft only; enter applies it, esc throws it away", () => {
  let d = filterDraft([]);
  d = filterKey(d, items, "down").draft;
  d = filterKey(d, items, " ").draft;
  assert.deepEqual(d.hidden, ["example-org/api"]);
  assert.deepEqual(filterKey(d, items, "enter"), { draft: d, done: "apply" });
  assert.equal(filterKey(d, items, "escape").done, "cancel");
});

test("esc first clears the typed text, and ctrl-a ticks all or none of the rows shown", () => {
  let d = { ...filterDraft(["other/website"]), query: "web" };
  const cleared = filterKey(d, items, "escape");
  assert.equal(cleared.done, null);
  assert.equal(cleared.draft.query, "");
  d = filterKey(d, items, "ctrl-a").draft;
  assert.deepEqual(d.hidden, [], "one shown row is hidden, so ctrl-a shows every shown row");
  d = filterKey(d, items, "ctrl-a").draft;
  assert.deepEqual(d.hidden.sort(), ["example-org/web", "other/website"], "all shown, so ctrl-a hides the shown rows and leaves the rest");
});

test("the selection stays on the rows shown", () => {
  let d = filterDraft([]);
  d = filterKey(filterKey(d, items, "down").draft, items, "down").draft;
  assert.equal(d.sel, 2);
  d = filterKey(d, items, "a").draft;
  assert.equal(d.sel, 0, "typing goes back to the first match");
  assert.equal(filterKey(d, items, "up").draft.sel, 0);
});
