// SPDX-License-Identifier: AGPL-3.0-or-later

// Sites: a Linux account with a home, its domains, an optional PHP version
// and HTTPS with a Let's Encrypt certificate. The panel database is the
// desired state; every change is applied to the web server at once.

import type { NodeApp } from "./node";
import { ApiError, request } from "./api";
import { formatBytes } from "./limits";

export type SiteTLS = {
  enabled: boolean; // HTTPS wanted
  active: boolean; // the web server serves the site over HTTPS now
  hsts: boolean;
  domains: string[]; // names in the live certificate
  notAfter: string | null;
  error?: string; // reason of the last failed attempt
  failedAt?: string;
  // Where the certificate comes from, and where the live one came from
  // ("" for none). trusted is false for an own certificate without a
  // public chain (ADR-0039). Older answers lack them.
  source?: "letsencrypt" | "custom";
  certSource?: "" | "letsencrypt" | "custom";
  trusted?: boolean;
  renewAt?: string;
  renewPending?: boolean;
};

export type Site = {
  name: string;
  pool: string;
  state: "creating" | "active" | "deleting" | string;
  // static, php, node, app, proxy or redirect (ADR-0054). Older answers lack it.
  type?: SiteType;
  php: string; // "" for no PHP
  // Document root folder below htdocs and the static-site options (ADR-0054).
  web?: SiteWeb;
  // false for a redirect site, which has no Linux account and no files.
  // Older answers lack it.
  account?: boolean;
  // The target of a redirect site, null otherwise (ADR-0054).
  redirect?: SiteRedirect | null;
  // The target of a reverse proxy site, null otherwise (ADR-0054).
  proxy?: SiteProxy | null;
  // The app of a Node.js site, null otherwise (ADR-0054).
  node?: NodeApp | null;
  phpPrograms: boolean; // PHP may start programs (exec, system, ...)
  disabled: boolean; // answers 503, PHP stopped
  disabledPage: string; // maintenance page shown while disabled (ADR-0047)
  maintenance: SiteMaintenance;
  domains: string[]; // the first one is the primary domain
  tls: SiteTLS;
  createdAt: string;
  updatedAt: string;
};

export type SiteType = "static" | "php" | "node" | "app" | "proxy" | "redirect";

/**
 * dir: folder below htdocs, "" for htdocs itself. spa: static sites only;
 * assetCache: static and PHP sites. assetCacheSeconds and assetCacheExts are the effective values
 * (the defaults when the site keeps them); older answers lack them.
 */
export type SiteWeb = { dir: string; spa: boolean; assetCache: boolean; assetCacheSeconds?: number; assetCacheExts?: string[] };

// Mirror of agentapi's asset cache bounds and defaults (ADR-0054).
export const assetCacheDefaults = {
  seconds: 30 * 24 * 3600,
  exts: ["css", "js", "mjs", "woff", "woff2", "ttf", "otf", "eot", "svg", "svgz", "png", "jpg", "jpeg", "gif", "webp", "avif", "ico", "mp4", "webm", "ogg", "ogv"],
};
export const assetCacheUnits = [
  { value: 60, label: "minutes" },
  { value: 3600, label: "hours" },
  { value: 86400, label: "days" },
] as const;

/** Splits a list of file types typed with spaces, commas or dots. */
export const parseExts = (v: string): string[] =>
  v
    .toLowerCase()
    .split(/[\s,;]+/)
    .map((e) => e.replace(/^\*?\./, ""))
    .filter((e) => e !== "");

export function assetCacheProblem(seconds: number, exts: string[]): string {
  if (!Number.isInteger(seconds) || seconds < 60 || seconds > 365 * 86400) return "Choose a time between 1 minute and 365 days.";
  if (exts.length === 0) return "Enter at least one file type.";
  if (exts.length > 40) return "Enter at most 40 file types.";
  const bad = exts.find((e) => !/^[a-z0-9]{1,10}$/.test(e));
  if (bad !== undefined) return `"${bad}" is not a file type. Use 1 to 10 letters or digits, without the dot.`;
  const dup = exts.find((e, i) => exts.indexOf(e) !== i);
  if (dup !== undefined) return `"${dup}" is listed twice.`;
  return "";
}

/** The largest unit that divides the time evenly. */
export function splitSeconds(seconds: number): { amount: number; unit: number } {
  for (const u of [86400, 3600, 60]) if (seconds % u === 0) return { amount: seconds / u, unit: u };
  return { amount: Math.max(1, Math.round(seconds / 60)), unit: 60 };
}

/** The type of a site; answers without the field follow the PHP version. */
export const siteType = (s: Pick<Site, "type" | "php">): SiteType => s.type ?? (s.php ? "php" : "static");

/** The type as the panel shows it, with the PHP version. */
export function siteTypeLabel(s: Pick<Site, "type" | "php">): string {
  switch (siteType(s)) {
    case "static":
      return "Static";
    case "php":
      return s.php ? `PHP ${s.php}` : "PHP";
    case "node":
      return "Node.js";
    case "app":
      return "App";
    case "proxy":
      return "Reverse proxy";
    case "redirect":
      return "Redirect";
  }
}

// Mirror of agentapi.ValidateWebDir: up to 4 folder names of letters,
// digits, ".", "_" and "-", separated by "/". The server checks again.
const webDirSegment = /^[A-Za-z0-9._-]{1,64}$/;

export function webDirProblem(dir: string): string {
  if (dir === "") return "";
  const segs = dir.split("/");
  if (dir.length > 255 || segs.length > 4) return "Use at most 4 folder levels.";
  if (segs.some((seg) => seg === "." || seg === ".." || !webDirSegment.test(seg)))
    return "Use folder names of letters, digits, '.', '_' and '-', separated by '/', without a leading or trailing '/'.";
  return "";
}

// SiteMaintenance: while enabled, addresses outside allowIps get the page
// with 503; the allowed ones see the site. PHP keeps running.
export type SiteMaintenance = { enabled: boolean; page: string; allowIps: string[] };

export type PHPVersions = {
  installed: string[];
  supported: string[];
  legacy: string[];
  // End of security fixes and of active support per version (ADR-0036), YYYY-MM-DD.
  securityUntil?: Record<string, string>;
  activeUntil?: Record<string, string>;
  // The version the distribution ships, "" on an unknown distribution.
  distro?: string;
};

export type NewSite = {
  name: string;
  pool?: string;
  php: string;
  domains: string[];
  tls: boolean;
  hsts: boolean;
  type?: "redirect" | "proxy" | "node";
  redirect?: SiteRedirect;
  proxy?: SiteProxy;
  node?: Pick<NodeApp, "version" | "workdir" | "startKind" | "start">;
};

/** The target of a reverse proxy site (ADR-0054). */
export type SiteProxy = { target: string; targetHost: boolean; insecure: boolean };

/** The target of a redirect site (ADR-0054). */
export type SiteRedirect = { target: string; status: number; keepPath: boolean };

export const redirectStatuses = [
  { value: 301, label: "301 Moved permanently" },
  { value: 302, label: "302 Found (temporary)" },
  { value: 307, label: "307 Temporary, keeps the method" },
  { value: 308, label: "308 Permanent, keeps the method" },
];

/**
 * proxyProblem mirrors agentapi.ParseProxyTarget: the rules of a redirect
 * target, and no address of this server (loopback, link-local such as the
 * cloud metadata service, 0.0.0.0, multicast) or .localhost name. The
 * server also checks the addresses a name resolves to.
 */
export function proxyProblem(target: string, own: string[]): string {
  const problem = redirectProblem(target, own);
  if (problem) return problem.replace("redirect to", "pass requests to");
  const host = new URL(target).hostname.replace(/^\[|\]$/g, "").toLowerCase();
  if (host === "localhost" || host.endsWith(".localhost")) return "The address points to this server.";
  const v4 = /^(\d+)\.(\d+)\.(\d+)\.(\d+)$/.exec(host);
  if (v4) {
    const [a, b] = [Number(v4[1]), Number(v4[2])];
    if (a === 127 || a === 0) return "The address points to this server.";
    if (a === 169 && b === 254) return "Link-local addresses (such as the cloud metadata service) are not allowed.";
    if (a >= 224) return "Multicast and broadcast addresses are not allowed.";
  }
  if (host.includes(":")) {
    if (host === "::1" || host === "::" || host.startsWith("::ffff:")) return "The address points to this server.";
    if (/^fe[89ab]/.test(host)) return "Link-local addresses are not allowed.";
    if (host.startsWith("ff")) return "Multicast addresses are not allowed.";
  }
  return "";
}

/** hasAccount: the site has a Linux account with files. */
export const hasAccount = (s: Pick<Site, "account">) => s.account !== false;

/**
 * redirectProblem mirrors agentapi.ParseRedirectTarget: http(s), a host,
 * an optional port and path, no user, query or fragment, not one of the
 * site's own domains.
 */
export function redirectProblem(target: string, own: string[]): string {
  if (!target) return "Enter the address to redirect to.";
  if (target.length > 2048) return "The address is too long.";
  if (!/^https?:\/\//.test(target)) return "The address must start with https:// or http://.";
  if (!/^https?:\/\/[A-Za-z0-9.:[\]-]+(\/[A-Za-z0-9._~%!*+,=:@&()-]*)*$/.test(target))
    return "Use a host and a path of letters, digits and - . _ ~ % ! * + , = : @ & ( ) / only, without ? or #.";
  let host = "";
  try {
    host = new URL(target).hostname;
  } catch {
    return "This is not a valid address.";
  }
  if (!host) return "The address needs a host.";
  if (/[A-Z]/.test(target.split("/")[2] ?? "")) return "Write the host in lower case.";
  if (own.includes(host)) return "The address points to the site itself.";
  return "";
}

// Pool is a storage pool for site homes (ADR-0016). Root adds pools on the
// server (hostashi pool add); the panel only chooses one for a new site.
export type Pool = {
  name: string;
  path: string;
  sites: number;
  // Mounted with its marker; only then are quota and the sizes set.
  available: boolean;
  quota?: "off" | "counting" | "enforced";
  freeBytes?: number;
  totalBytes?: number;
};

export const listPools = () => request<{ pools: Pool[] }>("GET", "/pools").then((r) => r.pools);

/** The text of a pool in the choice: name, path and free space. */
export function poolLabel(p: Pool): string {
  const base = `${p.name} (${p.path})`;
  if (!p.available) return `${base}, not available`;
  if (p.freeBytes === undefined) return base;
  return `${base}, ${formatBytes(p.freeBytes)} free`;
}

/** What to know before putting a site on the pool, or "". */
export function poolNote(p: Pool | undefined): string {
  if (!p) return "";
  if (!p.available) return "The volume of this pool is not mounted. Sites cannot be created there until it is.";
  if (p.quota === "off") return "This pool has no disk quota: sites on it get no disk limit.";
  if (p.quota === "counting") return "Disk limits on this pool are not enforced. See Health.";
  return "";
}

const sitePath = (name: string) => "/sites/" + encodeURIComponent(name);

export const listSites = async () => (await request<{ sites: Site[] }>("GET", "/sites")).sites;
export const phpVersions = () => request<PHPVersions>("GET", "/php/versions");
export const createSite = (s: NewSite) => request<Site>("POST", "/sites", s);
export const deleteSite = (name: string) => request<void>("DELETE", sitePath(name));

/** Deletes the site and parks the chosen domains of it (ADR-0048). */
export type ParkSite = { domains: string[]; page: string; tls: boolean; confirm: string };
export const deleteAndPark = (name: string, p: ParkSite) => request<void>("POST", sitePath(name) + "/park", p);

/** The address of the New site form, filled in with a parked domain (ADR-0048). */
export const newSiteHash = (domain: string, tls: boolean) =>
  "#/sites?" + new URLSearchParams({ new: "1", domain, tls: tls ? "1" : "0" }).toString();
export const setDomains = (name: string, domains: string[]) => request<Site>("PUT", sitePath(name) + "/domains", { domains });
export const setPHP = (name: string, php: string) => request<Site>("PUT", sitePath(name) + "/php", { php });
export const setRedirect = (name: string, r: SiteRedirect) => request<Site>("PUT", sitePath(name) + "/redirect", r);
export const setProxy = (name: string, p: SiteProxy) => request<Site>("PUT", sitePath(name) + "/proxy", p);
export const setWebOptions = (name: string, w: SiteWeb) => request<Site>("PUT", sitePath(name) + "/web", w);
export const setTLS = (name: string, enabled: boolean) => request<Site>("PUT", sitePath(name) + "/tls", { enabled });
// setDisabled switches a site off or on; page is the maintenance page a
// disabled site shows (empty: the built-in page), only sent when disabling.
export const setDisabled = (name: string, disabled: boolean, page?: string) =>
  request<Site>("PUT", sitePath(name) + "/disabled", disabled && page ? { disabled, page } : { disabled });
export const setHSTS = (name: string, enabled: boolean) => request<Site>("PUT", sitePath(name) + "/hsts", { enabled });
export const setPHPPrograms = (name: string, enabled: boolean) =>
  request<Site>("PUT", sitePath(name) + "/php-programs", { enabled });

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
      case "domain_reserved":
        return "A domain is the panel's own address. Choose another domain.";
      case "tls_unavailable":
        return "Certificates are not available on this panel. Start it with a public address to use Let's Encrypt.";
      case "renew_too_soon":
        return "The certificate was renewed less than 10 minutes ago. Try again later.";
      case "certificate_in_use":
        return "Turn HTTPS off before deleting the certificate.";
      case "not_letsencrypt":
        return "Only a certificate from Let's Encrypt can be renewed here. Upload a new own certificate instead.";
      case "node_missing":
        return "This Node.js version is not installed. An administrator installs it under Node.js versions.";
      case "apps_unavailable":
        return "Node.js apps are not available on this panel.";
      case "wrong_site_type":
        return "This is not possible for this type of site.";
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

export type SiteStatus = { tone: "ok" | "warn" | "err" | "off"; word: string; key: "active" | "creating" | "deleting" | "disabled" | "maintenance" };

// siteStatus is the state of a site as a badge: a word and a tone.
export function siteStatus(s: Site): SiteStatus {
  if (s.state === "deleting") return { tone: "off", word: "Being deleted", key: "deleting" };
  if (s.state !== "active") return { tone: "warn", word: "Being created", key: "creating" };
  if (s.disabled) return { tone: "off", word: "Disabled", key: "disabled" };
  if (s.maintenance.enabled) return { tone: "warn", word: "Maintenance", key: "maintenance" };
  return { tone: "ok", word: "Active", key: "active" };
}

// httpsTone is the badge tone for httpsLabel.
export function httpsTone(t: SiteTLS): "ok" | "warn" | "err" | "off" {
  if (!t.enabled) return "off";
  if (t.active) return "ok";
  return t.error ? "err" : "warn";
}

// PHPOptionsOrder lists installed versions newest first and keeps a current
// version that is no longer installed (ADR-0037: newest first).
export function phpChoices(php: PHPVersions | null, current = ""): string[] {
  const installed = [...(php?.installed ?? [])].sort(compareVersions).reverse();
  if (current && !installed.includes(current)) {
    installed.push(current);
    installed.sort(compareVersions).reverse();
  }
  return installed;
}

// compareVersions orders "8.4" < "8.10" numerically.
export function compareVersions(a: string, b: string): number {
  const pa = a.split(".").map(Number);
  const pb = b.split(".").map(Number);
  for (let i = 0; i < Math.max(pa.length, pb.length); i++) {
    const d = (pa[i] ?? 0) - (pb[i] ?? 0);
    if (d !== 0) return d;
  }
  return 0;
}
