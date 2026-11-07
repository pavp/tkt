import { test } from "node:test";
import assert from "node:assert/strict";
import { completing, suggestions, accepted } from "../mod/lib/complete.js";

test("adding a repository completes its owner, then its name, then its clone's folder", () => {
  assert.deepEqual(completing("addRepo", "exa"), { source: "owners", base: "", text: "exa" });
  assert.deepEqual(completing("addRepo", "example-org/we"), { source: "repos", owner: "example-org", base: "example-org/", text: "we" });
  assert.deepEqual(completing("addRepo", "example-org/web ~/src/w"), { source: "folders", dir: "~/src/", base: "example-org/web ~/src/", text: "w" });
  assert.deepEqual(completing("addRepo", "example-org/web src"), null, "a folder starts with ~ or /");
  assert.deepEqual(completing("addRoot", "~/de"), { source: "folders", dir: "~/", base: "~/", text: "de" });
  assert.deepEqual(completing("addRoot", "/"), { source: "folders", dir: "/", base: "/", text: "" });
  assert.equal(completing("comment", "anything"), null);
});

test("suggestions match the typed part, starts first, hidden folders only when asked for", () => {
  const known = { owners: ["example-org", "other"], repos: { "example-org": ["example-org/web", "example-org/api", "example-org/web-admin"] },
    folders: { "~/": [".cache", "dev", "Documents", "downloads"] } };
  assert.deepEqual(suggestions(completing("addRepo", "ex"), known), ["example-org"]);
  assert.deepEqual(suggestions(completing("addRepo", "example-org/adm"), known), ["web-admin"]);
  assert.deepEqual(suggestions(completing("addRepo", "example-org/"), known), ["web", "api", "web-admin"]);
  assert.deepEqual(suggestions(completing("addRoot", "~/do"), known), ["Documents", "downloads"]);
  assert.deepEqual(suggestions(completing("addRoot", "~/"), known), ["dev", "Documents", "downloads"]);
  assert.deepEqual(suggestions(completing("addRoot", "~/."), known), [".cache"]);
  assert.equal(suggestions(completing("addRepo", "nobody/x"), known), undefined, "not listed yet");
});

test("accepting a suggestion writes it into the text, ready for the next part", () => {
  assert.equal(accepted(completing("addRepo", "ex"), "example-org"), "example-org/");
  assert.equal(accepted(completing("addRepo", "example-org/adm"), "web-admin"), "example-org/web-admin ");
  assert.equal(accepted(completing("addRepo", "example-org/web ~/s"), "src"), "example-org/web ~/src/");
  assert.equal(accepted(completing("addRoot", "~/do"), "downloads"), "~/downloads/");
  assert.equal(accepted(completing("addRoot", "~do"), "downloads"), "~/downloads/", "a ~ without its slash means the home");
});

test("the owners offered are those of the spaces' repositories, configured or seen, once each", async () => {
  const { ownersOf } = await import("../mod/lib/complete.js");
  const spaces = [{ repos: [{ repo: "example-org/web" }, { repo: "Example-Org/api" }], inferred: ["other/tools"] }, { repos: [] }];
  assert.deepEqual(ownersOf(spaces), ["example-org", "other"]);
});
