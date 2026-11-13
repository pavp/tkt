/** Scrolling from the keyboard where GPUI scrolls only virtual lists from code. Pure, for `node --test`. */

const LINE = 24;
const CHIP_GAP = 4;

/** The pane's new offset for a key (pane = {offset, view, content} in px), or null when the key does not scroll. */
export function scrolled({ offset, view, content }, k) {
  const max = Math.max(0, content - view);
  const page = Math.max(LINE, view - LINE);
  const to = { down: offset + LINE, j: offset + LINE, s: offset + LINE, up: offset - LINE, k: offset - LINE, w: offset - LINE,
    pagedown: offset + page, " ": offset + page, f: offset + page, pageup: offset - page, b: offset - page, home: 0, end: max }[k];
  return to === undefined ? null : Math.max(0, Math.min(max, to));
}

export const MENU_ROW = 28;

/** How tall a menu's item list is: all of it, or as much as the window leaves (12 rows before it is measured). */
export function menuHeight(items, windowHeight) {
  const fits = windowHeight > 0 ? Math.max(4, Math.floor((windowHeight - 220) / MENU_ROW)) : 12;
  return Math.min(items, fits) * MENU_ROW;
}

/** A card's height in px: its padding and border, the key, summary, pull request and details lines, its pull
 *  request chips in the rows they pack into, and its label line when the board has a label field. */
export function cardHeight({ lines, chips = 1, label, compact = false }) {
  if (compact) return 2 + 16 + 18 + 5 + 20;
  const rows = Math.max(1, chips);
  return 2 + 20 + 18 + 7 + lines * 20 + 7 + rows * 20 + (rows - 1) * CHIP_GAP + (label ? 7 + 18 : 0) + 7 + 16;
}

/** How many cards of heights, gap apart, end below what a pane shows, for its "↓ N more"; none while unmeasured. */
export function cardsBelow(heights, gap, { offset, view }) {
  if (view <= 0) return 0;
  let top = 0;
  let n = 0;
  for (const h of heights) {
    if (top + h > offset + view) n++;
    top += h + gap;
  }
  return n;
}

/** How many rows of the lanes, each counts[i] rows `row` tall and `gap` apart from `top` down, sit below the pane. */
export function rowsBelow(counts, { top, row, gap }, { offset, view }) {
  return counts.reduce((n, c) => n + cardsBelow(Array(c).fill(row), gap, { offset: offset - top, view }), 0);
}

/** Where the i-th of such rows starts. */
export function rowTop({ top, row, gap }, i) {
  return top + i * (row + gap);
}

/** The offset that shows a card at top..top+height in a pane, moving as little as it can; unmeasured panes stay. */
export function revealed({ offset, view }, top, height) {
  if (view <= 0) return offset;
  if (top < offset) return top;
  if (top + height > offset + view) return top + height - view;
  return offset;
}

const MARGIN = 8;

/** The offset that shows an item from top to top + height in a pane, moving the pane as little as it can. */
export function keepVisible({ offset, view, content }, top, height) {
  const max = Math.max(0, content - view);
  let to = offset;
  if (top - MARGIN < offset) to = top - MARGIN;
  else if (top + height + MARGIN > offset + view) to = top + height + MARGIN - view;
  return Math.max(0, Math.min(max, to));
}
