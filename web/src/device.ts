// SPDX-License-Identifier: AGPL-3.0-or-later

// Browser login for the CLI (ADR-0058 addendum 1): the panel approves the
// code that "hostashi login" shows.

import { ApiError, request } from "./api";
import { areaErrorText } from "./i18n";

export type DeviceLogin = {
  userCode: string;
  client: string;
  ip: string;
  createdAt: string;
  expiresAt: string;
};

export type Scope = { name: string; description: string; highRisk: boolean };

export const getDeviceLogin = (code: string) => request<DeviceLogin>("GET", "/auth/device/" + encodeURIComponent(code));

/** sites null is every site the account can reach. */
export const approveDeviceLogin = (userCode: string, approve: boolean, scopes: string[] = [], sites: string[] | null = null, expiresInDays = 90) =>
  request<void>("POST", "/auth/device/approve", { userCode, approve, scopes, sites, expiresInDays });

export const listScopes = () => request<{ scopes: Scope[] }>("GET", "/scopes").then((r) => r.scopes);

/** normalizeCode upper-cases a typed code and puts the dash in: wdjbmjht -> WDJB-MJHT. */
export function normalizeCode(s: string): string {
  const c = s.toUpperCase().replace(/[\s-]/g, "");
  return c.length === 8 ? c.slice(0, 4) + "-" + c.slice(4) : c;
}

/** deviceMessage explains an error of the device login in the current language. */
export function deviceMessage(err: unknown): string {
  if (err instanceof ApiError && err.code === "invalid_argument") return err.message;
  return areaErrorText("tokens.device.errors", err);
}
