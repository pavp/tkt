const CHECKS = { fail: ["✕", "blocked"], pending: ["…", "working"], pass: ["✓", "done"] };

/** The details screen of GET /v1/tickets/{key}, opened from the tab named from. */
export function detailView(d, from) {
  const t = d.ticket ?? {};
  const fields = [["Status", t.status], ["Type", t.type], ["Points", t.points ? String(t.points) : ""], ["Assignee", t.assignee || "unassigned"], ["Label", t.label], ["Board", t.board]];
  return {
    key: t.key,
    breadcrumb: `${from} › ${t.key}`,
    title: t.summary ?? "",
    url: t.url ?? "",
    fields: fields.filter(([, v]) => v),
    attention: d.attention?.[0] ?? "",
    agent: d.agent ?? "",
    description: d.description ?? "",
    acceptance: d.acceptance_criteria ?? "",
    subtasks: (t.subtasks ?? []).map((s) => ({ key: s.key, status: s.status, summary: s.summary })),
    prs: (t.prs ?? []).map((p) => {
      const [mark, tone] = p.state === "open" ? CHECKS[p.checks] ?? ["—", "dim"] : [p.state, "dim"];
      return { ref: `${p.repo}#${p.number}`, title: p.title, url: p.url, mark, tone };
    }),
    comments: (d.comments ?? []).map((c) => ({ who: c.author, when: (c.created ?? "").slice(0, 10), body: c.body })),
    work: (d.work ?? []).map((w) => ({ where: `${w.repo} · ${w.branch}`, path: w.path ?? "" })),
  };
}

/** The tickets before and after key in the list the details were opened from. */
export function neighbors(keys, key) {
  const i = keys.indexOf(key);
  if (i < 0) return { prev: null, next: null };
  return { prev: keys[i - 1] ?? null, next: keys[i + 1] ?? null };
}

/** What a key does on the details screen, as the TUI's: back to the board, or copy the ticket as markdown. */
export function detailsKey(k) {
  if (["escape", "i", "left", "h"].includes(k)) return "back";
  if (k === "c") return "copy";
  return null;
}
