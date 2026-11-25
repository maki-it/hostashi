// SPDX-License-Identifier: AGPL-3.0-or-later

import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { useState } from "react";
import { describe, expect, it } from "vitest";
import { Backups } from "./Backups";
import type { BackupInfo } from "./backups";
import { mockFetch } from "./test-utils";

const status = { running: false, ok: false, items: [], maintenanceOk: true };
const defaults = { version: 1, retention: { hourly: 0, daily: 7, weekly: 4, monthly: 6, yearly: 0 }, intervalHours: 24, windowStart: 1, windowEnd: 5, readParts: 12 };
const target = { user: "u123456", host: "u123456.your-storagebox.de", port: 23, path: "hostashi" };
const pub = "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIGtest hostashi-backup@web3";

const fresh: BackupInfo = { settings: defaults, publicKey: "", hostKey: "", ready: false, status };
const withTarget: BackupInfo = { ...fresh, settings: { ...defaults, target }, publicKey: pub };
const ready: BackupInfo = {
  ...withTarget,
  hostKey: "SHA256:abc",
  ready: true,
  status: { ...status, ok: true, started: "2026-11-19T01:12:00Z", finished: "2026-11-19T01:20:00Z" },
};

// Harness keeps the address query in state, as the app does.
function Harness({ initial = "" }: { initial?: string }) {
  const [query, setQuery] = useState(new URLSearchParams(initial));
  return <Backups query={query} onQuery={setQuery} />;
}

const snapFiles = "a".repeat(64);
const snapDB = "b".repeat(64);

describe("Backups", () => {
  it("sets a Storage Box target and shows the key", async () => {
    const user = userEvent.setup();
    const calls = mockFetch({
      "GET /api/v1/backups": { body: fresh },
      "PUT /api/v1/backups/settings": { body: withTarget },
    });
    render(<Harness />);
    expect(await screen.findByText(/Not set up yet/)).toBeInTheDocument();
    await user.type(screen.getByLabelText("User"), "u123456");
    await user.type(screen.getByLabelText("Host"), "U123456.your-storagebox.de ");
    const port = screen.getByLabelText("Port");
    await user.clear(port);
    await user.type(port, "23");
    await user.click(screen.getByRole("button", { name: "Save" }));
    expect(await screen.findByLabelText("Public key")).toHaveTextContent(pub);
    expect(calls.find((c) => c.method === "PUT")?.body).toEqual({ ...defaults, target });
    expect(screen.getByRole("heading", { name: "Host key" })).toBeInTheDocument();
  });

  it("sets a plain SFTP server on port 22 by default", async () => {
    const user = userEvent.setup();
    const nas = { user: "backup_web3", host: "192.0.2.10", port: 22, path: "/srv/backups/web3" };
    const calls = mockFetch({
      "GET /api/v1/backups": { body: fresh },
      "PUT /api/v1/backups/settings": { body: { ...fresh, settings: { ...defaults, target: nas }, publicKey: pub } },
    });
    render(<Harness />);
    expect(await screen.findByText(/Not set up yet/)).toBeInTheDocument();
    expect(screen.getByLabelText("Port")).toHaveValue("22");
    await user.type(screen.getByLabelText("User"), "backup_web3");
    await user.type(screen.getByLabelText("Host"), "192.0.2.10");
    const dir = screen.getByLabelText("Directory");
    await user.clear(dir);
    await user.type(dir, "/srv/backups/web3");
    await user.click(screen.getByRole("button", { name: "Save" }));
    expect(await screen.findByLabelText("Public key")).toHaveTextContent(pub);
    expect(calls.find((c) => c.method === "PUT")?.body).toEqual({ ...defaults, target: nas });
    expect(screen.queryByText(/Hetzner.s console/)).not.toBeInTheDocument();
  });

  it("runs backups every hour and keeps hourly and yearly snapshots", async () => {
    const user = userEvent.setup();
    const nas = { user: "backup", host: "192.0.2.10", port: 22, path: "hostashi" };
    const hourly = { ...defaults, target: nas, intervalHours: 1, retention: { ...defaults.retention, hourly: 48, yearly: 3 } };
    const calls = mockFetch({
      "GET /api/v1/backups": { body: fresh },
      "PUT /api/v1/backups/settings": { body: { ...fresh, settings: hourly, publicKey: pub } },
    });
    render(<Harness />);
    expect(await screen.findByLabelText("Start from (hour)")).toBeInTheDocument();
    await user.type(screen.getByLabelText("User"), "backup");
    await user.type(screen.getByLabelText("Host"), "192.0.2.10");
    await user.selectOptions(screen.getByLabelText("Run backups"), "Every hour");
    expect(screen.queryByLabelText("Start from (hour)")).not.toBeInTheDocument();
    expect(screen.getByText(/skips that one/)).toBeInTheDocument();
    const h = screen.getByLabelText("Hourly");
    await user.clear(h);
    await user.type(h, "48");
    const y = screen.getByLabelText("Yearly");
    await user.clear(y);
    await user.type(y, "3");
    await user.click(screen.getByRole("button", { name: "Save" }));
    expect(await screen.findByLabelText("Public key")).toHaveTextContent(pub);
    expect(calls.find((c) => c.method === "PUT")?.body).toEqual(hourly);
    expect(screen.getByLabelText("Run backups")).toHaveValue("1");
  });

  it("shows an older server's settings as daily without hourly or yearly snapshots", async () => {
    const old = { version: 1, retention: { daily: 7, weekly: 4, monthly: 6 }, windowStart: 1, windowEnd: 5, readParts: 12 };
    mockFetch({ "GET /api/v1/backups": { body: { ...fresh, settings: old } } });
    render(<Harness />);
    expect(await screen.findByLabelText("Run backups")).toHaveValue("24");
    expect(screen.getByLabelText("Hourly")).toHaveValue("0");
    expect(screen.getByLabelText("Yearly")).toHaveValue("0");
  });

  it("shows an overdue backup with the interval", async () => {
    mockFetch({
      "GET /api/v1/backups": { body: { ...ready, overdue: true, settings: { ...ready.settings, intervalHours: 6 } } },
      "GET /api/v1/sites": { body: { sites: [] } },
    });
    render(<Harness />);
    expect(await screen.findByText(/No backup has finished in the last 8 hours, although backups run every 6 hours/)).toBeInTheDocument();
    expect(screen.getByText("Overdue")).toBeInTheDocument();
  });

  it("warns when a Storage Box is set on another port than 23", async () => {
    const user = userEvent.setup();
    mockFetch({ "GET /api/v1/backups": { body: fresh } });
    render(<Harness />);
    await user.type(await screen.findByLabelText("Host"), "u123456.your-storagebox.de");
    expect(screen.getByText("Hetzner Storage Boxes serve SFTP on port 23.")).toBeInTheDocument();
    const port = screen.getByLabelText("Port");
    await user.clear(port);
    await user.type(port, "23");
    expect(screen.queryByText("Hetzner Storage Boxes serve SFTP on port 23.")).not.toBeInTheDocument();
  });

  it("shows the fingerprint and trusts the key only on confirmation", async () => {
    const user = userEvent.setup();
    const calls = mockFetch({
      "GET /api/v1/backups": { body: withTarget },
      "POST /api/v1/backups/hostkey/scan": { body: { key: "ssh-ed25519 AAAAhost", fingerprint: "SHA256:abc" } },
      "PUT /api/v1/backups/hostkey": { body: ready },
      "GET /api/v1/sites": { body: { sites: [] } },
    });
    render(<Harness />);
    await user.click(await screen.findByRole("button", { name: "Fetch the host key" }));
    expect(await screen.findByText(/Compare it with the fingerprint of the server/)).toHaveTextContent("SHA256:abc");
    expect(calls.filter((c) => c.method === "PUT")).toHaveLength(0);
    await user.click(screen.getByRole("button", { name: "Trust this key" }));
    expect(await screen.findByText("Trusted:")).toBeInTheDocument();
    expect(calls.find((c) => c.method === "PUT")?.body).toEqual({ key: "ssh-ed25519 AAAAhost" });
    expect(await screen.findByText("No sites yet.")).toBeInTheDocument();
  });

  it("explains an unreachable target", async () => {
    const user = userEvent.setup();
    mockFetch({
      "GET /api/v1/backups": { body: withTarget },
      "POST /api/v1/backups/hostkey/scan": { status: 502, body: { error: { code: "unreachable", message: "x" } } },
    });
    render(<Harness />);
    await user.click(await screen.findByRole("button", { name: "Fetch the host key" }));
    expect(await screen.findByRole("alert")).toHaveTextContent("did not answer over SSH");
  });

  it("starts a backup", async () => {
    const user = userEvent.setup();
    const calls = mockFetch({
      "GET /api/v1/backups": { body: ready },
      "POST /api/v1/backups/run": { status: 202 },
      "GET /api/v1/sites": { body: { sites: [] } },
    });
    render(<Harness />);
    expect(await screen.findByText("The last backup succeeded.")).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Back up now" }));
    expect(await screen.findByText(/Backup started/)).toBeInTheDocument();
    expect(calls.some((c) => c.method === "POST" && c.path === "/api/v1/backups/run")).toBe(true);
  });

  it("restores files and imports a dump into a new database", async () => {
    const user = userEvent.setup();
    const restore = { site: "blog", files: 3, bytes: 10, state: "done" };
    const calls = mockFetch({
      "GET /api/v1/backups": { body: ready },
      "GET /api/v1/sites": { body: { sites: [{ name: "blog", state: "active" }] } },
      "GET /api/v1/sites/blog/backups": {
        body: {
          snapshots: [
            { id: snapFiles, time: "2026-11-19T01:12:00Z", kind: "files" },
            { id: snapDB, time: "2026-11-19T01:13:00Z", kind: "db", database: "blog_wp" },
          ],
        },
      },
      ["POST /api/v1/sites/blog/backups/" + snapFiles + "/restore"]: {
        status: 202,
        body: { ...restore, id: "1111111111111111", snapshot: snapFiles, kind: "files", dir: "restore-1", state: "queued" },
      },
      "GET /api/v1/backups/restores/1111111111111111": { body: { ...restore, id: "1111111111111111", kind: "files", dir: "restore-1" } },
      ["POST /api/v1/sites/blog/backups/" + snapDB + "/restore"]: {
        status: 202,
        body: { ...restore, id: "2222222222222222", kind: "db", database: "blog_wp", dir: "restore-2", state: "queued" },
      },
      "GET /api/v1/backups/restores/2222222222222222": { body: { ...restore, id: "2222222222222222", kind: "db", database: "blog_wp", dir: "restore-2" } },
      "POST /api/v1/sites/blog/databases": { status: 201, body: { name: "blog_wp_restored" } },
      "POST /api/v1/backups/restores/2222222222222222/import": { status: 202, body: { id: "3333333333333333", state: "running" } },
      "GET /api/v1/backups/imports/3333333333333333": { body: { id: "3333333333333333", state: "done" } },
    });
    render(<Harness />);
    const table = await screen.findByRole("table", { name: "Snapshots of blog" });
    // Newest first: the database dump of 01:13, then the files of 01:12.
    const rows = within(table).getAllByRole("row").slice(1);
    expect(within(rows[0]!).getByText("Database blog_wp")).toBeInTheDocument();
    await user.click(within(rows[1]!).getByRole("button", { name: /^Restore files of / }));
    expect(await screen.findByText(/Restored 3 files into/)).toHaveTextContent("restore-1");
    await user.click(within(rows[0]!).getByRole("button", { name: /^Restore database blog_wp of / }));
    await user.click(await screen.findByRole("button", { name: "Create and import" }));
    expect(await screen.findByText(/The dump is in the new database/)).toHaveTextContent("blog_wp_restored");
    expect(calls.find((c) => c.path === "/api/v1/sites/blog/databases")?.body).toEqual({ name: "blog_wp_restored" });
    expect(calls.find((c) => c.path.endsWith("/import"))?.body).toEqual({ database: "blog_wp_restored" });
  });

  describe("a PostgreSQL dump", () => {
    const local = { name: "localhost", local: true, default: true, engine: "MariaDB", state: "ok", databases: 1, tlsMode: "socket", tlsActive: false };
    const pg1 = { ...local, name: "pg1", local: false, default: false, engine: "PostgreSQL", host: "pg.example.net", port: 5432 };
    const pgRoutes = (servers: unknown[]) => {
      const restore = { site: "blog", files: 1, bytes: 10, state: "done", id: "2222222222222222", kind: "db", database: "shop_pg", dir: "restore-2" };
      return {
        "GET /api/v1/backups": { body: ready },
        "GET /api/v1/sites": { body: { sites: [{ name: "blog", state: "active" }] } },
        "GET /api/v1/sites/blog/backups": { body: { snapshots: [{ id: snapDB, time: "2026-11-19T01:13:00Z", kind: "db", database: "shop_pg" }] } },
        ["POST /api/v1/sites/blog/backups/" + snapDB + "/restore"]: { status: 202, body: { ...restore, state: "queued" } },
        "GET /api/v1/backups/restores/2222222222222222": { body: { ...restore, dumpFile: "shop_pg.dump" } },
        "GET /api/v1/db-servers": { body: { servers } },
        "POST /api/v1/sites/blog/databases": { status: 201, body: { name: "shop_pg_restored" } },
        "POST /api/v1/backups/restores/2222222222222222/import": { status: 202, body: { id: "3333333333333333", state: "running" } },
        "GET /api/v1/backups/imports/3333333333333333": { body: { id: "3333333333333333", state: "done" } },
      };
    };

    it("goes into a new database on a PostgreSQL server", async () => {
      const user = userEvent.setup();
      const calls = mockFetch(pgRoutes([local, pg1]));
      render(<Harness />);
      await user.click(await screen.findByRole("button", { name: /^Restore database shop_pg of / }));
      await user.click(await screen.findByRole("button", { name: "Create and import" }));
      expect(await screen.findByText(/The dump is in the new database/)).toBeInTheDocument();
      // Only one PostgreSQL server: no choice to make.
      expect(screen.queryByLabelText("On the server")).not.toBeInTheDocument();
      expect(calls.find((c) => c.path === "/api/v1/sites/blog/databases")?.body).toEqual({ name: "shop_pg_restored", server: "pg1" });
    });

    it("asks for a PostgreSQL server when there is none", async () => {
      const user = userEvent.setup();
      mockFetch(pgRoutes([local]));
      render(<Harness />);
      await user.click(await screen.findByRole("button", { name: /^Restore database shop_pg of / }));
      expect(await screen.findByText(/Add a PostgreSQL server under Databases/)).toBeInTheDocument();
      expect(screen.queryByRole("button", { name: "Create and import" })).not.toBeInTheDocument();
    });
  });

  it("shows a failed backup with its badge and relative times", async () => {
    const failed: BackupInfo = { ...ready, status: { ...ready.status, ok: false, error: "target full", items: [{ kind: "files", site: "blog", error: "no space left", dataAdded: 0, files: 0, bytes: 0 }] } };
    mockFetch({ "GET /api/v1/backups": { body: failed }, "GET /api/v1/sites": { body: { sites: [] } } });
    render(<Harness />);
    const status = await screen.findByRole("region", { name: "Status" });
    expect(within(status).getByText("Failed")).toBeInTheDocument();
    expect(within(status).getByText("The last backup failed: target full")).toBeInTheDocument();
    expect(within(status).getByText(/no space left/)).toBeInTheDocument();
    expect(within(status).getAllByText((_, el) => el?.tagName === "TIME")[0]).toHaveAttribute("datetime", "2026-11-19T01:12:00Z");
  });

  it("lists the snapshots of the site in the address", async () => {
    const calls = mockFetch({
      "GET /api/v1/backups": { body: ready },
      "GET /api/v1/sites": { body: { sites: [{ name: "blog", state: "active" }, { name: "shop", state: "active" }] } },
      "GET /api/v1/sites/shop/backups": { body: { snapshots: [] } },
    });
    render(<Harness initial="site=shop" />);
    expect(await screen.findByText("No snapshots of this site yet.")).toBeInTheDocument();
    expect(screen.getByRole("combobox", { name: "Site" })).toHaveValue("shop");
    expect(calls.some((c) => c.path === "/api/v1/sites/blog/backups")).toBe(false);
  });
});
