// SPDX-License-Identifier: AGPL-3.0-or-later

// Service management and the server dashboard (ADR-0052).

import { ApiError, request } from "./api";

export type ServiceAction = "start" | "stop" | "restart" | "reload";

export type ServiceRow = {
  id: string;
  site?: string;
  unit: string;
  installed: boolean;
  active?: string;
  sub?: string;
  unitFile?: string;
  result?: string;
  nRestarts?: number;
  memoryBytes?: number;
  memoryKnown?: boolean;
  activeSince?: string;
  startedAt?: string;
  actions: ServiceAction[];
  confirm?: ServiceAction[];
  restarts24h: number;
};

export type ServiceEvent = {
  at: string;
  service: string;
  site?: string;
  kind: "crash-restart" | "started" | "failed" | "action" | "recovered";
  detail?: string;
  restarts?: number;
  user?: string;
};

export type HostPoint = {
  at: string;
  cpuBusy: number;
  cpuIowait: number;
  cpuSteal: number;
  load1: number;
  memUsed: number;
  memTotal: number;
  swapUsed: number;
  swapTotal: number;
  disks: { name: string; used: number; total: number }[];
  net: { name: string; rx: number; tx: number }[];
};

export type Range = "1h" | "24h" | "7d" | "30d";
export const ranges: readonly Range[] = ["1h", "24h", "7d", "30d"];

export type Dashboard = { range: Range; step: number; points: HostPoint[] };

export const listServices = (site?: string) =>
  request<{ services: ServiceRow[] }>("GET", site ? `/sites/${site}/services` : "/services").then((r) => r.services);

export const listServiceEvents = (service?: string, site?: string) => {
  const q = new URLSearchParams();
  if (service) q.set("service", service);
  if (site) q.set("site", site);
  q.set("limit", "50");
  return request<{ events: ServiceEvent[] }>("GET", `/services/events?${q}`).then((r) => r.events);
};

export const runServiceAction = (row: Pick<ServiceRow, "id" | "site">, action: ServiceAction, confirm?: string) =>
  request<ServiceRow>(
    "POST",
    row.site ? `/sites/${row.site}/services/${row.id}/${action}` : `/services/${row.id}/${action}`,
    confirm ? { confirm } : undefined,
  );

export const getDashboard = (range: Range) => request<Dashboard>("GET", `/dashboard?range=${range}`);

const names: Record<string, string> = {
  nginx: "Web server (nginx)",
  mariadb: "MariaDB",
  ssh: "SSH server",
  fail2ban: "Brute-force protection (fail2ban)",
  firewall: "Firewall",
  adminer: "Database tool (Adminer)",
  freshclam: "Virus scan signatures (freshclam)",
  clamd: "Virus scanner (clamd)",
  clamonacc: "Real-time virus scan (clamonacc)",
  panel: "Panel",
  agent: "Agent",
  php: "PHP-FPM",
  valkey: "Object cache (Valkey)",
};

/** serviceName is the label of a catalog ID. */
export const serviceName = (id: string) => names[id] ?? id;

export type StateWord = { word: string; tone: "ok" | "warn" | "err" | "off" };

/** stateWord turns systemd's state into a badge. */
export function stateWord(r: Pick<ServiceRow, "installed" | "active">): StateWord {
  if (!r.installed) return { word: "Not installed", tone: "off" };
  switch (r.active) {
    case "active":
    case "reloading":
    case "refreshing":
      return { word: "Running", tone: "ok" };
    case "activating":
      return { word: "Starting", tone: "warn" };
    case "deactivating":
      return { word: "Stopping", tone: "warn" };
    case "failed":
      return { word: "Failed", tone: "err" };
    default:
      return { word: "Stopped", tone: "off" };
  }
}

const actionWords: Record<ServiceAction, string> = { start: "Start", stop: "Stop", restart: "Restart", reload: "Reload" };
export const actionWord = (a: ServiceAction) => actionWords[a];

/** eventText describes one event in a sentence. */
export function eventText(e: ServiceEvent): string {
  switch (e.kind) {
    case "crash-restart":
      return e.restarts && e.restarts > 1 ? `Restarted by systemd ${e.restarts} times after a crash` : "Restarted by systemd after a crash";
    case "started":
      return "Started";
    case "failed":
      return e.detail ? `Failed (${e.detail})` : "Failed";
    case "recovered":
      return "Running again after a failure";
    case "action":
      return `${actionWords[e.detail as ServiceAction] ?? e.detail ?? "Action"}${e.user ? " by " + e.user : ""}`;
  }
}

/** serviceMessage is the text for a failed call. */
export function serviceMessage(err: unknown): string {
  if (err instanceof ApiError) return err.message;
  return "The panel could not reach the server. Try again.";
}

/** bytes formats a size with a binary unit. */
export function bytes(n: number): string {
  const units = ["B", "KiB", "MiB", "GiB", "TiB"];
  let v = n;
  let i = 0;
  while (v >= 1024 && i < units.length - 1) {
    v /= 1024;
    i++;
  }
  return `${v >= 10 || i === 0 ? v.toFixed(0) : v.toFixed(1)} ${units[i]}`;
}

/** rate formats bytes per second as bits per second, as network rates are usually read. */
export function rate(bps: number): string {
  const bits = bps * 8;
  const units = ["bit/s", "kbit/s", "Mbit/s", "Gbit/s"];
  let v = bits;
  let i = 0;
  while (v >= 1000 && i < units.length - 1) {
    v /= 1000;
    i++;
  }
  return `${v >= 10 || i === 0 ? v.toFixed(0) : v.toFixed(1)} ${units[i]}`;
}
