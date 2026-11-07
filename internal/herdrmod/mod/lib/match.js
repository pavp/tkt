/** Lower case without accents, so "Revisión" and "revision" compare equal. */
export function fold(s) {
  return (s ?? "").normalize("NFD").replace(/\p{M}/gu, "").toLowerCase();
}

/** 0 when the name starts with the text (or the text is empty), 1 when it only contains it, -1 when it does not. */
export function matchRank(name, text) {
  const q = fold(text).trim();
  if (!q) return 0;
  const n = fold(name);
  if (n.startsWith(q)) return 0;
  return n.includes(q) ? 1 : -1;
}

/** The items whose name matches the text: those starting with it first, each group in its given order. */
export function matching(items, text, nameOf = (x) => x) {
  const ranked = items.map((item) => ({ item, rank: matchRank(nameOf(item), text) })).filter((r) => r.rank >= 0);
  return [...ranked.filter((r) => r.rank === 0), ...ranked.filter((r) => r.rank === 1)].map((r) => r.item);
}
