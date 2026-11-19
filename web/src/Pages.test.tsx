// SPDX-License-Identifier: AGPL-3.0-or-later

import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import { Pages } from "./Pages";
import { allowIpsProblem, fileProblem, parseAllowIps, type StatusPage } from "./pages";
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
    render(<Pages />);
    const list = within(await screen.findByRole("region", { name: "Pages" }));
    expect(list.getByText("Built-in maintenance page")).toBeInTheDocument();
    expect(list.queryByRole("button", { name: "Delete" })).not.toBeInTheDocument();

    const upload = within(screen.getByRole("region", { name: "Upload a page" }));
    await user.click(upload.getByRole("button", { name: "Upload page" }));
    expect(upload.getByRole("alert")).toHaveTextContent("Enter a name.");

    await user.type(upload.getByRole("textbox", { name: "Name" }), "Summer break");
    await user.upload(upload.getByLabelText(/^File/), new File(["<h1>x</h1>"], "summer.zip", { type: "application/zip" }));
    await user.click(upload.getByRole("button", { name: "Upload page" }));
    expect(await upload.findByRole("status")).toHaveTextContent("Page “Summer break” uploaded with 2 files.");

    const post = calls.find((c) => c.method === "POST");
    expect(post?.body).toBeInstanceOf(FormData);
    const form = post?.body as FormData;
    expect(form.get("name")).toBe("Summer break");
    expect(form.get("kind")).toBe("maintenance");
    expect((form.get("file") as File).name).toBe("summer.zip");

    expect(await list.findByText("Summer break")).toBeInTheDocument();
    expect(list.getByText("css/site.css")).toBeInTheDocument();
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
    const { unmount } = render(<Pages />);
    await user.click(await screen.findByRole("button", { name: "Delete" }));
    expect(screen.getByText(/blog still use this page/)).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Delete Summer break" })).toBeDisabled();
    unmount();

    pages = [builtin, { ...summer, sites: [] }];
    render(<Pages />);
    await user.click(await screen.findByRole("button", { name: "Delete" }));
    await user.click(screen.getByRole("button", { name: "Delete Summer break" }));
    await screen.findByText("Built-in maintenance page");
    expect(screen.queryByText("Summer break")).not.toBeInTheDocument();
    expect(calls.filter((c) => c.method === "DELETE")).toHaveLength(1);
  });
});
