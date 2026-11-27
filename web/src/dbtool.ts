// SPDX-License-Identifier: AGPL-3.0-or-later

// The web database tool (ADR-0038): Adminer on its own port. Opening a
// database returns an address with a one-time ticket; the database password
// stays on the server.

import { ApiError, request } from "./api";
import i18n, { areaErrorText } from "./i18n";

export type DBToolStatus = { available: boolean; minutes: number[] };
export type DBToolSession = { url: string; expires: string };

export const getDBTool = () => request<DBToolStatus>("GET", "/dbtool");

export const openDBTool = (db: string, minutes: number) =>
  request<DBToolSession>("POST", "/databases/" + encodeURIComponent(db) + "/tool", { minutes });

// durationLabel names a session length.
export const durationLabel = (m: number) =>
  m % 60 === 0 ? i18n.t("databases.tool.hours", { count: m / 60 }) : i18n.t("databases.tool.minutes", { count: m });

// toolMessage turns an API error into a sentence.
export function toolMessage(err: unknown): string {
  if (err instanceof ApiError && err.code === "not_found" && err.message === "the session has ended") return i18n.t("databases.tool.errors.sessionEnded");
  return areaErrorText("databases.tool.errors", err);
}

// safeToolURL accepts only the https address the panel built.
export function safeToolURL(u: string): string | null {
  try {
    const p = new URL(u);
    return p.protocol === "https:" && /^[0-9a-f]{32}$/.test(p.searchParams.get("ticket") ?? "") ? p.toString() : null;
  } catch {
    return null;
  }
}

// withLook adds what Adminer needs to look like the panel: the colour
// scheme the account chose (none for the system's) and the panel's https
// port, for its links back. Both only change the page's look.
export function withLook(u: string, scheme: string | undefined, panel: Pick<Location, "protocol" | "port"> = window.location): string {
  const p = new URL(u);
  if (scheme === "light" || scheme === "dark") p.searchParams.set("scheme", scheme);
  if (panel.protocol === "https:") p.searchParams.set("panel", panel.port || "443");
  return p.toString();
}

// An open Adminer session on one database: who opened it and until when.
// user is empty once that account is gone.
export type DBToolOpen = { id: string; user: string; opened: string; expires: string };

const sessionsPath = (db: string) => "/databases/" + encodeURIComponent(db) + "/tool/sessions";

export const listToolSessions = async (db: string) => (await request<{ sessions: DBToolOpen[] }>("GET", sessionsPath(db))).sessions;

// extendToolSession moves the end 30 minutes later, at most two hours ahead.
export const extendToolSession = (db: string, id: string) =>
  request<DBToolOpen>("POST", sessionsPath(db) + "/" + encodeURIComponent(id) + "/extend");

export const endToolSession = (db: string, id: string) => request<void>("DELETE", sessionsPath(db) + "/" + encodeURIComponent(id));
