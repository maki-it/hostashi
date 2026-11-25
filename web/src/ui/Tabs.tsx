// SPDX-License-Identifier: AGPL-3.0-or-later

import { useRef, type KeyboardEvent } from "react";

export type Tab = { id: string; label: string };

type TabsProps = {
  /** Accessible name of the tab list, for example the site's name. */
  label: string;
  tabs: readonly Tab[];
  active: string;
  onChange: (id: string) => void;
  /** Prefix for the tab and panel ids; the panel uses `${idPrefix}-panel`. */
  idPrefix: string;
};

/**
 * A tab list (WAI-ARIA tabs pattern, automatic activation): arrow keys,
 * Home and End move between tabs, only the active tab is in the tab order.
 * The caller renders the panel with tabPanelProps().
 */
export function Tabs({ label, tabs, active, onChange, idPrefix }: TabsProps) {
  const refs = useRef(new Map<string, HTMLButtonElement>());

  const move = (e: KeyboardEvent, index: number) => {
    let next: number;
    if (e.key === "ArrowRight") next = (index + 1) % tabs.length;
    else if (e.key === "ArrowLeft") next = (index - 1 + tabs.length) % tabs.length;
    else if (e.key === "Home") next = 0;
    else if (e.key === "End") next = tabs.length - 1;
    else return;
    e.preventDefault();
    const tab = tabs[next];
    if (!tab) return;
    onChange(tab.id);
    refs.current.get(tab.id)?.focus();
  };

  return (
    <div className="ui-tabs" role="tablist" aria-label={label}>
      {tabs.map((t, i) => (
        <button
          key={t.id}
          ref={(el) => {
            if (el) refs.current.set(t.id, el);
            else refs.current.delete(t.id);
          }}
          type="button"
          role="tab"
          className="ui-tab"
          id={`${idPrefix}-tab-${t.id}`}
          aria-selected={t.id === active}
          aria-controls={`${idPrefix}-panel`}
          tabIndex={t.id === active ? 0 : -1}
          onClick={() => onChange(t.id)}
          onKeyDown={(e) => move(e, i)}
        >
          {t.label}
        </button>
      ))}
    </div>
  );
}

/** Props for the element that shows the active tab's content. */
export function tabPanelProps(idPrefix: string, active: string) {
  return {
    role: "tabpanel",
    id: `${idPrefix}-panel`,
    "aria-labelledby": `${idPrefix}-tab-${active}`,
    tabIndex: 0,
  } as const;
}
