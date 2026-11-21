// SPDX-License-Identifier: AGPL-3.0-or-later

// The object cache of a site (ADR-0045): one Valkey per site on a Unix
// socket that only the site's own user reaches. Everyone with access to
// the site turns it on or off and flushes it; admins and managers set its
// size, which counts against the site's memory limit.

import { ApiError, request } from "./api";
import type { Tone } from "./ui/Badge";

export type ObjectCacheStatus = { state: string; paused: boolean; usedBytes: number };

export type ObjectCache = {
  enabled: boolean;
  memoryMb: number;
  minMb: number;
  maxMb: number;
  memoryLimitBytes: number;
  socket: string;
  status: ObjectCacheStatus | null;
};

const cachePath = (site: string) => "/sites/" + encodeURIComponent(site) + "/object-cache";

export const getObjectCache = (site: string) => request<ObjectCache>("GET", cachePath(site));
export const setObjectCache = (site: string, enabled: boolean) => request<ObjectCache>("PUT", cachePath(site), { enabled });
export const setObjectCacheSize = (site: string, memoryMb: number) => request<ObjectCache>("PUT", cachePath(site) + "/size", { memoryMb });
export const flushObjectCache = (site: string) => request<{ status: ObjectCacheStatus }>("POST", cachePath(site) + "/flush");

// The sizes offered in the panel (MiB), within minMb and maxMb.
export const cacheSizes = [32, 64, 128, 256, 512, 1024];

// sizeChoices lists the offered sizes that fit the site's memory limit
// (the cache may take at most half of it), always with the current one.
export function sizeChoices(c: Pick<ObjectCache, "memoryMb" | "minMb" | "maxMb" | "memoryLimitBytes">): number[] {
  const max = c.memoryLimitBytes > 0 ? Math.min(c.maxMb, Math.floor(c.memoryLimitBytes / 2 / (1 << 20))) : c.maxMb;
  const out = cacheSizes.filter((n) => n >= c.minMb && n <= max);
  if (!out.includes(c.memoryMb)) out.push(c.memoryMb);
  return out.sort((a, b) => a - b);
}

// wpConfig is what WordPress with the Redis Object Cache plugin needs in
// wp-config.php to use the socket.
export const wpConfig = (socket: string) => `define('WP_REDIS_SCHEME', 'unix');\ndefine('WP_REDIS_PATH', '${socket}');`;

export function cacheState(c: ObjectCache): { word: string; tone: Tone } {
  if (!c.enabled) return { word: "Off", tone: "off" };
  if (!c.status) return { word: "Unknown", tone: "warn" };
  if (c.status.paused) return { word: "Paused", tone: "off" };
  if (c.status.state === "active") return { word: "Running", tone: "ok" };
  if (c.status.state === "failed") return { word: "Failed", tone: "err" };
  return { word: c.status.state || "Stopped", tone: "warn" };
}

export function cacheMessage(err: unknown): string {
  if (err instanceof ApiError) {
    switch (err.code) {
      case "object_cache_unavailable":
        return "The object cache is not available on this server.";
      case "object_cache_off":
        return "The object cache is off.";
      case "site_busy":
        return "The site is being created, deleted or is disabled. Try again later.";
      case "forbidden":
        return "Only administrators and managers can change the size.";
      case "not_found":
        return "The site does not exist.";
    }
    if (err.status === 409 || err.status === 502) return "The cache did not start. The previous setting stays.";
    return err.message;
  }
  return "The request failed. Check the connection and try again.";
}
