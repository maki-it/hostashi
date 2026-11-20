// SPDX-License-Identifier: AGPL-3.0-or-later

import type { ReactNode } from "react";

type CardProps = {
  title?: string;
  /** Buttons or links on the right of the header. */
  actions?: ReactNode;
  /** false for content that brings its own spacing, such as a table. */
  padded?: boolean;
  children: ReactNode;
};

/** A titled section. With a title it is a labelled region. */
export function Card({ title, actions, padded = true, children }: CardProps) {
  const head =
    title !== undefined ? (
      <div className="ui-card-head">
        <h2>{title}</h2>
        {actions !== undefined && <div className="ui-card-actions">{actions}</div>}
      </div>
    ) : null;
  const body = padded ? <div className="ui-card-body">{children}</div> : children;
  if (title === undefined) return <div className="ui-card">{body}</div>;
  return (
    <section className="ui-card" aria-label={title}>
      {head}
      {body}
    </section>
  );
}
