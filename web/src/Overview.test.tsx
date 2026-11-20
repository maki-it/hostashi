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

describe("Overview", () => {
  it("shows what needs attention and the state at a glance", async () => {
    mockFetch({
      "GET /api/v1/sites": { body: { sites: [site("shop"), broken, site("old", { disabled: true })] } },
      "GET /api/v1/doctor": { body: doctor },
      "GET /api/v1/backups": { body: backups(true) },
      "GET /api/v1/parked-domains": { body: { parkedDomains: [] } },
    });
    render(<Overview user={{ username: "kim", role: "admin", secondFactor: true } as never} />);
    const list = within(await screen.findByRole("region", { name: "Needs attention" }));
    expect(list.getAllByRole("listitem")).toHaveLength(2);
    expect(list.getByRole("link", { name: "Open wiki" })).toHaveAttribute("href", "#/sites/wiki");
    const sites = within(screen.getByRole("region", { name: "Sites" }));
    expect(sites.getByText("3")).toBeInTheDocument();
    expect(sites.getByText("1 disabled")).toBeInTheDocument();
    expect(within(screen.getByRole("region", { name: "Backups" })).getByText("OK")).toBeInTheDocument();
  });

  it("asks only for what a user may see", async () => {
    const calls = mockFetch({ "GET /api/v1/sites": { body: { sites: [site("shop")] } } });
    render(<Overview user={{ username: "uma", role: "user", secondFactor: true } as never} />);
    expect(await screen.findByText("Nothing needs attention.")).toBeInTheDocument();
    expect(calls.map((c) => c.path)).toEqual(["/api/v1/sites"]);
    expect(screen.queryByRole("region", { name: "Health" })).not.toBeInTheDocument();
  });
});
