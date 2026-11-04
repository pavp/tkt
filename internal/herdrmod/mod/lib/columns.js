const CHECKS = { fail: ["✕", "blocked"], pending: ["…", "working"], pass: ["✓", "done"] };

/** A pull request on a card: its number and CI mark, as the TUI colors them; merged or closed say so. */
function prMark(pr) {
  if (pr.state === "merged" || pr.state === "closed") return { ref: `#${pr.number}`, mark: pr.state, tone: "dim" };
  const [mark, tone] = CHECKS[pr.checks] ?? ["—", "dim"];
  return { ref: `#${pr.number}`, mark, tone };
}

/** One card from a /v1/board row: the ticket, its card words from the daemon, and its agent's state. */
export function cardOf(row, agents) {
  const t = row.ticket ?? {};
  const card = row.card ?? {};
  const subtask = Boolean(row.parent);
  const name = subtask ? `of ${row.parent}` : t.type || (card.progress ? "" : t.status);
  return {
    key: row.key,
    subtask,
    summary: t.summary ?? "",
    team: Boolean(t.team),
    points: card.points ?? "",
    who: card.who ?? "",
    info: [card.progress, name].filter(Boolean).join(" · "),
    typeKind: card.type_kind ?? "",
    attention: card.attention?.[0] ?? "",
    mark: t.mark ?? "",
    status: t.status ?? "",
    label: t.label ?? "",
    agent: agents?.[row.key] ?? "",
    prs: (t.prs ?? []).filter((p) => p.state !== "merged" && p.state !== "closed").map(prMark),
    finished: card.finished ?? "",
  };
}

/** The Sprint's columns as /v1/board lays them out, each with its count, points and cards. */
export function columnCards(board) {
  return (board?.columns ?? []).map((c) => ({
    title: c.title,
    category: c.category,
    count: c.rows.length,
    points: c.points > 0 ? `${c.points} pts` : "",
    cards: c.rows.map((r) => cardOf(r, board.agents)),
  }));
}

/** Words of text in lines of at most width characters, at most max lines, the last one cut with "…". */
export function wrapWords(text, width, max) {
  const lines = [];
  let line = "";
  for (const word of (text ?? "").split(/\s+/).filter(Boolean)) {
    if (!line) line = word;
    else if (line.length + 1 + word.length <= width) line += " " + word;
    else {
      lines.push(line);
      line = word;
    }
  }
  if (line) lines.push(line);
  if (lines.length <= max) return lines;
  const kept = lines.slice(0, max);
  const last = kept[max - 1];
  kept[max - 1] = (last.length + 1 > width ? last.slice(0, width - 1) : last) + "…";
  return kept;
}

// The TUI's status marks (internal/tui markIcons): an icon, and the theme color nearest the TUI's.
const MARKS = {
  diamond: ["◇", "ansi4"],
  blocked: ["⊘", "ansi3"],
  clock: ["◷", "ansi6"],
  flag: ["⚑", "ansi5"],
  star: ["★", "ansi11"],
  triangle: ["▲", "ansi2"],
};

/** A status mark as the card shows it: its icon, the icon before the status, and its color; null for none. */
export function markOf(mark, status) {
  const m = MARKS[mark];
  return m ? { glyph: m[0], word: `${m[0]} ${status}`, tone: m[1] } : null;
}

/** A list row's PR cell: its first open pull request, else its finished ones, as the TUI's list column says. */
export function prCell(c) {
  const pr = c.prs[0];
  if (pr) return { text: `${pr.ref} ${pr.mark}`, tone: pr.tone ?? "dim" };
  return { text: c.finished || "—", tone: "dim" };
}

const TYPE_ICONS = { bug: ["●", "ansi1"], epic: ["◆", "ansi5"], subtask: ["■", "ansi6"], story: ["■", "ansi2"], task: ["■", "ansi4"] };

/** The icon of a card's Jira type kind, in the TUI's colors (theme ansi names); null without a type. */
export function typeIcon(kind) {
  if (!kind) return null;
  const [glyph, tone] = TYPE_ICONS[kind] ?? ["■", "dim"];
  return { glyph, tone };
}

export function agentGlyph(status) {
  return status === "idle" ? "○" : "●";
}

/** The top right of a card: why it needs you with its status mark's icon, else the mark with its status. */
export function cardFlag(attention, mark) {
  if (attention) return mark ? `! ${attention} ${mark.glyph}` : `! ${attention}`;
  return mark ? mark.word : "";
}

export const EMPTY_COLUMN = "— nothing here";
