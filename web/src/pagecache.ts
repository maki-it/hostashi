// SPDX-License-Identifier: AGPL-3.0-or-later

// The page cache of a site (ADR-0046): nginx keeps the answers of the
// site's PHP for a few minutes. Logged-in visitors, carts, admin pages and
// excluded paths always skip it.

import { ApiError, request } from "./api";

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

export const presetLabel: Record<Preset, string> = {
  wordpress: "WordPress",
  woocommerce: "WooCommerce",
  php: "Other PHP application",
};

export const presetHint: Record<Preset, string> = {
  wordpress: "Skips logged-in users, password-protected posts, comment authors, the admin area, the login, cron, XML-RPC, the REST API and previews.",
  woocommerce: "Like WordPress, and skips carts, the checkout, the account pages and add-to-cart requests.",
  php: "Skips every request with a cookie, because a cookie may mean a session. Safe for any application, but fewer pages come from the cache.",
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
  if (paths.length > max) return `At most ${max} excluded paths.`;
  for (const p of paths) {
    const segs = p.replace(/^\//, "").replace(/\*$/, "").split("/");
    const badSeg = segs.some((s, i) => s === "." || s === ".." || (s === "" && i < segs.length - 1));
    if (!excludeRe.test(p) || badSeg) return `“${p}” is not a path. Start with /, use letters, digits and . _ ~ % / - only, and put * only at the end.`;
  }
  return "";
}

export const formatSize = (mb: number) => (mb >= 1024 ? `${mb / 1024} GB` : `${mb} MB`);

export function pageCacheMessage(err: unknown): string {
  if (err instanceof ApiError) {
    switch (err.code) {
      case "rate_limited":
        return "The cache was emptied a moment ago. Try again in a few seconds.";
      case "forbidden":
        return "Only managers and administrators can change the size.";
      case "site_busy":
        return "The site is being created or deleted. Try again in a moment.";
      case "not_found":
        return "The site does not exist.";
      case "invalid_argument": {
        const m = err.message.replace(/^hosting: invalid input: (page cache: )?/, "");
        return m.charAt(0).toUpperCase() + m.slice(1) + ".";
      }
    }
    if (err.status === 409 || err.status === 502 || err.status === 503) return "The web server did not accept the change. The previous settings stay active.";
    return err.message;
  }
  return "The request failed. Check the connection and try again.";
}
