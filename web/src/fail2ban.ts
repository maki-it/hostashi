// SPDX-License-Identifier: AGPL-3.0-or-later

// fail2ban's bans in the panel (ADR-0078): the Hostashi jails, the addresses
// they block, and unblocking one address in every jail at once.

import { ApiError, request } from "./api";

export type Fail2banBan = { address: string; since: string; until: string | null };

export type Fail2banJail = {
  name: string;
  running: boolean;
  currentlyFailed: number;
  totalFailed: number;
  currentlyBanned: number;
  totalBanned: number;
  bans: Fail2banBan[];
};

export type Fail2banStatus = { jails: Fail2banJail[] };

export const getBans = () => request<Fail2banStatus>("GET", "/firewall/bans");

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

// A blocked address with every jail that blocks it: unblocking lifts all.
export type BlockedAddress = { address: string; jails: string[]; since: string; until: string | null };

export function blockedAddresses(st: Fail2banStatus): BlockedAddress[] {
  const by = new Map<string, BlockedAddress>();
  for (const j of st.jails) {
    for (const b of j.bans) {
      const cur = by.get(b.address);
      if (!cur) {
        by.set(b.address, { address: b.address, jails: [j.name], since: b.since, until: b.until });
        continue;
      }
      cur.jails.push(j.name);
      if (b.since < cur.since) cur.since = b.since;
      // The address stays blocked until its last ban ends; null is no end.
      if (cur.until !== null && (b.until === null || b.until > cur.until)) cur.until = b.until;
    }
  }
  return [...by.values()].sort((a, b) => (a.since < b.since ? 1 : a.since > b.since ? -1 : 0));
}

export function fail2banMessage(err: unknown): string {
  if (err instanceof ApiError) {
    switch (err.code) {
      case "forbidden":
        return "Only administrators can see and unblock addresses.";
      case "fail2ban_unavailable":
        return "fail2ban does not answer, so nothing is blocked right now. Check the service under Services.";
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
