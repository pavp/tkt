import { test } from "node:test";
import assert from "node:assert/strict";
import { apiArgs, parseAnswer, machineOrder } from "../mod/lib/client.js";

test("a request becomes the tkt api arguments", () => {
  assert.deepEqual(apiArgs("GET", "/v1/board"), ["api", "GET", "/v1/board"]);
  assert.deepEqual(apiArgs("PATCH", "/v1/board/state", { team: true }), ["api", "PATCH", "/v1/board/state", "--body", '{"team":true}']);
});

test("the answer is the daemon's status and body", () => {
  assert.deepEqual(parseAnswer({ code: 0, stdout: '{"status":200,"body":{"ok":true}}\n', stderr: "" }), { status: 200, body: { ok: true } });
});

test("a tkt that could not reach its daemon is an error with its message", () => {
  assert.throws(() => parseAnswer({ code: 1, stdout: "", stderr: "tkt: the tkt daemon is not reachable: no\n" }), /the tkt daemon is not reachable/);
  assert.throws(() => parseAnswer({ code: 0, stdout: "not json", stderr: "" }), /tkt api/);
});

test("machines are tried as remembered, then this computer, then the others", () => {
  const machines = [{ name: null }, { name: "wsl-dev" }, { name: "build" }];
  assert.deepEqual(machineOrder(machines, undefined), [null, "wsl-dev", "build"]);
  assert.deepEqual(machineOrder(machines, "wsl-dev"), ["wsl-dev", null, "build"]);
  assert.deepEqual(machineOrder(machines, "gone"), [null, "wsl-dev", "build"]);
});
