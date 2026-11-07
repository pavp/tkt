import { markOf } from "./columns.js";
import { statusTone } from "./rows.js";
/** The preview of a selected /v1/board row: its title line, the card's details and hint, and its label. */
export function previewOf(row) {
  if (!row?.ticket) return null;
  const t = row.ticket;
  const card = row.card ?? {};
  return {
    title: `${row.key} ${t.status} · ${t.summary}`,
    line: [...(card.details ?? []), card.hint].filter(Boolean).join(" · "),
    label: t.label ?? "",
  };
}

/** The keys and clicks that work on the tab in view, as [what to press, what it does], as the TUI's legend lists
 *  them; opts.strip says the Sprint shows review requests, opts.bots how many bots the PRs tab hides. */
export function legendFor(tab, view, { strip = false, bots = 0, merged = false } = {}) {
  const columns = tab === "sprint" && view === "columns";
  const legend = [[columns ? "←↑↓→ wasd" : "↑↓ ws", tab === "sprint" && strip ? "move · ↑ to reviews" : "move"]];
  if (tab === "prs" && bots > 0) legend.push(["b", "bots"]);
  legend.push(["click", tab === "sprint" && strip ? "select · review menu" : "select"], ["double-click", "actions"], ["enter", tab === "sprint" ? "actions" : "actions · fold group"]);
  if (tab === "backlog") legend.push(["i", "details"], ["o", "Jira ↗"], ["O", "copy link"], ["tab 1-3", "switch tab"], ["v", merged ? "split" : "merge"]);
  else legend.push(["f", "send feedback"], ["i", "details"], ["o", "Jira ↗"], ["p", "PR ↗"], ["O P", "copy link"], ["tab 1-3", "switch tab"]);
  if (tab === "sprint") legend.push(["v", "view"]);
  if (columns) legend.push(["z", "density"]);
  if (tab !== "backlog") legend.push(["t", tab === "prs" ? "requested" : "team"]);
  legend.push(tab === "prs" ? ["R", "repos"] : ["B", "boards"], ["g", "refresh"], [",", "settings"], ["q", "close"]);
  return legend;
}

/** The selected ticket's preview title: its key, its status with its mark's icon on its column's color, its summary. */
export function previewBadge(row) {
  if (!row?.ticket) return null;
  const t = row.ticket;
  const mark = markOf(t.mark, t.status);
  return { key: row.key, status: mark ? mark.word : t.status, tone: statusTone(t.category), summary: t.summary ?? "" };
}

/** A sync label that warns ("! cached · 10:30") takes the warning color; the others stay dim. */
export function syncTone(label) {
  return label?.startsWith("!") ? "ansi3" : "dim";
}

/** What to tell the person when tkt answered with an error, on the machine its tkt runs on. */
export function problemOf(message, machine) {
  if (/not configured/.test(message)) {
    return machine ? `tkt isn't set up on ${machine}: run \`tkt init\` in a terminal there` : "tkt isn't set up on this computer: run `tkt init` in a terminal";
  }
  return message;
}

/** The sync's warnings on one line, as the TUI's header shows them. */
export function warningsLine(warnings) {
  return warnings?.length ? `! ${warnings.join(" · ")}` : "";
}
