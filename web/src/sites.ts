// SPDX-License-Identifier: AGPL-3.0-or-later

// Sites: a Linux account with a home, its domains, an optional PHP version
// and HTTPS with a Let's Encrypt certificate. The panel database is the
// desired state; every change is applied to the web server at once.

import type { NodeApp, ProgramApp } from "./node";
import type { PythonApp } from "./python";
import { request } from "./api";
import { formatBytes } from "./limits";
import i18n, { areaErrorText } from "./i18n";

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

// SiteWWW is the www redirect of a site (ADR-0065). It acts on pairs only:
// a domain and its www name that are both domains of the site.
export type WWWMode = "off" | "to-www" | "to-non-www";
export type SiteWWW = {
  mode: WWWMode;
  pairs: { bare: string; www: string }[];
  missing: string[]; // names whose adding would make a further pair
};

export type Site = {
  name: string;
  // Display name (ADR-0072), "" for none; name stays the system name
  // (Linux user, home folder, API path). Older answers lack it.
  title?: string;
  pool: string;
  state: "creating" | "active" | "deleting" | string;
  // static, php, node, app, proxy or redirect (ADR-0054), python (ADR-0061). Older answers lack it.
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
  // The program of an app site (ADR-0054 addendum 2), null otherwise.
  app?: ProgramApp | null;
  // The app of a Python site (ADR-0061), null otherwise.
  python?: PythonApp | null;
  phpPrograms: boolean; // PHP may start programs (exec, system, ...)
  disabled: boolean; // answers 503, PHP stopped
  disabledPage: string; // maintenance page shown while disabled (ADR-0047)
  maintenance: SiteMaintenance;
  domains: string[]; // the first one is the primary domain
  // Names that answer with 301 to the primary domain and stay on the
  // certificate (ADR-0068). Older answers lack it.
  redirectDomains?: string[];
  // A domain change that waits for its certificate, null for none
  // (ADR-0068). Older answers lack it.
  pendingDomains?: PendingDomains | null;
  // The redirect between example.com and www.example.com (ADR-0065).
  // Older answers lack it.
  www?: SiteWWW;
  tls: SiteTLS;
  createdAt: string;
  updatedAt: string;
};

/** A domain change that waits for a certificate for its names (ADR-0068). */
export type PendingDomains = {
  domains: string[];
  redirectDomains: string[];
  requestedAt: string;
  error: string; // the last failed attempt, "" for none
  failedAt: string | null;
};

export type SiteType = "static" | "php" | "node" | "app" | "python" | "proxy" | "redirect";

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
    case "php":
      return s.php ? i18n.t("sites.type.phpVersion", { version: s.php }) : i18n.t("sites.type.php");
    default:
      return i18n.t(`sites.type.${siteType(s)}` as const);
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
  title?: string;
  pool?: string;
  php: string;
  domains: string[];
  tls: boolean;
  hsts: boolean;
  /** Application profile of a PHP site (ADR-0069); omitted: generic PHP. */
  profile?: string;
  type?: "redirect" | "proxy" | "node" | "app" | "python";
  redirect?: SiteRedirect;
  proxy?: SiteProxy;
  node?: Pick<NodeApp, "version" | "workdir" | "startKind" | "start">;
  app?: Pick<ProgramApp, "workdir" | "program" | "args">;
  python?: Pick<PythonApp, "version" | "workdir" | "startKind" | "target" | "args">;
};

/** The target of a reverse proxy site (ADR-0054). */
export type SiteProxy = { target: string; targetHost: boolean; insecure: boolean };

/** The target of a redirect site (ADR-0054). */
export type SiteRedirect = { target: string; status: number; keepPath: boolean };

/** The status codes of a redirect; the label is siteDomains.redirect.statuses.<code>. */
export const redirectStatuses = [301, 302, 307, 308] as const;

/**
 * proxyProblem mirrors agentapi.ParseProxyTarget: the rules of a redirect
 * target, and no address of this server (loopback, link-local such as the
 * cloud metadata service, 0.0.0.0, multicast) or .localhost name. The
 * server also checks the addresses a name resolves to.
 */
export function proxyProblem(target: string, own: string[]): string {
  if (!target) return i18n.t("siteDomains.target.enterProxy");
  const problem = redirectProblem(target, own);
  if (problem) return problem;
  const host = new URL(target).hostname.replace(/^\[|\]$/g, "").toLowerCase();
  if (host === "localhost" || host.endsWith(".localhost")) return i18n.t("siteDomains.target.thisServer");
  const v4 = /^(\d+)\.(\d+)\.(\d+)\.(\d+)$/.exec(host);
  if (v4) {
    const [a, b] = [Number(v4[1]), Number(v4[2])];
    if (a === 127 || a === 0) return i18n.t("siteDomains.target.thisServer");
    if (a === 169 && b === 254) return i18n.t("siteDomains.target.linkLocalMetadata");
    if (a >= 224) return i18n.t("siteDomains.target.multicastBroadcast");
  }
  if (host.includes(":")) {
    if (host === "::1" || host === "::" || host.startsWith("::ffff:")) return i18n.t("siteDomains.target.thisServer");
    if (/^fe[89ab]/.test(host)) return i18n.t("siteDomains.target.linkLocal");
    if (host.startsWith("ff")) return i18n.t("siteDomains.target.multicast");
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
  if (!target) return i18n.t("siteDomains.target.enterRedirect");
  if (target.length > 2048) return i18n.t("siteDomains.target.tooLong");
  if (!/^https?:\/\//.test(target)) return i18n.t("siteDomains.target.scheme");
  if (!/^https?:\/\/[A-Za-z0-9.:[\]-]+(\/[A-Za-z0-9._~%!*+,=:@&()-]*)*$/.test(target))
    return i18n.t("siteDomains.target.characters");
  let host = "";
  try {
    host = new URL(target).hostname;
  } catch {
    return i18n.t("siteDomains.target.invalid");
  }
  if (!host) return i18n.t("siteDomains.target.noHost");
  if (/[A-Z]/.test(target.split("/")[2] ?? "")) return i18n.t("siteDomains.target.lowerCase");
  if (own.includes(host)) return i18n.t("siteDomains.target.itself");
  return "";
}

// Pool is a storage pool for site homes (ADR-0016). Root adds and removes
// pools on the server (hostashi pool add); admins rename them in the panel
// and choose the default (addendum 2).
export type Pool = {
  name: string;
  path: string;
  sites: number;
  // Mounted with its marker; only then are quota and the sizes set.
  available: boolean;
  quota?: "off" | "counting" | "enforced";
  freeBytes?: number;
  totalBytes?: number;
  // New sites that name no pool land here.
  default: boolean;
};

export const listPools = () => request<{ pools: Pool[] }>("GET", "/pools").then((r) => r.pools);
export const renamePool = (name: string, newName: string) =>
  request<Pool>("POST", "/pools/" + encodeURIComponent(name) + "/rename", { name: newName });
export const setDefaultPool = (name: string) => request<Pool>("POST", "/pools/" + encodeURIComponent(name) + "/default");

/** The name of the default pool; "default" for an agent that marks none. */
export function defaultPool(pools: readonly Pool[]): string {
  return pools.find((p) => p.default)?.name ?? "default";
}

// Pool names as the agent accepts them (poolNameRe in internal/agentapi).
const poolName = /^[a-z][a-z0-9-]{0,31}$/;

/** Why a new pool name cannot be used, or "". */
export function poolNameProblem(name: string, pools: readonly Pool[], current: string): string {
  if (name === "") return "Enter a name.";
  if (!poolName.test(name)) return "Lowercase letters, digits and hyphens, starting with a letter, at most 32 characters.";
  if (name !== current && pools.some((p) => p.name === name)) return "Another pool already has this name.";
  return "";
}

/** The used share of a pool's volume, or null without sizes. */
export function poolUsed(p: Pool): number | null {
  if (!p.available || !p.totalBytes || p.freeBytes === undefined) return null;
  return Math.max(0, p.totalBytes - p.freeBytes);
}

/** The text of a pool in the choice: name, path and free space. */
export function poolLabel(p: Pool): string {
  const v = { name: p.name, path: p.path };
  if (!p.available) return i18n.t("sites.pool.unavailable", v);
  if (p.freeBytes === undefined) return i18n.t("sites.pool.label", v);
  return i18n.t("sites.pool.free", { ...v, free: formatBytes(p.freeBytes) });
}

/** What to know before putting a site on the pool, or "". */
export function poolNote(p: Pool | undefined): string {
  if (!p) return "";
  if (!p.available) return i18n.t("sites.pool.unmounted");
  if (p.quota === "off") return i18n.t("sites.pool.noQuota");
  if (p.quota === "counting") return i18n.t("sites.pool.counting");
  return "";
}

const sitePath = (name: string) => "/sites/" + encodeURIComponent(name);

/** SiteMarks are shown beside a site in the list. */
export type SiteMarks = { openFindings: number; cloneOf: string; staging: boolean };
/** listSitesMarked returns the sites with their marks, by site name; older answers lack them. */
export const listSitesMarked = async () => {
  const r = await request<{ sites: Site[]; marks?: Record<string, SiteMarks> }>("GET", "/sites");
  return { sites: r.sites, marks: r.marks ?? {} };
};
export const listSites = async () => (await listSitesMarked()).sites;
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

/**
 * Sets the served and redirect domains (ADR-0068). On a site live on HTTPS
 * the change waits for its certificate unless waitForCertificate is false;
 * the answer then has pendingDomains.
 */
export const changeDomains = (name: string, domains: string[], redirectDomains: string[], waitForCertificate = true) =>
  request<Site>("PUT", sitePath(name) + "/domains", { domains, redirectDomains, waitForCertificate });

/** Cancels the domain change that waits for its certificate. */
export const cancelDomainChange = (name: string) => request<Site>("DELETE", sitePath(name) + "/domains/pending");
/** The display name if set, else the system name (ADR-0072). */
export const siteLabel = (s: Pick<Site, "name" | "title">) => s.title || s.name;

/** The longest display name in characters, as in migration 0065. */
export const maxTitleLength = 64;

export const setTitle = (name: string, title: string) => request<Site>("PUT", sitePath(name) + "/title", { title });
export const setPHP = (name: string, php: string) => request<Site>("PUT", sitePath(name) + "/php", { php });

/** A new type with its settings (ADR-0054 addendum 3); static takes none. */
export type SiteTypeRequest = {
  type: SiteType;
  php?: string;
  redirect?: SiteRedirect;
  proxy?: SiteProxy;
  node?: Pick<NodeApp, "version" | "workdir" | "startKind" | "start">;
  app?: Pick<ProgramApp, "workdir" | "program" | "args">;
  python?: Pick<PythonApp, "version" | "workdir" | "startKind" | "target" | "args">;
};
export const setSiteType = (name: string, t: SiteTypeRequest) => request<Site>("POST", sitePath(name) + "/type", t);
/** Removes the Linux account a redirect or proxy site kept, with its files and databases. */
export const removeSiteAccount = (name: string) => request<Site>("DELETE", sitePath(name) + "/account");

const accountTypes: SiteType[] = ["static", "php", "node", "app", "python"];

/**
 * typeChange says what a change of the site to another type starts, stops
 * and keeps, for the summary before the change.
 */
export function typeChange(s: Site, to: SiteTypeRequest): { starts: string[]; stops: string[]; stays: string[] } {
  const from = siteType(s);
  const starts: string[] = [];
  const stops: string[] = [];
  const stays: string[] = [];
  const account = hasAccount(s);
  const toAccount = accountTypes.includes(to.type);

  const t = i18n.t;
  const target = (v: string | undefined) => v ?? t("sites.change.theTarget");
  if (from === "php") stops.push(t("sites.change.phpStops", { version: s.php }));
  // Between the app types the port stays (addendum 3).
  const port = " " + (to.type === "node" || to.type === "app" || to.type === "python" ? t("sites.change.keepsPort") : t("sites.change.freesPort"));
  if (from === "node") stops.push(t("sites.change.nodeStops") + port);
  if (from === "app") stops.push(t("sites.change.appStops") + port);
  if (from === "python") stops.push(t("sites.change.pythonStops") + port + " " + t("sites.change.venvStays"));
  if (from === "proxy") stops.push(t("sites.change.proxyStops", { target: target(s.proxy?.target) }));
  if (from === "redirect") stops.push(t("sites.change.redirectStops", { target: target(s.redirect?.target) }));

  if (toAccount && !account) starts.push(t("sites.change.accountStarts", { pool: s.pool }));
  switch (to.type) {
    case "static":
      starts.push(t("sites.change.staticStarts", { dir: "htdocs" + (s.web?.dir ? "/" + s.web.dir : "") }));
      break;
    case "php":
      starts.push(t("sites.change.phpStarts", { version: to.php ?? "" }));
      break;
    case "node":
      starts.push(to.node?.startKind ? t("sites.change.nodeStarts", { version: to.node.version }) : t("sites.change.nodeWaits"));
      break;
    case "app":
      starts.push(to.app?.program ? t("sites.change.appStarts", { program: to.app.program }) : t("sites.change.appWaits"));
      break;
    case "python":
      starts.push(t("sites.change.pythonStarts", { version: to.python?.version ?? "" }));
      break;
    case "proxy":
      starts.push(t("sites.change.proxyStarts", { target: target(to.proxy?.target) }));
      break;
    case "redirect":
      starts.push(t("sites.change.redirectStarts", { target: target(to.redirect?.target), status: to.redirect?.status ?? 301 }));
      break;
  }

  stays.push(t("sites.change.domainsStay"));
  if (account && toAccount) stays.push(t("sites.change.accountStays"));
  if (account && !toAccount) stays.push(t("sites.change.accountKept"));
  return { starts, stops, stays };
}

export const setRedirect = (name: string, r: SiteRedirect) => request<Site>("PUT", sitePath(name) + "/redirect", r);
export const setProxy = (name: string, p: SiteProxy) => request<Site>("PUT", sitePath(name) + "/proxy", p);
export const setWebOptions = (name: string, w: SiteWeb) => request<Site>("PUT", sitePath(name) + "/web", w);
export const setTLS = (name: string, enabled: boolean) => request<Site>("PUT", sitePath(name) + "/tls", { enabled });
// setDisabled switches a site off or on; page is the maintenance page a
// disabled site shows (empty: the built-in page), only sent when disabling.
export const setDisabled = (name: string, disabled: boolean, page?: string) =>
  request<Site>("PUT", sitePath(name) + "/disabled", disabled && page ? { disabled, page } : { disabled });
export const setWWW = (name: string, mode: WWWMode) => request<Site>("PUT", sitePath(name) + "/www", { mode });
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
  if (v === "") return i18n.t("sites.check.nameEmpty");
  if (!siteNameRe.test(v)) return i18n.t("sites.check.nameFormat");
  if (reservedSiteNames.includes(v) || v.startsWith("systemd-")) return i18n.t("sites.check.nameReserved");
  return "";
}

const labelRe = /^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$/;

export function domainProblem(d: string): string {
  if (d.length < 3 || d.length > 253) return i18n.t("sites.check.domainInvalid", { domain: d });
  const labels = d.split(".");
  if (labels.length < 2) return i18n.t("sites.check.domainParts", { domain: d });
  if (!labels.every((l) => labelRe.test(l))) return i18n.t("sites.check.domainChars", { domain: d });
  if (/^[0-9]+$/.test(labels[labels.length - 1] ?? "")) return i18n.t("sites.check.domainIp", { domain: d });
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
  if (domains.length === 0) return i18n.t("sites.check.domainsEmpty");
  if (domains.length > maxDomainsPerSite) return i18n.t("sites.check.domainsMax", { count: maxDomainsPerSite });
  const seen = new Set<string>();
  for (const d of domains) {
    const p = domainProblem(d);
    if (p) return p;
    if (seen.has(d)) return i18n.t("sites.check.domainTwice", { domain: d });
    seen.add(d);
  }
  return "";
}

// httpsLabel says in a few words where HTTPS of a site stands.
export function httpsLabel(t: SiteTLS): string {
  if (!t.enabled) return i18n.t("sites.https.httpOnly");
  if (t.active) return i18n.t("sites.https.on");
  if (t.error) return i18n.t("sites.https.failed");
  return i18n.t("sites.https.waiting");
}

// siteMessage turns an API error into a sentence: the site texts of
// sites.errors first, then the shared ones (ADR-0071).
export function siteMessage(err: unknown): string {
  return areaErrorText("sites.errors", err);
}

export type SiteStatus = { tone: "ok" | "warn" | "err" | "off"; word: string; key: "active" | "creating" | "deleting" | "disabled" | "maintenance" };

// siteStatus is the state of a site as a badge: a word and a tone.
/**
 * runtimeWord names what runs a site's code: "PHP", "app" for Node.js,
 * Python and app sites, "" for static, reverse proxy and redirect sites,
 * whose texts then leave the runtime out.
 */
export function runtimeWord(s: Pick<Site, "type" | "php">): "PHP" | "app" | "" {
  const t = siteType(s);
  if (t === "php") return "PHP";
  return t === "node" || t === "app" || t === "python" ? "app" : "";
}

export function siteStatus(s: Site): SiteStatus {
  const st = (tone: SiteStatus["tone"], key: SiteStatus["key"]): SiteStatus => ({ tone, key, word: i18n.t(`sites.state.${key}`) });
  if (s.state === "deleting") return st("off", "deleting");
  if (s.state !== "active") return st("warn", "creating");
  if (s.disabled) return st("off", "disabled");
  if (s.maintenance.enabled) return st("warn", "maintenance");
  return st("ok", "active");
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
