// SPDX-License-Identifier: AGPL-3.0-or-later

// The page cache of a site (ADR-0046): nginx keeps the answers of the
// site's PHP for a few minutes. Logged-in visitors, carts, admin pages and
// excluded paths always skip it.

import { ApiError, request } from "./api";
import { formatNumber } from "./format";
import i18n from "./i18n";

export type Preset = "wordpress" | "woocommerce" | "php";

export type PageCache = {
  enabled: boolean;
  preset: Preset;
  minutes: number;
  sizeMB: number;
  exclude: string[];
  updatedAt?: string;
  choices: { presets: Preset[]; minutes: number[]; sizesMB: number[]; maxExclude: number };
};

// Getters, so the texts follow the language shown.
export const presetLabel: Record<Preset, string> = {
  get wordpress() { return i18n.t("siteApp.pageCache.presets.wordpress.label"); },
  get woocommerce() { return i18n.t("siteApp.pageCache.presets.woocommerce.label"); },
  get php() { return i18n.t("siteApp.pageCache.presets.php.label"); },
};

export const presetHint: Record<Preset, string> = {
  get wordpress() { return i18n.t("siteApp.pageCache.presets.wordpress.hint"); },
  get woocommerce() { return i18n.t("siteApp.pageCache.presets.woocommerce.hint"); },
  get php() { return i18n.t("siteApp.pageCache.presets.php.hint"); },
};

const path = (site: string) => "/sites/" + encodeURIComponent(site) + "/page-cache";

export const getPageCache = (site: string) => request<PageCache>("GET", path(site));

export const setPageCache = (site: string, c: { enabled: boolean; preset: Preset; minutes: number; exclude: string[] }) =>
  request<PageCache>("PUT", path(site), c);

export const setPageCacheSize = (site: string, sizeMB: number) => request<PageCache>("PUT", path(site) + "/size", { sizeMB });

export const purgePageCache = (site: string) => request<void>("POST", path(site) + "/purge");

// excludeRe mirrors agentapi's check: a path, optionally ending in "*".
const excludeRe = /^\/[A-Za-z0-9._~%/-]{0,200}\*?$/;

/** parseExclude turns the text field into a list: one path per line, blanks dropped, duplicates removed. */
export function parseExclude(text: string): string[] {
  const out: string[] = [];
  for (const line of text.split("\n")) {
    const p = line.trim();
    if (p !== "" && !out.includes(p)) out.push(p);
  }
  return out;
}

/** excludeProblem checks the list as the server does, so most mistakes show before saving. */
export function excludeProblem(paths: string[], max: number): string {
  if (paths.length > max) return i18n.t("siteApp.pageCache.problems.tooMany", { max });
  for (const p of paths) {
    const segs = p.replace(/^\//, "").replace(/\*$/, "").split("/");
    const badSeg = segs.some((s, i) => s === "." || s === ".." || (s === "" && i < segs.length - 1));
    if (!excludeRe.test(p) || badSeg) return i18n.t("siteApp.pageCache.problems.badPath", { path: p });
  }
  return "";
}

export const formatSize = (mb: number) => (mb >= 1024 ? `${formatNumber(mb / 1024)} GB` : `${formatNumber(mb)} MB`);

export function pageCacheMessage(err: unknown): string {
  if (err instanceof ApiError) {
    switch (err.code) {
      case "rate_limited":
      case "forbidden":
      case "site_busy":
      case "not_found":
        return i18n.t(`siteApp.pageCache.errors.${err.code}`);
      case "invalid_argument": {
        const m = err.message.replace(/^hosting: invalid input: (page cache: )?/, "");
        return m.charAt(0).toUpperCase() + m.slice(1) + ".";
      }
    }
    if (err.status === 409 || err.status === 502 || err.status === 503) return i18n.t("siteApp.pageCache.errors.refused");
    return err.message;
  }
  return i18n.t("siteApp.pageCache.errors.failed");
}
