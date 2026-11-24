import { stackTone } from "./details.js";

/** The PRs tab's rows from /v1/prs: each group's title (with its count, how many need you, folded or not), then
 *  its pull requests unless folded. */
export function prRows(view) {
  const rows = [];
  for (const s of view?.sections ?? []) {
    rows.push({ kind: "title", group: s.group, title: s.title, count: s.count, needs: s.needs, collapsed: Boolean(s.collapsed) });
    if (s.collapsed) continue;
    for (const r of s.rows ?? []) {
      const grouped = (r.stack?.shown ?? 0) >= 2;
      if (grouped && r.stack.first) {
        const here = r.stack.shown < r.stack.urls.length ? ` · ${r.stack.shown} here` : "";
        rows.push({ kind: "stack", group: r.stack.key, title: `${r.stack.name} · ${r.stack.urls.length} PRs${here}`, block: r.stack.collapsed ? "only" : "top", status: r.stack.status,
          tone: stackTone(r.stack.status), count: r.stack.shown, collapsed: Boolean(r.stack.collapsed), urls: r.stack.urls });
      }
      if (grouped && r.stack.collapsed) continue;
      rows.push({
        kind: "row",
        key: r.key,
        group: s.group,
        ticketKey: r.ticket?.key ?? "",
        title: r.pr?.title || r.ticket?.summary || "",
        repo: r.pr?.repo ?? "",
        url: r.pr?.url ?? "",
        status: r.status ?? "",
        needsYou: Boolean(r.needs_you),
        review: Boolean(r.review),
        other: Boolean(r.other),
        who: r.pr?.author || r.ticket?.assignee || "",
        approvals: r.approvals ?? "",
        after: r.after ?? 0,
        on: r.on ?? "",
        inStack: grouped,
        stackKey: grouped ? r.stack.key : "",
        block: grouped ? "mid" : undefined,
        stackNote: r.stack && !grouped ? `1 of ${r.stack.urls.length}` : "",
        raw: r,
      });
    }
  }
  rows.forEach((r, i) => {
    if (r.block === "mid" && rows[i + 1]?.stackKey !== r.stackKey) r.block = "bottom";
  });
  return rows;
}

/** list with name added, or taken out when it was there. */
export function toggled(list, name) {
  const l = list ?? [];
  return l.includes(name) ? l.filter((n) => n !== name) : [...l, name];
}

/** The Repos filter's chip: "Repos" while every repository shows, else how many show of how many. */
export function reposChip(repos) {
  const all = repos?.length ?? 0;
  const shown = (repos ?? []).filter((r) => !r.hidden).length;
  return shown === all ? "Repos" : `Repos ${shown}/${all}`;
}

const CI = { fail: "failing ✕", pending: "running …", pass: "passing ✓" };

/** The preview of a selected PRs-tab row, worded as the TUI's (its preview for pull requests). */
export function prPreview(row) {
  if (row?.kind === "stack") return { title: row.title, line: `${row.status} · enter to ${row.collapsed ? "unfold" : "fold"} · P copies the stack's ${row.urls.length} URLs` };
  if (!row || row.kind !== "row") return null;
  const pr = row.raw?.pr ?? {};
  const ci = `CI ${CI[pr.checks] ?? "not reported"}`;
  if (!row.other && !row.review && row.ticketKey) {
    return { title: `${row.key} · ${row.ticketKey} · ${row.title}`, line: `${row.status} in ${row.repo} · ${ci} · enter for actions` };
  }
  const how = row.other ? "opened by" : "review requested by";
  const approvals = row.approvals ? ` · ${row.approvals}` : "";
  return { title: `${row.key} · ${pr.title || row.title}`, line: `${how} ${row.who || "—"} in ${row.repo}${approvals} · ${ci} · enter for actions` };
}

/** The PRs tab's toggle, as the TUI's: Requested, lit while it narrows the list (team_prs off). */
export function prsToggle(teamPRs) {
  return { label: "Requested", on: !teamPRs };
}
