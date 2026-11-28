// SPDX-License-Identifier: AGPL-3.0-or-later

// DNS accounts for DNS-01 certificates (ADR-0076). Secret fields are
// write-only: the API never returns them, only the fields that are no
// secret under settings.
import { request } from "./api";
import { areaErrorText } from "./i18n";

export type DNSField = { name: string; label: string; secret: boolean; optional: boolean };
export type DNSProvider = { id: string; name: string; fields: DNSField[]; hint: string };

export type DNSAccount = {
  id: number;
  label: string;
  provider: string;
  zones: string[];
  settings: Record<string, string>;
  state: "ok" | "error" | "unchecked";
  lastError?: string;
  checkedAt?: string;
  createdAt: string;
  updatedAt: string;
};

export type NewDNSAccount = { label: string; provider: string; zones: string[]; credentials: Record<string, string> };
export type DNSAccountUpdate = { label: string; zones: string[]; credentials?: Record<string, string> };

export const dnsProviders = async () => (await request<{ providers: DNSProvider[] }>("GET", "/dns-providers")).providers;
export const dnsAccounts = async () => (await request<{ accounts: DNSAccount[] }>("GET", "/dns-accounts")).accounts;
export const addDNSAccount = (a: NewDNSAccount) => request<DNSAccount>("POST", "/dns-accounts", a);
export const updateDNSAccount = (id: number, u: DNSAccountUpdate) => request<DNSAccount>("PUT", `/dns-accounts/${id}`, u);
export const deleteDNSAccount = (id: number) => request<void>("DELETE", `/dns-accounts/${id}`);
export const checkDNSAccount = (id: number) => request<DNSAccount>("POST", `/dns-accounts/${id}/check`);

export function dnsMessage(err: unknown): string {
  return areaErrorText("dnsAccounts.errors", err);
}

/**
 * parseZones splits the zone field: one zone per line or separated by
 * commas or spaces, lower case, without a trailing dot, each once.
 */
export function parseZones(text: string): string[] {
  const out: string[] = [];
  for (const z of text.split(/[\s,]+/)) {
    const v = z.trim().toLowerCase().replace(/\.$/, "");
    if (v !== "" && !out.includes(v)) out.push(v);
  }
  return out;
}

// zoneRe is a zone as the server accepts it: two or more labels of
// letters, digits, inner hyphens and underscores.
const zoneRe = /^([a-z0-9_]([a-z0-9_-]{0,61}[a-z0-9_])?\.)+[a-z0-9_]([a-z0-9_-]{0,61}[a-z0-9_])?$/;

/** badZone returns the first zone the server would refuse, "" if none. */
export const badZone = (zones: string[]) => zones.find((z) => !zoneRe.test(z)) ?? "";

/** providerName is the display name of a provider id. */
export const providerName = (providers: DNSProvider[], id: string) => providers.find((p) => p.id === id)?.name ?? id;
