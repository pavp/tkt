/**
 * The board's list view as rows to draw: each section's title, then its tickets, in the order the daemon's
 * GET /v1/board lays them out. Pure, so `node --test` checks it without Herdr.
 */
export function listRows(sections) {
  const rows = [];
  for (const s of sections ?? []) {
    rows.push({ kind: "title", title: s.title, count: s.count });
    for (const r of s.rows ?? []) {
      rows.push({ kind: "row", key: r.key, parent: r.parent ?? null, ticket: r.ticket });
    }
  }
  return rows;
}

/** The section title to pin over the list: the one the first shown row belongs to, once that title itself
 *  scrolled out of view; null while the top row is a title or nothing scrolled. */
export function stickyAt(rows, range) {
  if (!rows[range.start] || rows[range.start].kind === "title") return null;
  for (let i = range.start - 1; i >= 0; i--) {
    if (rows[i].kind === "title") return rows[i];
  }
  return null;
}

/** How many tickets the list hides above and below the shown range, for the "↑/↓ N more" hints. */
export function moreAround(rows, range) {
  const count = (from, to) => rows.slice(from, to).filter((r) => r.kind === "row").length;
  return { above: count(0, range.start), below: count(range.end, rows.length) };
}
