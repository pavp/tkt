/** The "Review requested" strip above the Sprint, as the TUI draws it. Pure, for `node --test`. */

/** The chips that fit (at most max) and the "+N more · press 2" label for the rest. */
export function stripChips(reviews, max) {
  const all = reviews ?? [];
  const chips = all.slice(0, max);
  const rest = all.length - chips.length;
  return { chips, more: rest > 0 ? `+${rest} more · press 2` : "" };
}

/** Whether up from selected leaves the tickets for the strip: from the first row of a list, the top of a column,
 *  or nothing selected. */
export function atTop(targets, selected) {
  if (targets.cols) {
    const col = targets.cols.find((c) => c.includes(selected));
    return !col || col.indexOf(selected) === 0;
  }
  return !selected || targets.ids.indexOf(selected) <= 0;
}
