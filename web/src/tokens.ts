// SPDX-License-Identifier: AGPL-3.0-or-later

// API tokens of the logged-in account (ADR-0018, ADR-0035). Creating,
// listing and revoking them needs a browser session; a token cannot do it.

import { ApiError, request } from "./api";
import { areaErrorText } from "./i18n";

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

/** tokenMessage explains an error of the token forms in the current language. */
export function tokenMessage(err: unknown): string {
  // "auth: invalid IP allowlist: …" -> "…", the server's English reason.
  if (err instanceof ApiError && err.code === "invalid_argument") return err.message.replace(/^auth: [^:]+: /, "");
  return areaErrorText("tokens.errors", err);
}

// The addresses API tokens may be used from (ADR-0018). It is one list for
// the whole panel, kept by admins; an empty list accepts tokens from
// anywhere. 127.0.0.1 and ::1 are always allowed. Browser logins are not
// affected.
export const getTokenAllowlist = () => request<{ entries: string[] }>("GET", "/settings/api-ip-allowlist").then((r) => r.entries);

export const setTokenAllowlist = (entries: string[]) =>
  request<{ entries: string[] }>("PUT", "/settings/api-ip-allowlist", { entries }).then((r) => r.entries);
