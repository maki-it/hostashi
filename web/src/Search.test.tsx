// SPDX-License-Identifier: AGPL-3.0-or-later

import { fireEvent, render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import userEvent from "@testing-library/user-event";
import { matchItems, Search, searchItems } from "./Search";
import type { Site } from "./sites";
import { mockFetch } from "./test-utils";

const site = (name: string, extra: Partial<Site> = {}) =>
  ({ name, title: "", state: "active", php: "8.4", type: "php", domains: [`${name}.example.com`], account: true, ...extra }) as Site;

describe("search items", () => {
  it("finds sites, their tabs and pages by every word", () => {
    const items = searchItems("admin", [site("blog"), site("shop", { title: "Müller Shop" })]);
    expect(matchItems(items, "blog logs").map((i) => i.label)).toEqual(["blog › Logs"]);
    expect(matchItems(items, "müller").map((i) => i.label)).toContain("Müller Shop");
    expect(matchItems(items, "shop.example").map((i) => i.group)).toContain("sites");
    expect(matchItems(items, "backups").map((i) => i.href)).toContain("#/backups");
    expect(matchItems(items, "blog php")[0]!.href).toBe("#/sites/blog/php");
    expect(matchItems(items, "blog lo").filter((i) => i.group === "sections")[0]!.label).toBe("blog › Logs");
  });

  it("leaves out tabs a site does not have and pages the role does not see", () => {
    const items = searchItems("user", [site("docs", { php: "", type: "static" })]);
    expect(matchItems(items, "docs php")).toEqual([]);
    expect(matchItems(items, "firewall")).toEqual([]);
    expect(matchItems(items, "account").map((i) => i.label)).toEqual(["Your account"]);
  });

  it("finds each runtime on the Runtimes page", () => {
    const hits = matchItems(searchItems("admin", []), "python");
    expect(hits.map((h) => [h.label, h.href])).toEqual([["Runtimes › Python", "#/python"]]);
  });

  it("shows at most six hits per group", () => {
    const sites = Array.from({ length: 10 }, (_, i) => site(`site${i}`));
    expect(matchItems(searchItems("admin", sites), "site").filter((i) => i.group === "sites")).toHaveLength(6);
  });
});

describe("Search", () => {
  it("opens the chosen hit with Enter and closes with Esc", async () => {
    mockFetch({ "GET /api/v1/sites": { body: { sites: [site("blog")] } } });
    const close = vi.fn();
    render(<Search role="admin" onClose={close} />);
    const input = screen.getByRole("combobox", { name: "Search sites, site sections and pages" });
    await userEvent.type(input, "blog cron");
    expect(await screen.findByRole("option", { name: /blog › Cron/ })).toHaveAttribute("aria-selected", "true");
    fireEvent.keyDown(input, { key: "Enter" });
    expect(window.location.hash).toBe("#/sites/blog/cron");
    expect(close).toHaveBeenCalled();
    fireEvent.keyDown(input, { key: "Escape" });
    expect(close).toHaveBeenCalledTimes(2);
    window.location.hash = "";
  });
});
