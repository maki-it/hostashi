// SPDX-License-Identifier: AGPL-3.0-or-later

// The web database tool (ADR-0038): Adminer on its own port. Opening a
// database returns an address with a one-time ticket; the database password
// stays on the server.

import { ApiError, request } from "./api";

export type DBToolStatus = { available: boolean; minutes: number[] };
export type DBToolSession = { url: string; expires: string };

export const getDBTool = () => request<DBToolStatus>("GET", "/dbtool");

export const openDBTool = (db: string, minutes: number) =>
  request<DBToolSession>("POST", "/databases/" + encodeURIComponent(db) + "/tool", { minutes });

// durationLabel names a session length.
export const durationLabel = (m: number) => (m % 60 === 0 ? (m === 60 ? "1 hour" : `${m / 60} hours`) : `${m} minutes`);

// toolMessage turns an API error into a sentence.
export function toolMessage(err: unknown): string {
  if (err instanceof ApiError) {
    switch (err.code) {
      case "dbtool_unavailable":
        return "The database tool is not available. It needs the panel's own certificate; see Health.";
      case "not_found":
        return "This database does not exist any more. Reload the page.";
      case "forbidden":
        return "Your role does not allow opening this database.";
      case "site_busy":
        return "The site is being created or deleted. Try again in a moment.";
      default:
        return err.message ? err.message.charAt(0).toUpperCase() + err.message.slice(1) + "." : "Something went wrong (" + err.code + ").";
    }
  }
  if (err instanceof TypeError) return "The server is not reachable.";
  return "Something went wrong. Try again.";
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
