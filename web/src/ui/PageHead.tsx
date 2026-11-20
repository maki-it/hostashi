// SPDX-License-Identifier: AGPL-3.0-or-later

import type { ReactNode } from "react";

type PageHeadProps = {
  title: string;
  description?: ReactNode;
  /** Usually one primary action, top right. */
  actions?: ReactNode;
};

/** The title of a page with a short description and its main action. */
export function PageHead({ title, description, actions }: PageHeadProps) {
  return (
    <div className="ui-page-head">
      <div>
        <h1>{title}</h1>
        {description !== undefined && <p>{description}</p>}
      </div>
      {actions !== undefined && <div className="ui-page-actions">{actions}</div>}
    </div>
  );
}
