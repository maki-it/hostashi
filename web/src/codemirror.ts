// SPDX-License-Identifier: AGPL-3.0-or-later

// The code editor of the file manager (ADR-0040), built from CodeMirror 6.
// This module is loaded only when the editor opens, so CodeMirror stays
// out of the main bundle. Colours come from the panel's tokens (ADR-0037),
// so the editor follows the light and dark scheme.

import { defaultKeymap, history, historyKeymap } from "@codemirror/commands";
import { css } from "@codemirror/lang-css";
import { html } from "@codemirror/lang-html";
import { javascript } from "@codemirror/lang-javascript";
import { json } from "@codemirror/lang-json";
import { markdown } from "@codemirror/lang-markdown";
import { php } from "@codemirror/lang-php";
import { xml } from "@codemirror/lang-xml";
import { yaml } from "@codemirror/lang-yaml";
import { bracketMatching, HighlightStyle, indentOnInput, syntaxHighlighting } from "@codemirror/language";
import { highlightSelectionMatches, openSearchPanel, search, searchKeymap } from "@codemirror/search";
import { Compartment, EditorState, type Extension } from "@codemirror/state";
import {
  drawSelection,
  EditorView,
  highlightActiveLine,
  highlightActiveLineGutter,
  highlightSpecialChars,
  keymap,
  lineNumbers,
} from "@codemirror/view";
import { tags as t } from "@lezer/highlight";

/** language picks the syntax by the file name; plain text when unknown. */
export function language(name: string): { label: string; ext: Extension } {
  const n = name.toLowerCase();
  const ext = n.includes(".") ? n.slice(n.lastIndexOf(".") + 1) : "";
  switch (ext) {
    case "php":
    case "phtml":
    case "inc":
      return { label: "PHP", ext: php() };
    case "js":
    case "mjs":
    case "cjs":
      return { label: "JavaScript", ext: javascript() };
    case "ts":
    case "mts":
      return { label: "TypeScript", ext: javascript({ typescript: true }) };
    case "jsx":
    case "tsx":
      return { label: ext === "tsx" ? "TypeScript" : "JavaScript", ext: javascript({ jsx: true, typescript: ext === "tsx" }) };
    case "css":
    case "scss":
      return { label: "CSS", ext: css() };
    case "html":
    case "htm":
    case "twig":
      return { label: "HTML", ext: html() };
    case "json":
    case "webmanifest":
      return { label: "JSON", ext: json() };
    case "yml":
    case "yaml":
      return { label: "YAML", ext: yaml() };
    case "md":
    case "markdown":
      return { label: "Markdown", ext: markdown() };
    case "xml":
    case "svg":
      return { label: "XML", ext: xml() };
    default:
      return { label: "Plain text", ext: [] };
  }
}

// Syntax colours from the panel's tokens: they keep the WCAG contrast that
// tokens.test.ts checks, in both schemes.
const highlight = HighlightStyle.define([
  { tag: [t.keyword, t.controlKeyword, t.operatorKeyword, t.modifier, t.definitionKeyword], color: "var(--accent)", fontWeight: "600" },
  { tag: [t.string, t.special(t.string), t.regexp], color: "var(--ok)" },
  { tag: [t.number, t.bool, t.null, t.atom], color: "var(--warn)" },
  { tag: [t.comment, t.lineComment, t.blockComment, t.docComment], color: "var(--muted)", fontStyle: "italic" },
  { tag: [t.tagName, t.typeName, t.className], color: "var(--err)" },
  { tag: [t.attributeName, t.propertyName], color: "var(--accent)" },
  { tag: [t.heading], fontWeight: "700" },
  { tag: [t.emphasis], fontStyle: "italic" },
  { tag: [t.strong], fontWeight: "700" },
  { tag: [t.link, t.url], textDecoration: "underline" },
  { tag: t.invalid, color: "var(--err)" },
]);

const theme = EditorView.theme({
  "&": { color: "var(--fg)", backgroundColor: "var(--surface)", height: "100%", fontSize: "14px" },
  ".cm-scroller": { fontFamily: "var(--font-mono)", lineHeight: "1.55" },
  ".cm-content": { caretColor: "var(--fg)" },
  ".cm-cursor, .cm-dropCursor": { borderLeftColor: "var(--fg)" },
  "&.cm-focused": { outline: "none" },
  // A clear selection colour; the active line is half transparent, since
  // CodeMirror paints it over the selection layer.
  // The selectors match CodeMirror's base theme, which would win otherwise
  // with its fixed light colour (unreadable in the dark scheme).
  "&.cm-focused > .cm-scroller > .cm-selectionLayer .cm-selectionBackground, & > .cm-scroller > .cm-selectionLayer .cm-selectionBackground, .cm-content ::selection":
    { backgroundColor: "var(--selection)" },
  ".cm-gutters": { backgroundColor: "var(--sunken)", color: "var(--muted)", borderRight: "1px solid var(--line)" },
  ".cm-activeLine": { backgroundColor: "color-mix(in srgb, var(--sunken) 50%, transparent)" },
  ".cm-activeLineGutter": { backgroundColor: "var(--line)", color: "var(--fg)" },
  ".cm-matchingBracket": { outline: "1px solid var(--accent)", backgroundColor: "transparent" },
  // A bracket without a partner is not marked: the PHP and HTML parsers
  // count "<?php" and "<" as one, and a red mark there looks like an error.
  "&.cm-focused .cm-nonmatchingBracket, .cm-nonmatchingBracket": { color: "inherit", backgroundColor: "transparent" },
  ".cm-searchMatch": { backgroundColor: "var(--warn-bg)", outline: "1px solid var(--warn)" },
  ".cm-searchMatch.cm-searchMatch-selected": { backgroundColor: "var(--ok-bg)" },
  ".cm-selectionMatch": { backgroundColor: "var(--ok-bg)" },
  ".cm-panels": { backgroundColor: "var(--sunken)", color: "var(--fg)" },
  ".cm-panels.cm-panels-top": { borderBottom: "1px solid var(--line)" },
  ".cm-panels.cm-panels-bottom": { borderTop: "1px solid var(--line)" },
  ".cm-panel.cm-search": { fontFamily: "var(--font-body)", padding: "8px 12px" },
  ".cm-panel.cm-search input, .cm-panel.cm-search button": { fontFamily: "inherit", fontSize: "14px" },
  ".cm-textfield": { border: "1px solid var(--control-border)", borderRadius: "var(--radius)", backgroundColor: "var(--surface)", color: "var(--fg)" },
  ".cm-button": {
    backgroundImage: "none",
    backgroundColor: "var(--surface)",
    color: "var(--fg)",
    border: "1px solid var(--control-border)",
    borderRadius: "var(--radius)",
  },
});

export type EditorHandle = {
  view: EditorView;
  setWrap: (on: boolean) => void;
  /** Opens the search and replace bar, as Ctrl/Cmd+F does. */
  openSearch: () => void;
  destroy: () => void;
};

export type EditorOptions = {
  parent: HTMLElement;
  doc: string;
  name: string;
  wrap: boolean;
  label: string;
  /** CodeMirror's own words (the search bar) in the panel's language. */
  phrases?: Record<string, string>;
  onChange: (doc: string) => void;
  onCursor: (line: number, col: number) => void;
};

/** styleNonce is the nonce the panel gives index.html for injected styles. */
export function styleNonce(): string {
  return document.querySelector<HTMLMetaElement>('meta[name="hostashi-style-nonce"]')?.content ?? "";
}

/** createEditor puts an editor into parent and returns its handle. */
export function createEditor(o: EditorOptions): EditorHandle {
  const wrap = new Compartment();
  const nonce = styleNonce();
  const extensions: Extension[] = [
    lineNumbers(),
    highlightActiveLineGutter(),
    highlightSpecialChars(),
    history(),
    drawSelection(),
    indentOnInput(),
    bracketMatching(),
    highlightActiveLine(),
    highlightSelectionMatches(),
    search({ top: true }),
    syntaxHighlighting(highlight),
    theme,
    language(o.name).ext,
    wrap.of(o.wrap ? EditorView.lineWrapping : []),
    // Ctrl/Cmd+S is handled by the dialog, so that it works outside the
    // text too.
    keymap.of([
      ...defaultKeymap,
      ...historyKeymap,
      ...searchKeymap,
    ]),
    EditorView.contentAttributes.of({ "aria-label": o.label }),
    EditorView.updateListener.of((u) => {
      if (u.docChanged) o.onChange(u.state.doc.toString());
      if (u.docChanged || u.selectionSet) {
        const head = u.state.selection.main.head;
        const line = u.state.doc.lineAt(head);
        o.onCursor(line.number, head - line.from + 1);
      }
    }),
  ];
  if (nonce !== "") extensions.push(EditorView.cspNonce.of(nonce));
  if (o.phrases) extensions.push(EditorState.phrases.of(o.phrases));


  const view = new EditorView({ parent: o.parent, state: EditorState.create({ doc: o.doc, extensions }) });
  return {
    view,
    setWrap: (on) => view.dispatch({ effects: wrap.reconfigure(on ? EditorView.lineWrapping : []) }),
    openSearch: () => {
      openSearchPanel(view);
    },
    destroy: () => view.destroy(),
  };
}
