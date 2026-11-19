// SPDX-License-Identifier: AGPL-3.0-or-later

// Resource limits (ADR-0015): presets the admin edits, one of them the
// default for new sites, and the limits of each site with its disk use.
// 0 means unlimited in every limit except the PHP workers.

import { ApiError, request } from "./api";
import { siteMessage } from "./sites";

export type CPUPriority = "low" | "normal" | "high";

export type Limits = {
  diskBytes: number;
  diskInodes: number;
  memoryBytes: number;
  phpWorkers: number;
  tasks: number;
  cpuPriority: CPUPriority;
  cpuQuotaPercent: number; // 0: no hard CPU limit
  unlimited?: string[]; // set by the server: the limits that are 0
};

export type Preset = { name: string; default: boolean; limits: Limits; createdAt: number; updatedAt: number };

export type Usage = {
  enforced: boolean; // false: no project quota, disk limits are not enforced
  bytes: number;
  inodes: number;
  bytesPercent: number;
  inodesPercent: number;
  level: "ok" | "warning" | "critical";
};

export type SiteLimits = {
  set: boolean; // false: a site from before limits existed
  preset: string; // "": custom values
  limits: Limits;
  appliedAt: number;
  applyError: string;
  updatedAt: number;
  usage?: Usage;
};

const presetPath = (name?: string) => "/limit-presets" + (name === undefined ? "" : "/" + encodeURIComponent(name));
const sitePath = (site: string) => "/sites/" + encodeURIComponent(site) + "/limits";

export const listPresets = async () => (await request<{ presets: Preset[] }>("GET", presetPath())).presets;
export const createPreset = (name: string, isDefault: boolean, limits: Limits) =>
  request<Preset>("POST", presetPath(), { name, default: isDefault, limits: strip(limits) });
export const updatePreset = (old: string, name: string, isDefault: boolean, limits: Limits) =>
  request<Preset>("PUT", presetPath(old), { name, default: isDefault, limits: strip(limits) });
export const deletePreset = (name: string) => request<void>("DELETE", presetPath(name));

export const getSiteLimits = (site: string) => request<SiteLimits>("GET", sitePath(site));
export const setSitePreset = (site: string, preset: string) => request<SiteLimits>("PUT", sitePath(site), { preset });
export const setSiteCustom = (site: string, custom: Limits) => request<SiteLimits>("PUT", sitePath(site), { custom: strip(custom) });

// The server refuses unknown fields; "unlimited" is only part of answers.
function strip(l: Limits): Limits {
  const { unlimited: _, ...rest } = l;
  return rest;
}

export const KiB = 1024;
export const MiB = 1024 * KiB;
export const GiB = 1024 * MiB;

export function formatBytes(n: number): string {
  if (n >= GiB) return trim(n / GiB) + " GiB";
  if (n >= MiB) return trim(n / MiB) + " MiB";
  if (n >= KiB) return trim(n / KiB) + " KiB";
  return String(n) + " B";
}

const trim = (v: number) => (Number.isInteger(v) ? String(v) : v.toFixed(1));

export const formatCount = (n: number) => n.toLocaleString("en-US");

export const priorityLabel: Record<CPUPriority, string> = { low: "Low", normal: "Normal", high: "High" };

// Mirrors of internal/agentapi and internal/hosting, so a form can say what
// is wrong before a request. The server checks again.
export const bounds = {
  diskGiB: { min: 1, max: 1024 * 1024 }, // 64 MiB is the server's minimum; the form works in GiB
  inodes: { min: 1000, max: 2 ** 32 - 1 },
  memoryMiB: { min: 64, max: 1024 * 1024 },
  tasks: { min: 16, max: 2 ** 20 },
  workers: { min: 1, max: 500 },
  cpuQuota: { min: 0, max: 100000 },
};

export const presetNameProblem = (name: string) =>
  /^[a-z0-9][a-z0-9-]{0,31}$/.test(name) ? "" : "A preset name has 1 to 32 lower-case letters, digits and dashes.";

// LimitFields is what a form edits: the numbers as typed, in GiB and MiB,
// and which limits are switched to unlimited.
export type LimitFields = {
  diskGiB: string;
  diskUnlimited: boolean;
  inodes: string;
  inodesUnlimited: boolean;
  memoryMiB: string;
  memoryUnlimited: boolean;
  tasks: string;
  tasksUnlimited: boolean;
  workers: string;
  cpuPriority: CPUPriority;
  cpuQuota: string;
};

export function fieldsOf(l: Limits): LimitFields {
  return {
    diskGiB: l.diskBytes ? trim(l.diskBytes / GiB) : "10",
    diskUnlimited: l.diskBytes === 0,
    inodes: l.diskInodes ? String(l.diskInodes) : "500000",
    inodesUnlimited: l.diskInodes === 0,
    memoryMiB: l.memoryBytes ? String(Math.round(l.memoryBytes / MiB)) : "512",
    memoryUnlimited: l.memoryBytes === 0,
    tasks: l.tasks ? String(l.tasks) : "128",
    tasksUnlimited: l.tasks === 0,
    workers: String(l.phpWorkers || 5),
    cpuPriority: l.cpuPriority,
    cpuQuota: String(l.cpuQuotaPercent),
  };
}

function whole(s: string, what: string, min: number, max: number): number | string {
  const t = s.trim();
  if (!/^[0-9]+$/.test(t)) return `${what} must be a whole number.`;
  const v = Number(t);
  if (v < min || v > max) return `${what} must be between ${formatCount(min)} and ${formatCount(max)}.`;
  return v;
}

// limitsOf checks the fields and turns them into limits, or returns the
// first problem. isDefault refuses unlimited values, like the server.
export function limitsOf(f: LimitFields, isDefault = false): Limits | string {
  if (isDefault && (f.diskUnlimited || f.inodesUnlimited || f.memoryUnlimited || f.tasksUnlimited)) {
    return "The default preset must limit disk space, files, memory and processes.";
  }
  const disk = f.diskUnlimited ? 0 : whole(f.diskGiB, "Disk space", bounds.diskGiB.min, bounds.diskGiB.max);
  if (typeof disk === "string") return disk;
  const inodes = f.inodesUnlimited ? 0 : whole(f.inodes, "Files", bounds.inodes.min, bounds.inodes.max);
  if (typeof inodes === "string") return inodes;
  const mem = f.memoryUnlimited ? 0 : whole(f.memoryMiB, "Memory", bounds.memoryMiB.min, bounds.memoryMiB.max);
  if (typeof mem === "string") return mem;
  const tasks = f.tasksUnlimited ? 0 : whole(f.tasks, "Processes", bounds.tasks.min, bounds.tasks.max);
  if (typeof tasks === "string") return tasks;
  const workers = whole(f.workers, "PHP workers", bounds.workers.min, bounds.workers.max);
  if (typeof workers === "string") return workers;
  const quota = whole(f.cpuQuota, "The CPU limit", bounds.cpuQuota.min, bounds.cpuQuota.max);
  if (typeof quota === "string") return quota;
  return {
    diskBytes: disk * GiB,
    diskInodes: inodes,
    memoryBytes: mem * MiB,
    phpWorkers: workers,
    tasks,
    cpuPriority: f.cpuPriority,
    cpuQuotaPercent: quota,
  };
}

// limitsMessage turns an API error into a sentence. On the presets page a
// missing thing is a preset, not a site.
export function limitsMessage(err: unknown, about: "site" | "preset" = "site"): string {
  if (err instanceof ApiError) {
    switch (err.code) {
      case "not_found":
        if (about === "preset") return "This preset does not exist any more. Reload the page.";
        break;
      case "limits_unavailable":
        return "Resource limits are not available on this panel.";
      case "preset_exists":
        return "A preset with this name exists. Choose another name.";
      case "preset_is_default":
        return "This is the default preset. Make another preset the default first.";
      case "forbidden":
        return "Only administrators can manage resource limits.";
    }
  }
  return siteMessage(err);
}
