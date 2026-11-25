// SPDX-License-Identifier: AGPL-3.0-or-later

import { CaretRight, List, Moon, SignOut, Sun, X } from "@phosphor-icons/react";
import { useEffect, useState, type ReactNode } from "react";
import { logout, type User } from "./auth";
import { navFor, navItem } from "./nav";
import { countLabel, healthCount, servicesCount, type NavCount } from "./navcounts";
import { runDoctor } from "./doctor";
import { listServices } from "./services";
import { formatHash, type Route } from "./routes";
import { isDark, saveColorScheme, type ColorScheme } from "./scheme";
import { panelVersion, releaseUrl, versionLabel } from "./system";
import { IconButton } from "./ui";

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
  if (route.page === "account") return [{ label: "Your account" }];
  const item = navItem(route.page);
  if (!item) return [];
  const out: Crumb[] = [];
  if (item.group) out.push({ label: item.group });
  if (route.site) {
    out.push({ label: item.label, href: formatHash({ page: route.page }) });
    out.push({ label: route.site.name });
  } else {
    out.push({ label: item.label });
  }
  return out;
}

/**
 * The frame around every page once a user is logged in (ADR-0037): a
 * sidebar with the grouped navigation and the current user, and a top bar
 * with breadcrumbs. Below 700 px the sidebar collapses into a menu.
 */
export function Shell({ user, route, onLogout, onScheme, children }: ShellProps) {
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

  async function doLogout() {
    setBusy(true);
    setError("");
    try {
      await logout();
      onLogout();
    } catch {
      setError("Logout failed. Try again.");
      setBusy(false);
    }
  }

  // The sun/moon button switches between light and dark in one click; the
  // account page also offers following the system (ADR-0037).
  const dark = isDark(user.colorScheme ?? "system");
  async function toggleScheme() {
    const next: ColorScheme = dark ? "light" : "dark";
    onScheme(next);
    setError("");
    try {
      await saveColorScheme(next);
    } catch {
      setError("The colour scheme could not be saved. It applies until you reload.");
    }
  }

  // navScrolled draws a line under the brand once the nav scrolls under it.
  const [navScrolled, setNavScrolled] = useState(false);

  return (
    <div className="ui-root app">
      <aside className="app-side" data-open={menuOpen} data-scrolled={navScrolled}>
        <div className="app-brand">
          <Mark />
          <span className="app-brand-name">
            Hostashi
            <span className="app-host">{window.location.hostname}</span>
          </span>
          <span className="app-menu-btn">
            <IconButton
              label={menuOpen ? "Close menu" : "Open menu"}
              icon={menuOpen ? <X size={20} /> : <List size={20} />}
              aria-expanded={menuOpen}
              aria-controls="app-nav"
              tipBelow
              onClick={() => setMenuOpen(!menuOpen)}
            />
          </span>
        </div>
        <nav aria-label="Main" id="app-nav" className="app-nav" onScroll={(e) => setNavScrolled(e.currentTarget.scrollTop > 0)}>
          {groups.map((g) => (
            <div className="app-nav-group" key={g.label ?? "top"}>
              {g.label && <h2 className="app-nav-label">{g.label}</h2>}
              <ul>
                {g.items.map((i) => {
                  const Icon = i.icon;
                  return (
                    <li key={i.page}>
                      <a
                        className="app-nav-item"
                        href={formatHash({ page: i.page })}
                        aria-current={route.page === i.page ? "page" : undefined}
                        onClick={() => setMenuOpen(false)}
                      >
                        <Icon size={20} aria-hidden="true" />
                        {i.label}
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
              <small>{user.role}</small>
            </a>
            <IconButton label="Log out" icon={<SignOut size={20} />} disabled={busy} onClick={() => void doLogout()} />
          </div>
          {error && (
            <p role="alert" className="app-user-error">
              {error}
            </p>
          )}
          {isAdmin && version && (
            <p className="app-version">
              <a href={releaseUrl(version)} target="_blank" rel="noopener noreferrer" title="Release notes">
                Hostashi {versionLabel(version)}
              </a>
            </p>
          )}
        </div>
      </aside>
      <div className="app-main">
        <header className="app-top">
          <nav aria-label="Breadcrumb">
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
            <IconButton
              label={dark ? "Use light colours" : "Use dark colours"}
              icon={dark ? <Sun size={20} /> : <Moon size={20} />}
              tipBelow
              onClick={() => void toggleScheme()}
            />
          </span>
        </header>
        <div className="app-content">{children}</div>
      </div>
    </div>
  );
}
