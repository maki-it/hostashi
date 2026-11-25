// SPDX-License-Identifier: AGPL-3.0-or-later

import type { ButtonHTMLAttributes, ReactNode } from "react";

export type ButtonVariant = "primary" | "secondary" | "danger";

type ButtonProps = Omit<ButtonHTMLAttributes<HTMLButtonElement>, "className"> & {
  variant?: ButtonVariant;
  /** A Phosphor icon element, shown before the label. */
  icon?: ReactNode;
  /** A smaller button, for actions next to a value or in a table row. */
  small?: boolean;
  children: ReactNode;
};

/** A labelled button. Final actions (dialog footers, forms) always use this. */
export function Button({ variant = "secondary", icon, small = false, children, type = "button", ...rest }: ButtonProps) {
  const cls = ["ui-btn", variant !== "secondary" && `ui-btn-${variant}`, small && "ui-btn-sm"].filter(Boolean).join(" ");
  return (
    <button type={type} className={cls} {...rest}>
      {icon}
      {children}
    </button>
  );
}

type IconButtonProps = Omit<ButtonHTMLAttributes<HTMLButtonElement>, "className" | "children" | "aria-label"> & {
  /** Accessible name and visible tooltip text. */
  label: string;
  icon: ReactNode;
  danger?: boolean;
  /** Opens the tooltip below the button, for the first row of a table. */
  tipBelow?: boolean;
  /** A short visible word, part of label; the button then shows icon and text. */
  text?: string;
};

/**
 * An icon-only button for toolbars and table rows. The label is the
 * accessible name and shows as a tooltip on hover and keyboard focus.
 */
export function IconButton({ label, icon, danger = false, tipBelow = false, text, type = "button", ...rest }: IconButtonProps) {
  if (text !== undefined) {
    return (
      <button type={type} className={danger ? "ui-btn ui-btn-sm ui-btn-danger" : "ui-btn ui-btn-sm"} aria-label={label === text ? undefined : label} {...rest}>
        {icon}
        {text}
      </button>
    );
  }
  const cls = ["ui-icon-btn", danger && "ui-danger", tipBelow && "ui-tip-below"].filter(Boolean).join(" ");
  return (
    <button type={type} className={cls} aria-label={label} data-tip={label} {...rest}>
      {icon}
    </button>
  );
}

type IconLinkProps = {
  label: string;
  icon: ReactNode;
  href: string;
  tipBelow?: boolean;
  /** A short visible word, part of label; the link then shows icon and text. */
  text?: string;
};

/** An icon-only link that looks like an IconButton, for row actions that open a page. */
export function IconLink({ label, icon, href, tipBelow = false, text }: IconLinkProps) {
  if (text !== undefined) {
    return (
      <a className="ui-btn ui-btn-sm" href={href} aria-label={label === text ? undefined : label}>
        {icon}
        {text}
      </a>
    );
  }
  return (
    <a className={tipBelow ? "ui-icon-btn ui-tip-below" : "ui-icon-btn"} href={href} aria-label={label} data-tip={label}>
      {icon}
    </a>
  );
}

/** Groups the icon buttons of one table row. */
export function RowActions({ children }: { children: ReactNode }) {
  return <div className="ui-row-actions">{children}</div>;
}
