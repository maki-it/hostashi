// SPDX-License-Identifier: AGPL-3.0-or-later

// The second check of ADR-0071: text a user reads must come from the
// catalog through t(), not stand in the JSX. It reports text between tags,
// string literals in {…} between tags, and string literals in text
// attributes such as label, title or placeholder. Pages not yet moved to
// the catalog are listed in pending.json; that list only shrinks.
import { readFileSync, readdirSync } from "node:fs";
import { relative, resolve } from "node:path";
import { parseAst } from "vite";
import { describe, expect, it } from "vitest";
import pending from "./pending.json";

const src = resolve(import.meta.dirname, "..");

// Attributes whose value a user reads (or hears). aria-labelledby and
// aria-describedby hold ids, not text.
const textAttr = /^(aria-label|aria-description|aria-roledescription|aria-valuetext|alt|placeholder|title|label|hint|description|legend|tip|data-tip|summary|empty|confirm\w*|\w+Label|\w+Title|\w+Text|\w+Hint|\w+Message)$/;

// Text that reads the same in every language: the product name, keys,
// software and technical names.
const sameEverywhere = new Set(["Hostashi", "Esc", "ClamAV", "CPU", "UTF-8", "CRLF", "LF"]);


// One token with a path, domain, flag or code character in it, such as
// "bin/server", "example.com" or "--config", is an example, not prose.
const looksLikeCode = (s: string) => /^\S+$/.test(s) && /[/.:@_=<>]|^-/.test(s);

const isText = (s: string) => /\p{L}/u.test(s) && !sameEverywhere.has(s.trim()) && !looksLikeCode(s.trim());

type Node = { type: string; start: number; [k: string]: unknown };
type Finding = { line: number; text: string };

function lineOf(code: string, at: number): number {
  return code.slice(0, at).split("\n").length;
}

/** literals returns the string literals an expression shows as it is: itself, or a branch of ?:, && or ??. */
function literals(e: Node | null | undefined): Node[] {
  if (!e) return [];
  switch (e.type) {
    case "Literal":
      return typeof e.value === "string" ? [e] : [];
    case "TemplateLiteral":
      return [e];
    case "ConditionalExpression":
      return [...literals(e.consequent as Node), ...literals(e.alternate as Node)];
    case "LogicalExpression":
      return [...literals(e.left as Node), ...literals(e.right as Node)];
    default:
      return [];
  }
}

function literalText(n: Node): string {
  if (n.type === "Literal") return String(n.value);
  return ((n.quasis as { value: { cooked: string } }[]) ?? []).map((q) => q.value.cooked).join(" ");
}

/** findings lists the user-facing text in the JSX of a file. */
export function findings(code: string): Finding[] {
  const out: Finding[] = [];
  const report = (n: Node, text: string) => {
    if (isText(text)) out.push({ line: lineOf(code, n.start), text: text.trim().replace(/\s+/g, " ") });
  };
  const visit = (n: unknown) => {
    if (!n || typeof n !== "object") return;
    if (Array.isArray(n)) {
      n.forEach(visit);
      return;
    }
    const node = n as Node;
    if (node.type === "JSXText") report(node, String(node.value));
    if ((node.type === "JSXElement" || node.type === "JSXFragment") && Array.isArray(node.children)) {
      for (const c of node.children as Node[]) {
        if (c.type === "JSXExpressionContainer") for (const l of literals(c.expression as Node)) report(l, literalText(l));
      }
    }
    if (node.type === "JSXAttribute") {
      const name = node.name as { type: string; name: string; namespace?: { name: string } };
      const attr = name.type === "JSXNamespacedName" ? `${name.namespace?.name}:${name.name}` : name.name;
      if (textAttr.test(attr)) {
        const v = node.value as Node | null;
        if (v?.type === "Literal") report(v, String(v.value));
        else if (v?.type === "JSXExpressionContainer") for (const l of literals(v.expression as Node)) report(l, literalText(l));
      }
    }
    for (const [k, v] of Object.entries(node)) if (k !== "type" && typeof v === "object") visit(v);
  };
  visit(parseAst(code, { lang: "tsx" }));
  return out;
}

function sources(dir: string): string[] {
  return readdirSync(dir, { withFileTypes: true }).flatMap((e) => {
    const p = resolve(dir, e.name);
    if (e.isDirectory()) return sources(p);
    // Tests and the design gallery are for developers.
    if (!e.name.endsWith(".tsx") || e.name.endsWith(".test.tsx") || e.name === "gallery.tsx") return [];
    return [p];
  });
}

describe("JSX text", () => {
  it("finds text, literals and text attributes", () => {
    const f = findings(`const a = <Card title="Hi there" className="x" aria-labelledby="id">Hello {name} <b>{ok ? "Yes" : t("no")}</b>{"—"}<Button label={\`Save \${n}\`} hint={t("x")} placeholder="bin/server">Hostashi</Button></Card>;`);
    expect(f.map((x) => x.text)).toEqual(["Hi there", "Hello", "Yes", "Save"]);
  });

  it("is in the catalog outside the pending pages", () => {
    const pendingSet = new Set<string>(pending.files);
    const report: string[] = [];
    const done: string[] = [];
    for (const file of sources(src)) {
      const rel = relative(src, file);
      const found = findings(readFileSync(file, "utf8"));
      if (pendingSet.has(rel)) {
        if (found.length === 0) done.push(rel);
        continue;
      }
      for (const f of found) report.push(`${rel}:${f.line}: ${f.text}`);
    }
    // A page that moved to the catalog leaves pending.json.
    expect({ textOutsideT: report, removeFromPending: done }).toEqual({ textOutsideT: [], removeFromPending: [] });
  });

  it("lists only files that exist", () => {
    const all = new Set(sources(src).map((f) => relative(src, f)));
    expect(pending.files.filter((f) => !all.has(f))).toEqual([]);
  });
});
