// SPDX-License-Identifier: AGPL-3.0-or-later

import { CaretDown, CaretRight, List, MagnifyingGlass, Moon, SignOut, Sun, X } from "@phosphor-icons/react";
import { useCallback, useEffect, useRef, useState, type ReactNode } from "react";
import { useTranslation } from "react-i18next";
import { logout, type User } from "./auth";
import i18n from "./i18n";
import { groupLabel, navFor, navItem, pageLabel } from "./nav";
import { runtimes } from "./Runtimes";
import { siteGroups } from "./SiteNav";
import { countLabel, healthCount, servicesCount, type NavCount } from "./navcounts";
import { runDoctor } from "./doctor";
import { Search, shortcut } from "./Search";
import { listServices } from "./services";
import { formatHash, type Route } from "./routes";
import { saveColorScheme, schemes, type ColorScheme } from "./scheme";
import { panelVersion, releaseUrl, versionLabel } from "./system";
import { IconButton } from "./ui";
import { roleLabel } from "./roles";

type ShellProps = {
  user: User;
  route: Route;
  onLogout: () => void;
  /** Called with the scheme the user picks, before it is saved. */
  onScheme: (s: ColorScheme) => void;
  children: ReactNode;
};

/** The Hostashi mark: the brand colour with an "H". */
export function Mark() {
  return (
    <span className="app-mark" aria-hidden="true">
      <svg viewBox="0 0 32 32" width="18" height="18">
        <path d="M9 8v16M23 8v16M9 16h14" stroke="currentColor" strokeWidth="3.5" strokeLinecap="round" fill="none" />
      </svg>
    </span>
  );
}

/**
 * SunMoon is the icon for "follow the system": a sun and a moon together,
 * drawn on Phosphor's 256 grid with the stroke of its regular weight.
 */
function SunMoon({ size }: { size: number }) {
  return (
    <svg viewBox="0 0 256 256" width={size} height={size} fill="none" stroke="currentColor" strokeWidth="16" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
      <circle cx="84" cy="84" r="30" />
      <path d="M130,84L146,84M84,130L84,146M51,117L40,128M38,84L22,84M51,51L40,40M84,38L84,22M117,51L128,40" />
      <path d="M105,179A66,66,0,1,0,179,105A54,54,0,0,1,105,179Z" />
    </svg>
  );
}

/** NavBadge is the count beside a sidebar entry; its text says what it counts. */
function NavBadge({ count }: { count: NavCount }) {
  return (
    <span className={`app-nav-count ui-tone-${count.tone}`} title={countLabel(count)}>
      <span aria-hidden="true">{count.n}</span>
      <span className="ui-sr-only">, {countLabel(count)}</span>
    </span>
  );
}

type Crumb = { label: string; href?: string };

function crumbs(route: Route): Crumb[] {
  if (route.page === "account") return [{ label: i18n.t("shell.account") }];
  const item = navItem(route.page);
  if (!item) return [];
  const out: Crumb[] = [];
  if (item.group) out.push({ label: groupLabel(item.group) });
  const tab = item.also ? runtimes.find((r) => r.page === route.page) : undefined;
  if (route.site) {
    out.push({ label: pageLabel(item), href: formatHash({ page: route.page }) });
    out.push({ label: route.site.name });
  } else if (tab) {
    out.push({ label: pageLabel(item), href: formatHash({ page: item.page }) });
    out.push({ label: tab.label });
  } else {
    out.push({ label: pageLabel(item) });
  }
  return out;
}

/**
 * pageTitle names the browser tab after the page, most specific first, and
 * ends in the panel's host name, so tabs of several servers stay apart:
 * "Logs · blog · panel.example.com".
 */
export function pageTitle(route: Route, host: string): string {
  const parts: string[] = [];
  if (route.site) {
    const tab = siteGroups.flatMap((g) => g.tabs).find((t) => t.id === route.site?.tab);
    if (tab && route.site.tab !== "overview") parts.push(tab.label);
    parts.push(route.site.name);
  } else if (route.page === "account") {
    parts.push(i18n.t("shell.account"));
  } else {
    const item = navItem(route.page);
    const tab = runtimes.find((r) => r.page === route.page);
    if (item && tab) parts.push(tab.label);
    if (item) parts.push(pageLabel(item));
  }
  parts.push(host || "Hostashi");
  return parts.join(" · ");
}

/**
 * The frame around every page once a user is logged in (ADR-0037): a
 * sidebar with the grouped navigation and the current user, and a top bar
 * with breadcrumbs. Below 700 px the sidebar collapses into a menu.
 */
export function Shell({ user, route, onLogout, onScheme, children }: ShellProps) {
  // The hook re-renders the frame when the language changes.
  const { t } = useTranslation();
  const [menuOpen, setMenuOpen] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [version, setVersion] = useState("");
  const groups = navFor(user.role);

  // Only admins see the running version; it tells others nothing they
  // need and helps no one who probes the panel.
  const isAdmin = user.role === "admin";
  useEffect(() => {
    if (!isAdmin) return;
    let live = true;
    void panelVersion().then((v) => live && setVersion(v));
    return () => {
      live = false;
    };
  }, [isAdmin]);

  // Counts beside Health and Services. The doctor runs real checks, so
  // they load once, every five minutes, and when the admin leaves one of
  // those pages (something may have been fixed there).
  const [counts, setCounts] = useState<Partial<Record<"health" | "services", NavCount | null>>>({});
  const onCountPage = route.page === "health" || route.page === "services" ? route.page : "";
  useEffect(() => {
    if (!isAdmin) return;
    let live = true;
    const load = () => {
      runDoctor()
        .then((r) => {
          // Counted here, not in the state updater: a bad answer must not
          // take the whole frame down.
          const health = healthCount(r);
          if (live) setCounts((c) => ({ ...c, health }));
        })
        .catch(() => undefined);
      listServices()
        .then((rows) => {
          const services = servicesCount(rows);
          if (live) setCounts((c) => ({ ...c, services }));
        })
        .catch(() => undefined);
    };
    load();
    const t = setInterval(load, 5 * 60 * 1000);
    return () => {
      live = false;
      clearInterval(t);
    };
  }, [isAdmin, onCountPage]);
  const trail = crumbs(route);
  const title = pageTitle(route, window.location.hostname);
  useEffect(() => {
    document.title = title;
    return () => {
      document.title = "Hostashi";
    };
  }, [title]);

  async function doLogout() {
    setBusy(true);
    setError("");
    try {
      await logout();
      onLogout();
    } catch {
      setError(t("shell.logoutFailed"));
      setBusy(false);
    }
  }

  // The colour button in the top bar steps through the same three choices
  // as the account page (ADR-0037) with one click each, system → light →
  // dark → system, and stores the same value. Its icon shows the current
  // choice (sun and moon together for following the system), its tooltip also the next.
  const scheme = user.colorScheme ?? "system";
  const nextScheme: ColorScheme = schemes[(schemes.indexOf(scheme) + 1) % schemes.length] ?? "system";
  async function cycleScheme() {
    onScheme(nextScheme);
    setError("");
    try {
      await saveColorScheme(nextScheme);
    } catch {
      setError(t("shell.schemeFailed"));
    }
  }
  const schemeIcon = scheme === "light" ? <Sun size={20} /> : scheme === "dark" ? <Moon size={20} /> : <SunMoon size={20} />;
  const schemeButton = t("shell.schemeButton", { current: t(`shell.scheme.${scheme}`), next: t(`shell.scheme.${nextScheme}`) });

  // Ctrl K (⌘ K on a Mac) opens the search from anywhere.
  const [searching, setSearching] = useState(false);
  useEffect(() => {
    const key = (e: KeyboardEvent) => {
      if ((e.ctrlKey || e.metaKey) && !e.altKey && e.key.toLowerCase() === "k") {
        e.preventDefault();
        setSearching((v) => !v);
      }
    };
    document.addEventListener("keydown", key);
    return () => document.removeEventListener("keydown", key);
  }, []);

  // The top bar sticks to the top of the window. Its height goes to a CSS
  // variable, so other sticky parts (the file manager's bar) stick below it.
  const topRef = useRef<HTMLElement>(null);
  useEffect(() => {
    const top = topRef.current;
    if (!top || typeof ResizeObserver === "undefined") return;
    const html = document.documentElement;
    const measure = () => html.style.setProperty("--app-top-h", `${String(top.offsetHeight)}px`);
    const ro = new ResizeObserver(measure);
    ro.observe(top);
    measure();
    return () => {
      ro.disconnect();
      html.style.removeProperty("--app-top-h");
    };
  }, []);

  // navScrolled draws a line under the brand once the nav scrolls under it.
  const [navScrolled, setNavScrolled] = useState(false);
  // navMore shows a hint at the bottom of the nav while entries are hidden
  // below it, on a low screen (C2 of the UX review).
  const navRef = useRef<HTMLElement>(null);
  const [navMore, setNavMore] = useState(false);
  const measureNav = useCallback(() => {
    const el = navRef.current;
    if (!el) return;
    setNavScrolled(el.scrollTop > 0);
    setNavMore(el.scrollTop + el.clientHeight < el.scrollHeight - 8);
  }, []);
  useEffect(() => {
    measureNav();
    window.addEventListener("resize", measureNav);
    return () => window.removeEventListener("resize", measureNav);
  }, [measureNav, groups.length]);
  const lastGroup = groupLabel(groups[groups.length - 1]?.id ?? null);

  return (
    <div className="ui-root app">
      <aside className="app-side" data-open={menuOpen} data-scrolled={navScrolled}>
        <div className="app-brand">
          {/* The name and the panel's host lead to the overview (Kim, 2026-11-25). */}
          <a className="app-brand-link" href={formatHash({ page: "home" })} aria-label={t("shell.brandHome")} onClick={() => setMenuOpen(false)}>
            <Mark />
            <span className="app-brand-name">
              Hostashi
              <span className="app-host">{window.location.hostname}</span>
            </span>
          </a>
          <span className="app-menu-btn">
            <IconButton
              label={menuOpen ? t("shell.closeMenu") : t("shell.openMenu")}
              icon={menuOpen ? <X size={20} /> : <List size={20} />}
              aria-expanded={menuOpen}
              aria-controls="app-nav"
              tipBelow
              onClick={() => setMenuOpen(!menuOpen)}
            />
          </span>
        </div>
        <nav aria-label={t("shell.nav.label")} id="app-nav" className="app-nav" ref={navRef} onScroll={measureNav}>
          {groups.map((g) => (
            <div className="app-nav-group" key={g.id ?? "top"}>
              {g.id && <h2 className="app-nav-label">{groupLabel(g.id)}</h2>}
              <ul>
                {g.items.map((i) => {
                  const Icon = i.icon;
                  return (
                    <li key={i.page}>
                      <a
                        className="app-nav-item"
                        href={formatHash({ page: i.page })}
                        aria-current={route.page === i.page || i.also?.includes(route.page) ? "page" : undefined}
                        onClick={() => setMenuOpen(false)}
                      >
                        <Icon size={20} aria-hidden="true" />
                        {pageLabel(i)}
                        {(i.page === "health" || i.page === "services") && counts[i.page] && (
                          <NavBadge count={counts[i.page]!} />
                        )}
                      </a>
                    </li>
                  );
                })}
              </ul>
            </div>
          ))}
        </nav>
        {navMore && lastGroup && (
          <div className="app-nav-more">
            <button type="button" tabIndex={-1} onClick={() => navRef.current?.scrollTo({ top: navRef.current.scrollHeight, behavior: "smooth" })}>
              <CaretDown size={12} aria-hidden="true" />
              {lastGroup}
            </button>
          </div>
        )}
        {/* The account, logout and version stay in view while the nav scrolls. */}
        <div className="app-foot">
          <div className="app-user">
            <span className="app-avatar" aria-hidden="true">
              {user.username.slice(0, 2).toUpperCase()}
            </span>
            <a
              className="app-user-name"
              href={formatHash({ page: "account" })}
              aria-current={route.page === "account" ? "page" : undefined}
              onClick={() => setMenuOpen(false)}
            >
              {user.username}
              <small>{roleLabel(user.role)}</small>
            </a>
            <IconButton label={t("shell.logout")} icon={<SignOut size={20} />} disabled={busy} onClick={() => void doLogout()} />
          </div>
          {error && (
            <p role="alert" className="app-user-error">
              {error}
            </p>
          )}
          {isAdmin && version && (
            <p className="app-version">
              <a href={releaseUrl(version)} target="_blank" rel="noopener noreferrer" title={t("shell.releaseNotes")}>
                Hostashi {versionLabel(version)}
              </a>
            </p>
          )}
        </div>
      </aside>
      <div className="app-main">
        <header className="app-top" ref={topRef}>
          <nav aria-label={t("shell.breadcrumb")}>
            <ol className="app-crumbs">
              {trail.map((c, i) => {
                const last = i === trail.length - 1;
                return (
                  <li key={`${i}-${c.label}`}>
                    {c.href && !last ? (
                      <a href={c.href}>{c.label}</a>
                    ) : (
                      <span aria-current={last ? "page" : undefined}>{c.label}</span>
                    )}
                    {!last && <CaretRight size={12} aria-hidden="true" />}
                  </li>
                );
              })}
            </ol>
          </nav>
          <span className="app-top-actions">
            <button type="button" className="app-search-btn" aria-haspopup="dialog" aria-keyshortcuts="Control+K Meta+K" onClick={() => setSearching(true)}>
              <MagnifyingGlass size={16} aria-hidden="true" />
              <span className="app-search-text">{t("shell.search.open")}</span>
              <kbd className="app-kbd" aria-hidden="true">
                {shortcut}
              </kbd>
            </button>
            <IconButton label={schemeButton} icon={schemeIcon} tipBelow onClick={() => void cycleScheme()} />
          </span>
        </header>
        <div className="app-content">{children}</div>
      </div>
      {searching && <Search role={user.role} onClose={() => setSearching(false)} />}
    </div>
  );
}
