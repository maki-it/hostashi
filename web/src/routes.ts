// SPDX-License-Identifier: AGPL-3.0-or-later

// Hash routes of the panel (ADR-0037). An address looks like
//   #/                    overview
//   #/<page>              a list page, for example #/databases
//   #/sites/<name>        a site, on its overview tab
//   #/sites/<name>/<tab>  a tab of a site, for example #/sites/shop/php
//   #/account             the logged-in account
//   #/device?code=…       approve a login of the CLI
// and may end in ?key=value pairs for a table's sort and filter, which
// pages read with route.query.

export const pages = [
  "home",
  "sites",
  "databases",
  "parked",
  "pages",
  "health",
  "services",
  "system",
  "php",
  "backups",
  "firewall",
  "limits",
  "users",
  "mail",
  "import",
  "audit",
  "account",
  "device",
] as const;

export type Page = (typeof pages)[number];

export const siteTabs = ["overview", "domains", "web", "files", "php", "databases", "cron", "ssh", "limits", "logs", "nginx", "backups"] as const;

export type SiteTab = (typeof siteTabs)[number];

export type Route = {
  page: Page;
  /** Set on a site's own page. */
  site?: { name: string; tab: SiteTab };
  query: URLSearchParams;
};

// Site names as the API accepts them (siteUserRe in internal/agentapi).
const siteName = /^[a-z][a-z0-9_-]{1,31}$/;

const isPage = (s: string): s is Page => (pages as readonly string[]).includes(s) && s !== "home";
const isTab = (s: string): s is SiteTab => (siteTabs as readonly string[]).includes(s);

/**
 * parseHash turns an address into a route. Anything it does not know,
 * including a malformed site name or tab, is the overview: the panel never
 * shows an error page for a hand-edited address.
 */
export function parseHash(hash: string): Route {
  const raw = hash.replace(/^#/, "");
  const q = raw.indexOf("?");
  const path = q < 0 ? raw : raw.slice(0, q);
  const query = new URLSearchParams(q < 0 ? "" : raw.slice(q + 1));
  const parts = path.split("/").filter((p) => p !== "");
  const home: Route = { page: "home", query: new URLSearchParams() };

  const [first, second, third, ...rest] = parts;
  if (first === undefined) return { ...home, query };
  if (!isPage(first) || rest.length > 0) return home;
  if (second === undefined) return { page: first, query };
  if (first !== "sites" || !siteName.test(second)) return home;
  const tab = third ?? "overview";
  if (!isTab(tab)) return home;
  return { page: "sites", site: { name: second, tab }, query };
}

/** formatHash is the inverse of parseHash. The overview tab is left out. */
export function formatHash(route: { page: Page; site?: { name: string; tab?: SiteTab }; query?: URLSearchParams }): string {
  let path = route.page === "home" ? "#/" : `#/${route.page}`;
  if (route.page === "sites" && route.site) {
    path += `/${route.site.name}`;
    if (route.site.tab && route.site.tab !== "overview") path += `/${route.site.tab}`;
  }
  const q = route.query?.toString() ?? "";
  return q === "" ? path : `${path}?${q}`;
}
