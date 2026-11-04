/** The board's last copy, kept in the mod's storage so the window opens on it while tkt answers, as the TUI opens
 *  on its disk cache. Pure, for `node --test`. */

const KEPT = ["sprint", "prs"];

export function packCache(machine, answers, now) {
  const kept = {};
  for (const id of KEPT) if (answers[id]?.status === 200) kept[id] = answers[id];
  return JSON.stringify({ machine: machine ?? null, at: now, answers: kept });
}

/** The kept copy for machine, or null when there is none for it. */
export function unpackCache(raw, machine) {
  try {
    const c = JSON.parse(raw ?? "null");
    return c && c.machine === (machine ?? null) ? { at: c.at, answers: c.answers ?? {} } : null;
  } catch {
    return null;
  }
}

export function cachedLabel(at) {
  const d = new Date(at);
  return `! cached · ${String(d.getHours()).padStart(2, "0")}:${String(d.getMinutes()).padStart(2, "0")}`;
}
