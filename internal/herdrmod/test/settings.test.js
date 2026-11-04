import { test } from "node:test";
import assert from "node:assert/strict";
import { fixture } from "./fixtures.js";
import { SECTIONS, reposPatch, boardsPatch, statusPatch, rootsPatch, statusRows } from "../mod/lib/settings.js";

test("Settings has the TUI's sections, and the machine the window uses", () => {
  assert.deepEqual(SECTIONS.map((s) => s.title), ["Spaces & repositories", "Team boards", "Statuses", "Local clones", "Board", "Machine"]);
});

test("a repository is added to or removed from its space as the whole list", () => {
  const space = fixture("settings.json").spaces[0];
  assert.deepEqual(reposPatch(space, { add: { repo: "example-org/api", path: "~/src/api" } }), { spaces: { DEMO: { repos: [{ repo: "example-org/web", path: "~/src/web" }, { repo: "example-org/api", path: "~/src/api" }] } } });
  assert.deepEqual(reposPatch(space, { remove: "example-org/web" }), { spaces: { DEMO: { repos: [] } } });
});

test("a team board is turned on or off as the whole list of ids", () => {
  const space = fixture("settings.json").spaces[0];
  assert.deepEqual(boardsPatch(space, 9), { spaces: { DEMO: { team_boards: [7, 9] } } });
  assert.deepEqual(boardsPatch(space, 7), { spaces: { DEMO: { team_boards: [] } } });
});

test("a status gets a column or mark, or goes back to automatic", () => {
  assert.deepEqual(statusPatch("DEMO", "Testing", { column: "done" }), { spaces: { DEMO: { statuses: { Testing: { column: "done" } } } } });
  assert.deepEqual(statusPatch("DEMO", "Testing", null), { spaces: { DEMO: { statuses: { Testing: null } } } });
  const rows = statusRows(fixture("settings-statuses.json"), fixture("settings.json").columns);
  assert.deepEqual(rows[2], { name: "Testing", column: "In Review", columnAuto: false, mark: "diamond", markAuto: true, changed: true });
});

test("a clones folder is added or removed", () => {
  assert.deepEqual(rootsPatch(["~/src"], { add: "~/code" }), { repo_roots: ["~/src", "~/code"] });
  assert.deepEqual(rootsPatch(["~/src", "~/code"], { remove: "~/src" }), { repo_roots: ["~/code"] });
});
