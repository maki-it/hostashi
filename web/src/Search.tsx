// SPDX-License-Identifier: AGPL-3.0-or-later

import { FileText, Globe, MagnifyingGlass, UserCircle, type Icon } from "@phosphor-icons/react";
import { useEffect, useId, useMemo, useRef, useState } from "react";
import { navFor } from "./nav";
import { formatHash } from "./routes";
import { runtimes } from "./Runtimes";
import { groupsForSite } from "./SiteNav";
import { listSites, type Site } from "./sites";

export type SearchItem = { group: "Sites" | "Site sections" | "Pages"; label: string; detail: string; href: string; icon: Icon; words: string };

// Each group shows this many hits, so a short query does not list every tab
// of every site.
const perGroup = 6;

/** searchItems builds what the search can find for a role and its sites. */
export function searchItems(role: string, sites: readonly Site[]): SearchItem[] {
  const out: SearchItem[] = [];
  for (const s of sites) {
    const name = s.title || s.name;
    const domain = s.domains[0] ?? "";
    out.push({
      group: "Sites",
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
          group: "Site sections",
          label: `${name} › ${t.label}`,
          detail: g.label,
          href: formatHash({ page: "sites", site: { name: s.name, tab: t.id } }),
          icon: FileText,
          words: `${s.name} ${s.title ?? ""} ${domain} ${t.label} ${g.label}`,
        });
      }
    }
  }
  for (const g of navFor(role)) {
    for (const i of g.items) {
      if (i.also) {
        // Runtimes: one hit per language, so "python" finds its versions.
        for (const r of runtimes) {
          out.push({ group: "Pages", label: `${i.label} › ${r.label}`, detail: g.label ?? "", href: formatHash({ page: r.page }), icon: i.icon, words: `${i.label} ${r.label} versions ${g.label ?? ""}` });
        }
        continue;
      }
      out.push({ group: "Pages", label: i.label, detail: g.label ?? "", href: formatHash({ page: i.page }), icon: i.icon, words: `${i.label} ${g.label ?? ""}` });
    }
  }
  out.push({ group: "Pages", label: "Your account", detail: "Password, two-factor, colours", href: formatHash({ page: "account" }), icon: UserCircle, words: "account password two-factor totp colours api" });
  return out;
}

/** score ranks a hit: a word that starts a word of the label counts most. */
function score(label: string, words: readonly string[]): number {
  const parts = label.toLowerCase().split(/[\s›.]+/);
  return words.reduce((n, w) => n + (parts.some((p) => p.startsWith(w)) ? 2 : label.toLowerCase().includes(w) ? 1 : 0), 0);
}

const groupOrder: Record<SearchItem["group"], number> = { Sites: 0, "Site sections": 1, Pages: 2 };

/**
 * matchItems keeps the items that contain every word of the query, the best
 * first in each group and at most perGroup per group.
 */
export function matchItems(items: readonly SearchItem[], query: string): SearchItem[] {
  const words = query.toLowerCase().split(/\s+/).filter(Boolean);
  if (words.length === 0) return items.filter((i) => i.group !== "Site sections").slice(0, perGroup * 2);
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

  const items = useMemo(() => searchItems(role, sites), [role, sites]);
  const hits = matchItems(items, query);
  const cur = Math.min(active, Math.max(hits.length - 1, 0));

  function open(i: SearchItem | undefined) {
    if (!i) return;
    onClose();
    window.location.hash = i.href;
  }

  return (
    <div className="app-search-back" ref={back} onMouseDown={(e) => e.target === back.current && onClose()}>
      <div className="app-search-dialog" role="dialog" aria-modal="true" aria-label="Search">
        <div className="app-search-input">
          <MagnifyingGlass size={20} aria-hidden="true" />
          <input
            autoFocus
            role="combobox"
            aria-expanded="true"
            aria-controls={listId}
            aria-activedescendant={hits.length > 0 ? `${listId}-${cur}` : undefined}
            aria-label="Search sites, site sections and pages"
            placeholder="Search sites, site sections and pages"
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
        <ul className="app-search-list" id={listId} role="listbox" aria-label="Results">
          {hits.length === 0 && <li className="app-search-empty">Nothing found.</li>}
          {hits.map((h, k) => {
            const head = k === 0 || hits[k - 1]!.group !== h.group;
            const Icon = h.icon;
            return (
              <li key={h.href} role="presentation">
                {head && (
                  <div className="app-search-group" role="presentation">
                    {h.group}
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
        <p className="app-search-foot">↑ ↓ move · Enter opens · Esc closes</p>
      </div>
    </div>
  );
}
