// SPDX-License-Identifier: AGPL-3.0-or-later

// Resource usage per site (ADR-0063): CPU, memory, disk I/O and web traffic
// of one site over a range, and all sites by their usage for admins.

import { request } from "./api";
import type { Range } from "./services";

export type SitePoint = {
  at: string;
  cpu: number; // percent of one core
  mem: number; // working set, bytes
  cache: number; // file cache, bytes
  tasks: number;
  ioRead: number; // bytes per second
  ioWrite: number;
  httpReq: number; // requests per second
  httpIn: number; // bytes per second
  httpOut: number;
};

// cpus is the number of CPU cores of the server, cores how many the site
// may use: its CPU limit, else all of them. 100 % is cores.
export type SiteUsage = { site: string; range: Range; step: number; cpus: number; cores: number; points: SitePoint[] };

export type SiteTotal = {
  site: string;
  cpuAvg: number;
  cpuMax: number;
  memAvg: number;
  memMax: number;
  ioReadBytes: number;
  ioWriteBytes: number;
  httpRequests: number;
  httpInBytes: number;
  httpOutBytes: number;
  cores: number; // as in SiteUsage
};

export type UsageSort = "cpu" | "memory" | "traffic" | "io";

export const getSiteUsage = (site: string, range: Range) =>
  request<SiteUsage>("GET", `/sites/${encodeURIComponent(site)}/usage?range=${range}`);

export const rankSites = (range: Range, sort: UsageSort = "cpu") =>
  request<{ range: Range; sort: UsageSort; cpus: number; sites: SiteTotal[] }>("GET", `/usage/sites?range=${range}&sort=${sort}`);

/** percent formats a share in percent: 12 %, 3.5 %, 0 %. */
export function percent(pct: number): string {
  return `${pct >= 10 || pct === 0 ? pct.toFixed(0) : pct.toFixed(1)} %`;
}

/** ofCores turns percent of one core into percent of the cores a site may use. */
export const ofCores = (pctOfCore: number, cores: number) => pctOfCore / (cores > 0 ? cores : 1);

/** cores formats percent of one core as cores: 150 % is "1.5 cores". */
export function cores(pctOfCore: number): string {
  const n = pctOfCore / 100;
  const s = n >= 10 || n === 0 ? n.toFixed(0) : n >= 1 ? n.toFixed(1) : n.toFixed(2);
  return `${s} ${s === "1" || s === "1.0" ? "core" : "cores"}`;
}

/** count formats a number of requests with a k or M suffix. */
export function count(n: number): string {
  if (n >= 1e6) return `${(n / 1e6).toFixed(1)} M`;
  if (n >= 1e4) return `${(n / 1e3).toFixed(0)} k`;
  return n.toFixed(0);
}
