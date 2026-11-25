// SPDX-License-Identifier: AGPL-3.0-or-later

// Backups (ADR-0008): the target, its host key, the schedule, runs,
// snapshots, restores and the import of a restored database dump. The
// agent keeps the secrets; nothing here ever sees a password or a key.

import { ApiError, request } from "./api";

export type Target = { user: string; host: string; port: number; path: string };

export type Settings = {
  version: number;
  target?: Target;
  retention: { hourly: number; daily: number; weekly: number; monthly: number; yearly: number };
  // Hours between two scheduled runs; 24 is the daily run in the window.
  intervalHours: number;
  windowStart: number;
  windowEnd: number;
  readParts: number;
};

// The intervals the agent accepts (ADR-0008, amendment 2026-11-25).
export const intervals = [1, 2, 3, 4, 6, 8, 12, 24];

export type ItemResult = {
  kind: string;
  site?: string;
  database?: string;
  snapshotId?: string;
  dataAdded: number;
  files: number;
  bytes: number;
  partial?: boolean;
  error?: string;
};

export type Status = {
  running: boolean;
  started?: string;
  finished?: string;
  ok: boolean;
  error?: string;
  items: ItemResult[];
  lastPrune?: string;
  lastCheck?: string;
  lastReadCheck?: string;
  maintenanceOk: boolean;
  maintenance?: string;
};

export type BackupInfo = {
  settings: Settings;
  publicKey: string;
  hostKey: string;
  ready: boolean;
  status: Status;
  // No run finished within the interval plus two hours (ADR-0008, 8).
  overdue?: boolean;
};

// The wording of the interval, as in the form.
export function everyText(hours: number) {
  if (hours === 1) return "every hour";
  if (hours > 0 && hours < 24) return `every ${hours} hours`;
  return "once a day";
}

// The age after which a run counts as missing, as the panel computes it.
export const overdueHours = (hours: number) => (hours > 0 && hours < 24 ? hours : 24) + 2;

export type Snapshot = { id: string; time: string; kind: string; database?: string };

export type Restore = {
  id: string;
  site: string;
  snapshot: string;
  kind: string;
  database?: string;
  dir: string;
  /** The dump's file name in dir: <db>.sql, or <db>.dump for PostgreSQL. */
  dumpFile?: string;
  state: "queued" | "running" | "done" | "failed";
  files: number;
  bytes: number;
  skipped?: number;
  error?: string;
};

export type ImportJob = { id: string; database: string; state: "running" | "done" | "failed"; error?: string };

export type Site = { name: string; state: string };

export const getBackups = () => request<BackupInfo>("GET", "/backups");
export const saveSettings = (s: Settings) => request<BackupInfo>("PUT", "/backups/settings", s);
export const scanHostKey = () => request<{ key: string; fingerprint: string }>("POST", "/backups/hostkey/scan");
export const confirmHostKey = (key: string) => request<BackupInfo>("PUT", "/backups/hostkey", { key });
export const runBackup = () => request<void>("POST", "/backups/run");
export const listSites = async () => (await request<{ sites: Site[] }>("GET", "/sites")).sites;
export const siteSnapshots = async (site: string) =>
  (await request<{ snapshots: Snapshot[] }>("GET", "/sites/" + encodeURIComponent(site) + "/backups")).snapshots;
export const startRestore = (site: string, snapshot: string) =>
  request<Restore>("POST", "/sites/" + encodeURIComponent(site) + "/backups/" + snapshot + "/restore");
export const getRestore = (id: string) => request<Restore>("GET", "/backups/restores/" + id);
export const createDatabase = (site: string, name: string, server = "") =>
  request<unknown>("POST", "/sites/" + encodeURIComponent(site) + "/databases", { name, ...(server ? { server } : {}) });
export const importDump = (restore: string, database: string) =>
  request<ImportJob>("POST", "/backups/restores/" + restore + "/import", { database });
export const getImport = (id: string) => request<ImportJob>("GET", "/backups/imports/" + id);

// backupMessage turns an API error into a sentence.
export function backupMessage(err: unknown): string {
  if (err instanceof ApiError) {
    switch (err.code) {
      case "invalid_argument":
        return err.message.charAt(0).toUpperCase() + err.message.slice(1) + ".";
      case "unreachable":
        return "The backup target did not answer over SSH. Check host and port.";
      case "running":
        return "A backup run is in progress.";
      case "restore_running":
        return "A restore is in progress. Wait for it to finish.";
      case "database_exists":
        return "A database with this name exists. Choose a new name.";
      case "database_not_new":
        return "A restored dump only goes into a new database.";
      case "forbidden":
        return "You are not allowed to do this.";
      default:
        return err.message ? err.message.charAt(0).toUpperCase() + err.message.slice(1) + "." : "Something went wrong (" + err.code + ").";
    }
  }
  if (err instanceof TypeError) return "The server is not reachable.";
  return "Something went wrong. Try again.";
}

// poll calls get every interval until done says so, at most max times.
export async function poll<T>(get: () => Promise<T>, done: (v: T) => boolean, interval = 2000, max = 900): Promise<T> {
  for (let i = 0; ; i++) {
    const v = await get();
    if (done(v) || i >= max) return v;
    await new Promise((r) => setTimeout(r, interval));
  }
}
