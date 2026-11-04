/** The header's tab counts, context line and filter notice, worded as the TUI's. Pure, for `node --test`. */

const plural = (n, one, many) => (n === 1 ? one : many);
const sum = (xs) => xs.reduce((a, b) => a + b, 0);

/** What a tab's badge counts: the Sprint's tickets (subtasks ride with their parent), the pull requests shown,
 *  the Backlog's tickets, "–" until the Backlog loads. */
export function tabCount(tab, body) {
  if (!body) return tab === "backlog" ? "–" : "";
  if (tab === "prs") return String(sum((body.sections ?? []).map((s) => s.count ?? (s.rows ?? []).length)));
  if (tab === "backlog") return String(sum((body.groups ?? []).map((g) => g.count ?? 0)));
  return String(sum((body.sections ?? []).map((s) => (s.rows ?? []).filter((r) => !r.parent).length)));
}

export function contextText(tab, body, { loading = false } = {}) {
  if (tab === "backlog") {
    if (!body) return loading ? "loading backlog…" : "";
    const groups = body.groups ?? [];
    const boards = (body.boards ?? []).filter((b) => !b.hidden).length;
    const sprints = groups.filter((g) => g.group.startsWith("sprint:")).length;
    const rest = groups.find((g) => g.group === "backlog:")?.count ?? 0;
    return `${boards} ${plural(boards, "board", "boards")} · ${sprints} ${plural(sprints, "sprint", "sprints")} · ${rest} in backlog`;
  }
  if (!body) return "";
  if (tab === "prs") {
    const rows = (body.sections ?? []).flatMap((s) => s.rows ?? []);
    const mine = rows.filter((r) => !r.review && !r.other);
    const needs = mine.filter((r) => r.needs_you).length;
    const reviews = rows.filter((r) => r.review).length;
    const parts = [`${mine.length} open`];
    if (needs > 0) parts.push(`${needs} needs you`);
    parts.push(`${reviews} review ${plural(reviews, "request", "requests")}`);
    return parts.join(" · ");
  }
  return (body.sprints ?? []).join("   ");
}

/** "Showing N of M boards · B change" while the tab's filter hides some, else null. */
export function scopeNotice(tab, body) {
  if (!body) return null;
  if (tab === "prs") {
    const repos = (body.repos ?? []).map((r) => r.name);
    const hidden = repos.filter((r) => (body.hidden_repos ?? []).includes(r)).length;
    return hidden ? { text: `Showing ${repos.length - hidden} of ${repos.length} repositories · `, key: "R", after: " change" } : null;
  }
  const boards = body.boards ?? [];
  const hidden = boards.filter((b) => b.hidden).length;
  return hidden ? { text: `Showing ${boards.length - hidden} of ${boards.length} boards · `, key: "B", after: " change" } : null;
}

/** The routes a load asks: the tab in view first, then the Sprint and PRs, whose counts the header always shows;
 *  the Backlog, which asks Jira, only while it is in view. */
export function routesFor(tabs, current, { bots = false, refreshBacklog = false } = {}) {
  const route = (t) => {
    if (t.id === "prs" && bots) return `${t.route}?bots=true`;
    if (t.id === "backlog" && refreshBacklog) return `${t.route}?refresh=true`;
    return t.route;
  };
  const first = tabs.find((t) => t.id === current);
  const rest = tabs.filter((t) => t.id !== current && t.id !== "backlog");
  return [first, ...rest].map((t) => [t.id, route(t)]);
}

export function viewButton(view) {
  return view === "list" ? { label: "≣ View", next: "columns" } : { label: "◫ View", next: "list" };
}
