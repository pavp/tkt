/** Moving the selection with the keys the TUI uses. Pure, so `node --test` checks it without Herdr. */
import { listRows } from "./list.js";
import { prRows } from "./prs.js";
import { backlogRows } from "./backlog.js";

const DIRECTIONS = { down: [0, 1], s: [0, 1], j: [0, 1], up: [0, -1], w: [0, -1], k: [0, -1], right: [1, 0], d: [1, 0], l: [1, 0], left: [-1, 0], a: [-1, 0], h: [-1, 0] };
const TAB_ORDER = ["sprint", "prs", "backlog"];

/** The move a key asks for as [columns, rows], or null when it is no move. */
export function direction(key) {
  return DIRECTIONS[key] ?? null;
}

/** The tab a key leads to from current, or null when it leads nowhere. */
export function tabAfter(keystroke, current) {
  const at = TAB_ORDER.indexOf(current);
  if (["1", "2", "3"].includes(keystroke)) return TAB_ORDER[Number(keystroke) - 1];
  if (keystroke === "tab") return TAB_ORDER[(at + 1) % TAB_ORDER.length];
  if (keystroke === "shift-tab") return TAB_ORDER[(at + TAB_ORDER.length - 1) % TAB_ORDER.length];
  if (keystroke === "n" || keystroke === "r") return current === "prs" ? "sprint" : "prs";
  return null;
}

export function stepList(ids, current, dr) {
  if (ids.length === 0) return null;
  const at = ids.indexOf(current);
  if (at < 0) return ids[0];
  return ids[Math.max(0, Math.min(ids.length - 1, at + dr))];
}

/** The card a move reaches in columns of card keys: across, the nearest column with cards, at the same row or its
 *  last; down and up, within the column. */
export function stepColumns(cols, current, dc, dr) {
  let col = cols.findIndex((c) => c.includes(current));
  if (col < 0) {
    const first = cols.find((c) => c.length > 0);
    return first ? first[0] : null;
  }
  let row = cols[col].indexOf(current);
  for (let c = col + dc; dc !== 0 && c >= 0 && c < cols.length; c += dc) {
    if (cols[c].length > 0) {
      col = c;
      row = Math.min(row, cols[c].length - 1);
      break;
    }
  }
  row = Math.max(0, Math.min(cols[col].length - 1, row + dr));
  return cols[col][row];
}

/** What the arrows move between on a tab: the cards of each column, or a list's rows with their indexes in the
 *  drawn list (titles included) and that list's id, to scroll the selection into view. */
export function targets(tab, view, body) {
  if (tab === "sprint" && view === "columns") return { cols: (body?.columns ?? []).map((c) => c.rows.map((r) => r.key)) };
  const [rows, id, list] = tab === "prs" ? [prRows(body), (r) => r.key + r.group, "pr-rows"]
    : tab === "backlog" ? [backlogRows(body), (r) => r.key, "backlog-rows"]
    : [listRows(body?.sections), (r) => r.key, "rows"];
  const ids = [];
  const at = [];
  rows.forEach((r, i) => {
    if (r.kind !== "row") return;
    ids.push(id(r));
    at.push(i);
  });
  return { ids, at, list };
}
