// SPDX-License-Identifier: AGPL-3.0-or-later

import { describe, expect, it } from "vitest";
import { hadWWW, newNames, planPrimaryChange, waitsForCertificate, wpSearchReplace } from "./domains";
import type { Site } from "./sites";

const site = (over: Partial<Site> = {}): Site =>
  ({
    name: "shop",
    domains: ["old.example", "www.old.example", "shop.example.net"],
    redirectDomains: [],
    tls: { enabled: true, active: true, hsts: false, domains: ["old.example", "www.old.example", "shop.example.net"], source: "letsencrypt" },
    ...over,
  }) as unknown as Site;

describe("planPrimaryChange", () => {
  it("moves the old pair to redirect domains", () => {
    const s = site();
    expect(hadWWW(s)).toBe(true);
    expect(planPrimaryChange(s, { primary: "New.Example", withWWW: true, redirectOld: true })).toEqual({
      domains: ["new.example", "www.new.example", "shop.example.net"],
      redirectDomains: ["old.example", "www.old.example"],
    });
  });

  it("drops the old names without redirect", () => {
    expect(planPrimaryChange(site(), { primary: "new.example", withWWW: false, redirectOld: false })).toEqual({
      domains: ["new.example", "shop.example.net"],
      redirectDomains: [],
    });
  });

  it("keeps redirect domains, but not one that is served now", () => {
    const s = site({ redirectDomains: ["older.example", "new.example"] });
    expect(planPrimaryChange(s, { primary: "new.example", withWWW: false, redirectOld: true }).redirectDomains).toEqual([
      "older.example",
      "old.example",
      "www.old.example",
    ]);
  });

  it("promotes an existing alias without listing it twice", () => {
    expect(planPrimaryChange(site(), { primary: "shop.example.net", withWWW: false, redirectOld: true })).toEqual({
      domains: ["shop.example.net"],
      redirectDomains: ["old.example", "www.old.example"],
    });
  });

  it("adds no www name to a www primary", () => {
    expect(planPrimaryChange(site(), { primary: "www.new.example", withWWW: true, redirectOld: false }).domains[0]).toBe("www.new.example");
  });
});

describe("certificate", () => {
  it("waits when HTTPS is live and a name is new", () => {
    const s = site();
    const p = planPrimaryChange(s, { primary: "new.example", withWWW: false, redirectOld: true });
    expect(newNames(s, p)).toEqual(["new.example"]);
    expect(waitsForCertificate(s, p)).toBe(true);
    expect(waitsForCertificate(site({ tls: { ...s.tls, active: false } }), p)).toBe(false);
    expect(waitsForCertificate(site({ tls: { ...s.tls, source: "custom" } }), p)).toBe(false);
  });

  it("does not wait when a wildcard covers the names", () => {
    const s = site({ tls: { ...site().tls, domains: ["*.example.net", "old.example", "www.old.example"] } });
    expect(waitsForCertificate(s, { domains: ["a.example.net"], redirectDomains: ["old.example"] })).toBe(false);
    expect(waitsForCertificate(s, { domains: ["a.b.example.net"], redirectDomains: [] })).toBe(true);
  });

  it("builds the WordPress command", () => {
    expect(wpSearchReplace(site(), "new.example")).toBe("wp search-replace 'https://old.example' 'https://new.example' --all-tables");
  });
});
