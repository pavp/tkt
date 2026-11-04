import { test } from "node:test";
import assert from "node:assert/strict";
import { fixture, fixtureNames } from "./fixtures.js";

const uses = {
  "board.json": ["fetched_at", "team", "boards", "columns", "sections", "agents"],
  "prs.json": ["fetched_at", "repos", "sections"],
  "backlog.json": ["fetched_at", "merged", "boards", "groups"],
  "board-state.json": ["view", "team", "hidden_boards", "hidden_repos", "collapsed", "team_prs", "backlog_merged"],
  "settings.json": ["spaces", "repo_roots", "columns", "marks"],
  "transitions.json": ["key", "moves"],
  "task-agents.json": ["agents", "kinds"],
};

test("every contract fixture is JSON", () => {
  for (const name of fixtureNames()) {
    assert.doesNotThrow(() => fixture(name), name);
  }
});

for (const [name, fields] of Object.entries(uses)) {
  test(`${name} keeps the fields the mod reads`, () => {
    const got = fixture(name);
    for (const f of fields) assert.ok(f in got, `${name} lost ${f}`);
  });
}
