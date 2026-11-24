const CHECKS = { fail: ["✕", "blocked"], pending: ["…", "working"], pass: ["✓", "done"] };

/** The details screen of GET /v1/tickets/{key}, opened from the tab named from. */
export function detailView(d, from) {
  const t = d.ticket ?? {};
  const stacks = d.pr_stacks ?? (t.prs ?? []).map((p) => ({ name: "", status: "", prs: [{ ...p, depth: 0 }] }));
  const prOf = (p) => {
    const [mark, tone] = p.state === "open" ? CHECKS[p.checks] ?? ["—", "dim"] : [p.state, "dim"];
    return { ref: `${p.repo}#${p.number}`, title: p.title, url: p.url, mark, tone, depth: p.depth ?? 0, number: p.number, status: p.status ?? "", after: p.after ?? 0, updated: p.updated_at ?? "" };
  };
  const fields = [["Status", t.status], ["Type", t.type], ["Points", t.points ? String(t.points) : ""], ["Assignee", t.assignee || "unassigned"], ["Label", t.label], ["Board", t.board]];
  return {
    key: t.key,
    breadcrumb: `${from} › ${t.key}`,
    title: t.summary ?? "",
    category: t.category ?? "",
    url: t.url ?? "",
    fields: fields.filter(([, v]) => v),
    attention: d.attention?.[0] ?? "",
    agent: d.agent ?? "",
    description: d.description ?? "",
    acceptance: d.acceptance_criteria ?? "",
    subtasks: (t.subtasks ?? []).map((s) => ({ key: s.key, status: s.status, summary: s.summary })),
    prs: stacks.flatMap((s) => s.prs.map(prOf)),
    stacks: stacks.map((s) => ({ name: s.name, status: s.status, tone: stackTone(s.status), prs: s.prs.map(prOf) })),
    stackUrls: stacks.filter((s) => s.prs.length > 1).map((s) => s.prs.map((p) => p.url).join("\n")),
    comments: (d.comments ?? []).map((c) => ({ who: c.author, when: (c.created ?? "").slice(0, 10), body: c.body })),
    work: (d.work ?? []).map((w) => ({ where: `${w.repo} · ${w.branch}`, path: w.path ?? "" })),
  };
}

/** The details' pull request entries: the first three stacks, a stack of several as its title row then, unless
 *  folded, its pull requests on two lines each; h is each entry's height. more holds the pull requests after them. */
export function prEntries(stacks, folded, limit = 3) {
  const entries = [];
  for (const s of stacks.slice(0, limit)) {
    if (s.prs.length === 1) {
      entries.push({ kind: "pr", pr: s.prs[0], h: 32 });
      continue;
    }
    const key = s.prs[0].ref;
    entries.push({ kind: "stack", stack: s, key, folded: Boolean(folded[key]), h: 30 });
    if (!folded[key]) for (const p of s.prs) entries.push({ kind: "row", pr: p, h: 46 });
  }
  return { entries, more: stacks.slice(limit).flatMap((s) => s.prs) };
}

/** The theme color a pull request status reads in: red when it needs acting on, green when it can go in. */
export function stackTone(status) {
  if (/^(CI failing|Changes requested|Conflicts)$|unresolved$/.test(status ?? "")) return "ansi1";
  if (["Ready to merge", "Approved"].includes(status)) return "ansi2";
  if (["Draft", "Merged", "Closed"].includes(status)) return "muted";
  return "foreground";
}

/** How long ago iso was, as "2m", "14h", "4d" or "5w"; empty when iso is not a time. */
export function age(iso, now = Date.now()) {
  const at = Date.parse(iso ?? "");
  if (Number.isNaN(at)) return "";
  const m = Math.max(0, Math.floor((now - at) / 60000));
  if (m < 60) return `${m}m`;
  if (m < 60 * 24) return `${Math.floor(m / 60)}h`;
  if (m < 60 * 24 * 14) return `${Math.floor(m / (60 * 24))}d`;
  return `${Math.floor(m / (60 * 24 * 7))}w`;
}

/** "2 of 8": where key sits in the list the details were opened from; empty when it is not in it. */
export function positionOf(keys, key) {
  const i = keys.indexOf(key);
  return i < 0 ? "" : `${i + 1} of ${keys.length}`;
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
  if (["right", "l", "tab"].includes(k)) return "actions";
  return null;
}

/** A ticket's pull requests in one phrase for the details' header: how their checks stand, else how many merged. */
export function prSummary(prs) {
  if (!prs || prs.length === 0) return "";
  const count = `${prs.length} ${prs.length === 1 ? "PR" : "PRs"}`;
  const marks = prs.map((p) => p.mark);
  if (marks.includes("✕")) return `${count} · ✕ checks failing`;
  if (marks.includes("…")) return `${count} · … checks running`;
  if (marks.includes("✓")) return `${count} · ✓ checks passing`;
  const merged = marks.filter((m) => m === "merged").length;
  return merged > 0 ? `${count} · ${merged} merged` : count;
}

/** Whether a comment's author is a bot or an automation rather than a person. */
export function isBot(who) {
  return /\bbot\b|\[bot\]|automation|\bfor jira\b/i.test(who ?? "");
}
