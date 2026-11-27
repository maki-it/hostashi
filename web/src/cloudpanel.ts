// SPDX-License-Identifier: AGPL-3.0-or-later

// CloudPanel import (ADR-0017, ADR-0026): the scan report of the old
// server, its plan per site, the SSH source and the import jobs.

import { ApiError, RawJSON, request } from "./api";
import { nameProblem } from "./databases";
import i18n, { apiErrorText, areaErrorText } from "./i18n";

export type PlanAction = "import" | "review" | "skip";
export type PlanReason = { code: string; msg: string };

export type PlanSite = {
  name: string;
  type: string;
  domains: string[];
  redirectFrom: string[];
  // The www redirect the new site gets for the old www host (ADR-0065).
  wwwRedirect?: "to-www" | "to-non-www";
  docRoot: string;
  php: string;
  proxy?: string; // target of a reverse proxy site, which gets no account
  node?: string; // Node.js line of a Node.js site
  python?: string; // Python line of a Python site
  tls: "none" | "reissue" | "copy";
  customSnippets: number;
  cronJobs: number;
  cronImport: number; // of cronJobs, created paused with the site
  pageCache?: string; // page cache preset proposed for a site that had a cache in front
  profile?: string; // application profile the new site gets, missing for the default (ADR-0069)
  homeBytes?: number; // what the old home uses, missing if not measured
  action: PlanAction;
  reasons: PlanReason[];
  source: string;
};

export type Plan = {
  sites: PlanSite[];
  summary: { sites: number; import: number; review: number; skip: number };
};

export type Report = { id: string; sites: number; createdAt: string; plan?: Plan };

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
const sitePath = (id: string, name: string) => reportPath(id) + "/sites/" + encodeURIComponent(name);
const source = "/migrations/cloudpanel/source";

// The report is sent as the file's own JSON text: the panel checks it.
export const uploadReport = (text: string) => request<Report>("POST", reports, new RawJSON(text));
export const listReports = async () => (await request<{ reports: Report[] }>("GET", reports)).reports;
export const getReport = (id: string) => request<Report>("GET", reportPath(id));
export const deleteReport = (id: string) => request<void>("DELETE", reportPath(id));

export const createFromPlan = (id: string, name: string, reviewed: boolean, php: string, pageCache?: boolean, pool = "", node = "", python = "") =>
  request<PlanSite>("POST", sitePath(id, name), {
    reviewed,
    ...(php ? { php } : {}),
    ...(node ? { node } : {}),
    ...(python ? { python } : {}),
    ...(pageCache === undefined ? {} : { pageCache }),
    ...(pool ? { pool } : {}),
  });
export const startImport = (id: string, name: string, databases: DBMap[]) =>
  request<ImportJob>("POST", sitePath(id, name) + "/import", { databases });
// listImports returns a site's imports, newest first, without skippedPaths.
export const listImports = async (site: string) =>
  (await request<{ imports: ImportJob[] }>("GET", "/migrations/cloudpanel/imports?site=" + encodeURIComponent(site))).imports;
// activateImport makes the imported files the site's web root; the old
// web root is kept under the returned name.
export const activateImport = (site: string) =>
  request<{ site: string; previous: string }>("POST", "/migrations/cloudpanel/sites/" + encodeURIComponent(site) + "/activate");
// DumpLoad is the load of one copied dump into its database (ADR-0025).
export type DumpLoad = {
  database: string;
  path: string;
  job?: { id: string; state: "running" | "done" | "failed"; error?: string; started: string; finished?: string };
  error?: string; // why the load could not start
};
const importPath = (id: string) => "/migrations/cloudpanel/imports/" + encodeURIComponent(id);
export const listLoads = async (id: string) => (await request<{ loads: DumpLoad[] }>("GET", importPath(id) + "/loads")).loads;
export const startLoads = async (id: string) => (await request<{ loads: DumpLoad[] }>("POST", importPath(id) + "/load")).loads;
export const getImport = (id: string) => request<ImportJob>("GET", "/migrations/cloudpanel/imports/" + encodeURIComponent(id));

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
const cutoverPath = (site: string) => "/migrations/cloudpanel/sites/" + encodeURIComponent(site) + "/cutover";
// getCutover returns null when the site has no cut-over yet.
export async function getCutover(site: string): Promise<Cutover | null> {
  try {
    return await request<Cutover>("GET", cutoverPath(site));
  } catch (err) {
    if (err instanceof ApiError && err.status === 404) return null;
    throw err;
  }
}
export const startCutover = (site: string) => request<Cutover>("POST", cutoverPath(site));
export const cancelCutover = (site: string) => request<Cutover>("DELETE", cutoverPath(site));

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
  if (err instanceof ApiError && ["cutover_running", "site_state", "not_found"].includes(err.code)) {
    return areaErrorText("migrate.cutoverErrors", err);
  }
  return migrationMessage(err);
}

export const getSource = () => request<SourceInfo>("GET", source);
export const setSource = (host: string, port: number) => request<SourceInfo>("PUT", source, { host, port });
export const deleteSource = () => request<SourceInfo>("DELETE", source);
export const scanSourceHostKey = () => request<{ key: string; fingerprint: string }>("POST", source + "/hostkey/scan");
export const trustSourceHostKey = (key: string) => request<SourceInfo>("PUT", source + "/hostkey", { key });
export const newDBPassword = () => request<DBCredentials>("POST", source + "/database");

// A site the plan skips only because it exists here was created from the
// plan (or by hand): the import copies the old files into it.
export const existsHere = (s: PlanSite) =>
  s.action === "skip" && s.reasons.some((r) => r.code === "site_exists") && s.reasons.every((r) => r.code === "site_exists" || r.code === "domain_taken");

const hostRe = /^(?=.{1,253}$)[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?(\.[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?)*$/;
const ipv4Re = /^(25[0-5]|2[0-4]\d|1\d\d|[1-9]?\d)(\.(25[0-5]|2[0-4]\d|1\d\d|[1-9]?\d)){3}$/;

// hostProblem mirrors CloudPanelSource.Validate loosely; the agent decides.
export function hostProblem(host: string, port: string): string {
  if (host === "") return i18n.t("migrate.source.noAddress");
  if (!ipv4Re.test(host) && !host.includes(":") && !hostRe.test(host)) return i18n.t("migrate.source.badHost");
  if (!/^\d{1,5}$/.test(port) || Number(port) < 1 || Number(port) > 65535) return i18n.t("migrate.source.badPort");
  return "";
}

// parseDBMap reads "old new" pairs, one per line. Empty lines are skipped.
export function parseDBMap(text: string): { maps: DBMap[]; problem: string } {
  const maps: DBMap[] = [];
  const seen = new Set<string>();
  const lines = text.split("\n").map((l) => l.trim()).filter((l) => l !== "");
  for (const [i, line] of lines.entries()) {
    const parts = line.split(/\s+/);
    const at = (problem: string) => i18n.t("migrate.dbMap.line", { line: i + 1, problem });
    if (parts.length !== 2) return { maps: [], problem: at(i18n.t("migrate.dbMap.pair")) };
    const [from, to] = parts as [string, string];
    const p = nameProblem("database", from) || nameProblem("database", to);
    if (p) return { maps: [], problem: at(p) };
    if (seen.has(to)) return { maps: [], problem: at(i18n.t("migrate.dbMap.twice", { name: to })) };
    seen.add(to);
    maps.push({ from, to });
  }
  if (maps.length > 50) return { maps: [], problem: i18n.t("migrate.dbMap.tooMany") };
  return { maps, problem: "" };
}

// cronLabel says what happens to the old site's cron jobs.
export function cronLabel(s: Pick<PlanSite, "cronJobs" | "cronImport">): string {
  const n = s.cronImport ?? 0;
  const rest = s.cronJobs - n;
  const parts: string[] = [];
  if (n > 0) parts.push(i18n.t("migrate.site.cronCreated", { count: n }));
  if (rest > 0) parts.push(i18n.t("migrate.site.cronByHand", { count: rest }));
  return i18n.t("migrate.site.cronSummary", { total: s.cronJobs, parts: parts.join(", ") });
}

/** migrationMessage explains an error of the import routes in the current language. */
export function migrationMessage(err: unknown): string {
  if (err instanceof ApiError) {
    // These codes carry the server's detail, which stays English (ADR-0071).
    if (err.code === "invalid_report" || err.code === "skipped") return i18n.t(`migrate.errors.${err.code}`, { message: err.message });
    return areaErrorText("migrate.errors", err);
  }
  return apiErrorText(err);
}
