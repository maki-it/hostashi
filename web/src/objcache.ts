// SPDX-License-Identifier: AGPL-3.0-or-later

// The object cache of a site (ADR-0045): one Valkey per site on a Unix
// socket that only the site's own user reaches. Everyone with access to
// the site turns it on or off and flushes it; admins and managers set its
// size, which counts against the site's memory limit.

import { ApiError, request } from "./api";
import i18n from "./i18n";
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
  if (!c.enabled) return { word: i18n.t("siteApp.objectCache.states.off"), tone: "off" };
  if (!c.status) return { word: i18n.t("siteApp.objectCache.states.unknown"), tone: "warn" };
  if (c.status.paused) return { word: i18n.t("siteApp.objectCache.states.paused"), tone: "off" };
  if (c.status.state === "active") return { word: i18n.t("siteApp.objectCache.states.running"), tone: "ok" };
  if (c.status.state === "failed") return { word: i18n.t("siteApp.objectCache.states.failed"), tone: "err" };
  return { word: c.status.state || i18n.t("siteApp.objectCache.states.stopped"), tone: "warn" };
}

export function cacheMessage(err: unknown): string {
  if (err instanceof ApiError) {
    switch (err.code) {
      case "object_cache_unavailable":
      case "object_cache_off":
      case "site_busy":
      case "forbidden":
      case "not_found":
        return i18n.t(`siteApp.objectCache.errors.${err.code}`);
    }
    if (err.status === 409 || err.status === 502) return i18n.t("siteApp.objectCache.errors.notStarted");
    return err.message;
  }
  return i18n.t("siteApp.objectCache.errors.failed");
}
