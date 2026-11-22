// SPDX-License-Identifier: AGPL-3.0-or-later

// Time zone, hostname and reboot of the server (ADR-0060).

import { request } from "./api";

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
};

export const getSystem = () => request<SystemInfo>("GET", "/system");

export const listTimezones = () => request<{ zones: string[] }>("GET", "/system/timezones").then((r) => r.zones);

export const setTimezone = (zone: string) => request<SystemInfo>("PUT", "/system/timezone", { zone });

export const setHostname = (hostname: string) => request<SystemInfo>("PUT", "/system/hostname", { hostname });

export const rebootServer = (confirm: string) => request<{ delaySeconds: number }>("POST", "/system/reboot", { confirm });

const labelRe = /^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$/;

/** hostnameError mirrors the server's check; "" means the name is fine. */
export function hostnameError(name: string): string {
  if (name === "") return "Enter a hostname.";
  if (name.length > 64) return "At most 64 characters.";
  const labels = name.split(".");
  if (!labels.every((l) => labelRe.test(l))) return "Use lowercase letters, digits and hyphens, with dots between the parts.";
  if (/^[0-9]+$/.test(labels[labels.length - 1] ?? "")) return "This looks like an address, not a name.";
  return "";
}

/** uptime formats seconds as days, hours and minutes. */
export function uptime(seconds: number): string {
  const s = Math.max(0, Math.floor(seconds));
  const d = Math.floor(s / 86400);
  const h = Math.floor((s % 86400) / 3600);
  const m = Math.floor((s % 3600) / 60);
  if (d > 0) return `${d} d ${h} h`;
  if (h > 0) return `${h} h ${m} min`;
  return `${m} min`;
}

/** clockIn shows the instant iso in a zone, as the server would; "" if the browser does not know the zone. */
export function clockIn(iso: string, zone: string): string {
  try {
    return new Intl.DateTimeFormat("en-GB", {
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
