// SPDX-License-Identifier: AGPL-3.0-or-later

// The append-only audit log of the panel: who did what and when. Only
// administrators read it (scope audit:read).

import { request } from "./api";
import { compare } from "./format";
import i18n, { areaErrorText } from "./i18n";

export type AuditEntry = {
  id: number;
  at: string;
  /** null for a failed login and for accounts deleted since. */
  userId: number | null;
  /** The username at the time of the action; empty without an account. */
  actor: string;
  action: string;
  target: string;
  detail: Record<string, unknown>;
  /** The client's address; empty for work without a request. */
  ip: string;
  /** The fields an update changed; null for a side that did not exist. */
  diff: Record<string, { old: string | null; new: string | null }>;
};

export type AuditPage = { entries: AuditEntry[]; next: string | null };

/** The filters of the page, as the address keeps them. Dates are local days (YYYY-MM-DD). */
export type AuditFilter = { action?: string; actor?: string; target?: string; ip?: string; from?: string; to?: string };

const day = /^(\d{4})-(\d{2})-(\d{2})$/;

/** localDay turns a local day into the instant it starts, or null. */
export function localDay(s: string, plusDays = 0): Date | null {
  const m = day.exec(s);
  if (!m) return null;
  const d = new Date(Number(m[1]), Number(m[2]) - 1, Number(m[3]) + plusDays);
  return Number.isNaN(d.getTime()) ? null : d;
}

/**
 * auditQuery turns the filters into the API's query. The days are local
 * and "to" includes its whole day, so until is the start of the next one.
 */
export function auditQuery(f: AuditFilter): URLSearchParams {
  const q = new URLSearchParams();
  if (f.action) q.set("action", f.action);
  if (f.actor?.trim()) q.set("actor", f.actor.trim());
  if (f.target?.trim()) q.set("target", f.target.trim());
  if (f.ip?.trim()) q.set("ip", f.ip.trim());
  const since = f.from ? localDay(f.from) : null;
  const until = f.to ? localDay(f.to, 1) : null;
  if (since) q.set("since", since.toISOString());
  if (until) q.set("until", until.toISOString());
  return q;
}

export function listAudit(f: AuditFilter, before?: string, limit = 50) {
  const q = auditQuery(f);
  q.set("limit", String(limit));
  if (before) q.set("before", before);
  return request<AuditPage>("GET", "/audit?" + q.toString());
}

export const auditActions = () => request<{ actions: string[] }>("GET", "/audit/actions").then((r) => r.actions);

/** exportUrl is the download address of the filtered log; the session cookie authorizes it. */
export function exportUrl(format: "csv" | "json", f: AuditFilter): string {
  const q = auditQuery(f);
  q.set("format", format);
  return "/api/v1/audit/export?" + q.toString();
}

/** actionGroups groups actions by their first part, for the filter: site, user, login, …, in the order of their names. */
export function actionGroups(actions: readonly string[]): { prefix: string; actions: string[] }[] {
  const groups = new Map<string, string[]>();
  for (const a of actions) {
    const prefix = a.split(".")[0] ?? a;
    groups.set(prefix, [...(groups.get(prefix) ?? []), a]);
  }
  return [...groups.entries()].sort(([a], [b]) => compare(actionGroupLabel(a), actionGroupLabel(b))).map(([prefix, list]) => ({ prefix, actions: list }));
}

/** detailPairs shows the detail object as key and value text, in a fixed order. */
export function detailPairs(detail: Record<string, unknown>): [string, string][] {
  return Object.keys(detail)
    .sort()
    .map((k) => {
      const v = detail[k];
      return [k, typeof v === "string" ? v : JSON.stringify(v)];
    });
}

/** diffRows lists the changed fields in a fixed order; a missing side shows as an empty value. */
export function diffRows(diff: AuditEntry["diff"] | undefined): { field: string; old: string; new: string }[] {
  return Object.keys(diff ?? {})
    .sort()
    .map((field) => ({ field, old: diff?.[field]?.old ?? "", new: diff?.[field]?.new ?? "" }));
}

/** actionGroupLabel names a group of actions, such as "Sites" for site.*; an unknown group keeps its prefix. */
export function actionGroupLabel(prefix: string): string {
  const key = `audit.groups.${prefix}`;
  return i18n.exists(key) ? (i18n.t as (k: string) => string)(key) : prefix;
}

/** auditMessage explains an error of the audit routes in the current language; invalid_query keeps the server's words. */
export const auditMessage = (err: unknown): string => areaErrorText("audit.errors", err);
