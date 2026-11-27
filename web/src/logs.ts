// SPDX-License-Identifier: AGPL-3.0-or-later

// The logs of a site (ADR-0053): nginx's access and error log, the PHP
// error log or the app's output, and the WordPress install (ADR-0069). Everyone with access to the site reads them; admins set how
// many days the server keeps them.

import { ApiError, request } from "./api";
import i18n from "./i18n";

export type LogKind = "access" | "error" | "php" | "app" | "wordpress";

/** The logs, each with its name and what it holds in the panel's language. */
export const logKinds: { id: LogKind; readonly label: string; readonly hint: string }[] = (["access", "error", "php", "app", "wordpress"] as const).map((id) => ({
  id,
  get label() {
    return i18n.t(`logs.kinds.${id}.label`);
  },
  get hint() {
    return i18n.t(`logs.kinds.${id}.hint`);
  },
}));

/**
 * The logs a site has: the app log for a Node.js site, the PHP log for the
 * others, and the WordPress install log for a PHP site.
 */
export const siteLogKinds = (type: string) =>
  logKinds.filter((k) => {
    if (k.id === "wordpress") return type === "php";
    return type === "node" || type === "app" ? k.id !== "php" : k.id !== "app";
  });

export type LogChunk = { lines: string[]; cursor: string; rotated: boolean; more: boolean };

export type LogRetention = { days: number; minDays: number; maxDays: number; defaultDays: number };

const logPath = (site: string, kind: LogKind) => "/sites/" + encodeURIComponent(site) + "/logs/" + kind;

export const tailLog = (site: string, kind: LogKind, lines: number) => request<LogChunk>("GET", logPath(site, kind) + "?lines=" + String(lines));

export const followLog = (site: string, kind: LogKind, cursor: string) =>
  request<LogChunk>("GET", logPath(site, kind) + "?cursor=" + encodeURIComponent(cursor));

export const getLogRetention = () => request<LogRetention>("GET", "/settings/logs");
export const setLogRetention = (days: number) => request<LogRetention>("PUT", "/settings/logs", { days });

// The most lines the view keeps; older ones drop off the top.
export const maxShownLines = 2000;

// appendLines adds new lines and keeps the last maxShownLines.
export function appendLines(old: string[], add: string[], max = maxShownLines): string[] {
  const all = old.length + add.length > max ? [...old, ...add].slice(-max) : [...old, ...add];
  return all;
}

// filterLines keeps the lines that contain every word of the filter, case
// insensitive. The filter runs in the browser on what is loaded.
export function filterLines(lines: string[], filter: string): string[] {
  const words = filter.toLowerCase().split(/\s+/).filter(Boolean);
  if (words.length === 0) return lines;
  return lines.filter((l) => {
    const low = l.toLowerCase();
    return words.every((w) => low.includes(w));
  });
}

// lineTone colours a line by what it reports: an access line by its HTTP
// status, an error line by its level.
export function lineTone(kind: LogKind, line: string): "err" | "warn" | "" {
  if (kind === "access") {
    const m = /" (\d{3}) /.exec(line);
    if (!m) return "";
    const s = Number(m[1]);
    return s >= 500 ? "err" : s >= 400 ? "warn" : "";
  }
  if (/\[(crit|alert|emerg|error)\]|PHP (Fatal|Parse) error|ERROR:/i.test(line)) return "err";
  if (/\[warn\]|PHP Warning|WARNING:/i.test(line)) return "warn";
  return "";
}

export function retentionProblem(text: string, r: Pick<LogRetention, "minDays" | "maxDays">): string {
  if (!/^\d+$/.test(text.trim())) return i18n.t("logs.check.whole");
  const n = Number(text);
  if (n < r.minDays || n > r.maxDays) return i18n.t("logs.check.range", { min: r.minDays, max: r.maxDays });
  return "";
}

export function logMessage(err: unknown): string {
  if (err instanceof ApiError) {
    if (err.code === "agent_failed_precondition") return i18n.t("logs.errors.precondition", { reason: err.message });
    if (err.status === 404) return i18n.t("logs.errors.notFound");
    return err.message;
  }
  return i18n.t("logs.errors.unreachable");

}
