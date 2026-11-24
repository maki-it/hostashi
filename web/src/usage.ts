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

export type SiteUsage = { site: string; range: Range; step: number; points: SitePoint[] };

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
};

export type UsageSort = "cpu" | "memory" | "traffic" | "io";

export const getSiteUsage = (site: string, range: Range) =>
  request<SiteUsage>("GET", `/sites/${encodeURIComponent(site)}/usage?range=${range}`);

export const rankSites = (range: Range, sort: UsageSort = "cpu") =>
  request<{ range: Range; sort: UsageSort; sites: SiteTotal[] }>("GET", `/usage/sites?range=${range}&sort=${sort}`).then((r) => r.sites);

/** cores formats percent of one core: 50 % is half a core, 200 % two. */
export function cores(pct: number): string {
  return `${pct >= 10 || pct === 0 ? pct.toFixed(0) : pct.toFixed(1)} %`;
}

/** count formats a number of requests with a k or M suffix. */
export function count(n: number): string {
  if (n >= 1e6) return `${(n / 1e6).toFixed(1)} M`;
  if (n >= 1e4) return `${(n / 1e3).toFixed(0)} k`;
  return n.toFixed(0);
}
