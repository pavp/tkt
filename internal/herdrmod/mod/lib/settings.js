// Settings as the TUI's sections (internal/tui/settings.go), each change a PATCH /v1/settings of only what changed.

export const SECTIONS = [
  { id: "spaces", title: "Spaces & repositories" },
  { id: "boards", title: "Team boards" },
  { id: "statuses", title: "Statuses" },
  { id: "clones", title: "Local clones" },
  { id: "board", title: "Board" },
  { id: "colors", title: "Colors" },
  { id: "machine", title: "Machine" },
];

/** The mod's colors: the Herdr theme's ("herdr") or tkt's own ("tkt"). */
export function palettePatch(palette) {
  return { palette };
}

/** A space's repositories with one added ({repo, path}) or one removed (its name). */
export function reposPatch(space, change) {
  let repos = (space.repos ?? []).map((r) => ({ repo: r.repo, path: r.path }));
  if (change.remove) repos = repos.filter((r) => r.repo !== change.remove);
  if (change.add) repos.push(change.add);
  return { spaces: { [space.key]: { repos } } };
}

/** A space's team boards with board id turned on or off. */
export function boardsPatch(space, id) {
  const ids = (space.team_boards ?? []).map((b) => b.id);
  return { spaces: { [space.key]: { team_boards: ids.includes(id) ? ids.filter((b) => b !== id) : [...ids, id] } } };
}

/** A status's column or mark ({column} or {mark}), or null to bring it back to automatic. */
export function statusPatch(space, name, change) {
  return { spaces: { [space]: { statuses: { [name]: change } } } };
}

/** The clones folders with one added or removed. */
export function rootsPatch(roots, change) {
  let next = [...(roots ?? [])];
  if (change.remove) next = next.filter((r) => r !== change.remove);
  if (change.add && !next.includes(change.add)) next.push(change.add);
  return { repo_roots: next };
}

/** The Statuses section's rows: each status's column (by title) and mark, and whether either was set by hand. */
export function statusRows(view, columns) {
  const title = Object.fromEntries((columns ?? []).map((c) => [c.id, c.title]));
  return (view.statuses ?? []).map((s) => ({
    name: s.name, column: title[s.column] ?? s.column, columnAuto: s.column_auto, mark: s.mark, markAuto: s.mark_auto, changed: !s.column_auto || !s.mark_auto,
  }));
}

/** The Statuses board: the board's columns in order, each with its statuses, each saying its mark and whether you set
 *  its column or mark (else both are automatic). */
export function statusBoard(view, columns) {
  return (columns ?? []).map((c) => ({
    id: c.id,
    title: c.title,
    statuses: (view?.statuses ?? []).filter((s) => s.column === c.id)
      .map((s) => ({ name: s.name, mark: s.mark, manual: !s.column_auto || !s.mark_auto, markAuto: s.mark_auto })),
  }));
}

/** The column beside id in dir (1 right, -1 left), or null at the board's end. */
export function nextColumn(columns, id, dir) {
  const i = (columns ?? []).findIndex((c) => c.id === id);
  const next = columns?.[i + dir];
  return i < 0 || !next ? null : next.id;
}

const MARK_GLYPHS = { none: "—", diamond: "◇", blocked: "⊘", clock: "◷", flag: "⚑", star: "★", triangle: "▲" };

/** The marks the picker offers, each with its icon. */
export function markChoices(marks) {
  return (marks ?? []).map((name) => ({ name, glyph: MARK_GLYPHS[name] ?? "·" }));
}
