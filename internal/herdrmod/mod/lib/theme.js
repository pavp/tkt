/** tkt's colors over Herdr's theme: Herdr's backgrounds and agent colors, column and PR colors from Herdr's ANSI
 *  colors or from Catppuccin (the tkt palette), and every text color raised until it reads. Pure, for `node --test`. */

import { PEACH } from "./columns.js";

export const CATPPUCCIN = {
  mocha: { ansi1: "#f38ba8", ansi2: "#a6e3a1", ansi3: "#f9e2af", ansi4: "#89b4fa", ansi5: "#cba6f7", ansi6: "#94e2d5", ansi8: "#9399b2" },
  latte: { ansi1: "#d20f39", ansi2: "#40a02b", ansi3: "#df8e1d", ansi4: "#1e66f5", ansi5: "#8839ef", ansi6: "#179299", ansi8: "#7c7f93" },
};

const TEXT = 4.5;
const MARK = 3;
const MONO = ["JetBrains Mono", "Geist Mono", "SF Mono", "Menlo", "Cascadia Code", "Fira Code", "Consolas", "DejaVu Sans Mono", "Monaco"];

const rgb = (h) => [1, 3, 5].map((i) => parseInt(h.slice(i, i + 2), 16));
const hex = (c) => "#" + c.map((v) => Math.round(Math.max(0, Math.min(255, v))).toString(16).padStart(2, "0")).join("");
const isHex = (c) => typeof c === "string" && /^#[0-9a-fA-F]{6}$/.test(c);

/** a laid over b at weight t (0 to 1). */
export function mix(a, b, t) {
  const x = rgb(a), y = rgb(b);
  return hex(x.map((v, i) => v * t + y[i] * (1 - t)));
}

function luminance(c) {
  const [r, g, b] = rgb(c).map((v) => {
    const s = v / 255;
    return s <= 0.03928 ? s / 12.92 : Math.pow((s + 0.055) / 1.055, 2.4);
  });
  return 0.2126 * r + 0.7152 * g + 0.0722 * b;
}

/** The WCAG contrast ratio of two colors, 1 to 21. */
/** The veil behind menus and dialogs: black, lighter over a light surface, so the board behind clearly waits. */
export function scrim(surface) {
  return isHex(surface) && luminance(surface) > 0.4 ? "#00000052" : "#000000a6";
}

export function contrast(a, b) {
  const x = luminance(a), y = luminance(b);
  return (Math.max(x, y) + 0.05) / (Math.min(x, y) + 0.05);
}

/** fg, darkened over a light bg or lightened over a dark one in small steps, until it reads at min against bg. */
export function ensure(fg, bg, min) {
  if (!isHex(fg) || !isHex(bg)) return fg;
  const toward = contrast("#000000", bg) >= contrast("#ffffff", bg) ? "#000000" : "#ffffff";
  let c = fg;
  for (let i = 1; contrast(c, bg) < min && i <= 20; i++) c = mix(toward, fg, i * 0.05);
  return c;
}

/** fg raised until it reads at min against every one of bgs. */
function readable(fg, bgs, min) {
  return bgs.filter(isHex).reduce((c, bg) => ensure(c, bg, min), fg);
}

/** The colors the palette gives before tkt raises them: Herdr's theme, with Catppuccin's (Latte on a light theme) for
 *  palette "tkt". */
export function sourceColors(theme, palette = "herdr") {
  const light = isHex(theme.surface) && luminance(theme.surface) > 0.4;
  return palette === "tkt" ? { ...theme, ...CATPPUCCIN[light ? "latte" : "mocha"] } : { ...theme };
}

/** Herdr's theme with tkt's colors over it. palette "tkt" takes Catppuccin (Latte on a light theme); any other
 *  value keeps Herdr's ANSI colors. Backgrounds, the accent and the agent colors stay Herdr's. */
export function tktTheme(theme, palette = "herdr") {
  const name = palette === "tkt" ? "tkt" : "herdr";
  const light = isHex(theme.surface) && luminance(theme.surface) > 0.4;
  const well = isHex(theme.surface) ? mix("#000000", theme.surface, light ? 0.04 : 0.14) : theme.surface;
  const pill = isHex(theme.raised) && isHex(theme.foreground) ? mix(theme.foreground, theme.raised, 0.06) : theme.raised;
  const bgs = [theme.surface, theme.raised, theme.chrome, theme.popover, well, pill];
  const card = !isHex(well) ? theme.raised : light ? mix("#ffffff", well, 0.8) : isHex(theme.foreground) ? mix(theme.foreground, well, 0.05) : theme.raised;
  bgs.push(card);
  const out = { ...sourceColors(theme, name), peach: PEACH, palette: name, well, pill, card };
  for (const k of ["foreground", "muted", "dim"]) if (theme[k]) out[k] = readable(theme[k], bgs, TEXT);
  for (const k of ["ansi1", "ansi2", "ansi3", "ansi4", "ansi5", "ansi6", "ansi8"]) if (out[k]) out[k] = readable(out[k], bgs, MARK);
  return out;
}

/** The font for keys, numbers and paths: the terminal's from Herdr's settings, else a known coding font installed,
 *  else none (the UI font). */
export function monoFont(settings, installed) {
  const fonts = installed ?? [];
  const own = settings?.font_family;
  if (typeof own === "string" && own && (fonts.length === 0 || fonts.includes(own))) return own;
  return MONO.find((f) => fonts.includes(f)) ?? "";
}
