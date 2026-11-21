// SPDX-License-Identifier: AGPL-3.0-or-later

import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import { cleanPath, formatSize, modeProblem, nameProblem, zipUrl, type FileEntry } from "./files";
import { FileManager } from "./Files";
import type { Site } from "./sites";
import { mockFetch } from "./test-utils";

const site: Site = {
  name: "blog",
  pool: "default",
  state: "active",
  php: "8.4",
  phpPrograms: false,
  disabled: false,
  maintenance: { enabled: false, page: "builtin-maintenance", allowIps: [] },
  domains: ["blog.example.com"],
  tls: { enabled: false, active: false, hsts: false, domains: [], notAfter: null },
  createdAt: "2026-11-19T08:00:00Z",
  updatedAt: "2026-11-19T08:00:00Z",
};

const at = "2026-11-20T10:00:00Z";
const entries: FileEntry[] = [
  { name: "index.php", type: "file", size: 2048, modified: at, mode: "0644", version: "v1" },
  { name: "wp-content", type: "dir", size: 4096, modified: at, mode: "0755" },
  { name: "current", type: "symlink", size: 7, modified: at, mode: "0777", target: "../release" },
  { name: "site.zip", type: "file", size: 10, modified: at, mode: "0644" },
];

const api = "/api/v1/sites/blog/files";
const list = (path: string) => "GET " + api + "?" + new URLSearchParams({ path }).toString();
const listing = (path: string, more: Partial<{ truncated: boolean; unnamed: number }> = {}) => ({
  body: { path, entries, truncated: false, unnamed: 0, ...more },
});

describe("FileManager", () => {
  it("lists a folder, folders first, symlinks shown but not opened", async () => {
    mockFetch({ [list("htdocs")]: listing("htdocs") });
    const onPath = vi.fn();
    render(<FileManager site={site} path="htdocs" onPath={onPath} />);

    const table = await screen.findByRole("table", { name: "Files in htdocs" });
    const rows = within(table).getAllByRole("row").slice(1);
    expect(rows.map((r) => within(r).getAllByRole("cell")[1]!.textContent)).toEqual([
      "wp-content",
      "current→ ../release",
      "index.php",
      "site.zip",
    ]);
    expect(within(table).getByText("2.0 KiB")).toBeInTheDocument();
    expect(within(table).getByRole("link", { name: "Download index.php" })).toHaveAttribute("href", api + "/content?path=htdocs%2Findex.php");
    expect(within(table).getByRole("link", { name: "Download wp-content as ZIP" })).toHaveAttribute("href", api + "/zip?path=htdocs%2Fwp-content");
    expect(within(table).getByRole("button", { name: "Extract site.zip" })).toBeInTheDocument();
    expect(within(table).queryByRole("button", { name: "Extract index.php" })).toBeNull();
    expect(within(table).queryByRole("button", { name: "current" })).toBeNull();

    await userEvent.click(within(table).getByRole("button", { name: "wp-content" }));
    expect(onPath).toHaveBeenCalledWith("htdocs/wp-content");

    const crumbs = screen.getByRole("navigation", { name: "Folder" });
    await userEvent.click(within(crumbs).getByRole("button", { name: "blog" }));
    expect(onPath).toHaveBeenLastCalledWith("");
    expect(within(crumbs).getByRole("button", { name: "htdocs" })).toHaveAttribute("aria-current", "page");
  });

  it("warns about entries it cannot show and explains errors", async () => {
    mockFetch({
      [list("")]: listing("", { truncated: true, unnamed: 2 }),
      [list("gone")]: { status: 404, body: { error: { code: "files_not_found", message: "files: not found" } } },
    });
    const { rerender } = render(<FileManager site={site} path="" onPath={() => undefined} />);
    expect(await screen.findByText(/more entries than the panel shows/)).toBeInTheDocument();
    expect(screen.getByText(/2 entries have names that are not valid UTF-8/)).toBeInTheDocument();

    rerender(<FileManager site={site} path="gone" onPath={() => undefined} />);
    expect(await screen.findByText("It is not there any more. Reload the folder.")).toBeInTheDocument();
  });

  it("creates a folder after checking the name", async () => {
    const calls = mockFetch({ [list("htdocs")]: listing("htdocs"), ["POST " + api + "/mkdir"]: { body: {} } });
    render(<FileManager site={site} path="htdocs" onPath={() => undefined} />);
    await screen.findByRole("table");

    await userEvent.click(screen.getByRole("button", { name: "New folder" }));
    const dialog = within(screen.getByRole("dialog", { name: "New folder" }));
    await userEvent.type(dialog.getByLabelText("Name"), "a/b");
    await userEvent.click(dialog.getByRole("button", { name: "Create" }));
    expect(dialog.getByText("The name must not contain “/”.")).toBeInTheDocument();
    expect(calls.some((c) => c.path.endsWith("/mkdir"))).toBe(false);

    await userEvent.clear(dialog.getByLabelText("Name"));
    await userEvent.type(dialog.getByLabelText("Name"), "uploads");
    await userEvent.click(dialog.getByRole("button", { name: "Create" }));
    expect(await screen.findByText("Folder uploads created.")).toBeInTheDocument();
    expect(calls.find((c) => c.path.endsWith("/mkdir"))?.body).toEqual({ path: "htdocs/uploads" });
  });

  it("acts on a selection: ZIP link, mode and delete", async () => {
    const calls = mockFetch({
      [list("htdocs")]: listing("htdocs"),
      ["POST " + api + "/chmod"]: { body: {} },
      ["POST " + api + "/delete"]: { body: {} },
    });
    render(<FileManager site={site} path="htdocs" onPath={() => undefined} />);
    await screen.findByRole("table");

    await userEvent.click(screen.getByLabelText("Select index.php"));
    await userEvent.click(screen.getByLabelText("Select wp-content"));
    const bar = within(screen.getByRole("region", { name: "Selection" }));
    expect(bar.getByText("2 selected")).toBeInTheDocument();
    expect(bar.getByRole("link", { name: "Download as ZIP" })).toHaveAttribute("href", api + "/zip?path=htdocs%2Findex.php&path=htdocs%2Fwp-content");

    await userEvent.click(bar.getByRole("button", { name: "Change mode" }));
    let dialog = within(screen.getByRole("dialog"));
    await userEvent.clear(dialog.getByLabelText("Mode"));
    await userEvent.type(dialog.getByLabelText("Mode"), "4755");
    await userEvent.click(dialog.getByRole("button", { name: "Change mode" }));
    expect(dialog.getByText("setuid, setgid and the sticky bit cannot be set here.")).toBeInTheDocument();
    await userEvent.clear(dialog.getByLabelText("Mode"));
    await userEvent.type(dialog.getByLabelText("Mode"), "640");
    await userEvent.click(dialog.getByRole("button", { name: "Change mode" }));
    expect(await screen.findByText("Mode set to 640.")).toBeInTheDocument();
    expect(calls.find((c) => c.path.endsWith("/chmod"))?.body).toEqual({ paths: ["htdocs/index.php", "htdocs/wp-content"], mode: "0640" });

    // The selection is cleared after an action; pick again to delete.
    await userEvent.click(screen.getByLabelText("Select all"));
    await userEvent.click(within(screen.getByRole("region", { name: "Selection" })).getByRole("button", { name: "Delete" }));
    dialog = within(screen.getByRole("alertdialog", { name: "Delete 4 entries?" }));
    expect(dialog.getByText(/There is no recycle bin/)).toBeInTheDocument();
    await userEvent.click(dialog.getByRole("button", { name: "Delete 4 entries" }));
    expect(await screen.findByText("4 entries deleted.")).toBeInTheDocument();
    expect(calls.find((c) => c.path.endsWith("/delete"))?.body).toEqual({
      paths: ["htdocs/index.php", "htdocs/wp-content", "htdocs/current", "htdocs/site.zip"],
    });
  });

  it("uploads a file in chunks", async () => {
    const calls = mockFetch({
      [list("htdocs")]: listing("htdocs"),
      ["POST " + api + "/upload-start"]: { body: { id: "00000000000000ab", received: 0, done: false } },
      ["PUT " + api + "/upload?path=htdocs%2Fnew.txt&id=00000000000000ab&offset=0&size=5"]: {
        body: { id: "00000000000000ab", received: 5, done: true },
      },
    });
    render(<FileManager site={site} path="htdocs" onPath={() => undefined} />);
    await screen.findByRole("table");

    await userEvent.upload(screen.getByLabelText("Files to upload"), new File(["hello"], "new.txt", { type: "text/plain" }));
    expect(await screen.findByText("new.txt uploaded.")).toBeInTheDocument();
    expect(calls.find((c) => c.path.endsWith("/upload-start"))?.body).toEqual({ path: "htdocs/new.txt", size: 5, overwrite: false, parents: false });
  });

  it("extracts a ZIP and reports what was kept", async () => {
    const calls = mockFetch({
      [list("htdocs")]: listing("htdocs"),
      ["POST " + api + "/extract"]: { body: { done: true, result: { files: 3, dirs: 1, kept: 1, skipped: 1 } } },
    });
    render(<FileManager site={site} path="htdocs" onPath={() => undefined} />);
    await userEvent.click(await screen.findByRole("button", { name: "Extract site.zip" }));
    const dialog = within(screen.getByRole("dialog", { name: "Extract site.zip" }));
    await userEvent.click(dialog.getByRole("button", { name: "Extract" }));
    expect(await screen.findByText("3 files extracted into htdocs, 1 existing files kept, 1 symlinks or special entries skipped.")).toBeInTheDocument();
    expect(calls.find((c) => c.path.endsWith("/extract"))?.body).toEqual({ path: "htdocs/site.zip", to: "htdocs", overwrite: false });
  });
});

describe("files helpers", () => {
  it("cleans paths like the helper", () => {
    expect(cleanPath(" /htdocs/a/ ")).toBe("htdocs/a");
    expect(cleanPath("")).toBe("");
    for (const bad of ["a//b", "../x", "a/./b", "a/.."]) expect(cleanPath(bad)).toBeNull();
  });

  it("checks names and modes", () => {
    expect(nameProblem("ok.txt")).toBe("");
    expect(nameProblem(" x")).not.toBe("");
    expect(nameProblem("..")).not.toBe("");
    expect(nameProblem("x".repeat(256))).not.toBe("");
    expect(modeProblem("755")).toBe("");
    expect(modeProblem("0644")).toBe("");
    expect(modeProblem("1777")).not.toBe("");
    expect(modeProblem("89")).not.toBe("");
  });

  it("formats sizes and ZIP addresses", () => {
    expect(formatSize(512)).toBe("512 B");
    expect(formatSize(1536)).toBe("1.5 KiB");
    expect(formatSize(300 * 1024 * 1024)).toBe("300 MiB");
    expect(zipUrl("blog", ["a b", "c"])).toBe("/api/v1/sites/blog/files/zip?path=a+b&path=c");
  });
});
