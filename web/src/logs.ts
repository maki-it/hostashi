// SPDX-License-Identifier: AGPL-3.0-or-later

// The logs of a site (ADR-0053): nginx's access and error log and the PHP
// error log. Everyone with access to the site reads them; admins set how
// many days the server keeps them.

import { ApiError, request } from "./api";

export type LogKind = "access" | "error" | "php";

export const logKinds: { id: LogKind; label: string; hint: string }[] = [
  { id: "access", label: "Access", hint: "Every request to the site, with the visitor's address." },
  { id: "error", label: "Web server errors", hint: "What nginx reports for the site, including PHP messages it receives." },
  { id: "php", label: "PHP errors", hint: "Warnings and errors of the site's PHP, written by PHP-FPM." },
];

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
  if (!/^\d+$/.test(text.trim())) return "Enter a whole number of days.";
  const n = Number(text);
  if (n < r.minDays || n > r.maxDays) return `Enter ${r.minDays} to ${r.maxDays} days.`;
  return "";
}

export function logMessage(err: unknown): string {
  if (err instanceof ApiError) {
    if (err.code === "agent_failed_precondition") return "The log cannot be read on this server: " + err.message + ".";
    if (err.status === 404) return "There is no such site or log.";
    return err.message;
  }
  return "The log could not be loaded. Check the connection and try again.";
}
