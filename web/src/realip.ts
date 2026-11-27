// SPDX-License-Identifier: AGPL-3.0-or-later

// Real client address of a site behind a CDN or proxy (ADR-0059): types,
// API calls and the form model of the card in the "Domains & HTTPS" tab.

import { ApiError, request } from "./api";
import i18n from "./i18n";

export type WebRealIP = {
  cloudflare?: boolean;
  ranges?: string[];
  header?: string;
};

export type RealIPView = {
  realIp: WebRealIP;
  headers: string[];
  cloudflare: string[];
  cloudflareDate: string;
  maxRanges: number;
  updatedAt?: string;
};

export function getRealIP(site: string): Promise<RealIPView> {
  return request<RealIPView>(
    "GET",
    `/sites/${encodeURIComponent(site)}/real-ip`,
  );
}

export function setRealIP(site: string, r: WebRealIP): Promise<RealIPView> {
  return request<RealIPView>(
    "PUT",
    `/sites/${encodeURIComponent(site)}/real-ip`,
    r,
  );
}

export function getPanelRealIP(): Promise<RealIPView> {
  return request<RealIPView>("GET", "/settings/panel-real-ip");
}

export function setPanelRealIP(r: WebRealIP): Promise<RealIPView> {
  return request<RealIPView>("PUT", "/settings/panel-real-ip", r);
}

export type RealIPForm = {
  enabled: boolean;
  cloudflare: boolean;
  ranges: string; // one per line
  header: string; // "" is the default (the first of the list)
};

export function toRealIPForm(r: WebRealIP): RealIPForm {
  const ranges = r.ranges ?? [];
  return {
    enabled: r.cloudflare === true || ranges.length > 0,
    cloudflare: r.cloudflare === true,
    ranges: ranges.join("\n"),
    header: r.header ?? "",
  };
}

// splitRanges takes one range per line or separated by spaces or commas.
export function splitRanges(text: string): string[] {
  return text
    .split(/[\s,]+/)
    .map((s) => s.trim())
    .filter((s) => s !== "");
}

// fromRealIPForm builds the request body; it returns an error text for a
// form the server would refuse anyway.
export function fromRealIPForm(f: RealIPForm): {
  realIp: WebRealIP;
  error?: string;
} {
  if (!f.enabled) return { realIp: {} };
  const ranges = splitRanges(f.ranges);
  if (!f.cloudflare && ranges.length === 0)
    return {
      realIp: {},
      error: i18n.t("realIp.problems.noProxy"),
    };
  const r: WebRealIP = {};
  if (f.cloudflare) r.cloudflare = true;
  if (ranges.length > 0) r.ranges = ranges;
  if (f.header) r.header = f.header;
  return { realIp: r };
}

export function realIPMessage(err: unknown): string {
  if (err instanceof ApiError) {
    if (err.code === "real_ip_rejected")
      return i18n.t("realIp.errors.rejected", { reason: err.message.replace(/^real IP rejected: /, "") });
    if (err.code === "forbidden")
      return i18n.t("realIp.errors.forbidden");
    if (err.code === "not_found") return i18n.t("realIp.errors.siteGone");
    return err.message;
  }
  return i18n.t("realIp.errors.generic");
}
