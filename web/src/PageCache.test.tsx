// SPDX-License-Identifier: AGPL-3.0-or-later

import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import { PageCacheSettings } from "./PageCache";
import { excludeProblem, parseExclude, type PageCache } from "./pagecache";
import type { Site } from "./sites";
import { mockFetch } from "./test-utils";

const site: Site = {
  name: "blog",
  pool: "default",
  state: "active",
  php: "8.4",
  phpPrograms: false,
  disabled: false,
  disabledPage: "builtin-maintenance",
  maintenance: { enabled: false, page: "builtin-maintenance", allowIps: [] },
  domains: ["blog.example.com"],
  tls: { enabled: false, active: false, hsts: false, domains: [], notAfter: null },
  createdAt: "2026-11-19T08:00:00Z",
  updatedAt: "2026-11-19T08:00:00Z",
};

const choices = { presets: ["wordpress", "woocommerce", "php"], minutes: [1, 5, 10, 30, 60], sizesMB: [64, 128, 256, 512, 1024, 2048, 4096], maxExclude: 50 };
const off: PageCache = { enabled: false, preset: "wordpress", minutes: 10, sizeMB: 256, exclude: [], choices } as PageCache;
const path = "/api/v1/sites/blog/page-cache";

describe("PageCacheSettings", () => {
  it("switches the cache on with a preset, excluded paths and a size", async () => {
    const calls = mockFetch({
      ["GET " + path]: { body: off },
      ["PUT " + path]: (b) => ({ body: { ...off, ...(b as object) } }),
      ["PUT " + path + "/size"]: (b) => ({ body: { ...off, enabled: true, preset: "woocommerce", minutes: 30, exclude: ["/warenkorb*"], ...(b as object) } }),
    });
    const user = userEvent.setup();
    render(<PageCacheSettings site={site} canSize />);

    await user.click(await screen.findByRole("checkbox", { name: "Cache pages of this site" }));
    expect(screen.getByText(/Check that no page shows personal data/)).toBeInTheDocument();
    await user.selectOptions(screen.getByRole("combobox", { name: "Application" }), "woocommerce");
    await user.selectOptions(screen.getByRole("combobox", { name: "Cache time" }), "30");
    await user.type(screen.getByRole("textbox", { name: "Excluded paths" }), "/warenkorb*{enter}{enter}/warenkorb*");
    await user.selectOptions(screen.getByRole("combobox", { name: "Size" }), "1024");
    await user.click(screen.getByRole("button", { name: "Save page cache" }));

    expect(await screen.findByRole("status")).toHaveTextContent("The page cache is on.");
    expect(calls.filter((c) => c.method === "PUT")).toEqual([
      { method: "PUT", path, body: { enabled: true, preset: "woocommerce", minutes: 30, exclude: ["/warenkorb*"] } },
      { method: "PUT", path: path + "/size", body: { sizeMB: 1024 } },
    ]);
    expect(screen.getByRole("button", { name: "Empty cache" })).toBeInTheDocument();
  });

  it("shows the size to users without letting them change it", async () => {
    mockFetch({ ["GET " + path]: { body: { ...off, sizeMB: 512 } } });
    render(<PageCacheSettings site={site} canSize={false} />);
    expect(await screen.findByText(/at most 512 MB/)).toBeInTheDocument();
    expect(screen.queryByRole("combobox", { name: "Size" })).toBeNull();
  });

  it("refuses a bad path before saving", async () => {
    const calls = mockFetch({ ["GET " + path]: { body: off } });
    const user = userEvent.setup();
    render(<PageCacheSettings site={site} canSize />);
    await user.type(await screen.findByRole("textbox", { name: "Excluded paths" }), "shop");
    await user.click(screen.getByRole("button", { name: "Save page cache" }));
    expect(await screen.findByRole("alert")).toHaveTextContent("“shop” is not a path");
    expect(calls).toHaveLength(1);
  });

  it("empties the cache and says when it was emptied a moment ago", async () => {
    let n = 0;
    mockFetch({
      ["GET " + path]: { body: { ...off, enabled: true } },
      ["POST " + path + "/purge"]: () =>
        ++n === 1 ? { status: 204 } : { status: 429, body: { error: { code: "rate_limited", message: "try again shortly" } } },
    });
    const user = userEvent.setup();
    render(<PageCacheSettings site={site} canSize />);
    await user.click(await screen.findByRole("button", { name: "Empty cache" }));
    expect(await screen.findByRole("status")).toHaveTextContent("The page cache is empty.");
    await user.click(screen.getByRole("button", { name: "Empty cache" }));
    expect(await screen.findByRole("alert")).toHaveTextContent("emptied a moment ago");
  });
});

describe("excluded paths", () => {
  it("parses one per line without blanks and duplicates", () => {
    expect(parseExclude(" /a \n\n/b*\n/a\n")).toEqual(["/a", "/b*"]);
  });

  it("checks them like the server", () => {
    for (const ok of ["/", "/a", "/a/", "/a*", "/%C3%BC/x"]) expect(excludeProblem([ok], 50)).toBe("");
    for (const bad of ["a", "/a b", "/a*b", "/../x", "//a", "/a?b", '/a"'])
      expect(excludeProblem([bad], 50)).not.toBe("");
    expect(excludeProblem(["/a", "/b"], 1)).toBe("At most 1 excluded paths.");
  });
});
