// SPDX-License-Identifier: AGPL-3.0-or-later

// fail2ban's bans in the panel (ADR-0078): the Hostashi jails, the addresses
// they block, and unblocking one address in every jail at once.

import { ApiError, request } from "./api";

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

export const jailLabel = (name: string) => jailLabels[name] ?? name;

export function fail2banMessage(err: unknown): string {
  if (err instanceof ApiError) {
    switch (err.code) {
      case "forbidden":
        return "Only administrators can see and unblock addresses.";
      case "fail2ban_unavailable":
        return "fail2ban does not answer, so nothing is blocked right now. Check the service under Services.";
      case "invalid_argument":
        return "Enter an IP address, a part of one, or a range such as 203.0.113.0/24.";
      case "not_banned":
        return "This address is no longer blocked.";
      case "agent_timeout":
      case "agent_unavailable":
        return "fail2ban could not be reached. Try again.";
      default:
        return err.message ? err.message.charAt(0).toUpperCase() + err.message.slice(1) + "." : "Something went wrong (" + err.code + ").";
    }
  }
  if (err instanceof TypeError) return "The server is not reachable.";
  return "Something went wrong. Try again.";
}
