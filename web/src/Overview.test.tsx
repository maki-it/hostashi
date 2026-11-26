// SPDX-License-Identifier: AGPL-3.0-or-later

import { render, screen, within } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import type { BackupInfo } from "./backups";
import type { DoctorReport } from "./doctor";
import { attention, Overview } from "./Overview";
import type { Site } from "./sites";
import { mockFetch } from "./test-utils";

const site = (name: string, extra: Partial<Site> = {}): Site => ({
  name,
  pool: "default",
  state: "active",
  php: "8.4",
  phpPrograms: false,
  disabled: false,
  disabledPage: "builtin-maintenance",
  maintenance: { enabled: false, page: "builtin-maintenance", allowIps: [] },
  domains: [name + ".example"],
  tls: { enabled: true, active: true, hsts: false, domains: [], notAfter: null },
  createdAt: "2026-11-19T08:00:00Z",
  updatedAt: "2026-11-19T08:00:00Z",
  ...extra,
});
const broken = site("wiki", { tls: { enabled: true, active: false, hsts: false, domains: [], notAfter: null, error: "no DNS record" } });
const doctor: DoctorReport = {
  status: "fail",
  checkedAt: "2026-11-20T09:30:00Z",
  counts: { ok: 1, warn: 1, fail: 1 },
  checks: [
    { id: "system.os", status: "ok", summary: "supported" },
    { id: "system.quota", status: "warn", summary: "no project quota", hint: "Run the installer again." },
    { id: "site.tls", site: "wiki", status: "fail", summary: "certificate expired" },
  ],
};
const backups = (ok: boolean): BackupInfo =>
  ({
    ready: true,
    status: { running: false, started: "2026-11-20T02:00:00Z", finished: "2026-11-20T02:10:00Z", ok, error: ok ? "" : "repository locked", items: [], maintenanceOk: true },
  }) as unknown as BackupInfo;

describe("attention", () => {
  it("warns when no backup finished within the interval", () => {
    const late = { ...backups(true), overdue: true, settings: { intervalHours: 1 } } as unknown as BackupInfo;
    const items = attention({ backups: late });
    expect(items.map((i) => [i.key, i.tone])).toEqual([["backups-overdue", "warn"]]);
    expect(items[0]!.detail).toBe("No backup has finished in the last 3 hours.");
    expect(attention({ backups: backups(true) })).toEqual([]);
  });

  it("takes the doctor's problems, failures first, and a failed backup", () => {
    const items = attention({ sites: [broken], doctor, backups: backups(false) });
    expect(items.map((i) => i.word)).toEqual(["Failed", "Failed", "Warning"]);
    expect(items[0]!.title).toBe("The last backup failed");
    expect(items[1]!.title).toContain("site wiki: certificate expired");
    expect(items.some((i) => i.key === "tls-wiki")).toBe(false);
  });

  it("falls back to failed site certificates without health", () => {
    const items = attention({ sites: [site("shop"), broken] });
    expect(items).toHaveLength(1);
    expect(items[0]!.href).toBe("#/sites/wiki/domains");
    expect(items[0]!.detail).toBe("no DNS record");
  });
});

describe("attention for own certificates", () => {
  const inDays = (n: number) => new Date(Date.now() + n * 86_400_000 + 3_600_000).toISOString();
  const own = (name: string, days: number) =>
    site(name, { tls: { enabled: true, active: true, hsts: false, domains: [], notAfter: inDays(days), source: "custom", certSource: "custom" } });

  it("warns 30 days before and fails 7 days before, instead of the doctor", () => {
    const report: DoctorReport = { ...doctor, checks: [{ id: "site.tls", site: "shop", status: "warn", summary: "valid until soon" }] };
    const items = attention({ sites: [own("shop", 20), own("blog", 3), own("calm", 60), site("le")], doctor: report });
    expect(items.map((i) => [i.key, i.tone])).toEqual([
      ["owncert-blog", "err"],
      ["owncert-shop", "warn"],
    ]);
    expect(items[1]?.title).toContain("expires in 20 days");
  });

  it("covers parked domains too", () => {
    const parked = { domain: "soon.example", page: "builtin-parking", createdAt: "", updatedAt: "",
      tls: { enabled: true, active: true, notAfter: inDays(5), source: "custom" as const, certSource: "custom" as const } };
    expect(attention({ parked: [parked] }).map((i) => [i.key, i.tone])).toEqual([["owncert-park-soon.example", "err"]]);
  });

  it("says when it has expired", () => {
    expect(attention({ sites: [own("shop", -2)] })[0]?.title).toContain("has expired");
  });
});

describe("Overview", () => {
  it("shows what needs attention and the state at a glance", async () => {
    mockFetch({
      "GET /api/v1/sites": { body: { sites: [site("shop"), broken, site("old", { disabled: true })] } },
      "GET /api/v1/doctor": { body: doctor },
      "GET /api/v1/backups": { body: backups(true) },
      "GET /api/v1/parked-domains": { body: { parkedDomains: [] } },
      "GET /api/v1/pools": {
        body: {
          pools: [
            { name: "default", path: "/home", sites: 2, available: true, quota: "enforced", freeBytes: 10 * 2 ** 30, totalBytes: 40 * 2 ** 30, default: true },
            { name: "vol1", path: "/srv/vol1", sites: 1, available: false, default: false },
          ],
        },
      },
    });
    render(<Overview user={{ username: "kim", role: "admin", secondFactor: true } as never} />);
    const list = within(await screen.findByRole("region", { name: "Needs attention" }));
    expect(list.getAllByRole("listitem")).toHaveLength(2);
    expect(list.getByRole("link", { name: "Open wiki" })).toHaveAttribute("href", "#/sites/wiki");
    const sites = within(screen.getByRole("region", { name: "Sites" }));
    expect(sites.getByText("3")).toBeInTheDocument();
    expect(sites.getByText("1 disabled")).toBeInTheDocument();
    expect(within(screen.getByRole("region", { name: "Backups" })).getByText("OK")).toBeInTheDocument();
    const pools = within(screen.getByRole("region", { name: "Storage pools" }));
    expect(pools.getByRole("meter", { name: "default (default)" })).toHaveAttribute("aria-valuenow", "75");
    expect(pools.getByText("Not available")).toBeInTheDocument();
    expect(pools.getByRole("link", { name: "Open storage pools" })).toHaveAttribute("href", "#/pools");
  });

  it("asks only for what a user may see", async () => {
    const calls = mockFetch({
      "GET /api/v1/sites": { body: { sites: [site("shop")] } },
      "GET /api/v1/sites/shop/cron": { body: { statusKnown: true, jobs: [] } },
    });
    render(<Overview user={{ username: "uma", role: "user", secondFactor: true } as never} />);
    expect(await screen.findByText("Nothing needs attention.")).toBeInTheDocument();
    expect(calls.map((c) => c.path)).toEqual(["/api/v1/sites", "/api/v1/sites/shop/cron"]);
    expect(screen.queryByRole("region", { name: "Health" })).not.toBeInTheDocument();
  });

  it("shows a user the virus finds and failing cron jobs of their sites", async () => {
    mockFetch({
      "GET /api/v1/sites": { body: { sites: [site("shop")], marks: { shop: { openFindings: 2, cloneOf: "", staging: false } } } },
      "GET /api/v1/sites/shop/cron": {
        body: { statusKnown: true, jobs: [{ id: 1, schedule: "@daily", command: "./x.sh", timeoutSec: 0, nice: null, paused: false, createdAt: 1, updatedAt: 1, status: { running: false, result: "exit-code", exitStatus: 1, lastStart: 1795600000 } }] },
      },
    });
    render(<Overview user={{ username: "uma", role: "user", secondFactor: true } as never} />);
    expect(await screen.findByText("2 files flagged by the virus scan on site shop")).toBeInTheDocument();
    expect(screen.getByText("1 cron job of site shop fails")).toBeInTheDocument();
    expect(screen.getByRole("link", { name: "Open cron jobs" })).toHaveAttribute("href", "#/sites/shop/cron");
  });
});
