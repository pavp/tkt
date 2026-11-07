import { matching } from "./match.js";

/** A Repos or Boards filter being edited: nothing changes until it is applied. */
export function filterDraft(hidden) {
  return { query: "", hidden: [...(hidden ?? [])], sel: 0 };
}

/** The rows the typed text leaves, each with whether the draft hides it. */
export function filterRows(items, draft) {
  return matching(items, draft.query, (r) => r.name).map((r) => ({ ...r, hidden: draft.hidden.includes(r.name) }));
}

/**
 * A key on an open filter: letters type, up and down move, space ticks, ctrl-a ticks all or none of the rows shown,
 * enter applies (done "apply") and esc clears the text, or with none throws the draft away (done "cancel").
 */
export function filterKey(draft, items, k) {
  const rows = filterRows(items, draft);
  const keep = (d) => ({ draft: d, done: null });
  if (k === "enter") return { draft, done: "apply" };
  if (k === "escape") return draft.query ? keep({ ...draft, query: "", sel: 0 }) : { draft, done: "cancel" };
  if (k === "down" || k === "up") return keep({ ...draft, sel: Math.max(0, Math.min(rows.length - 1, draft.sel + (k === "down" ? 1 : -1))) });
  if (k === " ") {
    const row = rows[draft.sel];
    if (!row) return keep(draft);
    return keep({ ...draft, hidden: row.hidden ? draft.hidden.filter((n) => n !== row.name) : [...draft.hidden, row.name] });
  }
  if (k === "ctrl-a") {
    const names = rows.map((r) => r.name);
    const anyHidden = rows.some((r) => r.hidden);
    const rest = draft.hidden.filter((n) => !names.includes(n));
    return keep({ ...draft, hidden: anyHidden ? rest : [...rest, ...names] });
  }
  if (k === "backspace") return keep({ ...draft, query: [...draft.query].slice(0, -1).join(""), sel: 0 });
  if (typeof k === "string" && [...k].length === 1 && k > " ") return keep({ ...draft, query: draft.query + k, sel: 0 });
  return keep(draft);
}
