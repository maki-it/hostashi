// SPDX-License-Identifier: AGPL-3.0-or-later

import { readFileSync, readdirSync } from "node:fs";
import { resolve } from "node:path";
import { describe, expect, it } from "vitest";

const dir = import.meta.dirname;
const tokens = readFileSync(resolve(dir, "tokens.css"), "utf8");

// block returns the custom properties of the first rule whose selector is
// exactly sel, as a map from name to value.
function block(css: string, sel: string): Map<string, string> {
  const start = css.indexOf(`${sel} {`);
  if (start < 0) throw new Error(`no rule ${sel}`);
  const body = css.slice(css.indexOf("{", start) + 1, css.indexOf("}", start));
  const vars = new Map<string, string>();
  for (const m of body.matchAll(/(--[\w-]+):\s*([^;]+);/g)) vars.set(m[1]!, m[2]!.trim());
  return vars;
}

const light = block(tokens, ':root[data-theme="default"]');
const darkChosen = block(tokens, ':root[data-theme="default"][data-scheme="dark"]');
const darkSystem = block(tokens, ':root[data-theme="default"]:not([data-scheme="light"])');
const dark = new Map([...light, ...darkChosen]);

function luminance(hex: string): number {
  const m = /^#([0-9a-f]{6})$/i.exec(hex);
  if (!m) throw new Error(`not a #rrggbb colour: ${hex}`);
  const [r, g, b] = [0, 2, 4].map((i) => {
    const v = parseInt(m[1]!.slice(i, i + 2), 16) / 255;
    return v <= 0.03928 ? v / 12.92 : ((v + 0.055) / 1.055) ** 2.4;
  }) as [number, number, number];
  return 0.2126 * r + 0.7152 * g + 0.0722 * b;
}

function contrast(a: string, b: string): number {
  const [x, y] = [luminance(a), luminance(b)].sort((p, q) => q - p) as [number, number];
  return (x + 0.05) / (y + 0.05);
}

// Pairs the components draw: [foreground, background, minimum ratio].
// 4.5:1 for text, 3:1 for control borders and the focus ring (WCAG 2.2 AA).
const pairs: [string, string, number][] = [
  ["--fg", "--bg", 4.5],
  ["--fg", "--surface", 4.5],
  ["--fg", "--sunken", 4.5],
  ["--fg", "--selection", 4.5],
  ["--muted", "--surface", 4.5],
  ["--muted", "--sunken", 4.5],
  ["--accent", "--surface", 4.5],
  ["--accent", "--bg", 4.5],
  ["--accent", "--sunken", 4.5],
  ["--on-primary", "--primary", 4.5],
  ["--on-brand", "--brand", 4.5],
  ["--ok", "--ok-bg", 4.5],
  ["--warn", "--warn-bg", 4.5],
  ["--err", "--err-bg", 4.5],
  ["--err", "--surface", 4.5],
  ["--tip-fg", "--tip-bg", 4.5],
  ["--side-fg", "--side-bg", 4.5],
  ["--side-muted", "--side-bg", 4.5],
  ["--side-active-fg", "--side-active-bg", 4.5],
  ["--control-border", "--surface", 3],
  ["--focus", "--surface", 3],
  ["--focus", "--bg", 3],
  ["--active-bar", "--side-active-bg", 3],
  ["--series-1", "--surface", 3],
  ["--series-2", "--surface", 3],
];

describe("theme tokens", () => {
  it("defines the same names in light and dark", () => {
    expect([...darkChosen.keys()].every((k) => light.has(k))).toBe(true);
    expect(darkSystem).toEqual(darkChosen);
  });

  for (const [name, scheme] of [
    ["light", light],
    ["dark", dark],
  ] as const) {
    it.each(pairs)(`${name}: %s on %s reaches %d:1`, (fg, bg, min) => {
      const ratio = contrast(scheme.get(fg)!, scheme.get(bg)!);
      expect(ratio, `${fg} ${scheme.get(fg)} on ${bg} ${scheme.get(bg)}`).toBeGreaterThanOrEqual(min);
    });
  }
});

describe("component styles", () => {
  const sheets = readdirSync(dir).filter((f) => f.endsWith(".css") && f !== "tokens.css");

  it.each(sheets)("%s uses no raw colours", (file) => {
    const css = readFileSync(resolve(dir, file), "utf8");
    expect(css).not.toMatch(/#[0-9a-f]{3,8}\b|\brgba?\(|\bhsla?\(/i);
  });

  // Measured sizes that components set at runtime, not design tokens.
  const runtime = new Set(["--files-bar-h", "--files-sticky-h"]);

  it.each(sheets)("%s uses only defined tokens", (file) => {
    const css = readFileSync(resolve(dir, file), "utf8");
    const used = new Set([...css.matchAll(/var\((--[\w-]+)/g)].map((m) => m[1]!));
    expect([...used].filter((v) => !light.has(v) && !runtime.has(v))).toEqual([]);
  });

  // The sheets use no nesting, so a rule that starts at the left margin
  // while another is still open lost its closing brace (as after a merge).
  it.each(sheets)("%s closes every rule", (file) => {
    const css = readFileSync(resolve(dir, file), "utf8").replace(/\/\*[\s\S]*?\*\//g, "");
    let depth = 0;
    let inMedia = false;
    const open: number[] = [];
    css.split("\n").forEach((line, i) => {
      if (line && !/^[\s}]/.test(line) && depth > (inMedia ? 1 : 0) && !line.startsWith("@")) open.push(i + 1);
      if (depth === 0) inMedia = line.startsWith("@");
      depth += (line.match(/{/g) ?? []).length - (line.match(/}/g) ?? []).length;
    });
    expect(open).toEqual([]);
    expect(depth).toBe(0);
  });
});
