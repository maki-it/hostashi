// SPDX-License-Identifier: AGPL-3.0-or-later

import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import { groupsForSite } from "./SiteNav";
import { QuickActions, SiteActions, StatusTiles } from "./SiteStatus";
import type { Site } from "./sites";
import { mockFetch } from "./test-utils";

const blog: Site = {
  name: "blog",
  pool: "default",
  state: "active",
  php: "8.4",
  phpPrograms: false,
  disabled: false,
  disabledPage: "builtin-maintenance",
  maintenance: { enabled: false, page: "builtin-maintenance", allowIps: [] },
  domains: ["blog.example.com"],
  tls: { enabled: true, active: true, hsts: true, domains: ["blog.example.com"], notAfter: "2027-02-14T00:00:00Z" },
  createdAt: "2026-11-19T08:00:00Z",
  updatedAt: "2026-11-19T08:00:00Z",
};
const GiB = 1024 ** 3;
const base = "/api/v1/sites/blog";
const limits = { set: true, preset: "", limits: { diskBytes: 10 * GiB }, appliedAt: 1, applyError: "", updatedAt: 1, usage: { enforced: true, bytes: 2 * GiB, inodes: 12000, bytesPercent: 20, inodesPercent: 2, level: "ok" } };
const job = { schedule: "@daily", timeoutSec: 0, nice: null, createdAt: 1, updatedAt: 1 };

function routes(extra: Record<string, { status?: number; body?: unknown }> = {}) {
  return mockFetch({
    ["GET " + base + "/limits"]: { body: limits },
    ["GET " + base + "/malware-scan"]: {
      body: { engine: "on", schedule: "weekly", nextScheduled: "", runs: [], findings: [{ id: 1, path: "htdocs/x.php", signature: "S", sha256: "", size: 1, state: "open", firstSeen: "", lastSeen: "", decidedAt: "" }] },
    },
    ["GET " + base + "/cron"]: {
      body: { statusKnown: true, jobs: [{ ...job, id: 1, command: "./backup.sh", paused: false, status: { running: false, result: "exit-code", exitStatus: 3, lastStart: 1795600000 } }] },
    },
    ["GET " + base + "/git"]: { body: { configured: false } },
    ...extra,
  });
}

describe("StatusTiles", () => {
  it("shows each part of the site and marks what needs a look", async () => {
    routes();
    render(<StatusTiles site={blog} groups={groupsForSite(blog)} admin={false} />);
    const tiles = within(screen.getByRole("list", { name: "Status of blog" }));

    const https = tiles.getByRole("link", { name: /HTTPS/ });
    expect(https).toHaveTextContent("Valid until 2027-02-14");
    expect(https).toHaveAttribute("href", "#/sites/blog/https");
    expect(https).not.toHaveAttribute("data-tone");

    expect(await tiles.findByRole("link", { name: /Disk/ })).toHaveTextContent("2 GiB of 10 GiB");
    const virus = await tiles.findByRole("link", { name: /Virus scan/ });
    expect(virus).toHaveTextContent("1 file flagged");
    expect(virus).toHaveAttribute("data-tone", "err");
    const cron = await tiles.findByRole("link", { name: /Cron/ });
    expect(cron).toHaveTextContent("1 job failed");
    expect(cron).toHaveTextContent("./backup.sh");
    expect(cron).toHaveAttribute("data-tone", "warn");
    // No Git tile without a repository, no backup tile for non-admins.
    expect(tiles.queryByRole("link", { name: /Git deploy/ })).toBeNull();
    expect(tiles.queryByRole("link", { name: /Backup/ })).toBeNull();
  });

  it("shows the backup and the last deploy to an administrator", async () => {
    routes({
      "GET /api/v1/backups": { body: { ready: true, overdue: false, status: { running: false, ok: true, finished: new Date(Date.now() - 3 * 3600e3).toISOString(), items: [{ kind: "files", site: "blog" }], maintenanceOk: true } } },
      ["GET " + base + "/git"]: { body: { configured: true, branch: "main" } },
      ["GET " + base + "/deploys"]: { body: [{ id: 2, commit: "3f2a1c9e00", state: "succeeded", createdAt: "2026-11-24T10:00:00Z", finishedAt: "2026-11-24T10:01:00Z" }] },
    });
    render(<StatusTiles site={blog} groups={groupsForSite(blog)} admin />);
    const tiles = within(screen.getByRole("list", { name: "Status of blog" }));
    const backup = await tiles.findByRole("link", { name: /Backup/ });
    expect(backup).toHaveTextContent("3 hours ago");
    expect(backup).toHaveAttribute("href", "#/backups");
    expect(await tiles.findByRole("link", { name: /Git deploy/ })).toHaveTextContent("main @ 3f2a1c9");
  });

  it("marks a certificate that failed", () => {
    routes();
    render(<StatusTiles site={{ ...blog, tls: { ...blog.tls, active: false, error: "no DNS record" } }} groups={groupsForSite(blog)} admin={false} />);
    const https = screen.getByRole("link", { name: /HTTPS/ });
    expect(https).toHaveTextContent("Certificate failed");
    expect(https).toHaveTextContent("no DNS record");
    expect(https).toHaveAttribute("data-tone", "err");
  });
});

describe("QuickActions", () => {
  it("opens the site and its most used tabs", () => {
    render(<QuickActions site={blog} groups={groupsForSite(blog)} />);
    expect(screen.getByRole("link", { name: "Open site" })).toHaveAttribute("href", "https://blog.example.com");
    expect(screen.getByRole("link", { name: "File manager" })).toHaveAttribute("href", "#/sites/blog/files");
    expect(screen.getByRole("link", { name: "SSH & SFTP" })).toHaveAttribute("href", "#/sites/blog/ssh");
    expect(screen.getByRole("link", { name: "Databases" })).toHaveAttribute("href", "#/databases?site=blog");
  });
});

describe("SiteActions", () => {
  it("opens with the keyboard and goes to a tab", async () => {
    const user = userEvent.setup();
    const onTab = vi.fn();
    const onCard = vi.fn();
    render(<SiteActions site={blog} groups={groupsForSite(blog)} staff admin onTab={onTab} onCard={onCard} />);
    const button = screen.getByRole("button", { name: "Actions" });
    expect(button).toHaveAttribute("aria-expanded", "false");
    button.focus();
    await user.keyboard("{ArrowDown}");
    const menu = screen.getByRole("menu", { name: "Actions for blog" });
    expect(within(menu).getAllByRole("menuitem").map((i) => i.textContent)).toEqual(["Rename…", "Copy site…", "Change domains…", "Databases…", "Restart PHP…", "Reset permissions…"]);
    expect(within(menu).getByRole("menuitem", { name: "Rename…" })).toHaveFocus();
    await user.keyboard("{ArrowDown}{ArrowDown}{Enter}");
    expect(onTab).toHaveBeenCalledWith("domains");
    expect(screen.queryByRole("menu")).toBeNull();
    // Rename opens its card on the overview.
    await user.click(screen.getByRole("button", { name: "Actions" }));
    await user.click(screen.getByRole("menuitem", { name: "Rename…" }));
    expect(onCard).toHaveBeenCalledWith("rename");
    expect(onTab).toHaveBeenLastCalledWith("overview");
  });

  it("closes with Escape and leaves out copying for users", async () => {
    const user = userEvent.setup();
    render(<SiteActions site={blog} groups={groupsForSite(blog)} staff={false} admin={false} onTab={() => undefined} onCard={() => undefined} />);
    await user.click(screen.getByRole("button", { name: "Actions" }));
    expect(screen.queryByRole("menuitem", { name: "Copy site…" })).toBeNull();
    expect(screen.queryByRole("menuitem", { name: "Restart PHP…" })).toBeNull();
    expect(screen.queryByRole("menuitem", { name: "Change domains…" })).toBeNull();
    await user.keyboard("{Escape}");
    expect(screen.queryByRole("menu")).toBeNull();
    expect(screen.getByRole("button", { name: "Actions" })).toHaveFocus();
  });
});
