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

test("the agents to save are Herdr's installed agents, by key, once each", async () => {
  const { agentKinds } = await import("../mod/lib/client.js");
  const known = [
    { key: "claude", agent: true, installed: true },
    { key: "codex", agent: true, installed: false },
    { key: "nvim", agent: false, installed: true },
    { key: "claude", agent: true, installed: true },
    { key: "opencode", agent: true, installed: true },
  ];
  assert.deepEqual(agentKinds(known), ["claude", "opencode"]);
  assert.deepEqual(agentKinds(undefined), []);
});

test("tkt found on one machine is used; on several, the person chooses unless one was chosen before", async () => {
  const { pickMachine } = await import("../mod/lib/client.js");
  assert.deepEqual(pickMachine([null], undefined), { machine: null });
  assert.deepEqual(pickMachine(["wsl-dev"], null), { machine: "wsl-dev" });
  assert.deepEqual(pickMachine([null, "wsl-dev"], undefined), { choose: [null, "wsl-dev"] });
  assert.deepEqual(pickMachine([null, "wsl-dev"], "wsl-dev"), { machine: "wsl-dev" });
  assert.deepEqual(pickMachine([], undefined), { none: true });
});

test("the machine in use is looked for again after it failed a few times in a row", async () => {
  const { failed } = await import("../mod/lib/client.js");
  assert.deepEqual(failed(0), { failures: 1, lookAgain: false });
  assert.deepEqual(failed(1), { failures: 2, lookAgain: false });
  assert.deepEqual(failed(2), { failures: 0, lookAgain: true });
});
