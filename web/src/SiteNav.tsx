// SPDX-License-Identifier: AGPL-3.0-or-later

import { canDeploy } from "./git";
import type { SiteTab } from "./routes";
import { hasAccount, siteType, type Site } from "./sites";
import { tabPanelProps, Tabs } from "./ui";
import type { ReactNode } from "react";
import { useTranslation } from "react-i18next";
import i18n from "./i18n";
import type { Catalog } from "./locales/en";

export type SiteGroup = { id: string; label: string; tabs: { id: SiteTab; label: string }[] };

// group and tab read their label when it is used, so the names follow the
// language (ADR-0071).
type NavTab = keyof Catalog["sites"]["nav"]["tabs"] & SiteTab;
const tab = (id: NavTab): SiteGroup["tabs"][number] => ({
  id,
  get label(): string {
    return i18n.t(`sites.nav.tabs.${id}`);
  },
});
const group = (id: keyof Catalog["sites"]["nav"]["groups"], tabs: NavTab[]): SiteGroup => ({
  id,
  get label(): string {
    return i18n.t(`sites.nav.groups.${id}`);
  },
  tabs: tabs.map(tab),
});

// The sections of a site page: a few groups, each with its own tabs. The
// address keeps naming the inner tab (#/sites/<name>/<tab>), so links from
// before the groups still open the same settings.
export const siteGroups: readonly SiteGroup[] = [
  group("overview", ["overview"]),
  group("domains", ["domains", "https"]),
  group("app", ["web", "php", "cache", "cron"]),
  group("files", ["files", "git"]),
  group("security", ["access", "hardening", "headers", "malware", "ssh"]),
  group("ops", ["usage", "limits", "logs", "realip", "nginx"]),
];

/** visibleGroups keeps the tabs that pass shown, and the groups left with one. */
export function visibleGroups(shown: (tab: SiteTab) => boolean): SiteGroup[] {
  return siteGroups.map((g) => ({ id: g.id, label: g.label, tabs: g.tabs.filter((t) => shown(t.id)) })).filter((g) => g.tabs.length > 0);
}

// A site without an account (redirect, reverse proxy) has no files, cron,
// SSH or limits (ADR-0054).
const noAccount: readonly SiteTab[] = ["files", "malware", "php", "cache", "cron", "ssh", "limits"];

/**
 * groupsForSite returns the tabs a site has. The PHP and Cache tabs only for
 * sites with PHP: both caches serve PHP (ADR-0045, ADR-0046). A redirect or
 * reverse proxy that kept its account after a type change keeps the account
 * tabs. Redirect and reverse proxy have no nginx snippets, and a redirect
 * answers before nginx checks access, so it has no access tab (ADR-0066).
 * Profiles and hardening are for static and PHP sites (ADR-0069).
 */
export function groupsForSite(site: Site): SiteGroup[] {
  const type = siteType(site);
  return visibleGroups(
    (t) =>
      (!["php", "cache"].includes(t) || site.php !== "") &&
      (t !== "git" || canDeploy(site)) &&
      (hasAccount(site) || !noAccount.includes(t)) &&
      (!["redirect", "proxy"].includes(type) || t !== "nginx") &&
      (type !== "redirect" || t !== "access") &&
      (t !== "hardening" || ["static", "php"].includes(type)),
  );
}

/** groupOf returns the group that holds a tab. */
export function groupOf(groups: readonly SiteGroup[], tab: SiteTab): SiteGroup | undefined {
  return groups.find((g) => g.tabs.some((t) => t.id === tab));
}

type Props = {
  site: string;
  groups: readonly SiteGroup[];
  active: SiteTab;
  onTab: (tab: SiteTab) => void;
  children: ReactNode;
};

/**
 * SiteNav shows the groups as tabs and, for a group with more than one,
 * its own tabs below. On a phone both become one select with a heading per
 * group, so no tab hides behind the edge of the screen.
 */
export function SiteNav({ site, groups, active, onTab, children }: Props) {
  const { t } = useTranslation();
  const prefix = `site-${site}`;
  const group = groupOf(groups, active) ?? groups[0]!;
  const sub = group.tabs.length > 1;
  return (
    <>
      <div className="ui-site-select">
        <label className="ui-sr-only" htmlFor={`${prefix}-section`}>
          {t("sites.nav.section", { site })}
        </label>
        <select id={`${prefix}-section`} className="ui-select" value={active} onChange={(e) => onTab(e.target.value as SiteTab)}>
          {groups.map((g) =>
            g.tabs.length === 1 ? (
              <option key={g.id} value={g.tabs[0]!.id}>
                {g.label}
              </option>
            ) : (
              <optgroup key={g.id} label={g.label}>
                {g.tabs.map((t) => (
                  <option key={t.id} value={t.id}>
                    {t.label}
                  </option>
                ))}
              </optgroup>
            ),
          )}
        </select>
      </div>
      <div className="ui-site-tabs">
        <Tabs
          label={site}
          tabs={groups.map((g) => ({ id: g.id, label: g.label }))}
          active={group.id}
          onChange={(id) => {
            const g = groups.find((x) => x.id === id);
            if (g && g.id !== group.id) onTab(g.tabs[0]!.id);
          }}
          idPrefix={prefix}
        />
      </div>
      {sub ? (
        <div {...tabPanelProps(prefix, group.id)} className="ui-stack">
          <div className="ui-site-tabs ui-subtabs">
            <Tabs label={group.label} tabs={group.tabs} active={active} onChange={(t) => onTab(t as SiteTab)} idPrefix={`${prefix}-sub`} />
          </div>
          <div {...tabPanelProps(`${prefix}-sub`, active)} className="ui-stack">
            {children}
          </div>
        </div>
      ) : (
        <div {...tabPanelProps(prefix, group.id)} className="ui-stack">
          {children}
        </div>
      )}
    </>
  );
}
