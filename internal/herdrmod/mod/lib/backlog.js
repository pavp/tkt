/** The Backlog's rows from /v1/backlog: each group's title (board, dates, points, folded or not), then its
 *  tickets unless folded. */
export function backlogRows(view) {
  const rows = [];
  for (const g of view?.groups ?? []) {
    rows.push({ kind: "title", group: g.group, title: g.title, board: g.board ?? "", dates: g.dates ?? "", count: g.count, points: g.points, collapsed: Boolean(g.collapsed) });
    if (g.collapsed) continue;
    for (const r of g.rows ?? []) rows.push({ kind: "row", key: r.key, group: g.group, ticket: r.ticket, card: r.card });
  }
  return rows;
}

/** A group's story points as the TUI's badges: not started, in progress, done, after the unestimated count. */
export function badges(points) {
  if (!points) return null;
  return {
    unestimated: points.unestimated > 0 ? `${points.unestimated} unestimated` : "",
    stages: [["todo", String(points.todo)], ["doing", String(points.doing)], ["done", String(points.done)]],
  };
}
