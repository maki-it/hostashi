// SPDX-License-Identifier: AGPL-3.0-or-later

// Changing the domain of a site (ADR-0068): the plan the "Change primary
// domain" form shows before it saves.

import type { Site } from "./sites";

/** What the form asks for. */
export type PrimaryChange = {
  primary: string; // the new primary domain, lower-cased
  withWWW: boolean; // also serve www.<primary>
  redirectOld: boolean; // keep the old names as redirect domains
};

/** The served and redirect domains after the change. */
export type DomainPlan = { domains: string[]; redirectDomains: string[] };

const partnerOf = (d: string) => (d.startsWith("www.") ? d.slice(4) : "www." + d);

// oldNames are the old primary domain and its www partner if the site
// serves it.
export function oldNames(site: Site): string[] {
  const primary = site.domains[0];
  if (!primary) return [];
  const partner = partnerOf(primary);
  return site.domains.includes(partner) ? [primary, partner] : [primary];
}

// hadWWW: the old primary domain has its www partner on the site, so the
// form presets "also www".
export const hadWWW = (site: Site) => oldNames(site).length === 2;

/**
 * planPrimaryChange moves the site to a new primary domain: the new name
 * (and its www name) first, the other served domains stay, the old primary
 * domain and its www partner become redirect domains or go away. Redirect
 * domains the site already has stay, except names that are served now.
 */
export function planPrimaryChange(site: Site, c: PrimaryChange): DomainPlan {
  const primary = c.primary.trim().toLowerCase();
  const fresh = c.withWWW && !primary.startsWith("www.") ? [primary, "www." + primary] : [primary];
  const old = oldNames(site);
  const rest = site.domains.filter((d) => !old.includes(d) && !fresh.includes(d));
  const domains = [...fresh, ...rest];
  const redirect = [...(site.redirectDomains ?? []), ...(c.redirectOld ? old : [])].filter(
    (d, i, all) => !domains.includes(d) && all.indexOf(d) === i,
  );
  return { domains, redirectDomains: redirect };
}

/** The names a plan adds to the certificate. */
export function newNames(site: Site, p: DomainPlan): string[] {
  const now = new Set([...site.domains, ...(site.redirectDomains ?? [])]);
  return [...p.domains, ...p.redirectDomains].filter((d) => !now.has(d));
}

/**
 * waitsForCertificate says whether the panel will hold the change until a
 * certificate for the new names is there: the site is live on HTTPS from
 * Let's Encrypt and its certificate lacks a name.
 */
export function waitsForCertificate(site: Site, p: DomainPlan): boolean {
  if (!site.tls.enabled || !site.tls.active || site.tls.source !== "letsencrypt") return false;
  return uncovered(site, [...p.domains, ...p.redirectDomains]).length > 0;
}

// uncovered returns the names the site's live certificate does not name,
// wildcards included.
export function uncovered(site: Site, names: string[]): string[] {
  const covered = (d: string) =>
    site.tls.domains.some((c) => c === d || (c.startsWith("*.") && d.endsWith(c.slice(1)) && !d.slice(0, -c.length + 1).includes(".")));
  return names.filter((d) => !covered(d));
}

/** The WP-CLI command that replaces the old address in a WordPress database. */
export function wpSearchReplace(site: Site, primary: string): string {
  const scheme = site.tls.enabled ? "https" : "http";
  const old = site.domains[0] ?? "old.example.com";
  return `wp search-replace '${scheme}://${old}' '${scheme}://${primary}' --all-tables`;
}
