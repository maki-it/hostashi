// SPDX-License-Identifier: AGPL-3.0-or-later

// Resource limits (ADR-0015): presets the admin edits, one of them the
// default for new sites, and the limits of each site with its disk use.
// 0 means unlimited in every limit except the PHP workers.

import { formatBytes, formatCount } from "./format";
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
  databaseBytes: number; // warning threshold for the site's local databases (ADR-0057), 0: none
  databaseConnections: number; // connections per database user, 0: unlimited
  unlimited?: string[]; // set by the server: the limits that are 0
};

// PresetCaches are starting values for a site that gets the preset
// (ADR-0045, ADR-0046), not limits: the site can change them afterwards.
// 0 means off.
export type PresetCaches = { objectCacheMb: number; pageCacheMb?: number };

export const noCaches: PresetCaches = { objectCacheMb: 0, pageCacheMb: 0 };

// The page cache sizes a preset may give (agentapi.PageCacheSizesMB).
export const pageCacheSizes = [64, 128, 256, 512, 1024, 2048, 4096];

export type Preset = { name: string; default: boolean; limits: Limits; caches?: PresetCaches; createdAt: number; updatedAt: number };

export type Usage = {
  enforced: boolean; // false: no project quota, disk limits are not enforced
  bytes: number;
  inodes: number;
  bytesPercent: number;
  inodesPercent: number;
  level: "ok" | "warning" | "critical";
};

// DatabaseUsage is the size of the site's local databases (ADR-0057).
export type DatabaseUsage = {
  bytes: number;
  databases: number;
  percent: number; // of databaseBytes; 0 without a threshold
  level: "ok" | "warning" | "critical";
  measuredAt: number;
};

export type SiteLimits = {
  set: boolean; // false: a site from before limits existed
  preset: string; // "": custom values
  limits: Limits;
  appliedAt: number;
  applyError: string;
  updatedAt: number;
  usage?: Usage;
  databaseUsage?: DatabaseUsage;
};

const presetPath = (name?: string) => "/limit-presets" + (name === undefined ? "" : "/" + encodeURIComponent(name));
const sitePath = (site: string) => "/sites/" + encodeURIComponent(site) + "/limits";

// listPresets also says how many CPU cores the server has (ADR-0037), for
// the CPU limit in cores.
export const listPresets = () => request<{ presets: Preset[]; cpus?: number }>("GET", presetPath());
export const createPreset = (name: string, isDefault: boolean, limits: Limits, caches: PresetCaches) =>
  request<Preset>("POST", presetPath(), { name, default: isDefault, limits: strip(limits), caches });
// updatePreset replaces the whole preset: pass the caches it keeps.
export const updatePreset = (old: string, name: string, isDefault: boolean, limits: Limits, caches: PresetCaches) =>
  request<Preset>("PUT", presetPath(old), { name, default: isDefault, limits: strip(limits), caches });

// presetCacheProblem checks the object cache against the preset's memory
// limit, as the server does: at most half of it.
export function presetCacheProblem(c: PresetCaches, memoryBytes: number): string {
  if (c.objectCacheMb === 0 || memoryBytes === 0) return "";
  if (c.objectCacheMb * 1024 ** 2 > memoryBytes / 2) {
    return `The object cache may take at most half of the memory limit (${String(Math.floor(memoryBytes / 2 / 1024 ** 2))} MiB).`;
  }
  return "";
}
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

// Sizes and counts in the panel's language (ADR-0071).
export { formatBytes, formatCount };

const trim = (v: number) => (Number.isInteger(v) ? String(v) : v.toFixed(1));

export const priorityLabel: Record<CPUPriority, string> = { low: "Low", normal: "Normal", high: "High" };

// Mirrors of internal/agentapi and internal/hosting, so a form can say what
// is wrong before a request. The server checks again.
export const bounds = {
  diskGiB: { min: 1, max: 1024 * 1024 }, // 64 MiB is the server's minimum; the form works in GiB
  inodes: { min: 1000, max: 2 ** 32 - 1 },
  memoryMiB: { min: 64, max: 1024 * 1024 },
  tasks: { min: 16, max: 2 ** 20 },
  workers: { min: 1, max: 500 },
  cpuQuota: { min: 0, max: 100000 }, // percent of one core; the form works in cores
  dbGiB: { min: 1, max: 1024 * 1024 },
  dbConnections: { min: 1, max: 10000 }, // agentapi.MaxDBUserConnections
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
  /** The hard CPU limit in cores, such as "1.5"; "0" for none. */
  cpuCores: string;
  dbGiB: string;
  dbUnlimited: boolean;
  dbConnections: string;
  dbConnectionsUnlimited: boolean;
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
    cpuCores: formatCores(l.cpuQuotaPercent),
    dbGiB: l.databaseBytes ? trim(l.databaseBytes / GiB) : "2",
    dbUnlimited: l.databaseBytes === 0,
    dbConnections: l.databaseConnections ? String(l.databaseConnections) : "20",
    dbConnectionsUnlimited: l.databaseConnections === 0,
  };
}

function whole(s: string, what: string, min: number, max: number): number | string {
  const t = s.trim();
  if (!/^[0-9]+$/.test(t)) return `${what} must be a whole number.`;
  const v = Number(t);
  if (v < min || v > max) return `${what} must be between ${formatCount(min)} and ${formatCount(max)}.`;
  return v;
}

// formatCores shows a CPU quota in percent of one core as cores: 150 is
// "1.5", 0 is "0".
export const formatCores = (percent: number) => String(Math.round(percent) / 100);

// coresToPercent reads a number of cores with up to two decimals and turns
// it into percent of one core, or returns the problem.
export function coresToPercent(s: string): number | string {
  const t = s.trim().replace(",", ".");
  if (!/^[0-9]+(\.[0-9]{1,2})?$/.test(t)) return "CPU cores must be a number such as 2 or 0.5, or 0 for no limit.";
  const v = Math.round(Number(t) * 100);
  if (v > bounds.cpuQuota.max) return `CPU cores must be at most ${formatCount(bounds.cpuQuota.max / 100)}.`;
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
  const quota = coresToPercent(f.cpuCores);
  if (typeof quota === "string") return quota;
  const db = f.dbUnlimited ? 0 : whole(f.dbGiB, "Database size", bounds.dbGiB.min, bounds.dbGiB.max);
  if (typeof db === "string") return db;
  const conns = f.dbConnectionsUnlimited ? 0 : whole(f.dbConnections, "Database connections", bounds.dbConnections.min, bounds.dbConnections.max);
  if (typeof conns === "string") return conns;
  return {
    diskBytes: disk * GiB,
    diskInodes: inodes,
    memoryBytes: mem * MiB,
    phpWorkers: workers,
    tasks,
    cpuPriority: f.cpuPriority,
    cpuQuotaPercent: quota,
    databaseBytes: db * GiB,
    databaseConnections: conns,
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
