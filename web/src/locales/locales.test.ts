// SPDX-License-Identifier: AGPL-3.0-or-later

// The catalog checks of ADR-0071: every key exists in both languages, no
// text is empty, a translation keeps the placeholders and tags of the
// English text, and each area file exists in both folders.
import { readdirSync } from "node:fs";
import { resolve } from "node:path";
import { describe, expect, it } from "vitest";
import { navGroups } from "../nav";
import de from "./de";
import en from "./en";

type Tree = { [k: string]: string | Tree };

function flatten(tree: Tree, prefix = ""): Map<string, string> {
  const out = new Map<string, string>();
  for (const [k, v] of Object.entries(tree)) {
    const key = prefix ? `${prefix}.${k}` : k;
    if (typeof v === "string") out.set(key, v);
    else for (const [kk, vv] of flatten(v, key)) out.set(kk, vv);
  }
  return out;
}

const enKeys = flatten(en as unknown as Tree);
const deKeys = flatten(de as unknown as Tree);

// {{name}} placeholders, $t(...) nesting and <b>-style tags of Trans.
const marks = (s: string) => [...s.matchAll(/\{\{\s*[\w.]+\s*\}\}|\$t\([^)]*\)|<\/?\w+\s*\/?>/g)].map((m) => m[0].replace(/\s/g, "")).sort();

describe("catalogs", () => {
  it("have the same keys in English and German", () => {
    const missing = [...enKeys.keys()].filter((k) => !deKeys.has(k));
    const extra = [...deKeys.keys()].filter((k) => !enKeys.has(k));
    expect({ missingInGerman: missing, onlyInGerman: extra }).toEqual({ missingInGerman: [], onlyInGerman: [] });
  });

  it("have no empty texts", () => {
    const empty = [...enKeys, ...deKeys].filter(([, v]) => v.trim() === "").map(([k]) => k);
    expect(empty).toEqual([]);
  });

  it("keep placeholders and tags in the translation", () => {
    const differ = [...enKeys]
      .filter(([k, v]) => deKeys.has(k) && marks(v).join() !== marks(deKeys.get(k)!).join())
      .map(([k]) => k);
    expect(differ).toEqual([]);
  });

  it("have each area file in both languages", () => {
    const files = (lang: string) =>
      readdirSync(resolve(import.meta.dirname, lang))
        .filter((f) => f.endsWith(".json"))
        .sort();
    expect(files("de")).toEqual(files("en"));
    // Every file is wired into the catalog under its own name.
    expect(files("en").map((f) => f.replace(/\.json$/, ""))).toEqual(Object.keys(en).sort());
  });

  it("name every page and group of the sidebar", () => {
    for (const g of navGroups) {
      if (g.id) expect(en.shell.nav.groups).toHaveProperty(g.id);
      for (const i of g.items) expect(en.shell.nav.pages).toHaveProperty(i.page);
    }
  });
});
