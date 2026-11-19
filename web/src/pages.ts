// SPDX-License-Identifier: AGPL-3.0-or-later

// Maintenance and parking pages (ADR-0027): built-in ones and uploaded ones
// (a single HTML file or a ZIP file with HTML, CSS, JavaScript, images and
// fonts). A site in maintenance shows its page with 503 to every address
// that is not allowed.

import { ApiError, request } from "./api";
import type { Site, SiteMaintenance } from "./sites";

export type PageKind = "maintenance" | "parking";

export type StatusPage = {
  id: string;
  name: string;
  kind: PageKind;
  builtin: boolean;
  sha256: string;
  files: { path: string; size: number }[];
  sites: string[];
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

export const deletePage = (id: string) => request<void>("DELETE", pagePath(id));

export const setMaintenance = (site: string, m: SiteMaintenance) =>
  request<Site>("PUT", "/sites/" + encodeURIComponent(site) + "/maintenance", m);

export async function clientIp(): Promise<string> {
  const me = await request<{ clientIp?: string }>("GET", "/auth/me");
  return me.clientIp ?? "";
}

export function fileProblem(file: File | null): string {
  if (!file) return "Choose an .html or .zip file.";
  if (!/\.(html?|zip)$/i.test(file.name)) return "Upload an .html file or a .zip file.";
  if (file.size > maxPageBytes) return "The file is larger than 2 MiB.";
  if (file.size === 0) return "The file is empty.";
  return "";
}

export function pageNameProblem(name: string): string {
  const n = name.trim();
  if (n === "") return "Enter a name.";
  if ([...n].length > 64) return "Use at most 64 characters.";
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

export const kindLabel = (k: PageKind) => (k === "maintenance" ? "Maintenance" : "Parking");

export function formatSize(n: number): string {
  if (n < 1024) return n + " B";
  if (n < 1024 * 1024) return (n / 1024).toFixed(1) + " KiB";
  return (n / 1024 / 1024).toFixed(2) + " MiB";
}

export function pageMessage(err: unknown): string {
  if (err instanceof ApiError) {
    switch (err.code) {
      case "page_in_use":
        return "A site uses this page. Choose another page there first.";
      case "page_builtin":
        return "Built-in pages cannot be changed or deleted.";
      case "page_not_found":
        return "This page does not exist any more. Reload the page.";
      case "too_large":
        return "The file is larger than 2 MiB.";
      case "forbidden":
        return "Only administrators can manage pages.";
      case "not_found":
        return "This site does not exist any more. Reload the page.";
      case "site_busy":
        return "The site is being created or deleted. Reload the page in a moment.";
      case "agent_timeout":
        return "The server did not answer in time. Reload the page to see whether the change was made.";
      case "agent_unavailable":
        return "The change could not be completed. Try again.";
      default:
        return err.message ? err.message.charAt(0).toUpperCase() + err.message.slice(1) + "." : "Something went wrong (" + err.code + ").";
    }
  }
  if (err instanceof TypeError) return "The server is not reachable.";
  return "Something went wrong. Try again.";
}
