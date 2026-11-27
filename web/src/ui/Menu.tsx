// SPDX-License-Identifier: AGPL-3.0-or-later

import { Check } from "@phosphor-icons/react";
import { useEffect, useRef, useState, type ReactNode } from "react";

export type MenuItem = {
  label: string;
  icon?: ReactNode;
  /** Runs on a click; the menu closes first. */
  onSelect?: () => void;
  /** A link instead of an action, for example a download. */
  href?: string;
  download?: boolean;
  danger?: boolean;
  /** Makes the item one choice of several (menuitemradio); true marks the current one. */
  checked?: boolean;
};

type MenuProps = {
  /** The accessible name of the menu, for example "Actions for blog". */
  label: string;
  /** The content of the button that opens the menu. */
  button: ReactNode;
  /** The accessible name of the button when its content is only an icon. */
  buttonLabel?: string;
  /** The class of the button; a secondary button by default. */
  buttonClass?: string;
  /** A tooltip on the button (data-tip), for an icon button. */
  tip?: string;
  items: MenuItem[];
};

/**
 * Menu is a button that opens a list of actions. Arrow down opens it, the
 * arrow keys, Home and End move, Escape closes it and returns to the
 * button, Tab leaves it.
 */
export function Menu({ label, button, buttonLabel, buttonClass = "ui-btn ui-btn-secondary", tip, items }: MenuProps) {
  const [open, setOpen] = useState(false);
  const [at, setAt] = useState(0);
  const wrap = useRef<HTMLDivElement>(null);
  const btn = useRef<HTMLButtonElement>(null);
  const list = useRef<HTMLDivElement>(null);

  useEffect(() => {
    if (!open) return;
    list.current?.querySelectorAll<HTMLElement>("[role^=menuitem]")[at]?.focus();
  }, [open, at]);

  useEffect(() => {
    if (!open) return;
    const away = (e: MouseEvent) => {
      if (!wrap.current?.contains(e.target as Node)) setOpen(false);
    };
    document.addEventListener("mousedown", away);
    return () => document.removeEventListener("mousedown", away);
  }, [open]);

  if (items.length === 0) return null;

  function close() {
    setOpen(false);
    btn.current?.focus();
  }

  return (
    <div className="ui-menu-wrap" ref={wrap}>
      <button
        ref={btn}
        type="button"
        className={buttonClass}
        aria-haspopup="menu"
        aria-expanded={open}
        aria-label={buttonLabel}
        data-tip={tip}
        onClick={() => {
          setAt(Math.max(0, items.findIndex((i) => i.checked)));
          setOpen(!open);
        }}
        onKeyDown={(e) => {
          if (e.key === "ArrowDown") {
            e.preventDefault();
            setAt(Math.max(0, items.findIndex((i) => i.checked)));
            setOpen(true);
          }
        }}
      >
        {button}
      </button>
      {open && (
        <div
          className="ui-menu"
          role="menu"
          aria-label={label}
          ref={list}
          onKeyDown={(e) => {
            if (e.key === "Escape") close();
            else if (e.key === "ArrowDown") setAt((at + 1) % items.length);
            else if (e.key === "ArrowUp") setAt((at - 1 + items.length) % items.length);
            else if (e.key === "Home") setAt(0);
            else if (e.key === "End") setAt(items.length - 1);
            else if (e.key === "Tab") setOpen(false);
            else return;
            if (e.key !== "Tab") e.preventDefault();
          }}
        >
          {items.map((i, k) => {
            const common = {
              role: i.checked === undefined ? "menuitem" : "menuitemradio",
              "aria-checked": i.checked,
              tabIndex: k === at ? 0 : -1,
              "data-danger": i.danger ? "" : undefined,
            } as const;
            const body = (
              <>
                {i.icon}
                {i.label}
                {i.checked && <Check size={16} className="ui-menu-check" aria-hidden="true" />}
              </>
            );
            return i.href ? (
              <a key={i.label} {...common} href={i.href} download={i.download} onClick={() => setOpen(false)}>
                {body}
              </a>
            ) : (
              <button
                key={i.label}
                type="button"
                {...common}
                onClick={() => {
                  setOpen(false);
                  i.onSelect?.();
                }}
              >
                {body}
              </button>
            );
          })}
        </div>
      )}
    </div>
  );
}
