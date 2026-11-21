// SPDX-License-Identifier: AGPL-3.0-or-later

import { EditorView } from "@codemirror/view";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";
import { language } from "./codemirror";
import EditorDialog from "./Editor";
import type { FileEntry } from "./files";

// jsdom does not lay out text; CodeMirror measures ranges.
const emptyRects = () => Object.assign([], { item: () => null }) as unknown as DOMRectList;
Range.prototype.getClientRects = emptyRects;
Range.prototype.getBoundingClientRect = () => new DOMRect();

const entry: FileEntry = { name: "index.php", type: "file", size: 20, modified: "2026-11-20T10:00:00Z", mode: "0644", version: "v1" };
const url = "/api/v1/sites/blog/files/content?path=htdocs%2Findex.php";

type Call = { method: string; url: string; body: unknown };

// stubFetch serves the file's bytes for GET and answers PUT with put().
function stubFetch(bytes: Uint8Array | string, put: (body: string, url: string) => Response = () => Response.json({ ...entry, version: "v2" })) {
  const calls: Call[] = [];
  vi.stubGlobal(
    "fetch",
    vi.fn(async (input: string, init?: RequestInit) => {
      const method = init?.method ?? "GET";
      calls.push({ method, url: input, body: init?.body });
      if (method === "GET") return new Response(typeof bytes === "string" ? bytes : (bytes.buffer as ArrayBuffer), { headers: { "X-Hostashi-Version": "v1" } });
      return put(String(init?.body), input);
    }),
  );
  return calls;
}

// type changes the document the way a keystroke would.
function type(text: string) {
  const el = document.querySelector<HTMLElement>(".cm-content");
  const view = el && EditorView.findFromDOM(el);
  if (!view) throw new Error("no editor");
  view.dispatch({ changes: { from: view.state.doc.length, insert: text } });
}

afterEach(() => vi.unstubAllGlobals());

describe("EditorDialog", () => {
  it("opens a file and closes without asking when nothing changed", async () => {
    stubFetch("<?php\necho 1;\n");
    const onClose = vi.fn();
    render(<EditorDialog site="blog" path="htdocs/index.php" entry={entry} onClose={onClose} onSaved={() => undefined} />);

    await screen.findByText("LF");
    expect(document.querySelector(".cm-content")?.textContent).toContain("echo 1;");
    expect(screen.getByText("PHP")).toBeInTheDocument();
    expect(screen.getByText("LF")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Save changes" })).toBeDisabled();

    await userEvent.click(screen.getAllByRole("button", { name: "Close" })[1]!);
    expect(onClose).toHaveBeenCalled();
  });

  it("saves with the version it opened, keeping CRLF", async () => {
    const calls = stubFetch("a\r\nb\r\n");
    const onSaved = vi.fn();
    render(<EditorDialog site="blog" path="htdocs/index.php" entry={entry} onClose={() => undefined} onSaved={onSaved} />);
    await screen.findByText("CRLF");

    type("c");
    expect(await screen.findByText("Unsaved changes")).toBeInTheDocument();
    await userEvent.click(screen.getByRole("button", { name: "Save changes" }));

    await waitFor(() => expect(onSaved).toHaveBeenCalled());
    const put = calls.find((c) => c.method === "PUT")!;
    expect(put.url).toBe(url + "&version=v1");
    expect(put.body).toBe("a\r\nb\r\nc");
    expect(screen.getByText("Saved")).toBeInTheDocument();

    // The next save sends the new version.
    type("d");
    await screen.findByText("Unsaved changes");
    await userEvent.keyboard("{Control>}s{/Control}");
    await waitFor(() => expect(calls.filter((c) => c.method === "PUT")).toHaveLength(2));
    expect(calls.at(-1)!.url).toBe(url + "&version=v2");
  });

  it("asks before discarding changes", async () => {
    stubFetch("x\n");
    const onClose = vi.fn();
    render(<EditorDialog site="blog" path="htdocs/index.php" entry={entry} onClose={onClose} onSaved={() => undefined} />);
    await screen.findByText("LF");
    type("y");
    await screen.findByText("Unsaved changes");

    await userEvent.click(screen.getAllByRole("button", { name: "Close" })[0]!);
    expect(onClose).not.toHaveBeenCalled();
    expect(screen.getByText("Discard your changes?")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Keep editing" })).toHaveFocus();

    await userEvent.click(screen.getByRole("button", { name: "Keep editing" }));
    expect(screen.queryByText("Discard your changes?")).toBeNull();

    await userEvent.click(screen.getAllByRole("button", { name: "Close" })[1]!);
    await userEvent.click(screen.getByRole("button", { name: "Discard changes" }));
    expect(onClose).toHaveBeenCalled();
  });

  it("explains a file that changed meanwhile", async () => {
    stubFetch("x\n", () => Response.json({ error: { code: "files_changed", message: "files: changed" } }, { status: 409 }));
    render(<EditorDialog site="blog" path="htdocs/index.php" entry={entry} onClose={() => undefined} onSaved={() => undefined} />);
    await screen.findByText("LF");
    type("y");
    await screen.findByText("Unsaved changes");
    await userEvent.click(screen.getByRole("button", { name: "Save changes" }));
    expect(await screen.findByText(/changed by someone else since it was opened/)).toBeInTheDocument();
    expect(screen.getByText("Unsaved changes")).toBeInTheDocument();
  });

  it("refuses files that are not UTF-8 text", async () => {
    stubFetch(new Uint8Array([0x89, 0x50, 0x4e, 0x47, 0xff, 0x00]));
    render(<EditorDialog site="blog" path="htdocs/index.php" entry={entry} onClose={() => undefined} onSaved={() => undefined} />);
    expect(await screen.findByText(/not UTF-8 text and cannot be edited here/)).toBeInTheDocument();
  });
});

describe("language", () => {
  it("picks the syntax by the file name", () => {
    expect(language("wp-config.php").label).toBe("PHP");
    expect(language("style.CSS").label).toBe("CSS");
    expect(language(".htaccess").label).toBe("Plain text");
    expect(language("docker-compose.yml").label).toBe("YAML");
    expect(language("README").label).toBe("Plain text");
  });
});
