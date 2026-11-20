// SPDX-License-Identifier: AGPL-3.0-or-later

// Firewall rules from the panel (ADR-0034): opened ports and allowlists for
// SSH and the panel. Every change waits for a confirmation and rolls back
// without one.

import { ApiError, request } from "./api";

export type FirewallRule = {
  proto: "tcp" | "udp";
  from: number;
  to?: number;
  sources?: string[];
  note?: string;
};

export type FirewallState = {
  open: FirewallRule[];
  sshAllow: string[];
  panelAllow: string[];
};

export type Firewall = {
  enabled: boolean;
  state: FirewallState;
  pending: { id?: string; deadline: string } | null;
  rolledBack: string | null;
  clientIp: string;
  confirmWithin: number;
};

export const getFirewall = () => request<Firewall>("GET", "/firewall");

export const setFirewall = (state: FirewallState, otherWay: boolean) => request<Firewall>("PUT", "/firewall", { state, otherWay });

export const confirmFirewall = (id: string) => request<Firewall>("POST", "/firewall/confirm", { id });

// parseList splits a textarea into addresses: one per line, or separated by
// commas or spaces. The server checks each one.
export const parseList = (text: string): string[] =>
  text
    .split(/[\s,]+/)
    .map((s) => s.trim())
    .filter((s) => s !== "");

export const sameList = (a: string[], b: string[]) => a.length === b.length && a.every((v, i) => v === b[i]);

export function firewallMessage(err: unknown): string {
  if (err instanceof ApiError) {
    switch (err.code) {
      case "forbidden":
        return "Only administrators can change the firewall.";
      case "not_pending":
        return "The change was no longer pending: it was rolled back. Check the rules and try again.";
      case "firewall_unavailable":
        return err.message ? err.message.charAt(0).toUpperCase() + err.message.slice(1) + "." : "The firewall is not available.";
      case "agent_error":
        return "The firewall could not load the rules. The previous rules stay.";
      case "agent_timeout":
      case "agent_unavailable":
        return "The firewall could not be reached. Try again.";
      default:
        return err.message ? err.message.charAt(0).toUpperCase() + err.message.slice(1) + "." : "Something went wrong (" + err.code + ").";
    }
  }
  if (err instanceof TypeError) return "The server is not reachable.";
  return "Something went wrong. Try again.";
}
