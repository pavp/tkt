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

/** What clicks and keys do on the shown screen, as [what to press, what it does]. */
export function legendFor(tab, view) {
  const legend = [["click", "select"], ["double-click", "actions"], ["enter", "actions"], ["↻", "sync"]];
  if (tab === "sprint") legend.push(["◫ ≣", view === "columns" ? "list" : "columns"]);
  if (tab === "backlog") legend.push(["Split/Merged", "group sprints"]);
  return legend;
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
