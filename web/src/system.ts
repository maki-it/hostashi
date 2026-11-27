// SPDX-License-Identifier: AGPL-3.0-or-later

// Time zone, hostname and reboot of the server (ADR-0060).

import { request } from "./api";
import { formatDate, locale } from "./format";
import i18n from "./i18n";

export type SystemInfo = {
  hostname: string;
  transientHostname: string;
  timezone: string;
  time: string;
  uptimeSeconds: number;
  ntpSynchronized: boolean;
  rebootRequired: boolean;
  rebootPackages?: string[];
  cloudInit: boolean;
  /** The hostashi package (ADR-0050); absent when the server was not set up from it. */
  updates?: SystemUpdates;
};

export type SystemUpdates = {
  installed: string;
  /** The newest version apt saw in the Hostashi repository; absent while apt has no list. */
  available?: string;
  update: boolean;
  /** unattended-upgrades installs new versions by itself (--auto-update). */
  auto: boolean;
  /** When apt last fetched the list, RFC 3339. */
  listedAt?: string;
  /** The installed hostashi-runtimes, YYYYMMDDHHMMSS (ADR-0077); absent when it is not installed. */
  runtimes?: string;
  /** unattended-upgrades updates hostashi-runtimes. */
  runtimesAuto: boolean;
};

/** pinsDate turns a hostashi-runtimes version (YYYYMMDDHHMMSS, UTC) into a date; "" for anything else. */
export function pinsDate(v: string): string {
  const m = /^(\d{4})(\d{2})(\d{2})(\d{2})(\d{2})(\d{2})$/.exec(v);
  if (!m) return "";
  const d = new Date(Date.UTC(Number(m[1]), Number(m[2]) - 1, Number(m[3]), Number(m[4]), Number(m[5]), Number(m[6])));
  return formatDate(d);
}

export const getSystem = () => request<SystemInfo>("GET", "/system");

export const listTimezones = () => request<{ zones: string[] }>("GET", "/system/timezones").then((r) => r.zones);

export const setTimezone = (zone: string) => request<SystemInfo>("PUT", "/system/timezone", { zone });

export const setHostname = (hostname: string) => request<SystemInfo>("PUT", "/system/hostname", { hostname });

export const setAutoUpdate = (on: boolean) => request<SystemInfo>("PUT", "/system/auto-update", { on });

export const rebootServer = (confirm: string) => request<{ delaySeconds: number }>("POST", "/system/reboot", { confirm });

const labelRe = /^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$/;

/** hostnameError mirrors the server's check; "" means the name is fine. */
export function hostnameError(name: string): string {
  if (name === "") return i18n.t("server.system.hostname.errors.empty");
  if (name.length > 64) return i18n.t("server.system.hostname.errors.long");
  const labels = name.split(".");
  if (!labels.every((l) => labelRe.test(l))) return i18n.t("server.system.hostname.errors.chars");
  if (/^[0-9]+$/.test(labels[labels.length - 1] ?? "")) return i18n.t("server.system.hostname.errors.address");
  return "";
}

/** uptime formats seconds as days, hours and minutes. */
export function uptime(seconds: number): string {
  const s = Math.max(0, Math.floor(seconds));
  const d = Math.floor(s / 86400);
  const h = Math.floor((s % 86400) / 3600);
  const m = Math.floor((s % 3600) / 60);
  if (d > 0) return i18n.t("server.system.uptime.days", { days: d, hours: h });
  if (h > 0) return i18n.t("server.system.uptime.hours", { hours: h, minutes: m });
  return i18n.t("server.system.uptime.minutes", { minutes: m });
}

/** clockIn shows the instant iso in a zone, as the server would; "" if the browser does not know the zone. */
export function clockIn(iso: string, zone: string): string {
  try {
    return new Intl.DateTimeFormat(locale(), {
      timeZone: zone,
      dateStyle: "medium",
      timeStyle: "long",
    }).format(new Date(iso));
  } catch {
    return "";
  }
}

/**
 * waitForPanel resolves once the panel answers again after a reboot: it
 * first waits until /api/v1/health stops answering (or maxDown passes),
 * then until it answers again.
 */
export async function waitForPanel(opts: {
  interval?: number;
  maxDown?: number;
  timeout?: number;
  sleep?: (ms: number) => Promise<void>;
  ping?: () => Promise<boolean>;
  onPhase?: (phase: "going-down" | "starting") => void;
}): Promise<boolean> {
  const interval = opts.interval ?? 3000;
  const sleep = opts.sleep ?? ((ms: number) => new Promise<void>((r) => window.setTimeout(r, ms)));
  const ping =
    opts.ping ??
    (async () => {
      try {
        const res = await fetch("/api/v1/health", { cache: "no-store", credentials: "same-origin" });
        return res.ok;
      } catch {
        return false;
      }
    });
  const maxDown = opts.maxDown ?? 60000;
  const timeout = opts.timeout ?? 10 * 60000;

  let waited = 0;
  opts.onPhase?.("going-down");
  while (waited < maxDown && (await ping())) {
    await sleep(interval);
    waited += interval;
  }
  opts.onPhase?.("starting");
  while (waited < timeout) {
    await sleep(interval);
    waited += interval;
    if (await ping()) return true;
  }
  return false;
}

/**
 * panelVersion returns the version of the running panel from
 * /api/v1/health, "" when it cannot be read. A degraded panel (503) still
 * names its version.
 */
export async function panelVersion(): Promise<string> {
  try {
    const res = await fetch("/api/v1/health", { cache: "no-store", credentials: "same-origin", headers: { Accept: "application/json" } });
    const body = (await res.json()) as { version?: unknown };
    return typeof body.version === "string" ? body.version : "";
  } catch {
    return "";
  }
}

/** versionLabel shows a release as "v0.4.0" and anything else unchanged. */
export function versionLabel(v: string): string {
  return /^\d+\.\d+\.\d+/.test(v) ? `v${v}` : v;
}

const releases = "https://github.com/maki-it/hostashi/releases";

// releaseUrl links a released version (X.Y.Z, or a pre-release such as
// X.Y.Z-rc.1) to its release notes. A dev build has no tag ("dev", or
// "X.Y.Z-dev+commit" from a snapshot), so it links to the list of releases.
export function releaseUrl(v: string): string {
  return /^\d+\.\d+\.\d+(-(alpha|beta|rc)[0-9.]*)?$/.test(v) ? `${releases}/tag/v${v}` : releases;
}
