// SPDX-License-Identifier: AGPL-3.0-or-later

import { describe, expect, it } from "vitest";
import { formatHash, parseHash, type Route } from "./routes";

const plain = (r: Route) => ({ ...r, query: r.query.toString() });

describe("parseHash", () => {
  it.each([
    ["", { page: "home", query: "" }],
    ["#", { page: "home", query: "" }],
    ["#/", { page: "home", query: "" }],
    ["#/databases", { page: "databases", query: "" }],
    ["#/databases/", { page: "databases", query: "" }],
    ["#/sites?sort=name&dir=desc", { page: "sites", query: "sort=name&dir=desc" }],
    ["#/sites/shop", { page: "sites", site: { name: "shop", tab: "overview" }, query: "" }],
    ["#/sites/shop-2/php", { page: "sites", site: { name: "shop-2", tab: "php" }, query: "" }],
    ["#/sites/my_shop/cron", { page: "sites", site: { name: "my_shop", tab: "cron" }, query: "" }],
    ["#/sites/shop/files?path=%2Fhtdocs", { page: "sites", site: { name: "shop", tab: "files" }, query: "path=%2Fhtdocs" }],
    ["#/?q=x", { page: "home", query: "q=x" }],
  ])("%s", (hash, want) => {
    expect(plain(parseHash(hash))).toEqual(want);
  });

  it.each([
    "#/nope",
    "#/home",
    "#/invite/abc",
    "#/sites/Shop",
    "#/sites/-shop",
    "#/sites/sh%2Fop",
    "#/sites/shop/nope",
    "#/sites/shop/php/extra",
    "#/databases/x",
    "#/sites/2shop",
    "#/sites/s",
    "#/sites/" + "a".repeat(33),
  ])("treats %s as the overview", (hash) => {
    expect(plain(parseHash(hash))).toEqual({ page: "home", query: "" });
  });
});

describe("formatHash", () => {
  it.each([
    [{ page: "home" }, "#/"],
    [{ page: "backups" }, "#/backups"],
    [{ page: "sites", site: { name: "shop" } }, "#/sites/shop"],
    [{ page: "sites", site: { name: "shop", tab: "overview" } }, "#/sites/shop"],
    [{ page: "sites", site: { name: "shop", tab: "nginx" } }, "#/sites/shop/nginx"],
    [{ page: "sites", query: new URLSearchParams({ sort: "disk", dir: "desc" }) }, "#/sites?sort=disk&dir=desc"],
  ] as const)("%j", (route, want) => {
    expect(formatHash(route)).toBe(want);
  });

  it("round-trips", () => {
    for (const h of ["#/", "#/limits", "#/sites/shop/ssh", "#/databases?q=wp"]) {
      expect(formatHash(parseHash(h))).toBe(h);
    }
  });
});
