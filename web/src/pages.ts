// SPDX-License-Identifier: AGPL-3.0-or-later

// Maintenance and parking pages (ADR-0027): built-in ones and uploaded ones
// (a single HTML file or a ZIP file with HTML, CSS, JavaScript, images and
// fonts). A site in maintenance shows its page with 503 to every address
// that is not allowed.

import { request } from "./api";
import { formatNumber } from "./format";
import i18n, { areaErrorText } from "./i18n";
import type { Site, SiteMaintenance } from "./sites";

export type PageKind = "maintenance" | "parking";

export type StatusPage = {
  id: string;
  name: string;
  kind: PageKind;
  builtin: boolean;
  sha256: string;
  files: { path: string; size: number }[];
  sites: string[]; // sites that name the page
  parked: string[]; // parked domains that show it
  createdAt: string | null;
  updatedAt: string | null;
};

export const maxPageBytes = 2 << 20;
export const maxAllowIps = 20;

const pagePath = (id: string) => "/status-pages/" + encodeURIComponent(id);

export const listPages = async () => (await request<{ pages: StatusPage[] }>("GET", "/status-pages")).pages;

export function createPage(name: string, kind: PageKind, file: File) {
  const f = new FormData();
  f.append("name", name);
  f.append("kind", kind);
  f.append("file", file, file.name);
  return request<StatusPage>("POST", "/status-pages", f);
}

export function updatePage(id: string, change: { name?: string; file?: File }) {
  const f = new FormData();
  if (change.name) f.append("name", change.name);
  if (change.file) f.append("file", change.file, change.file.name);
  return request<StatusPage>("PUT", pagePath(id), f);
}

/** previewLink returns a short-lived, sandboxed link that shows the page. */
export const previewLink = (id: string) => request<{ url: string; expires: string }>("POST", pagePath(id) + "/preview");

/**
 * openPreview opens the page in a new tab. The tab opens at once, before
 * the link is fetched, so popup blockers let it through; it loses its
 * opener before it navigates. It returns an error text, "" on success.
 */
export async function openPreview(id: string, open: typeof window.open = window.open.bind(window)): Promise<string> {
  const tab = open("", "_blank");
  if (tab) tab.opener = null;
  try {
    const { url } = await previewLink(id);
    if (!tab) return i18n.t("pages.problems.popupBlocked");
    tab.location.href = url;
    return "";
  } catch (err) {
    tab?.close();
    return pageMessage(err);
  }
}

export const deletePage = (id: string) => request<void>("DELETE", pagePath(id));

export const setMaintenance = (site: string, m: SiteMaintenance) =>
  request<Site>("PUT", "/sites/" + encodeURIComponent(site) + "/maintenance", m);

export async function clientIp(): Promise<string> {
  const me = await request<{ clientIp?: string }>("GET", "/auth/me");
  return me.clientIp ?? "";
}

export function fileProblem(file: File | null): string {
  if (!file) return i18n.t("pages.problems.chooseFile");
  if (!/\.(html?|zip)$/i.test(file.name)) return i18n.t("pages.problems.fileType");
  if (file.size > maxPageBytes) return i18n.t("pages.problems.tooLarge");
  if (file.size === 0) return i18n.t("pages.problems.empty");
  return "";
}

export function pageNameProblem(name: string): string {
  const n = name.trim();
  if (n === "") return i18n.t("pages.problems.enterName");
  if ([...n].length > 64) return i18n.t("pages.problems.nameTooLong");
  return "";
}

// parseAllowIps splits a text field into addresses: one per line or
// separated by spaces or commas. The server checks and normalizes them.
export const parseAllowIps = (text: string) =>
  text
    .split(/[\s,]+/)
    .map((s) => s.trim())
    .filter((s) => s !== "");

const ipish = /^[0-9a-fA-F:.]+(\/[0-9]{1,3})?$/;

export function allowIpsProblem(list: string[]): string {
  if (list.length > maxAllowIps) return `At most ${maxAllowIps} addresses.`;
  const bad = list.find((s) => !ipish.test(s));
  if (bad) return `"${bad}" is not an IP address or range like 203.0.113.7 or 10.0.0.0/8.`;
  return "";
}

export const kindLabel = (k: PageKind) => (k === "maintenance" ? i18n.t("pages.kinds.maintenance") : i18n.t("pages.kinds.parking"));

export function formatSize(n: number): string {
  if (n < 1024) return formatNumber(n) + " B";
  if (n < 1024 * 1024) return formatNumber(n / 1024, { minimumFractionDigits: 1, maximumFractionDigits: 1 }) + " KiB";
  return formatNumber(n / 1024 / 1024, { minimumFractionDigits: 2, maximumFractionDigits: 2 }) + " MiB";
}

/** pageMessage explains an error of the maintenance pages in the current language. */
export const pageMessage = (err: unknown): string => areaErrorText("pages.errors", err);
