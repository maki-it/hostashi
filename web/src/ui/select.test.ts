// SPDX-License-Identifier: AGPL-3.0-or-later

import { readFileSync, readdirSync } from "node:fs";
import { resolve } from "node:path";
import { describe, expect, it } from "vitest";

const src = resolve(import.meta.dirname, "..");

function tsxFiles(dir: string): string[] {
  return readdirSync(dir, { withFileTypes: true, recursive: true })
    .filter((e) => e.isFile() && e.name.endsWith(".tsx") && !e.name.endsWith(".test.tsx"))
    .map((e) => resolve(e.parentPath, e.name));
}

// Native selects look different in every browser (Safari draws a grey pill),
// so each one must carry ui-select, which draws the shared look.
describe("select elements", () => {
  it("all use the ui-select class", () => {
    const bad: string[] = [];
    for (const file of tsxFiles(src)) {
      const text = readFileSync(file, "utf8");
      for (const m of text.matchAll(/<select\b[^>]*?>/gs)) {
        if (!/className="[^"]*\bui-select\b/.test(m[0])) bad.push(`${file.slice(src.length + 1)}: ${m[0].slice(0, 60)}`);
      }
    }
    expect(bad).toEqual([]);
  });
});
