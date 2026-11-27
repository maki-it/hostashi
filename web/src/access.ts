// SPDX-License-Identifier: AGPL-3.0-or-later

// Password protection and IP rules of a site (ADR-0066): types, API calls
// and the form model of the "Access" tab.

import { ApiError, request } from "./api";
import i18n from "./i18n";
import { splitRanges } from "./realip";

export type IPMode = "" | "allow" | "deny";

export type AccessSettings = {
  ipMode: IPMode;
  ips: string[];
  password: boolean;
  paths: string[];
  bypass: string[];
};

export type AccessUser = { name: string; createdAt: string; updatedAt: string };

export type AccessView = AccessSettings & {
  users: AccessUser[];
  limits: { ips: number; paths: number; bypass: number; users: number; minPassword: number; maxPassword: number };
  clientAddress: string;
  updatedAt?: string;
};

const base = (site: string) => `/sites/${encodeURIComponent(site)}/access`;

export function getAccess(site: string): Promise<AccessView> {
  return request<AccessView>("GET", base(site));
}

export function setAccess(site: string, a: AccessSettings): Promise<AccessView> {
  return request<AccessView>("PUT", base(site), a);
}

// addAccessUser returns the generated password when none was given.
export function addAccessUser(site: string, name: string, password: string): Promise<{ name: string; password?: string }> {
  return request("POST", `${base(site)}/users`, password ? { name, password } : { name });
}

export function setAccessUserPassword(site: string, name: string, password: string): Promise<{ name: string; password?: string }> {
  return request("PUT", `${base(site)}/users/${encodeURIComponent(name)}`, password ? { password } : {});
}

export function deleteAccessUser(site: string, name: string): Promise<void> {
  return request("DELETE", `${base(site)}/users/${encodeURIComponent(name)}`);
}

export type AccessForm = {
  ipMode: IPMode;
  ips: string; // one per line
  password: boolean;
  scope: "site" | "paths";
  paths: string; // one per line
  bypass: string; // one per line
};

export function toAccessForm(a: AccessSettings): AccessForm {
  return {
    ipMode: a.ipMode,
    ips: a.ips.join("\n"),
    password: a.password,
    scope: a.paths.length > 0 ? "paths" : "site",
    paths: a.paths.join("\n"),
    bypass: a.bypass.join("\n"),
  };
}

// splitPaths takes one path per line or separated by spaces or commas.
export function splitPaths(text: string): string[] {
  return text
    .split(/[\s,]+/)
    .map((s) => s.trim())
    .filter((s) => s !== "");
}

// fromAccessForm builds the request body; it returns an error text for a
// form the server would refuse anyway. Paths and bypass addresses are kept
// while the password is off, so switching it on again restores them.
export function fromAccessForm(f: AccessForm, users: number): { settings: AccessSettings; error?: string } {
  const ips = f.ipMode ? splitRanges(f.ips) : [];
  const paths = f.scope === "paths" ? splitPaths(f.paths) : [];
  const settings: AccessSettings = { ipMode: f.ipMode, ips, password: f.password, paths, bypass: splitRanges(f.bypass) };
  if (f.ipMode && ips.length === 0) return { settings, error: i18n.t("siteAccess.problems.noAddress") };
  if (f.password && users === 0) return { settings, error: i18n.t("siteAccess.problems.noUser") };
  if (f.password && f.scope === "paths" && paths.length === 0) return { settings, error: i18n.t("siteAccess.problems.noPath") };
  const bad = paths.find((p) => !p.startsWith("/"));
  if (bad) return { settings, error: i18n.t("siteAccess.problems.badPath", { path: bad }) };
  return { settings };
}

// lockout reports whether an "only these addresses" rule leaves out the
// caller's own address. It only compares exact addresses; a range is left
// to the server, so the warning may miss a range that does not hold it.
export function lockout(f: AccessForm, client: string): boolean {
  if (f.ipMode !== "allow" || !client) return false;
  const list = splitRanges(f.ips);
  if (list.length === 0) return false;
  return !list.some((e) => e === client || e === `${client}/32` || e === `${client}/128` || inRange4(client, e));
}

function ip4(s: string): number | null {
  const m = /^(\d{1,3})\.(\d{1,3})\.(\d{1,3})\.(\d{1,3})$/.exec(s);
  if (!m) return null;
  const [a = 0, b = 0, c = 0, d = 0] = m.slice(1).map(Number);
  if ([a, b, c, d].some((x) => x > 255)) return null;
  return ((a << 24) >>> 0) + (b << 16) + (c << 8) + d;
}

function inRange4(addr: string, range: string): boolean {
  const [net = "", bits] = range.split("/");
  const a = ip4(addr);
  const n = ip4(net);
  const b = Number(bits);
  if (a === null || n === null || bits === undefined || !Number.isInteger(b) || b < 0 || b > 32) return false;
  const mask = b === 0 ? 0 : (~0 << (32 - b)) >>> 0;
  return ((a & mask) >>> 0) === ((n & mask) >>> 0);
}

export function accessMessage(err: unknown): string {
  if (err instanceof ApiError) {
    if (err.code === "access_rejected") return i18n.t("siteAccess.errors.rejected", { reason: err.message });
    if (err.code === "access_conflict")
      return i18n.t("siteAccess.errors.conflict", { reason: err.message });
    if (err.code === "user_exists") return i18n.t("siteAccess.errors.userExists");
    if (err.code === "user_not_found") return i18n.t("siteAccess.errors.userNotFound");
    if (err.code === "not_found") return i18n.t("siteAccess.errors.siteGone");
    return err.message;
  }
  return i18n.t("siteAccess.errors.generic");
}
