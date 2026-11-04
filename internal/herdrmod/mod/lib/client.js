/** The `tkt api` arguments for one daemon request; the body goes as JSON. */
export function apiArgs(method, path, body) {
  const args = ["api", method, path];
  if (body !== undefined) args.push("--body", JSON.stringify(body));
  return args;
}

/** The daemon's answer from what `tkt api` printed: {status, body}; a tkt that failed throws its message. */
export function parseAnswer(result) {
  if (result.code !== 0) {
    throw new Error((result.stderr || result.stdout || `tkt api exited ${result.code}`).trim().replace(/^tkt: /, ""));
  }
  try {
    const answer = JSON.parse(result.stdout);
    if (typeof answer.status !== "number") throw new Error("no status");
    return { status: answer.status, body: answer.body };
  } catch {
    throw new Error(`tkt api printed something else than its answer: ${result.stdout.slice(0, 120)}`);
  }
}

/** The machines to look for tkt on: the one it was found on last, then this computer, then the others. */
export function machineOrder(machines, last) {
  const names = machines.map((m) => m.name ?? null);
  const order = names.includes(last) ? [last] : [];
  for (const n of [null, ...names]) if (!order.includes(n)) order.push(n);
  return order;
}

/** The agents to tell tkt about: Herdr desktop's installed agents (knownAgents), by key, most picked first. */
export function agentKinds(known) {
  const kinds = [];
  for (const p of known ?? []) {
    if (p.agent && p.installed && !kinds.includes(p.key)) kinds.push(p.key);
  }
  return kinds;
}

/** Which machine's tkt to use, given the machines it was found on: the only one, the one chosen before while
 *  it still has tkt, else {choose} so the person picks; looking never starts a daemon anywhere. */
export function pickMachine(found, chosen) {
  if (found.length === 0) return { none: true };
  if (found.length === 1) return { machine: found[0] };
  if (chosen !== undefined && found.includes(chosen)) return { machine: chosen };
  return { choose: found };
}

// A machine whose tkt fails this many requests in a row is looked for again: it may have lost tkt.
const LOOK_AGAIN = 3;

/** The failure count after one more failed request, and whether to look for tkt again. */
export function failed(failures) {
  const n = failures + 1;
  return n >= LOOK_AGAIN ? { failures: 0, lookAgain: true } : { failures: n, lookAgain: false };
}
