// SPDX-License-Identifier: AGPL-3.0-or-later

// Service management and the server dashboard (ADR-0052).

import { ApiError, request } from "./api";
import { formatNumber } from "./format";
import i18n, { apiErrorText } from "./i18n";

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

/** serviceName is the label of a catalog ID, in the current language. */
export function serviceName(id: string): string {
  const key = `server.services.names.${id}`;
  return i18n.exists(key) ? (i18n.t as (k: string) => string)(key) : (names[id] ?? id);
}

export type StateWord = { word: string; tone: "ok" | "warn" | "err" | "off" };

/** stateWord turns systemd's state into a badge. */
export function stateWord(r: Pick<ServiceRow, "installed" | "active">): StateWord {
  if (!r.installed) return { word: i18n.t("server.services.state.notInstalled"), tone: "off" };
  switch (r.active) {
    case "active":
    case "reloading":
    case "refreshing":
      return { word: i18n.t("server.services.state.running"), tone: "ok" };
    case "activating":
      return { word: i18n.t("server.services.state.starting"), tone: "warn" };
    case "deactivating":
      return { word: i18n.t("server.services.state.stopping"), tone: "warn" };
    case "failed":
      return { word: i18n.t("server.services.state.failed"), tone: "err" };
    default:
      return { word: i18n.t("server.services.state.stopped"), tone: "off" };
  }
}

const serviceActions: readonly string[] = ["start", "stop", "restart", "reload"];
export const actionWord = (a: ServiceAction) => i18n.t(`server.services.actions.${a}`);
/** actionVerb is the action as a verb in a phrase such as "Stop MariaDB" ("MariaDB stoppen"). */
export const actionVerb = (a: ServiceAction) => i18n.t(`server.services.verbs.${a}`);

/** eventText describes one event in a sentence. */
export function eventText(e: ServiceEvent): string {
  switch (e.kind) {
    case "crash-restart":
      return e.restarts && e.restarts > 1
        ? i18n.t("server.services.events.crashRestarts", { n: e.restarts })
        : i18n.t("server.services.events.crashRestart");
    case "started":
      return i18n.t("server.services.events.started");
    case "failed":
      return e.detail ? i18n.t("server.services.events.failedDetail", { detail: e.detail }) : i18n.t("server.services.events.failed");
    case "recovered":
      return i18n.t("server.services.events.recovered");
    case "action": {
      const action = e.detail && serviceActions.includes(e.detail) ? actionWord(e.detail as ServiceAction) : (e.detail ?? i18n.t("server.services.events.action"));
      return e.user ? i18n.t("server.services.events.by", { action, user: e.user }) : action;
    }
  }
}

/** serviceMessage is the text for a failed call. */
export function serviceMessage(err: unknown): string {
  // The server's own reason (such as "nginx -t rejected the configuration")
  // stays as it is unless the catalog has a sentence for its code.
  if (err instanceof ApiError) return i18n.exists(`errors.${err.code}`) || err.message === "" ? apiErrorText(err) : err.message;
  return i18n.t("server.services.errors.unreachable");
}

// fixed writes v with exactly the given decimals in the panel's language
// (ADR-0071): "1.5" or "1,5". No grouping: the values stay below 1024.
const fixed = (v: number, digits: number) => formatNumber(v, { minimumFractionDigits: digits, maximumFractionDigits: digits, useGrouping: false });

/** bytes formats a size with a binary unit. */
export function bytes(n: number): string {
  const units = ["B", "KiB", "MiB", "GiB", "TiB"];
  let v = n;
  let i = 0;
  while (v >= 1024 && i < units.length - 1) {
    v /= 1024;
    i++;
  }
  return `${fixed(v, v >= 10 || i === 0 ? 0 : 1)} ${units[i]}`;
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
  return `${fixed(v, v >= 10 || i === 0 ? 0 : 1)} ${units[i]}`;
}
