// SPDX-License-Identifier: AGPL-3.0-or-later

// The malware scan (ADR-0064): ClamAV from the distribution, switched on by
// an admin, scans each site on a schedule and on request. It reports and
// quarantines; it never deletes a file.

import { ApiError, request } from "./api";
import type { Tone } from "./ui";
import { siteMessage } from "./sites";

export type EngineState = "off" | "installing" | "removing" | "on" | "failed";

export type ScanEngine = {
  state: EngineState;
  version: string;
  /** When the newest signature file was written; "" before the first download. */
  signatures: string;
  /** The end of the install log when state is "failed". */
  error: string;
};

export type ScanSchedule = "off" | "daily" | "weekly";

export type ScanRun = {
  id: number;
  source: "panel" | "api" | "schedule";
  state: "queued" | "running" | "succeeded" | "failed";
  error: string;
  found: number;
  newFound: number;
  dropped: number;
  createdAt: string;
  startedAt: string;
  finishedAt: string;
};

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
  runs: ScanRun[];
  findings: ScanFinding[];
};

export const getScanEngine = () => request<ScanEngine>("GET", "/malware-scan");

export const setScanEngine = (on: boolean) => request<ScanEngine>("PUT", "/malware-scan", { on });

const path = (site: string) => "/sites/" + encodeURIComponent(site);

export const getSiteScan = (site: string) => request<SiteScan>("GET", path(site) + "/malware-scan");

export const setScanSchedule = (site: string, schedule: ScanSchedule) => request<void>("PUT", path(site) + "/malware-scan", { schedule });

export const startScan = (site: string) => request<ScanRun>("POST", path(site) + "/malware-scans");

export type FindingAction = "quarantine" | "restore" | "ignore" | "reopen";

export const decideFinding = (site: string, id: number, action: FindingAction) =>
  request<ScanFinding>("POST", path(site) + "/malware-findings/" + String(id) + "/" + action);

/** engineBusy: the install or removal is still running. */
export const engineBusy = (s: EngineState) => s === "installing" || s === "removing";

export function engineWord(s: EngineState): { word: string; tone: Tone } {
  switch (s) {
    case "on":
      return { word: "On", tone: "ok" };
    case "installing":
      return { word: "Installing…", tone: "warn" };
    case "removing":
      return { word: "Removing…", tone: "warn" };
    case "failed":
      return { word: "Failed", tone: "err" };
    default:
      return { word: "Off", tone: "off" };
  }
}

export const scheduleLabel: Record<ScanSchedule, string> = {
  weekly: "Weekly, at night",
  daily: "Every night",
  off: "Only on request",
};

export function runWord(r: ScanRun): { word: string; tone: Tone } {
  switch (r.state) {
    case "succeeded":
      return r.found > 0 ? { word: r.found === 1 ? "1 file flagged" : `${r.found} files flagged`, tone: "err" } : { word: "Clean", tone: "ok" };
    case "failed":
      return { word: "Failed", tone: "err" };
    case "running":
      return { word: "Scanning…", tone: "warn" };
    default:
      return { word: "Waiting", tone: "off" };
  }
}

export const runActive = (r: ScanRun) => r.state === "queued" || r.state === "running";

export const sourceLabel = (s: ScanRun["source"]) => ({ panel: "Panel", api: "API", schedule: "Schedule" })[s] ?? s;

export function findingWord(s: FindingState): { word: string; tone: Tone } {
  switch (s) {
    case "open":
      return { word: "Flagged", tone: "err" };
    case "quarantined":
      return { word: "In quarantine", tone: "warn" };
    case "ignored":
      return { word: "False positive", tone: "off" };
    case "restored":
      return { word: "Restored", tone: "warn" };
    default:
      return { word: "Gone", tone: "off" };
  }
}

/** The findings that still need a look come first, then the rest by path. */
export function sortFindings(fs: readonly ScanFinding[]): ScanFinding[] {
  const rank: Record<FindingState, number> = { open: 0, restored: 1, quarantined: 2, ignored: 3, gone: 4 };
  return [...fs].sort((a, b) => rank[a.state] - rank[b.state] || a.path.localeCompare(b.path));
}

export function scanMessage(err: unknown): string {
  if (err instanceof ApiError) {
    switch (err.code) {
      case "scan_unavailable":
        return "The virus scan is not available on this panel: the agent does not answer.";
      case "scan_off":
        return "The virus scan is off on this server. An administrator switches it on under System.";
      case "scan_running":
        return "A scan of this site is waiting or running. Wait until it ends.";
      case "finding_not_found":
        return "This finding is no longer there. Reload the page.";
      case "finding_state":
        return "The finding changed in the meantime. Reload the page.";
    }
  }
  return siteMessage(err);
}
