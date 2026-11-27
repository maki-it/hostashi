// SPDX-License-Identifier: AGPL-3.0-or-later

// Firewall rules from the panel (ADR-0034): opened ports and allowlists for
// SSH and the panel. Every change waits for a confirmation and rolls back
// without one.

import { ApiError, request } from "./api";
import { apiErrorText, areaErrorText } from "./i18n";

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
  // The firewall's own reason (for example "ufw is not installed") says
  // more than the general text, so it wins when there is one.
  if (err instanceof ApiError && err.code === "firewall_unavailable" && err.message.trim() !== "") return apiErrorText(err);
  return areaErrorText("server.firewall.errors", err);
}
