// SPDX-License-Identifier: AGPL-3.0-or-later

import { FileText, Globe, MagnifyingGlass, UserCircle, type Icon } from "@phosphor-icons/react";
import { useEffect, useId, useMemo, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import i18n from "./i18n";
import { groupLabel, navFor, pageLabel } from "./nav";
import { formatHash } from "./routes";
import { runtimes } from "./Runtimes";
import { groupsForSite } from "./SiteNav";
import { listSites, type Site } from "./sites";

/** group: shell.search.groups.<group> names it. */
export type SearchItem = { group: "sites" | "sections" | "pages"; label: string; detail: string; href: string; icon: Icon; words: string };

// Each group shows this many hits, so a short query does not list every tab
// of every site.
const perGroup = 6;

/**
 * searchItems builds what the search can find for a role and its sites, in
 * the current language. The English names of the pages stay findable in
 * every language.
 */
export function searchItems(role: string, sites: readonly Site[]): SearchItem[] {
  const out: SearchItem[] = [];
  for (const s of sites) {
    const name = s.title || s.name;
    const domain = s.domains[0] ?? "";
    out.push({
      group: "sites",
      label: name,
      detail: domain,
      href: formatHash({ page: "sites", site: { name: s.name } }),
      icon: Globe,
      words: `${s.name} ${s.title ?? ""} ${s.domains.join(" ")}`,
    });
    if (s.state !== "active") continue;
    for (const g of groupsForSite(s)) {
      for (const t of g.tabs) {
        if (t.id === "overview") continue;
        out.push({
          group: "sections",
          label: `${name} › ${t.label}`,
          detail: g.label,
          href: formatHash({ page: "sites", site: { name: s.name, tab: t.id } }),
          icon: FileText,
          words: `${s.name} ${s.title ?? ""} ${domain} ${t.label} ${g.label}`,
        });
      }
    }
  }
  const english = i18n.getFixedT("en");
  for (const g of navFor(role)) {
    const group = groupLabel(g.id);
    for (const i of g.items) {
      const label = pageLabel(i);
      const words = `${english(`shell.nav.pages.${i.page}`)} ${group}`;
      if (i.also) {
        // Runtimes: one hit per language, so "python" finds its versions.
        for (const r of runtimes) {
          out.push({ group: "pages", label: `${label} › ${r.label}`, detail: group, href: formatHash({ page: r.page }), icon: i.icon, words: `${words} ${r.label} versions ${i18n.t("shell.search.runtimeWords")}` });
        }
        continue;
      }
      out.push({ group: "pages", label, detail: group, href: formatHash({ page: i.page }), icon: i.icon, words });
    }
  }
  out.push({
    group: "pages",
    label: i18n.t("shell.account"),
    detail: i18n.t("shell.search.accountDetail"),
    href: formatHash({ page: "account" }),
    icon: UserCircle,
    words: `${english("shell.search.accountWords")} ${i18n.t("shell.search.accountWords")}`,
  });
  return out;
}

/** score ranks a hit: a word that starts a word of the label counts most. */
function score(label: string, words: readonly string[]): number {
  const parts = label.toLowerCase().split(/[\s›.]+/);
  return words.reduce((n, w) => n + (parts.some((p) => p.startsWith(w)) ? 2 : label.toLowerCase().includes(w) ? 1 : 0), 0);
}

const groupOrder: Record<SearchItem["group"], number> = { sites: 0, sections: 1, pages: 2 };

/**
 * matchItems keeps the items that contain every word of the query, the best
 * first in each group and at most perGroup per group.
 */
export function matchItems(items: readonly SearchItem[], query: string): SearchItem[] {
  const words = query.toLowerCase().split(/\s+/).filter(Boolean);
  if (words.length === 0) return items.filter((i) => i.group !== "sections").slice(0, perGroup * 2);
  const hits = items
    .map((item, at) => ({ item, at, score: score(item.label, words) }))
    .filter(({ item }) => {
      const hay = `${item.label} ${item.words}`.toLowerCase();
      return words.every((w) => hay.includes(w));
    })
    .sort((a, b) => groupOrder[a.item.group] - groupOrder[b.item.group] || b.score - a.score || a.at - b.at);
  const counts: Record<string, number> = {};
  return hits
    .filter(({ item }) => {
      counts[item.group] = (counts[item.group] ?? 0) + 1;
      return counts[item.group]! <= perGroup;
    })
    .map(({ item }) => item);
}

/** shortcut is the key hint beside the search button. */
export const shortcut = /Mac|iPhone|iPad/.test(navigator.platform) ? "⌘ K" : "Ctrl K";

/**
 * Search jumps to a site, a tab of a site or a page. It opens from the top
 * bar or with Ctrl K (⌘ K on a Mac); the arrow keys move, Enter opens and
 * Esc closes.
 */
export function Search({ role, onClose }: { role: string; onClose: () => void }) {
  const { t, i18n: lang } = useTranslation();
  const [query, setQuery] = useState("");
  const [sites, setSites] = useState<Site[]>([]);
  const [active, setActive] = useState(0);
  const listId = useId();
  const back = useRef<HTMLDivElement>(null);

  useEffect(() => {
    let live = true;
    listSites()
      .then((s) => live && setSites(s))
      .catch(() => undefined);
    return () => {
      live = false;
    };
  }, []);

  // The language is a dependency: the labels are built in it.
  const items = useMemo(() => searchItems(role, sites), [role, sites, lang.language]);
  const hits = matchItems(items, query);
  const cur = Math.min(active, Math.max(hits.length - 1, 0));

  function open(i: SearchItem | undefined) {
    if (!i) return;
    onClose();
    window.location.hash = i.href;
  }

  return (
    <div className="app-search-back" ref={back} onMouseDown={(e) => e.target === back.current && onClose()}>
      <div className="app-search-dialog" role="dialog" aria-modal="true" aria-label={t("shell.search.dialog")}>
        <div className="app-search-input">
          <MagnifyingGlass size={20} aria-hidden="true" />
          <input
            autoFocus
            role="combobox"
            aria-expanded="true"
            aria-controls={listId}
            aria-activedescendant={hits.length > 0 ? `${listId}-${cur}` : undefined}
            aria-label={t("shell.search.placeholder")}
            placeholder={t("shell.search.placeholder")}
            value={query}
            onChange={(e) => {
              setQuery(e.target.value);
              setActive(0);
            }}
            onKeyDown={(e) => {
              if (e.key === "Escape") onClose();
              else if (e.key === "ArrowDown") setActive(Math.min(cur + 1, hits.length - 1));
              else if (e.key === "ArrowUp") setActive(Math.max(cur - 1, 0));
              else if (e.key === "Enter") open(hits[cur]);
              else return;
              e.preventDefault();
            }}
          />
          <kbd className="app-kbd">Esc</kbd>
        </div>
        <ul className="app-search-list" id={listId} role="listbox" aria-label={t("shell.search.results")}>
          {hits.length === 0 && <li className="app-search-empty">{t("shell.search.empty")}</li>}
          {hits.map((h, k) => {
            const head = k === 0 || hits[k - 1]!.group !== h.group;
            const Icon = h.icon;
            return (
              <li key={h.href} role="presentation">
                {head && (
                  <div className="app-search-group" role="presentation">
                    {t(`shell.search.groups.${h.group}`)}
                  </div>
                )}
                <a
                  id={`${listId}-${k}`}
                  role="option"
                  aria-selected={k === cur}
                  className="app-search-item"
                  href={h.href}
                  tabIndex={-1}
                  onMouseMove={() => setActive(k)}
                  onClick={(e) => {
                    e.preventDefault();
                    open(h);
                  }}
                >
                  <Icon size={18} aria-hidden="true" />
                  <span>{h.label}</span>
                  <small>{h.detail}</small>
                </a>
              </li>
            );
          })}
        </ul>
        <p className="app-search-foot">{t("shell.search.foot")}</p>
      </div>
    </div>
  );
}
