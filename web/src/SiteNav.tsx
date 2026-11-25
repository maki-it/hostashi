// SPDX-License-Identifier: AGPL-3.0-or-later

import type { SiteTab } from "./routes";
import { tabPanelProps, Tabs } from "./ui";
import { Code, FolderSimple, Gauge, Globe, ShieldCheck, SquaresFour } from "@phosphor-icons/react";
import type { ReactNode } from "react";

export type SiteGroup = { id: string; label: string; icon: ReactNode; tabs: { id: SiteTab; label: string }[] };

// The sections of a site page: a few groups, each with its own tabs. The
// address keeps naming the inner tab (#/sites/<name>/<tab>), so links from
// before the groups still open the same settings.
export const siteGroups: readonly SiteGroup[] = [
  { id: "overview", icon: <SquaresFour size={18} aria-hidden />, label: "Overview", tabs: [{ id: "overview", label: "Overview" }] },
  {
    id: "domains",
    icon: <Globe size={18} aria-hidden />,
    label: "Domains & HTTPS",
    tabs: [
      { id: "domains", label: "Domains" },
      { id: "https", label: "HTTPS" },
    ],
  },
  {
    id: "app",
    icon: <Code size={18} aria-hidden />,
    label: "App",
    tabs: [
      { id: "web", label: "Web" },
      { id: "php", label: "PHP" },
      { id: "cron", label: "Cron" },
    ],
  },
  {
    id: "files",
    icon: <FolderSimple size={18} aria-hidden />,
    label: "Files",
    tabs: [
      { id: "files", label: "File manager" },
      { id: "git", label: "Git deploy" },
    ],
  },
  {
    id: "security",
    icon: <ShieldCheck size={18} aria-hidden />,
    label: "Security",
    tabs: [
      { id: "access", label: "Access" },
      { id: "headers", label: "Security headers" },
      { id: "malware", label: "Virus scan" },
      { id: "ssh", label: "SSH & SFTP" },
    ],
  },
  {
    id: "ops",
    icon: <Gauge size={18} aria-hidden />,
    label: "Operations",
    tabs: [
      { id: "usage", label: "Usage" },
      { id: "limits", label: "Limits" },
      { id: "logs", label: "Logs" },
      { id: "realip", label: "Visitor address" },
      { id: "nginx", label: "nginx" },
    ],
  },
];

/** visibleGroups keeps the tabs that pass shown, and the groups left with one. */
export function visibleGroups(shown: (tab: SiteTab) => boolean): SiteGroup[] {
  return siteGroups.map((g) => ({ ...g, tabs: g.tabs.filter((t) => shown(t.id)) })).filter((g) => g.tabs.length > 0);
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
  const prefix = `site-${site}`;
  const group = groupOf(groups, active) ?? groups[0]!;
  const sub = group.tabs.length > 1;
  return (
    <>
      <div className="ui-site-select">
        <label className="ui-sr-only" htmlFor={`${prefix}-section`}>
          Section of {site}
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
      <div className="ui-site-tabs ui-site-groups">
        <Tabs
          label={site}
          tabs={groups.map((g) => ({ id: g.id, label: g.label, icon: g.icon }))}
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
