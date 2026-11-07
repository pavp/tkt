import { test } from "node:test";
import assert from "node:assert/strict";
import { fold, matchRank, matching } from "../mod/lib/match.js";

test("matching ignores case and accents", () => {
  assert.equal(fold("En Revisión"), "en revision");
  assert.equal(matchRank("En revisión", "REVISION"), 1);
  assert.equal(matchRank("Ñandú", "nan"), 0);
});

test("a name that starts with the text ranks before one that only contains it", () => {
  assert.equal(matchRank("sprint board", "spr"), 0);
  assert.equal(matchRank("Team sprint", "spr"), 1);
  assert.equal(matchRank("Kanban", "spr"), -1);
  assert.equal(matchRank("anything", "  "), 0, "an empty text matches everything");
});

test("matching keeps the matches, starts first, each group in its given order", () => {
  const names = ["web-app", "api", "example-org/web", "Webhooks", "docs"];
  assert.deepEqual(matching(names, "web"), ["web-app", "Webhooks", "example-org/web"]);
  assert.deepEqual(matching(names, ""), names);
  const rows = [{ name: "Ops" }, { name: "Development" }, { name: "DevOps" }];
  assert.deepEqual(matching(rows, "ops", (r) => r.name).map((r) => r.name), ["Ops", "DevOps"]);
});
