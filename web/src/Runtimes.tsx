// SPDX-License-Identifier: AGPL-3.0-or-later

import type { ReactNode } from "react";
import { formatHash, type Page } from "./routes";
import { PageHead } from "./ui";

/** The versions pages of the languages, one tab each on the Runtimes page. */
export const runtimes: readonly { page: Page; label: string }[] = [
  { page: "php", label: "PHP" },
  { page: "node", label: "Node.js" },
  { page: "python", label: "Python" },
];

/**
 * RuntimesHead is the head of the PHP, Node.js and Python versions pages:
 * one Runtimes page with a tab per language (C2 of the UX review). The
 * tabs are links, so each keeps its own address.
 */
export function RuntimesHead({ active, description }: { active: Page; description: ReactNode }) {
  return (
    <>
      <PageHead title="Runtimes" description={description} />
      <nav className="ui-seg ui-seg-nav" aria-label="Runtimes">
        {runtimes.map((r) => (
          <a key={r.page} className="ui-seg-btn" href={formatHash({ page: r.page })} aria-current={r.page === active ? "page" : undefined}>
            {r.label}
          </a>
        ))}
      </nav>
    </>
  );
}
