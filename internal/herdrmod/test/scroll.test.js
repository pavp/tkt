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
