// SPDX-License-Identifier: AGPL-3.0-or-later

import type { ButtonHTMLAttributes, ReactNode } from "react";

export type ButtonVariant = "primary" | "secondary" | "danger";

type ButtonProps = Omit<ButtonHTMLAttributes<HTMLButtonElement>, "className"> & {
  variant?: ButtonVariant;
  /** A Phosphor icon element, shown before the label. */
  icon?: ReactNode;
  children: ReactNode;
};

/** A labelled button. Final actions (dialog footers, forms) always use this. */
export function Button({ variant = "secondary", icon, children, type = "button", ...rest }: ButtonProps) {
  const cls = variant === "secondary" ? "ui-btn" : `ui-btn ui-btn-${variant}`;
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
};

/**
 * An icon-only button for toolbars and table rows. The label is the
 * accessible name and shows as a tooltip on hover and keyboard focus.
 */
export function IconButton({ label, icon, danger = false, tipBelow = false, type = "button", ...rest }: IconButtonProps) {
  const cls = ["ui-icon-btn", danger && "ui-danger", tipBelow && "ui-tip-below"].filter(Boolean).join(" ");
  return (
    <button type={type} className={cls} aria-label={label} data-tip={label} {...rest}>
      {icon}
    </button>
  );
}

/** Groups the icon buttons of one table row. */
export function RowActions({ children }: { children: ReactNode }) {
  return <div className="ui-row-actions">{children}</div>;
}
