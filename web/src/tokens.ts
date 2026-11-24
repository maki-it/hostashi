// SPDX-License-Identifier: AGPL-3.0-or-later

// API tokens of the logged-in account (ADR-0018, ADR-0035). Creating,
// listing and revoking them needs a browser session; a token cannot do it.

import { ApiError, request } from "./api";

export type APIToken = {
  id: number;
  name: string;
  /** The first characters of the secret, such as hst_Ab3dEf. */
  prefix: string;
  scopes: string[];
  createdAt: string;
  /** null: the token never expires. */
  expiresAt: string | null;
  neverExpires: boolean;
  lastUsedAt: string | null;
  lastUsedIp?: string;
  revokedAt?: string | null;
  /** null: every site the account reaches. */
  sites: string[] | null;
  warnings?: string[];
};

export type CreatedToken = APIToken & { token: string; warnings: string[] };

export type TokenRequest = {
  name: string;
  scopes: string[];
  /** null: every site the account reaches, also later ones. */
  sites: string[] | null;
  /** 1 to 365; ignored when unlimited. */
  expiresInDays: number;
  unlimited: boolean;
};

export const listTokens = () => request<{ tokens: APIToken[] }>("GET", "/tokens").then((r) => r.tokens);

export const createToken = (t: TokenRequest) =>
  request<CreatedToken>("POST", "/tokens", {
    name: t.name,
    scopes: t.scopes,
    sites: t.sites,
    expiresInDays: t.unlimited ? 0 : t.expiresInDays,
    unlimited: t.unlimited,
  });

export const revokeToken = (id: number) => request<void>("DELETE", "/tokens/" + id);

export type TokenState = "active" | "expires_soon" | "expired" | "revoked";

const soon = 30 * 24 * 60 * 60 * 1000;

/** tokenState says whether a token works at now, in milliseconds. */
export function tokenState(t: APIToken, now: number): TokenState {
  if (t.revokedAt) return "revoked";
  if (t.expiresAt === null) return "active";
  const left = Date.parse(t.expiresAt) - now;
  if (left <= 0) return "expired";
  return left <= soon ? "expires_soon" : "active";
}

export function tokenMessage(err: unknown): string {
  if (err instanceof ApiError) {
    switch (err.code) {
      case "too_many_tokens":
        return "You have 100 working API tokens, the most there can be. Revoke one first.";
      case "second_factor_required":
        return "Set up a second factor (TOTP or a passkey) before you create a token.";
      case "not_found":
        return "This token does not exist any more. Reload the page.";
      case "invalid_argument":
        // "auth: invalid IP allowlist: …" -> "…"
        return err.message.replace(/^auth: [^:]+: /, "");
      case "forbidden":
        return "Only admins can change this.";
    }
    return err.message;
  }
  return "The server is not reachable.";
}

// The addresses API tokens may be used from (ADR-0018). It is one list for
// the whole panel, kept by admins; an empty list accepts tokens from
// anywhere. Browser logins are not affected.
export const getTokenAllowlist = () => request<{ entries: string[] }>("GET", "/settings/api-ip-allowlist").then((r) => r.entries);

export const setTokenAllowlist = (entries: string[]) =>
  request<{ entries: string[] }>("PUT", "/settings/api-ip-allowlist", { entries }).then((r) => r.entries);

function ipv4(s: string): number | null {
  const parts = s.split(".");
  if (parts.length !== 4) return null;
  let n = 0;
  for (const p of parts) {
    if (!/^\d{1,3}$/.test(p) || Number(p) > 255) return null;
    n = n * 256 + Number(p);
  }
  return n;
}

/**
 * allowsLoopback reports whether an allowlist lets the hostashi command on
 * the server itself through: it talks to http://127.0.0.1:8080 by default.
 * An empty list allows everything.
 */
export function allowsLoopback(entries: string[]): boolean {
  if (entries.length === 0) return true;
  const lo = ipv4("127.0.0.1")!;
  return entries.some((e) => {
    const [addr = "", bits = "32"] = e.split("/");
    const base = ipv4(addr);
    const n = Number(bits);
    if (base === null || !Number.isInteger(n) || n < 0 || n > 32) return false;
    const size = 2 ** (32 - n);
    return Math.floor(base / size) === Math.floor(lo / size);
  });
}
