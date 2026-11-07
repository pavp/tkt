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

test("a machine's tkt is ready, too old for the mod, or absent", async () => {
  const { tktOn, outdatedNote } = await import("../mod/lib/client.js");
  assert.equal(tktOn({ code: 0, stdout: "Sends one request…", stderr: "" }), "ready");
  assert.equal(tktOn({ code: 1, stdout: "", stderr: 'tkt: unknown command "api" for "tkt"\n' }), "old");
  assert.equal(tktOn({ code: 127, stdout: "", stderr: "sh: tkt: not found" }), "none");
  assert.equal(tktOn(null), "none");
  assert.equal(outdatedNote(["wsl-dev"]), "tkt on wsl-dev is too old for Herdr desktop: update tkt there (it needs `tkt api`)");
  assert.equal(outdatedNote([null]), "tkt on this computer is too old for Herdr desktop: update tkt (it needs `tkt api`)");
  assert.equal(outdatedNote([null, "wsl-dev"]), "tkt on this computer and wsl-dev are too old for Herdr desktop: update tkt there (it needs `tkt api`)");
});

test("the mod updates itself only from a released tkt whose version differs", async () => {
  const { needsUpdate } = await import("../mod/lib/client.js");
  assert.equal(needsUpdate("0.18.0", "0.19.0"), true);
  assert.equal(needsUpdate("0.18.0", "0.18.0"), false);
  assert.equal(needsUpdate("dev", "0.19.0"), false);
  assert.equal(needsUpdate("0.18.0", "dev"), false);
  assert.equal(needsUpdate("0.18.0", undefined), false);
});

test("an update writes only inside the mod's folder", async () => {
  const { safeModPath } = await import("../mod/lib/client.js");
  for (const ok of ["main.js", "lib/menus.js", "mod.json"]) assert.equal(safeModPath(ok), true, ok);
  for (const bad of ["../../keys.json", "lib/../../escaped.js", "/etc/x", "", "lib\\..\\x", "./main.js", "lib//x.js"]) assert.equal(safeModPath(bad), false, bad);
});

test("tkt's path on a machine is the last absolute path a shell printed", async () => {
  const { tktPathFrom } = await import("../mod/lib/client.js");
  assert.equal(tktPathFrom("/home/linuxbrew/.linuxbrew/bin/tkt\n"), "/home/linuxbrew/.linuxbrew/bin/tkt");
  assert.equal(tktPathFrom("welcome to zsh\n/home/x/bin/tkt\n"), "/home/x/bin/tkt");
  assert.equal(tktPathFrom("tkt not found\n"), null);
});

test("how Herdr starts an agent goes with it when it is not just the agent's own name, as programs.json's run says", async () => {
  const { agentRuns } = await import("../mod/lib/client.js");
  const known = [
    { key: "claude", agent: true, installed: true, run: ["claude"] },
    { key: "gentle-shell", agent: true, installed: true, run: ["gentle-shell"] },
    { key: "pi", agent: true, installed: true, run: ["gentle-shell", "--agent", "pi"] },
    { key: "codex", agent: true, installed: false, run: ["my-codex"] },
  ];
  assert.deepEqual(agentRuns(known), { pi: ["gentle-shell", "--agent", "pi"] });
  assert.deepEqual(agentRuns(undefined), {});
});

test("calls made together while the machine is unknown share one look for it", async () => {
  const { shared } = await import("../mod/lib/client.js");
  let looks = 0;
  const look = shared(async () => { looks++; await new Promise((r) => setTimeout(r, 10)); return "mac"; });
  assert.deepEqual(await Promise.all([look(), look(), look()]), ["mac", "mac", "mac"]);
  assert.equal(looks, 1);
  await look();
  assert.equal(looks, 2, "a later call looks again");
  const failing = shared(async () => { throw new Error("no tkt"); });
  await assert.rejects(failing(), /no tkt/);
  await assert.rejects(failing(), /no tkt/, "a failed look is not kept");
});
