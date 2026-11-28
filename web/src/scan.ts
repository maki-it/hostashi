// SPDX-License-Identifier: AGPL-3.0-or-later

// The malware scan (ADR-0064): ClamAV from the distribution, switched on by
// an admin, scans each site on a schedule and on request. It reports and
// quarantines; it never deletes a file.

import { ApiError, request } from "./api";
import { compare } from "./format";
import i18n from "./i18n";
import type { Tone } from "./ui";
import { siteMessage } from "./sites";

export type EngineState = "off" | "installing" | "removing" | "on" | "failed";

export type ScanEngine = {
  state: EngineState;
  version: string;
  /** When the newest signature file was written; "" before the first download. */
  signatures: string;
  /** The end of the install log when state or realtime is "failed". */
  error: string;
  /** The real-time mode (clamd and clamonacc), "off" while ClamAV is off. */
  realtime: EngineState;
  /** Whether clamonacc watches the sites now. */
  realtimeActive: boolean;
  /** The server's RAM in bytes, 0 if unknown. */
  memTotal: number;
};

export type ScanSchedule = "off" | "daily" | "weekly";

export type ScanRun = {
  id: number;
  source: "panel" | "api" | "schedule" | "realtime";
  state: "queued" | "running" | "succeeded" | "failed";
  error: string;
  found: number;
  newFound: number;
  dropped: number;
  createdAt: string;
  startedAt: string;
  finishedAt: string;
  /** The files and folders a scan of a selection covered, relative to the home; empty for the whole site. */
  paths?: string[];
  /** How far a running scan is; only set while it runs. */
  progress?: ScanProgress;
};

/**
 * ScanProgress counts files: scanned is how many ClamAV has reported on so
 * far, total how many the agent counted in the home before (0 while it
 * still counts). total is an estimate: files change during a scan.
 */
export type ScanProgress = { scanned: number; total: number };

/**
 * progressPercent is how much of a scan is done, in whole percent. It stays
 * at 99 until the run has finished, since the count is an estimate; null
 * while the total is not known yet.
 */
export function progressPercent(p: ScanProgress): number | null {
  if (p.total <= 0) return null;
  return Math.min(99, Math.floor((p.scanned / p.total) * 100));
}

export type FindingState = "open" | "ignored" | "quarantined" | "gone" | "restored";

export type ScanFinding = {
  id: number;
  /** Relative to the site's home. */
  path: string;
  signature: string;
  sha256: string;
  size: number;
  state: FindingState;
  firstSeen: string;
  lastSeen: string;
  decidedAt: string;
};

export type SiteScan = {
  engine: EngineState;
  schedule: ScanSchedule;
  /** The next scheduled scan, "" when off. */
  nextScheduled: string;
  /** A queued run waits for another site's scan; without it, it is about to start. */
  waitingForOther: boolean;
  runs: ScanRun[];
  findings: ScanFinding[];
};

export const getScanEngine = () => request<ScanEngine>("GET", "/malware-scan");

export const setScanEngine = (on: boolean) => request<ScanEngine>("PUT", "/malware-scan", { on });

export const setScanRealtime = (realtime: boolean) => request<ScanEngine>("PUT", "/malware-scan", { realtime });

const path = (site: string) => "/sites/" + encodeURIComponent(site);

export const getSiteScan = (site: string) => request<SiteScan>("GET", path(site) + "/malware-scan");

export const setScanSchedule = (site: string, schedule: ScanSchedule) => request<void>("PUT", path(site) + "/malware-scan", { schedule });

/** startScan scans the whole site, or with paths (relative to the home) only those files and folders. */
export const startScan = (site: string, paths?: string[]) =>
  request<ScanRun>("POST", path(site) + "/malware-scans", paths && paths.length > 0 ? { paths } : undefined);

/** maxScanPaths is how many files and folders one scan of a selection may name. */
export const maxScanPaths = 100;

/** scopeText describes what a run covered: "" for the whole site. */
export function scopeText(paths: string[] | undefined): string {
  if (!paths || paths.length === 0) return "";
  const shown = paths.slice(0, 3).join(", ");
  return paths.length > 3 ? i18n.t("malware.scope", { shown, count: paths.length - 3 }) : shown;
}

export type FindingAction = "quarantine" | "restore" | "ignore" | "reopen";

export const decideFinding = (site: string, id: number, action: FindingAction) =>
  request<ScanFinding>("POST", path(site) + "/malware-findings/" + String(id) + "/" + action);

/** engineBusy: the install or removal is still running. */
export const engineBusy = (s: EngineState) => s === "installing" || s === "removing";

export function engineWord(s: EngineState): { word: string; tone: Tone } {
  switch (s) {
    case "on":
      return { word: i18n.t("malware.words.on"), tone: "ok" };
    case "installing":
      return { word: i18n.t("malware.words.installing"), tone: "warn" };
    case "removing":
      return { word: i18n.t("malware.words.removing"), tone: "warn" };
    case "failed":
      return { word: i18n.t("malware.words.failed"), tone: "err" };
    default:
      return { word: i18n.t("malware.words.off"), tone: "off" };
  }
}

/** scheduleLabel names a schedule in the panel's language. */
export const scheduleLabel = (s: ScanSchedule): string => i18n.t(`malware.schedules.${s}`);

export function runWord(r: ScanRun): { word: string; tone: Tone } {
  switch (r.state) {
    case "succeeded":
      return r.found > 0 ? { word: i18n.t("malware.words.flagged", { count: r.found }), tone: "err" } : { word: i18n.t("malware.words.clean"), tone: "ok" };
    case "failed":
      return { word: i18n.t("malware.words.failed"), tone: "err" };
    case "running":
      return { word: i18n.t("malware.words.scanning"), tone: "warn" };
    default:
      return { word: i18n.t("malware.words.waiting"), tone: "off" };
  }
}

export const runActive = (r: ScanRun) => r.state === "queued" || r.state === "running";

const sources = new Set(["panel", "api", "schedule", "realtime"]);
export const sourceLabel = (s: ScanRun["source"]) => (sources.has(s) ? i18n.t(`malware.sources.${s as "panel"}`) : s);

const findingTones = { open: "err", quarantined: "warn", ignored: "off", restored: "warn" } as const;

export function findingWord(
s: FindingState): { word: string; tone: Tone } {
  switch (s) {
    case "open":
    case "quarantined":
    case "ignored":
    case "restored":
      return { word: i18n.t(`malware.findingWords.${s}`), tone: findingTones[s] };
    default:
      return { word: i18n.t("malware.findingWords.gone"), tone: "off" };
  }
}

/** The findings that still need a look come first, then the rest by path. */
export function sortFindings(fs: readonly ScanFinding[]): ScanFinding[] {
  const rank: Record<FindingState, number> = { open: 0, restored: 1, quarantined: 2, ignored: 3, gone: 4 };
  return [...fs].sort((a, b) => rank[a.state] - rank[b.state] || compare(a.path, b.path));
}

export function scanMessage(err: unknown): string {
  if (err instanceof ApiError) {
    const key = `malware.errors.${err.code}`;
    if (i18n.exists(key)) return (i18n.t as (k: string) => string)(key);
  }
  return siteMessage(err);
}
