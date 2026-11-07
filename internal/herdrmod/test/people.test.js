import { test } from "node:test";
import assert from "node:assert/strict";
import { peopleQuery, peopleStatus, peopleItems, SEARCH_WAIT } from "../mod/lib/people.js";

test("a people search sends the typed name once it has two letters, spaces collapsed", () => {
  assert.equal(peopleQuery(""), null);
  assert.equal(peopleQuery(" a "), null, "one letter is too short");
  assert.equal(peopleQuery("an"), "an");
  assert.equal(peopleQuery("  ana   maria "), "ana maria");
  assert.ok(SEARCH_WAIT >= 150 && SEARCH_WAIT <= 400, "waits a moment after the last key before asking Jira");
});

test("a people search tells too short, searching, no matches and failures apart", () => {
  assert.equal(peopleStatus({ text: "a" }), "Type at least two letters");
  assert.equal(peopleStatus({ text: "ana", searching: true }), "Searching Jira…");
  assert.equal(peopleStatus({ text: "ana", users: [] }), "Nobody in Jira matches “ana”");
  assert.equal(peopleStatus({ text: "ana", error: "tkt answered 502" }), "Could not search: tkt answered 502");
  assert.equal(peopleStatus({ text: "ana", users: [{ id: "1", name: "Ana" }] }), null);
  assert.equal(peopleStatus({ text: "ana", searching: true, users: [{ id: "1", name: "Ana" }] }), null, "earlier results stay while the next search runs");
});

test("each person found becomes the pick its prompt asks for", () => {
  const users = [{ id: "a1", name: "Ana" }];
  assert.deepEqual(peopleItems({ kind: "assignUser", key: "DEMO-1" }, users),
    [{ label: "Ana", action: "confirmAssign", key: "DEMO-1", who: { to: "user", account_id: "a1", name: "Ana" } }]);
  const field = { id: "customfield_1", name: "Reviewer" };
  assert.deepEqual(peopleItems({ kind: "fieldUser", key: "DEMO-1", field }, users),
    [{ label: "Ana", action: "fieldValue", field, value: { id: "a1", name: "Ana" } }]);
});
