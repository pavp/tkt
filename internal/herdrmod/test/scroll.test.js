import { test } from "node:test";
import assert from "node:assert/strict";
import { scrolled, menuHeight, MENU_ROW, cardHeight } from "../mod/lib/scroll.js";

test("a pane scrolls by a line with up and down, by a page with pgup, pgdown and space, and never past its ends, as the TUI's details do", () => {
  const pane = { offset: 0, view: 300, content: 1000 };
  assert.equal(scrolled(pane, "down"), 24);
  assert.equal(scrolled(pane, "j"), 24);
  assert.equal(scrolled({ ...pane, offset: 24 }, "up"), 0);
  assert.equal(scrolled(pane, "up"), 0);
  assert.equal(scrolled(pane, "pagedown"), 276);
  assert.equal(scrolled(pane, " "), 276);
  assert.equal(scrolled({ ...pane, offset: 600 }, "pagedown"), 700);
  assert.equal(scrolled({ ...pane, offset: 300 }, "pageup"), 24);
  assert.equal(scrolled({ ...pane, offset: 300 }, "home"), 0);
  assert.equal(scrolled(pane, "end"), 700);
  assert.equal(scrolled({ offset: 0, view: 500, content: 200 }, "down"), 0);
  assert.equal(scrolled(pane, "x"), null);
});

test("a long menu keeps inside the window, scrolling its items, as the TUI's menus fit the screen", () => {
  assert.equal(menuHeight(4, 700), 4 * MENU_ROW);
  assert.equal(menuHeight(30, 700), Math.floor((700 - 220) / MENU_ROW) * MENU_ROW);
  assert.equal(menuHeight(30, 0), 12 * MENU_ROW);
});

test("a card's height follows its summary lines and its label line, so the column can scroll to it", () => {
  const short = cardHeight({ lines: 1, label: false });
  assert.equal(cardHeight({ lines: 2, label: false }) - short, 20);
  assert.equal(cardHeight({ lines: 1, label: true }) - short, 25);
  assert.ok(cardHeight({ lines: 2, label: true, compact: true }) < short, "a compact card shows only its first row and one title line");
  assert.equal(cardHeight({ lines: 2, label: true, compact: true }), cardHeight({ lines: 1, label: false, compact: true }));
});

test("a column counts the cards below what it shows, for its ↓ N more", async () => {
  const { cardsBelow } = await import("../mod/lib/scroll.js");
  assert.equal(cardsBelow([100, 100, 100], 8, { offset: 0, view: 0 }), 0, "an unmeasured column hides nothing");
  assert.equal(cardsBelow([100, 100, 100], 8, { offset: 0, view: 220 }), 1);
  assert.equal(cardsBelow([100, 100, 100], 8, { offset: 0, view: 150 }), 2, "a card cut by the bottom counts as below");
  assert.equal(cardsBelow([100, 100, 100], 8, { offset: 116, view: 220 }), 0);
});

test("moving the selection scrolls a column just enough to show the card, as the TUI's columns follow it", async () => {
  const { revealed } = await import("../mod/lib/scroll.js");
  assert.equal(revealed({ offset: 0, view: 300 }, 100, 80), 0);
  assert.equal(revealed({ offset: 0, view: 300 }, 280, 80), 60);
  assert.equal(revealed({ offset: 200, view: 300 }, 100, 80), 100);
  assert.equal(revealed({ offset: 0, view: 0 }, 280, 80), 0);
});

test("a pane scrolls just enough to show the item the keyboard reached, with a small margin", async () => {
  const { keepVisible } = await import("../mod/lib/scroll.js");
  const pane = { offset: 0, view: 300, content: 900 };
  assert.equal(keepVisible(pane, 100, 30), 0, "an item already in view keeps the offset");
  assert.equal(keepVisible(pane, 400, 30), 138, "an item below scrolls up to sit at the bottom");
  assert.equal(keepVisible({ ...pane, offset: 500 }, 200, 30), 192, "an item above scrolls down to sit at the top");
  assert.equal(keepVisible(pane, 880, 30), 600, "never past the end");
  assert.equal(keepVisible({ ...pane, offset: 50 }, 4, 30), 0, "never before the start");
});

test("Settings › Statuses counts the statuses of every column below what the page shows", async () => {
  const { rowsBelow } = await import("../mod/lib/scroll.js");
  const rows = { top: 100, row: 34, gap: 6 };
  assert.equal(rowsBelow([3, 1], rows, { offset: 0, view: 0 }), 0, "an unmeasured page hides nothing");
  assert.equal(rowsBelow([3, 1], rows, { offset: 0, view: 600 }), 0, "every status fits");
  assert.equal(rowsBelow([10, 4, 1], rows, { offset: 0, view: 300 }), 5 + 0 + 0, "rows start at 100, so 5 of 10 fit in 300");
  assert.equal(rowsBelow([10, 4, 1], rows, { offset: 200, view: 300 }), 0, "scrolled to the end, nothing is left below");
});

test("Settings › Statuses scrolls the page to the status the keyboard moves to", async () => {
  const { rowTop } = await import("../mod/lib/scroll.js");
  assert.equal(rowTop({ top: 100, row: 34, gap: 6 }, 0), 100);
  assert.equal(rowTop({ top: 100, row: 34, gap: 6 }, 3), 220);
});
