// SPDX-License-Identifier: AGPL-3.0-or-later

import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import { assetCacheDefaults, webDirProblem, type Site } from "./sites";
import { SiteWebSettings } from "./SiteWeb";
import { mockFetch } from "./test-utils";

const php = { installed: ["8.2", "8.4"], supported: ["8.2", "8.3", "8.4"], legacy: ["8.2"] };

const site: Site = {
  name: "docs",
  pool: "default",
  state: "active",
  type: "static",
  php: "",
  web: { dir: "", spa: false, assetCache: false },
  phpPrograms: false,
  disabled: false,
  disabledPage: "builtin-maintenance",
  maintenance: { enabled: false, page: "builtin-maintenance", allowIps: [] },
  domains: ["docs.example.com"],
  tls: { enabled: false, active: false, hsts: false, domains: [], notAfter: null },
  createdAt: "2026-11-22T08:00:00Z",
  updatedAt: "2026-11-22T08:00:00Z",
};

describe("webDirProblem", () => {
  it.each(["", "dist", "public", "build/output", "a/b/c/d", "_site", "v1.2"])("accepts %j", (d) => expect(webDirProblem(d)).toBe(""));
  it.each(["/dist", "dist/", "..", ".", "a/../b", "a//b", "a b", "a/b/c/d/e", "ä", "x".repeat(65)])("refuses %j", (d) =>
    expect(webDirProblem(d)).not.toBe(""),
  );
});

describe("SiteWebSettings", () => {
  it("saves the folder and the static options of a static site", async () => {
    const user = userEvent.setup();
    const calls = mockFetch({ "PUT /api/v1/sites/docs/web": (b: unknown) => ({ body: { ...site, web: b } }) });
    render(<SiteWebSettings site={site} php={php} onChange={async () => undefined} />);
    const card = within(screen.getByRole("region", { name: "Web root" }));
    expect(card.getByRole("button", { name: "Save web root" })).toBeDisabled();

    await user.type(card.getByLabelText("Folder below htdocs"), "../etc");
    await user.click(card.getByRole("button", { name: "Save web root" }));
    expect(await card.findByRole("alert")).toHaveTextContent("Use folder names");
    expect(calls).toEqual([]);

    await user.clear(card.getByLabelText("Folder below htdocs"));
    await user.type(card.getByLabelText("Folder below htdocs"), "dist");
    await user.click(card.getByLabelText(/^Single-page app/));
    await user.click(card.getByLabelText(/^Cache assets/));
    await user.click(card.getByRole("button", { name: "Save web root" }));
    expect(await card.findByRole("status")).toHaveTextContent("The site serves htdocs/dist now.");
    expect(calls.map((c) => [c.method, c.path, c.body])).toEqual([
      [
        "PUT",
        "/api/v1/sites/docs/web",
        { dir: "dist", spa: true, assetCache: true, assetCacheSeconds: 2592000, assetCacheExts: assetCacheDefaults.exts },
      ],
    ]);
  });

  it("saves a custom cache time and file types and refuses bad ones", async () => {
    const user = userEvent.setup();
    const calls = mockFetch({ "PUT /api/v1/sites/docs/web": (b: unknown) => ({ body: { ...site, web: b } }) });
    render(<SiteWebSettings site={{ ...site, web: { dir: "", spa: false, assetCache: true } }} php={php} onChange={async () => undefined} />);
    const card = within(screen.getByRole("region", { name: "Web root" }));
    const time = card.getByLabelText("Cache time");
    const types = card.getByLabelText("File types");

    await user.clear(time);
    await user.type(time, "0");
    await user.click(card.getByRole("button", { name: "Save web root" }));
    expect(await card.findByRole("alert")).toHaveTextContent("between 1 minute and 365 days");

    await user.clear(time);
    await user.type(time, "12");
    await user.selectOptions(card.getByLabelText("Unit"), "hours");
    await user.clear(types);
    await user.type(types, ".css, java script!");
    await user.click(card.getByRole("button", { name: "Save web root" }));
    expect(await card.findByRole("alert")).toHaveTextContent('"script!" is not a file type');
    expect(calls).toEqual([]);

    await user.clear(types);
    await user.type(types, "css, js woff2");
    await user.click(card.getByRole("button", { name: "Save web root" }));
    await card.findByRole("status");
    expect(calls[0]?.body).toEqual({ dir: "", spa: false, assetCache: true, assetCacheSeconds: 43200, assetCacheExts: ["css", "js", "woff2"] });
  });

  it("restores the defaults and keeps stored values while the cache is off", async () => {
    const user = userEvent.setup();
    const calls = mockFetch({ "PUT /api/v1/sites/docs/web": (b: unknown) => ({ body: { ...site, web: b } }) });
    const stored = { dir: "", spa: false, assetCache: true, assetCacheSeconds: 3600, assetCacheExts: ["css"] };
    render(<SiteWebSettings site={{ ...site, web: stored }} php={php} onChange={async () => undefined} />);
    const card = within(screen.getByRole("region", { name: "Web root" }));
    expect(card.getByLabelText("Cache time")).toHaveValue(1);
    expect(card.getByLabelText("Unit")).toHaveDisplayValue("hours");
    await user.click(card.getByRole("button", { name: "Use the defaults" }));
    expect(card.getByLabelText("Cache time")).toHaveValue(30);
    expect(card.getByLabelText("File types")).toHaveValue(assetCacheDefaults.exts.join(" "));

    await user.click(card.getByLabelText(/^Cache assets/));
    expect(card.queryByLabelText("Cache time")).not.toBeInTheDocument();
    await user.click(card.getByRole("button", { name: "Save web root" }));
    await card.findByRole("status");
    expect(calls[0]?.body).toEqual({ dir: "", spa: false, assetCache: false, assetCacheSeconds: 3600, assetCacheExts: ["css"] });
  });

  it("offers the asset cache but no single-page app for a PHP site", async () => {
    const user = userEvent.setup();
    const calls = mockFetch({ "PUT /api/v1/sites/docs/web": (b: unknown) => ({ body: { ...site, web: b } }) });
    render(<SiteWebSettings site={{ ...site, type: "php", php: "8.4", web: { dir: "", spa: true, assetCache: false } }} php={php} onChange={async () => undefined} />);
    const card = within(screen.getByRole("region", { name: "Web root" }));
    expect(card.queryByLabelText(/^Single-page app/)).not.toBeInTheDocument();
    await user.type(card.getByLabelText("Folder below htdocs"), "public");
    await user.click(card.getByLabelText(/^Cache assets/));
    await user.click(card.getByRole("button", { name: "Save web root" }));
    await card.findByRole("status");
    expect(calls[0]?.body).toEqual({
      dir: "public",
      spa: false,
      assetCache: true,
      assetCacheSeconds: assetCacheDefaults.seconds,
      assetCacheExts: assetCacheDefaults.exts,
    });
  });

  it("turns a static site into a PHP site with the newest standard version", async () => {
    const user = userEvent.setup();
    const calls = mockFetch({ "PUT /api/v1/sites/docs/php": (b: unknown) => ({ body: { ...site, type: "php", php: (b as { php: string }).php } }) });
    render(<SiteWebSettings site={site} php={php} onChange={async () => undefined} />);
    const card = within(screen.getByRole("region", { name: "Site type" }));
    expect(card.getByRole("button", { name: "Save type" })).toBeDisabled();
    expect(card.queryByLabelText("PHP version")).not.toBeInTheDocument();
    await user.click(card.getByLabelText(/^PHP/));
    expect(card.getByLabelText("PHP version")).toHaveValue("8.4");
    expect(card.getByText(/The site gets its own PHP pool/)).toBeInTheDocument();
    await user.click(card.getByRole("button", { name: "Save type" }));
    expect(await card.findByRole("status")).toHaveTextContent("docs is a PHP site with PHP 8.4 now.");
    expect(calls[0]?.body).toEqual({ php: "8.4" });
  });

  it("turns a PHP site into a static site", async () => {
    const user = userEvent.setup();
    const calls = mockFetch({ "PUT /api/v1/sites/docs/php": () => ({ body: site }) });
    render(<SiteWebSettings site={{ ...site, type: "php", php: "8.2" }} php={php} onChange={async () => undefined} />);
    const card = within(screen.getByRole("region", { name: "Site type" }));
    await user.click(card.getByLabelText(/^Static/));
    expect(card.getByText(/Only the PHP pool is stopped/)).toBeInTheDocument();
    await user.click(card.getByRole("button", { name: "Save type" }));
    expect(await card.findByRole("status")).toHaveTextContent("docs is a static site now.");
    expect(calls[0]?.body).toEqual({ php: "" });
  });
});
