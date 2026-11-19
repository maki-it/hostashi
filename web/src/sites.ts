// SPDX-License-Identifier: AGPL-3.0-or-later

// Sites: a Linux account with a home, its domains, an optional PHP version
// and HTTPS with a Let's Encrypt certificate. The panel database is the
// desired state; every change is applied to the web server at once.

import { ApiError, request } from "./api";

export type SiteTLS = {
  enabled: boolean; // HTTPS wanted
  active: boolean; // the web server serves the site over HTTPS now
  hsts: boolean;
  domains: string[]; // names in the live certificate
  notAfter: string | null;
  error?: string; // reason of the last failed attempt
  failedAt?: string;
};

export type Site = {
  name: string;
  pool: string;
  state: "creating" | "active" | "deleting" | string;
  php: string; // "" for no PHP
  disabled: boolean; // answers 503, PHP stopped
  domains: string[]; // the first one is the primary domain
  tls: SiteTLS;
  createdAt: string;
  updatedAt: string;
};

export type PHPVersions = { installed: string[]; supported: string[]; legacy: string[] };

export type NewSite = { name: string; php: string; domains: string[]; tls: boolean; hsts: boolean };

const sitePath = (name: string) => "/sites/" + encodeURIComponent(name);

export const listSites = async () => (await request<{ sites: Site[] }>("GET", "/sites")).sites;
export const phpVersions = () => request<PHPVersions>("GET", "/php/versions");
export const createSite = (s: NewSite) => request<Site>("POST", "/sites", s);
export const deleteSite = (name: string) => request<void>("DELETE", sitePath(name));
export const setDomains = (name: string, domains: string[]) => request<Site>("PUT", sitePath(name) + "/domains", { domains });
export const setPHP = (name: string, php: string) => request<Site>("PUT", sitePath(name) + "/php", { php });
export const setTLS = (name: string, enabled: boolean) => request<Site>("PUT", sitePath(name) + "/tls", { enabled });
export const setDisabled = (name: string, disabled: boolean) => request<Site>("PUT", sitePath(name) + "/disabled", { disabled });
export const setHSTS = (name: string, enabled: boolean) => request<Site>("PUT", sitePath(name) + "/hsts", { enabled });

// Mirrors of the agent's rules (internal/agentapi) so a form can say what is
// wrong before a request. The server checks again.
const siteNameRe = /^[a-z][a-z0-9_-]{1,31}$/;
const reservedSiteNames = [
  "root", "daemon", "bin", "sys", "sync", "nobody", "www-data", "nginx", "mysql",
  "mariadb", "hostashi", "sshd", "postgres", "redis", "messagebus",
];
export const maxDomainsPerSite = 50;

export function siteNameProblem(v: string): string {
  if (v === "") return "Enter a name.";
  if (!siteNameRe.test(v)) return "Use 2 to 32 characters: lowercase letters, digits, _ and -, starting with a letter.";
  if (reservedSiteNames.includes(v) || v.startsWith("systemd-")) return "This name is reserved.";
  return "";
}

const labelRe = /^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$/;

export function domainProblem(d: string): string {
  if (d.length < 3 || d.length > 253) return `"${d}" is not a domain.`;
  const labels = d.split(".");
  if (labels.length < 2) return `"${d}" needs at least two parts, like example.com.`;
  if (!labels.every((l) => labelRe.test(l))) return `"${d}" is not a domain. Use letters, digits and inner hyphens; write other characters in their xn-- form.`;
  if (/^[0-9]+$/.test(labels[labels.length - 1] ?? "")) return `"${d}" is not a domain. IP addresses are not allowed.`;
  return "";
}

// parseDomains splits a text field into domains: one per line or separated
// by spaces or commas, lower-cased. The order is kept.
export const parseDomains = (text: string) =>
  text
    .split(/[\s,]+/)
    .map((d) => d.trim().toLowerCase())
    .filter((d) => d !== "");

export function domainsProblem(domains: string[]): string {
  if (domains.length === 0) return "Enter at least one domain.";
  if (domains.length > maxDomainsPerSite) return `A site can have at most ${maxDomainsPerSite} domains.`;
  const seen = new Set<string>();
  for (const d of domains) {
    const p = domainProblem(d);
    if (p) return p;
    if (seen.has(d)) return `"${d}" is listed twice.`;
    seen.add(d);
  }
  return "";
}

// httpsLabel says in a few words where HTTPS of a site stands.
export function httpsLabel(t: SiteTLS): string {
  if (!t.enabled) return "HTTP only";
  if (t.active) return "HTTPS";
  if (t.error) return "Certificate failed";
  return "Waiting for certificate";
}

// siteMessage turns an API error into a sentence.
export function siteMessage(err: unknown): string {
  if (err instanceof ApiError) {
    switch (err.code) {
      case "site_exists":
        return "A site with this name exists. Choose another name.";
      case "domain_taken":
        return "A domain belongs to another site. Remove it there first.";
      case "tls_unavailable":
        return "Certificates are not available on this panel. Start it with a public address to use Let's Encrypt.";
      case "site_busy":
        return "The site is being created or deleted. Reload the page in a moment.";
      case "not_found":
        return "This site does not exist any more. Reload the page.";
      case "forbidden":
        return "Only administrators can manage sites.";
      case "agent_timeout":
        return "The server did not answer in time. Reload the page to see whether the change was made.";
      case "agent_unavailable":
        return "The change could not be completed. Try again.";
      default:
        return err.message ? err.message.charAt(0).toUpperCase() + err.message.slice(1) + "." : "Something went wrong (" + err.code + ").";
    }
  }
  if (err instanceof TypeError) return "The server is not reachable.";
  return "Something went wrong. Try again.";
}
