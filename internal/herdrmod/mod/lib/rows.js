/** The list rows' cells as the TUI words and colors them (Sprint list, PRs tab, Backlog). Pure, for `node --test`.
 *  Tones are theme names: ansi0..15, dim, accent, foreground. */

const COLUMN_TONES = { todo: "dim", in_progress: "ansi4", review: "ansi5", done: "ansi2" };
const CHECK_MARKS = { pass: "✓", fail: "✕", pending: "…" };
const AGENT_WORDS = { working: "● working", blocked: "● needs you", done: "● done", idle: "○ idle" };

/** The gutter before a row: ▸ when selected, ! when it needs you (with its status mark's icon), else the mark. */
export function gutterOf({ selected = false, attention = "", needsYou = false, mark = null }) {
  if (selected) return { text: "▸", tone: "accent" };
  if (needsYou) return { text: "!", tone: "ansi3" };
  if (attention) return { text: mark ? `!${mark.glyph}` : "!", tone: "ansi3" };
  if (mark) return { text: mark.glyph, tone: mark.tone };
  return { text: "", tone: "dim" };
}

export function statusTone(category) {
  return COLUMN_TONES[category] ?? "foreground";
}

/** A PRs tab row's STATUS color: review requests mauve, needing you peach, draft dim, approved or ready green,
 *  waiting review mauve, CI running in the accent. */
export function prStatusTone(row) {
  const pr = row.pr ?? {};
  if (row.review) return "ansi5";
  if (row.needs_you) return "ansi3";
  if (pr.draft) return "dim";
  if (pr.merge === "ready" || pr.review === "approved") return "ansi2";
  if (pr.review === "review_required") return "ansi5";
  if (pr.checks === "pending") return "accent";
  return "foreground";
}

export function agentWord(status) {
  return AGENT_WORDS[status] ?? "";
}

const ref = (pr) => (pr.state === "merged" || pr.state === "closed" ? `#${pr.number} ${pr.state}` : `#${pr.number} ${CHECK_MARKS[pr.checks] ?? "—"}`);

/** Every open pull request with its CI mark, then the finished one or how many finished. */
export function prRefs(prs) {
  if (!prs?.length) return "—";
  const open = prs.filter((p) => p.state !== "merged" && p.state !== "closed");
  const finished = prs.filter((p) => p.state === "merged" || p.state === "closed");
  const parts = open.map(ref);
  if (finished.length === 1) parts.push(ref(finished[0]));
  else if (finished.length > 1) {
    const merged = finished.filter((p) => p.state === "merged").length;
    const closed = finished.length - merged;
    parts.push([merged && `${merged} merged`, closed && `${closed} closed`].filter(Boolean).join(" · "));
  }
  return parts.join(" ");
}

/** Which optional columns a list shows: SP when a ticket is estimated, ASSIGNEE whenever tickets show (the TUI
 *  hides it only on your own pull requests), AGENT when an agent runs. */
export function listColumns(rows, agents) {
  const tickets = rows.map((r) => r.ticket ?? {});
  return {
    points: tickets.some((t) => t.points),
    assignee: tickets.length > 0,
    agent: Object.keys(agents ?? {}).length > 0,
  };
}

export function pointsText(points) {
  return points ? String(points) : "";
}
