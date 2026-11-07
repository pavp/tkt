const CHECKS = { fail: ["✕", "blocked"], pending: ["…", "working"], pass: ["✓", "done"] };

/** The details screen of GET /v1/tickets/{key}, opened from the tab named from. */
export function detailView(d, from) {
  const t = d.ticket ?? {};
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
    prs: (t.prs ?? []).map((p) => {
      const [mark, tone] = p.state === "open" ? CHECKS[p.checks] ?? ["—", "dim"] : [p.state, "dim"];
      return { ref: `${p.repo}#${p.number}`, title: p.title, url: p.url, mark, tone };
    }),
    comments: (d.comments ?? []).map((c) => ({ who: c.author, when: (c.created ?? "").slice(0, 10), body: c.body })),
    work: (d.work ?? []).map((w) => ({ where: `${w.repo} · ${w.branch}`, path: w.path ?? "" })),
  };
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

const SECTION = /^([A-Z][\p{L}\d]*(?: [\p{L}\d]+){0,3}):\s*$/u;
const LEAD = /^([A-Z][\p{L}\d]*(?: [\p{L}\d]+)?):\s+(\S.*)$/u;

/** Jira text with its own sections ("Acceptance criteria:" alone on a line) as headings and a leading one- or
 *  two-word label ("Narrative: …") in bold, leaving code blocks as they are. */
export function structureText(text) {
  let code = false;
  return (text ?? "").split("\n").map((line) => {
    if (line.trimStart().startsWith("```")) code = !code;
    if (code || line.trimStart().startsWith("```")) return line;
    const section = line.match(SECTION);
    if (section) return `#### ${section[1]}`;
    const lead = line.match(LEAD);
    return lead ? `**${lead[1]}:** ${lead[2]}` : line;
  }).join("\n");
}
