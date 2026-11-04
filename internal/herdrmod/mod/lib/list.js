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
