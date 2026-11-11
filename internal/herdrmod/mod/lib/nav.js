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
 *  drawn list and that list's id, to scroll the selection into view; the PRs tab's and the Backlog's group titles
 *  are stops too ("group:<key>"), which enter folds, as the TUI's. */
export function targets(tab, view, body) {
  if (tab === "sprint" && view === "columns") return { cols: (body?.columns ?? []).map((c) => c.rows.map((r) => r.key)) };
  const [rows, id, list] = tab === "prs" ? [prRows(body), (r) => r.key + r.group, "pr-rows"]
    : tab === "backlog" ? [backlogRows(body), (r) => r.key, "backlog-rows"]
    : [listRows(body?.sections), (r) => r.key, "rows"];
  const ids = [];
  const at = [];
  rows.forEach((r, i) => {
    if (r.kind === "row") ids.push(id(r));
    else if (tab !== "sprint" && r.group) ids.push(`group:${r.group}`);
    else return;
    at.push(i);
  });
  return { ids, at, list };
}

/** A key event as the TUI's bindings spell it: the character it types (B, ",", " "), else its keystroke. */
export function keyOf(e) {
  const m = e.modifiers ?? {};
  const c = e.key_char;
  if (c && c.length === 1 && c > " " || c === " ") {
    if (!m.platform && !m.control && !m.alt) return c;
  }
  return e.keystroke;
}

const ACTIONS = {
  enter: "menu", " ": "menu", i: "details", o: "openJira", O: "copyJira", p: "openPR", P: "copyPR",
  g: "refresh", ",": "settings", m: "machines", q: "close", escape: "close", "?": "keys",
};

/** The action a key runs on the board's tab, as the TUI binds it; null when it runs none there. */
export function boardAction(k, tab) {
  if (ACTIONS[k]) return ACTIONS[k];
  if (k === "t" && tab !== "backlog") return "team";
  if (k === "f" && tab !== "backlog") return "feedback";
  if (k === "B" && tab !== "prs") return "boards";
  if (k === "R" && tab === "prs") return "repos";
  if (k === "b" && tab === "prs") return "bots";
  if (k === "z" && tab === "sprint") return "density";
  return null;
}

/** What the board selects when nothing is: the first card of the first column with cards, or the first row. */
export function firstTarget(targets) {
  if (targets.cols) return targets.cols.find((c) => c.length > 0)?.[0] ?? null;
  return targets.ids.find((id) => !id.startsWith("group:")) ?? null;
}

/** A highlighted option after up or down in a list of n, or null when the key moves nothing. */
export function listKey(sel, n, k) {
  const d = { down: 1, j: 1, s: 1, up: -1, k: -1, w: -1 }[k];
  if (d === undefined) return null;
  return Math.max(0, Math.min(Math.max(0, n - 1), sel + d));
}

export function sectionAfter(ids, current, k) {
  const at = ids.indexOf(current);
  const d = { tab: 1, "shift-tab": -1 }[k];
  return d === undefined ? null : ids[(at + d + ids.length) % ids.length];
}

/** Where a key takes Settings' focus ({zone: "menu" | "content", section, sel}) across `count` controls: up and down
 *  move within the menu, showing each section, or within the content; right or enter enter the content, left goes back
 *  to the menu, tab cycles the sections. Null when the key is not a move. */
export function settingsNav(at, ids, count, k) {
  const section = sectionAfter(ids, at.section, k);
  if (section) return { ...at, section, sel: 0 };
  if (at.zone === "menu") {
    const i = listKey(ids.indexOf(at.section), ids.length, k);
    if (i !== null) return { ...at, section: ids[i], sel: 0 };
    if (["right", "l", "enter", " "].includes(k)) return count > 0 ? { ...at, zone: "content", sel: 0 } : at;
    return null;
  }
  const sel = listKey(at.sel, count, k);
  if (sel !== null) return { ...at, sel };
  return k === "left" || k === "h" ? { ...at, zone: "menu" } : null;
}
