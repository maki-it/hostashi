// SPDX-License-Identifier: AGPL-3.0-or-later

import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import { Backups } from "./Backups";
import type { BackupInfo } from "./backups";
import { mockFetch } from "./test-utils";

const status = { running: false, ok: false, items: [], maintenanceOk: true };
const defaults = { version: 1, retention: { daily: 7, weekly: 4, monthly: 6 }, windowStart: 1, windowEnd: 5, readParts: 12 };
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

const snapFiles = "a".repeat(64);
const snapDB = "b".repeat(64);

describe("Backups", () => {
  it("sets the target and shows the key for the Storage Box", async () => {
    const user = userEvent.setup();
    const calls = mockFetch({
      "GET /api/v1/backups": { body: fresh },
      "PUT /api/v1/backups/settings": { body: withTarget },
    });
    render(<Backups />);
    expect(await screen.findByText(/Not set up yet/)).toBeInTheDocument();
    await user.type(screen.getByLabelText("User"), "u123456");
    await user.type(screen.getByLabelText("Host"), "U123456.your-storagebox.de ");
    await user.click(screen.getByRole("button", { name: "Save" }));
    expect(await screen.findByLabelText("Public key")).toHaveTextContent(pub);
    expect(calls.find((c) => c.method === "PUT")?.body).toEqual({ ...defaults, target });
    expect(screen.getByRole("heading", { name: "Host key" })).toBeInTheDocument();
  });

  it("shows the fingerprint and trusts the key only on confirmation", async () => {
    const user = userEvent.setup();
    const calls = mockFetch({
      "GET /api/v1/backups": { body: withTarget },
      "POST /api/v1/backups/hostkey/scan": { body: { key: "ssh-ed25519 AAAAhost", fingerprint: "SHA256:abc" } },
      "PUT /api/v1/backups/hostkey": { body: ready },
      "GET /api/v1/sites": { body: { sites: [] } },
    });
    render(<Backups />);
    await user.click(await screen.findByRole("button", { name: "Fetch the host key" }));
    expect(await screen.findByText(/Compare it with the fingerprints/)).toHaveTextContent("SHA256:abc");
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
    render(<Backups />);
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
    render(<Backups />);
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
    render(<Backups />);
    const buttons = await screen.findAllByRole("button", { name: "Restore" });
    await user.click(buttons[0]!);
    expect(await screen.findByText(/Restored 3 files into/)).toHaveTextContent("restore-1");
    await user.click(screen.getByRole("button", { name: "Restore" }));
    await user.click(await screen.findByRole("button", { name: "Create and import" }));
    expect(await screen.findByText(/The dump is in the new database/)).toHaveTextContent("blog_wp_restored");
    expect(calls.find((c) => c.path === "/api/v1/sites/blog/databases")?.body).toEqual({ name: "blog_wp_restored" });
    expect(calls.find((c) => c.path.endsWith("/import"))?.body).toEqual({ database: "blog_wp_restored" });
  });
});
