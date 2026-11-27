// SPDX-License-Identifier: AGPL-3.0-or-later

import { describe, expect, it } from "vitest";
import { cronLabel, existsHere, hostProblem, parseDBMap, ttlLabel, type PlanSite } from "./cloudpanel";

const site = (action: PlanSite["action"], codes: string[]): PlanSite => ({
  name: "shop",
  type: "php",
  domains: ["shop.example"],
  redirectFrom: [],
  docRoot: "/home/shop/htdocs/shop.example",
  php: "8.3",
  tls: "reissue",
  customSnippets: 0,
  cronJobs: 0,
  cronImport: 0,
  action,
  reasons: codes.map((code) => ({ code, msg: code })),
  source: "/etc/nginx/sites-enabled/shop.example.conf",
});

describe("parseDBMap", () => {
  it("reads pairs and skips empty lines", () => {
    expect(parseDBMap("shop_old shop\n\n  blog_db   blog \n")).toEqual({
      maps: [
        { from: "shop_old", to: "shop" },
        { from: "blog_db", to: "blog" },
      ],
      problem: "",
    });
    expect(parseDBMap("")).toEqual({ maps: [], problem: "" });
  });

  it("rejects bad lines", () => {
    expect(parseDBMap("shop").problem).toMatch(/^Line 1: write the old database/);
    expect(parseDBMap("a b c").problem).toMatch(/^Line 1:/);
    expect(parseDBMap("ok ok\nShop shop").problem).toMatch(/^Line 2: Use 1 to 64/);
    expect(parseDBMap("a shop\nb shop").problem).toBe("Line 2: shop is named twice.");
    expect(parseDBMap("mysql shop").problem).toBe("Line 1: This name is reserved.");
    expect(parseDBMap("a; shop").problem).toMatch(/^Line 1: Use/);
    const many = Array.from({ length: 51 }, (_, i) => `a${i} b${i}`).join("\n");
    expect(parseDBMap(many).problem).toBe("At most 50 databases per import.");
  });
});

describe("hostProblem", () => {
  it("accepts IP addresses and DNS names", () => {
    expect(hostProblem("203.0.113.7", "22")).toBe("");
    expect(hostProblem("web2.example.net", "2222")).toBe("");
    expect(hostProblem("2001:db8::7", "22")).toBe("");
  });

  it("rejects bad hosts and ports", () => {
    expect(hostProblem("", "22")).toBe("Enter the old server's address.");
    expect(hostProblem("web2 .example", "22")).toBe("Use an IP address or a lowercase DNS name.");
    expect(hostProblem("-web2", "22")).toBe("Use an IP address or a lowercase DNS name.");
    expect(hostProblem("web2", "0")).toBe("Use a port from 1 to 65535.");
    expect(hostProblem("web2", "65536")).toBe("Use a port from 1 to 65535.");
    expect(hostProblem("web2", "22a")).toBe("Use a port from 1 to 65535.");
  });
});

describe("existsHere", () => {
  it("is a skip only because the site is here", () => {
    expect(existsHere(site("skip", ["site_exists", "domain_taken"]))).toBe(true);
    expect(existsHere(site("skip", ["site_exists"]))).toBe(true);
    expect(existsHere(site("skip", ["domain_taken"]))).toBe(false);
    expect(existsHere(site("skip", ["site_exists", "user_twice"]))).toBe(false);
    expect(existsHere(site("import", []))).toBe(false);
  });
});

describe("cronLabel", () => {
  it("splits carried and manual jobs", () => {
    expect(cronLabel({ cronJobs: 3, cronImport: 3 })).toBe("3: 3 created paused");
    expect(cronLabel({ cronJobs: 3, cronImport: 1 })).toBe("3: 1 created paused, 2 to set up by hand");
    expect(cronLabel({ cronJobs: 2, cronImport: 0 })).toBe("2: 2 to set up by hand");
  });
});

describe("ttlLabel", () => {
  it("uses the largest whole unit", () => {
    expect(ttlLabel(0)).toBe("0 s");
    expect(ttlLabel(59)).toBe("59 s");
    expect(ttlLabel(90)).toBe("90 s");
    expect(ttlLabel(300)).toBe("5 min");
    expect(ttlLabel(5400)).toBe("90 min");
    expect(ttlLabel(3600)).toBe("1 h");
    expect(ttlLabel(86400)).toBe("1 d");
    expect(ttlLabel(90000)).toBe("25 h");
  });

  it("returns nothing for values that are not a TTL", () => {
    expect(ttlLabel(-1)).toBe("");
    expect(ttlLabel(Number.NaN)).toBe("");
  });
});
