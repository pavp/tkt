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
