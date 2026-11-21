// SPDX-License-Identifier: AGPL-3.0-or-later

// CloudPanel import (ADR-0017, ADR-0026): the scan report of the old
// server, its plan per site, the SSH source and the import jobs.

import { ApiError, RawJSON, request } from "./api";
import { nameProblem } from "./databases";

export type PlanAction = "import" | "review" | "skip";
export type PlanReason = { code: string; msg: string };

export type PlanSite = {
  name: string;
  type: string;
  domains: string[];
  redirectFrom: string[];
  docRoot: string;
  php: string;
  tls: "none" | "reissue" | "copy";
  customSnippets: number;
  cronJobs: number;
  cronImport: number; // of cronJobs, created paused with the site
  pageCache?: string; // page cache preset proposed for a site that had a cache in front
  action: PlanAction;
  reasons: PlanReason[];
  source: string;
};

export type Plan = {
  sites: PlanSite[];
  summary: { sites: number; import: number; review: number; skip: number };
};

export type Report = {
  id: string;
  sites: number;
  createdAt: string;
  plan?: Plan;
};

export type SourceInfo = {
  source: { host: string; port: number } | null;
  publicKey?: string;
  authorizedKeysLine?: string;
  hostKey: string;
  ready: boolean;
  dbUser?: string;
};

export type DBCredentials = { user: string; password: string; sql: string };
export type DBMap = { from: string; to: string };

export type ImportJob = {
  id: string;
  site: string;
  remoteUser: string;
  docRoot: string;
  dir: string;
  state: "queued" | "running" | "done" | "failed";
  created: string;
  started?: string;
  finished?: string;
  files: number;
  bytes: number;
  unchanged: number;
  deleted: number;
  skipped: number;
  skippedPaths?: string[];
  databases?: DBMap[];
  dumps?: string[];
  error?: string;
};

const reports = "/migrations/cloudpanel/reports";
const reportPath = (id: string) => reports + "/" + encodeURIComponent(id);
const sitePath = (id: string, name: string) =>
  reportPath(id) + "/sites/" + encodeURIComponent(name);
const source = "/migrations/cloudpanel/source";

// The report is sent as the file's own JSON text: the panel checks it.
export const uploadReport = (text: string) =>
  request<Report>("POST", reports, new RawJSON(text));
export const listReports = async () =>
  (await request<{ reports: Report[] }>("GET", reports)).reports;
export const getReport = (id: string) => request<Report>("GET", reportPath(id));
export const deleteReport = (id: string) =>
  request<void>("DELETE", reportPath(id));

export const createFromPlan = (
  id: string,
  name: string,
  reviewed: boolean,
  php: string,
  pageCache?: boolean,
) =>
  request<PlanSite>("POST", sitePath(id, name), {
    reviewed,
    ...(php ? { php } : {}),
    ...(pageCache === undefined ? {} : { pageCache }),
  });
export const startImport = (id: string, name: string, databases: DBMap[]) =>
  request<ImportJob>("POST", sitePath(id, name) + "/import", { databases });
// listImports returns a site's imports, newest first, without skippedPaths.
export const listImports = async (site: string) =>
  (
    await request<{ imports: ImportJob[] }>(
      "GET",
      "/migrations/cloudpanel/imports?site=" + encodeURIComponent(site),
    )
  ).imports;
// activateImport makes the imported files the site's web root; the old
// web root is kept under the returned name.
export const activateImport = (site: string) =>
  request<{ site: string; previous: string }>(
    "POST",
    "/migrations/cloudpanel/sites/" + encodeURIComponent(site) + "/activate",
  );
// DumpLoad is the load of one copied dump into its database (ADR-0025).
export type DumpLoad = {
  database: string;
  path: string;
  job?: {
    id: string;
    state: "running" | "done" | "failed";
    error?: string;
    started: string;
    finished?: string;
  };
  error?: string; // why the load could not start
};
const importPath = (id: string) =>
  "/migrations/cloudpanel/imports/" + encodeURIComponent(id);
export const listLoads = async (id: string) =>
  (await request<{ loads: DumpLoad[] }>("GET", importPath(id) + "/loads"))
    .loads;
export const startLoads = async (id: string) =>
  (await request<{ loads: DumpLoad[] }>("POST", importPath(id) + "/load"))
    .loads;
export const getImport = (id: string) =>
  request<ImportJob>(
    "GET",
    "/migrations/cloudpanel/imports/" + encodeURIComponent(id),
  );

// DNS cut-over of an imported site (ADR-0031): the person points the
// domains at this server; the panel checks them every minute and turns
// HTTPS on once all of them resolve only here.
export type CutoverState = "waiting" | "switched" | "failed" | "canceled";
export type DomainAnswer = {
  domain: string;
  addrs: string[];
  here: boolean;
  ttl?: number; // seconds, when the panel reports it
  error?: string;
};
export type Cutover = {
  site: string;
  state: CutoverState;
  startedAt: string;
  deadline: string;
  checkedAt?: string;
  doneAt?: string;
  server: string[];
  answers: DomainAnswer[];
  error?: string;
};
const cutoverPath = (site: string) =>
  "/migrations/cloudpanel/sites/" + encodeURIComponent(site) + "/cutover";
// getCutover returns null when the site has no cut-over yet.
export async function getCutover(site: string): Promise<Cutover | null> {
  try {
    return await request<Cutover>("GET", cutoverPath(site));
  } catch (err) {
    if (err instanceof ApiError && err.status === 404) return null;
    throw err;
  }
}
export const startCutover = (site: string) =>
  request<Cutover>("POST", cutoverPath(site));
export const cancelCutover = (site: string) =>
  request<Cutover>("DELETE", cutoverPath(site));

// ttlLabel writes a TTL in seconds the short way: 300 is "5 min".
export function ttlLabel(s: number): string {
  if (!Number.isFinite(s) || s < 0) return "";
  if (s < 60 || s % 60 !== 0) return s + " s";
  if (s < 3600 || s % 3600 !== 0) return s / 60 + " min";
  if (s < 86400 || s % 86400 !== 0) return s / 3600 + " h";
  return s / 86400 + " d";
}

// cutoverMessage explains the errors of the cut-over routes.
export function cutoverMessage(err: unknown): string {
  if (err instanceof ApiError) {
    switch (err.code) {
      case "cutover_running":
        return "A DNS check of this site is running already. Reload the page.";
      case "site_state":
        return "The site does not exist here or is disabled. Enable it first.";
      case "not_found":
        return "The DNS check is not available on this panel.";
    }
  }
  return migrationMessage(err);
}

export const getSource = () => request<SourceInfo>("GET", source);
export const setSource = (host: string, port: number) =>
  request<SourceInfo>("PUT", source, { host, port });
export const deleteSource = () => request<SourceInfo>("DELETE", source);
export const scanSourceHostKey = () =>
  request<{ key: string; fingerprint: string }>(
    "POST",
    source + "/hostkey/scan",
  );
export const trustSourceHostKey = (key: string) =>
  request<SourceInfo>("PUT", source + "/hostkey", { key });
export const newDBPassword = () =>
  request<DBCredentials>("POST", source + "/database");

// A site the plan skips only because it exists here was created from the
// plan (or by hand): the import copies the old files into it.
export const existsHere = (s: PlanSite) =>
  s.action === "skip" &&
  s.reasons.some((r) => r.code === "site_exists") &&
  s.reasons.every((r) => r.code === "site_exists" || r.code === "domain_taken");

const hostRe =
  /^(?=.{1,253}$)[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?(\.[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?)*$/;
const ipv4Re =
  /^(25[0-5]|2[0-4]\d|1\d\d|[1-9]?\d)(\.(25[0-5]|2[0-4]\d|1\d\d|[1-9]?\d)){3}$/;

// hostProblem mirrors CloudPanelSource.Validate loosely; the agent decides.
export function hostProblem(host: string, port: string): string {
  if (host === "") return "Enter the old server's address.";
  if (!ipv4Re.test(host) && !host.includes(":") && !hostRe.test(host))
    return "Use an IP address or a lowercase DNS name.";
  if (!/^\d{1,5}$/.test(port) || Number(port) < 1 || Number(port) > 65535)
    return "Use a port from 1 to 65535.";
  return "";
}

// parseDBMap reads "old new" pairs, one per line. Empty lines are skipped.
export function parseDBMap(text: string): { maps: DBMap[]; problem: string } {
  const maps: DBMap[] = [];
  const seen = new Set<string>();
  const lines = text
    .split("\n")
    .map((l) => l.trim())
    .filter((l) => l !== "");
  for (const [i, line] of lines.entries()) {
    const parts = line.split(/\s+/);
    const at = `Line ${i + 1}: `;
    if (parts.length !== 2)
      return {
        maps: [],
        problem: at + "write the old database, a space and the database here.",
      };
    const [from, to] = parts as [string, string];
    const p = nameProblem("database", from) || nameProblem("database", to);
    if (p) return { maps: [], problem: at + p };
    if (seen.has(to))
      return { maps: [], problem: at + `${to} is named twice.` };
    seen.add(to);
    maps.push({ from, to });
  }
  if (maps.length > 50)
    return { maps: [], problem: "At most 50 databases per import." };
  return { maps, problem: "" };
}

// cronLabel says what happens to the old site's cron jobs.
export function cronLabel(
  s: Pick<PlanSite, "cronJobs" | "cronImport">,
): string {
  const n = s.cronImport ?? 0;
  const rest = s.cronJobs - n;
  const parts: string[] = [];
  if (n > 0) parts.push(`${n} created paused`);
  if (rest > 0) parts.push(`${rest} to set up by hand`);
  return `${s.cronJobs}: ${parts.join(", ")}`;
}

export function formatBytes(n: number): string {
  const units = ["B", "KiB", "MiB", "GiB", "TiB"];
  let i = 0;
  let v = n;
  while (v >= 1024 && i < units.length - 1) {
    v /= 1024;
    i++;
  }
  return (i === 0 ? String(v) : v.toFixed(1)) + " " + units[i];
}

export function migrationMessage(err: unknown): string {
  if (err instanceof ApiError) {
    switch (err.code) {
      case "invalid_report":
        return (
          "This is not a valid scan report: " +
          err.message +
          ". Create it with hostashi cloudpanel scan -json -full."
        );
      case "too_large":
        return "The report is larger than 16 MiB.";
      case "too_many_reports":
        return "At most 20 reports are kept. Delete one first.";
      case "unsupported_media_type":
        return "The report must be a JSON file.";
      case "not_done":
        return "The import has not finished copying yet.";
      case "needs_review":
        return "Confirm that you checked this site first.";
      case "skipped":
        return "The plan skips this site: " + err.message + ".";
      case "no_site":
        return "The report has no importable site of this name. Reload the page.";
      case "not_found":
        return "This report or import does not exist any more. Reload the page.";
      case "no_source":
        return "Set up the old server first.";
      case "unreachable":
        return "The old server did not answer over SSH. Check the address, the port and its firewall.";
      case "unavailable":
        return "Importing is not available on this panel.";
      case "site_exists":
        return "A site with this name exists here already.";
      case "domain_taken":
        return "A domain of this site belongs to another site here.";
      case "forbidden":
        return "Only administrators can import sites.";
      case "agent_timeout":
        return "The server did not answer in time. Reload the page to see whether the change was made.";
      case "agent_unavailable":
        return "The change could not be completed. Try again.";
      default:
        return err.message
          ? err.message.charAt(0).toUpperCase() + err.message.slice(1) + "."
          : "Something went wrong (" + err.code + ").";
    }
  }
  if (err instanceof TypeError) return "The server is not reachable.";
  return "Something went wrong. Try again.";
}
