// SPDX-License-Identifier: AGPL-3.0-or-later

// fail2ban's bans in the panel (ADR-0078): the Hostashi jails, the addresses
// they block, and unblocking one address in every jail at once.

import { request } from "./api";
import i18n, { areaErrorText } from "./i18n";

export type Fail2banJail = {
  name: string;
  running: boolean;
  currentlyFailed: number;
  totalFailed: number;
  currentlyBanned: number;
  totalBanned: number;
};

// A blocked address with every jail that blocks it: unblocking lifts all.
export type BlockedAddress = { address: string; jails: string[]; since: string; until: string | null };

// One page of the blocked addresses, newest first. The server filters and
// pages, so thousands of blocked addresses never reach the browser at once.
export type Fail2banStatus = {
  jails: Fail2banJail[];
  addresses: BlockedAddress[];
  total: number;
  page: number;
  perPage: number;
  truncated: boolean;
};

export type BanFilter = { jail: string; address: string; page: number };

export const banPageSize = 50;

export function bansPath(f: BanFilter): string {
  const q = new URLSearchParams();
  if (f.jail) q.set("jail", f.jail);
  if (f.address.trim()) q.set("address", f.address.trim());
  if (f.page > 1) q.set("page", String(f.page));
  const s = q.toString();
  return "/firewall/bans" + (s ? "?" + s : "");
}

export const getBans = (f: BanFilter = { jail: "", address: "", page: 1 }) => request<Fail2banStatus>("GET", bansPath(f));

export const unban = (address: string) =>
  request<{ address: string; jails: string[] }>("DELETE", "/firewall/bans/" + encodeURIComponent(address));

// What each jail protects, in plain words.
export const jailLabels: Record<string, string> = {
  sshd: "SSH logins",
  "hostashi-panel": "Panel logins",
  "hostashi-site-auth": "Password-protected sites",
  recidive: "Repeat offenders",
};

/** jailLabel names a jail in the current language; an unknown jail keeps its name. */
export const jailLabel = (name: string) => (name in jailLabels ? i18n.t(`server.blocked.jails.${name as "sshd"}`) : name);

export const fail2banMessage = (err: unknown): string => areaErrorText("server.blocked.errors", err);
