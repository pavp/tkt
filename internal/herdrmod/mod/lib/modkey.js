/** Binding the key that opens tkt in Herdr desktop's keys.json, from the window's own machine, where a tkt running
 *  on another machine cannot write. Pure, for `node --test`. */

const COMMAND = "mod.open:tkt";

/** cmd-j on macOS; ctrl-shift-j elsewhere, where cmd is the Windows or Super key, which the system keeps. */
export function modKeyFor(platform) {
  return platform === "darwin" ? "cmd-j" : "ctrl-shift-j";
}

/** keys.json with key opening tkt ("added", with its new text), or why not: "present" when a key already opens
 *  tkt, "taken" (by its command) when key does something else, "unreadable" when the file is not JSON. */
export function withModKey(text, key) {
  let keys;
  try {
    keys = JSON.parse(text ?? "{}") ?? {};
  } catch {
    return { status: "unreadable" };
  }
  const global = keys.global ?? {};
  if (Object.values(global).includes(COMMAND)) return { status: "present" };
  if (global[key]) return { status: "taken", by: global[key] };
  return { status: "added", text: `${JSON.stringify({ ...keys, global: { ...global, [key]: COMMAND } }, null, 2)}\n` };
}
