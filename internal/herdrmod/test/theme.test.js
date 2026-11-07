import { test } from "node:test";
import assert from "node:assert/strict";

const herdrDark = {
  background: "#1e1e2e", surface: "#1c1f24", raised: "#22262c", chrome: "#16191d", popover: "#22262c",
  foreground: "#e6e7e8", muted: "#6c7086", dim: "#585b70", accent: "#89b4fa",
  working: "#f9e2af", blocked: "#f38ba8", done: "#94e2d5", idle: "#a6e3a1", unknown: "#6c7086",
  ansi1: "#e06c75", ansi2: "#98c379", ansi3: "#e5c07b", ansi4: "#61afef", ansi5: "#c678dd", ansi6: "#56b6c2", ansi8: "#5c6370",
};
const herdrLight = { ...herdrDark, background: "#fafafa", surface: "#ffffff", raised: "#f2f2f4", chrome: "#eeeef0", foreground: "#2a2c33", muted: "#a0a1a7", dim: "#c0c1c6", ansi3: "#c18401" };

test("contrast is the WCAG ratio, and ensure raises a color to it keeping its hue", async () => {
  const { contrast, ensure } = await import("../mod/lib/theme.js");
  assert.equal(Math.round(contrast("#000000", "#ffffff")), 21);
  const raised = ensure("#6c7086", "#22262c", 4.5);
  assert.ok(contrast(raised, "#22262c") >= 4.5, `${raised} reads at ${contrast(raised, "#22262c")}`);
  assert.notEqual(raised, "#6c7086");
  assert.equal(ensure("#e6e7e8", "#22262c", 4.5), "#e6e7e8", "a color that already reads stays as it is");
  const darker = ensure("#c18401", "#ffffff", 4.5);
  assert.ok(contrast(darker, "#ffffff") >= 4.5);
  const [r, g, b] = [1, 3, 5].map((i) => parseInt(darker.slice(i, i + 2), 16));
  assert.ok(r > g && g > b, `${darker} stays a yellow`);
});

test("ensure goes toward whichever of black or white reads better on a mid-tone background", async () => {
  const { contrast, ensure } = await import("../mod/lib/theme.js");
  const c = ensure("#555555", "#909090", 4.5);
  assert.ok(contrast(c, "#909090") >= 4.5, `${c} reads at ${contrast(c, "#909090")}`);
});

test("tkt's theme keeps Herdr's backgrounds and agent colors, and makes every text color read at 4.5:1", async () => {
  const { tktTheme, contrast } = await import("../mod/lib/theme.js");
  for (const palette of ["herdr", "tkt"]) {
    const t = tktTheme(herdrDark, palette);
    for (const k of ["background", "surface", "raised", "chrome", "popover", "accent", "working", "blocked", "done", "idle"]) {
      assert.equal(t[k], herdrDark[k], `${palette}: ${k} is Herdr's`);
    }
    for (const k of ["foreground", "muted", "dim"]) {
      for (const bg of ["surface", "raised", "chrome"]) assert.ok(contrast(t[k], t[bg]) >= 4.5, `${palette}: ${k} on ${bg} reads at ${contrast(t[k], t[bg])}`);
    }
    assert.equal(t.peach, "#fab387");
    assert.equal(t.palette, palette);
  }
});

test("the column and PR colors come from Herdr's ANSI colors, or from Catppuccin with the tkt palette", async () => {
  const { tktTheme, contrast } = await import("../mod/lib/theme.js");
  const herdr = tktTheme(herdrDark, "herdr");
  assert.equal(herdr.ansi4, "#61afef");
  const mocha = tktTheme(herdrDark, "tkt");
  assert.deepEqual([mocha.ansi1, mocha.ansi2, mocha.ansi3, mocha.ansi4, mocha.ansi5, mocha.ansi6], ["#f38ba8", "#a6e3a1", "#f9e2af", "#89b4fa", "#cba6f7", "#94e2d5"]);
  const latte = tktTheme(herdrLight, "tkt");
  assert.equal(latte.ansi4, "#1e66f5", "a light Herdr theme takes Catppuccin Latte");
  for (const k of ["ansi1", "ansi2", "ansi3", "ansi4", "ansi5", "ansi6", "ansi8"]) {
    assert.ok(contrast(tktTheme(herdrLight, "herdr")[k], herdrLight.surface) >= 3, `${k} reads as a mark on the light theme`);
  }
  assert.equal(tktTheme(herdrDark).palette, "herdr", "Herdr's colors unless tkt's are chosen");
});

test("the mono font is the terminal's from Herdr's settings, else a known coding font installed", async () => {
  const { monoFont } = await import("../mod/lib/theme.js");
  assert.equal(monoFont({ font_family: "Iosevka Term" }, ["Iosevka Term", "Inter"]), "Iosevka Term");
  assert.equal(monoFont({}, ["Inter", "JetBrains Mono", "Menlo"]), "JetBrains Mono");
  assert.equal(monoFont({}, ["Inter"]), "");
});
