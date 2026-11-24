// SPDX-License-Identifier: AGPL-3.0-or-later

import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { useState } from "react";
import { describe, expect, it, vi } from "vitest";
import { Pages } from "./Pages";
import { allowIpsProblem, fileProblem, openPreview, parseAllowIps, type StatusPage } from "./pages";
import { mockFetch } from "./test-utils";

const builtin: StatusPage = {
  id: "builtin-maintenance",
  name: "Built-in maintenance page",
  kind: "maintenance",
  builtin: true,
  sha256: "a".repeat(64),
  files: [{ path: "index.html", size: 1200 }],
  sites: [],
  parked: [],
  createdAt: null,
  updatedAt: null,
};
const summer: StatusPage = {
  id: "p1",
  name: "Summer break",
  kind: "maintenance",
  builtin: false,
  sha256: "b".repeat(64),
  files: [
    { path: "css/site.css", size: 300 },
    { path: "index.html", size: 2048 },
  ],
  sites: ["blog"],
  parked: [],
  createdAt: "2026-11-19T08:00:00Z",
  updatedAt: "2026-11-19T08:00:00Z",
};

// Harness keeps the address query in state, as the app does.
function Harness({ initial = "" }: { initial?: string }) {
  const [query, setQuery] = useState(new URLSearchParams(initial));
  return <Pages query={query} onQuery={setQuery} />;
}

describe("checks", () => {
  it("checks files", () => {
    expect(fileProblem(null)).toMatch("Choose");
    expect(fileProblem(new File(["x"], "a.php"))).toMatch(".html file or a .zip");
    expect(fileProblem(new File([""], "a.html"))).toMatch("empty");
    expect(fileProblem(new File(["x"], "A.ZIP"))).toBe("");
  });

  it("parses allowed addresses", () => {
    expect(parseAllowIps(" 10.0.0.0/8,\n2001:db8::1  ")).toEqual(["10.0.0.0/8", "2001:db8::1"]);
    expect(allowIpsProblem(["10.0.0.0/8", "2001:db8::/32"])).toBe("");
    expect(allowIpsProblem(["example.com"])).toMatch("is not an IP address");
    expect(allowIpsProblem(Array.from({ length: 21 }, (_, i) => "10.0.0." + String(i)))).toMatch("At most 20");
  });
});

describe("Pages", () => {
  it("lists pages and uploads one", async () => {
    const user = userEvent.setup();
    let pages = [builtin];
    const calls = mockFetch({
      "GET /api/v1/status-pages": () => ({ body: { pages } }),
      "POST /api/v1/status-pages": () => {
        pages = [builtin, summer];
        return { status: 201, body: summer };
      },
    });
    render(<Harness initial="new=1" />);
    const upload = within(await screen.findByRole("form", { name: "Upload a page" }));
    await user.click(upload.getByRole("button", { name: "Upload page" }));
    expect(upload.getByRole("alert")).toHaveTextContent("Enter a name.");

    await user.type(upload.getByRole("textbox", { name: "Name" }), "Summer break");
    await user.upload(upload.getByLabelText("File"), new File(["<h1>x</h1>"], "summer.zip", { type: "application/zip" }));
    await user.click(upload.getByRole("button", { name: "Upload page" }));
    expect(await screen.findByText("Page “Summer break” uploaded with 2 files.")).toBeInTheDocument();

    const post = calls.find((c) => c.method === "POST");
    expect(post?.body).toBeInstanceOf(FormData);
    const form = post?.body as FormData;
    expect(form.get("name")).toBe("Summer break");
    expect(form.get("kind")).toBe("maintenance");
    expect((form.get("file") as File).name).toBe("summer.zip");

    const table = within(await screen.findByRole("table", { name: "Pages" }));
    expect(await table.findByRole("link", { name: "Summer break" })).toHaveAttribute("href", "#/pages?page=p1");
    expect(table.getByText("Built-in maintenance page")).toBeInTheDocument();
    expect(table.queryByRole("button", { name: "Delete Built-in maintenance page" })).not.toBeInTheDocument();
    expect(table.getByText("blog")).toBeInTheDocument();
  });

  it("shows the files of a page and renames it", async () => {
    const user = userEvent.setup();
    const calls = mockFetch({
      "GET /api/v1/status-pages": { body: { pages: [builtin, summer] } },
      "PUT /api/v1/status-pages/p1": { body: { ...summer, name: "Winter break" } },
    });
    render(<Harness initial="page=p1" />);
    expect(await screen.findByText("css/site.css")).toBeInTheDocument();
    await user.clear(screen.getByLabelText("New name for Summer break"));
    await user.type(screen.getByLabelText("New name for Summer break"), "Winter break");
    await user.click(screen.getByRole("button", { name: "Save name" }));
    expect(await screen.findByText("Page renamed.")).toBeInTheDocument();
    expect(calls.some((c) => c.method === "PUT")).toBe(true);
  });

  it("does not delete a page in use, and deletes an unused one", async () => {
    const user = userEvent.setup();
    let pages = [builtin, summer];
    const calls = mockFetch({
      "GET /api/v1/status-pages": () => ({ body: { pages } }),
      "DELETE /api/v1/status-pages/p1": () => {
        pages = [builtin];
        return { status: 204 };
      },
    });
    const { unmount } = render(<Harness />);
    await user.click(await screen.findByRole("button", { name: "Delete Summer break" }));
    const dialog = within(screen.getByRole("alertdialog"));
    expect(dialog.getByText(/blog still use this page/)).toBeInTheDocument();
    expect(dialog.getByRole("button", { name: "Delete Summer break" })).toBeDisabled();
    unmount();

    pages = [builtin, { ...summer, sites: [] }];
    render(<Harness />);
    await user.click(await screen.findByRole("button", { name: "Delete Summer break" }));
    await user.click(within(screen.getByRole("alertdialog")).getByRole("button", { name: "Delete Summer break" }));
    expect(await screen.findByText("Page “Summer break” deleted.")).toBeInTheDocument();
    expect(screen.queryByRole("link", { name: "Summer break" })).not.toBeInTheDocument();
    expect(calls.filter((c) => c.method === "DELETE")).toHaveLength(1);
  });
});

describe("openPreview", () => {
  it("opens a tab first, drops its opener and sends it to the link", async () => {
    mockFetch({ "POST /api/v1/status-pages/p7/preview": { body: { url: "/api/page-preview/tok/", expires: "2026-11-24T10:15:00Z" } } });
    const tab = { opener: {} as unknown, location: { href: "" }, close: vi.fn() };
    const open = vi.fn(() => tab as unknown as Window);
    expect(await openPreview("p7", open)).toBe("");
    expect(open).toHaveBeenCalledWith("", "_blank");
    expect(tab.opener).toBeNull();
    expect(tab.location.href).toBe("/api/page-preview/tok/");
  });

  it("closes the tab when the link cannot be had, and explains a blocked tab", async () => {
    mockFetch({ "POST /api/v1/status-pages/p7/preview": { status: 404, body: { error: { code: "not_found", message: "unknown page" } } } });
    const tab = { opener: {}, location: { href: "" }, close: vi.fn() };
    expect(await openPreview("p7", () => tab as unknown as Window)).not.toBe("");
    expect(tab.close).toHaveBeenCalled();

    mockFetch({ "POST /api/v1/status-pages/p7/preview": { body: { url: "/x/", expires: "" } } });
    expect(await openPreview("p7", () => null)).toMatch(/blocked/);
  });
});
