// SPDX-License-Identifier: AGPL-3.0-or-later

import { CheckCircle, Info, Warning, XCircle } from "@phosphor-icons/react";
import type { ReactNode } from "react";

export type NoticeTone = "info" | "ok" | "warn" | "err";

const icons = { info: Info, ok: CheckCircle, warn: Warning, err: XCircle };
const toneClass = { info: "ui-tone-off", ok: "ui-tone-ok", warn: "ui-tone-warn", err: "ui-tone-err" };

/**
 * A message at the top of a page or card. Errors are announced at once
 * (role "alert"); the other tones politely (role "status").
 */
export function Notice({ tone, title, children }: { tone: NoticeTone; title: string; children?: ReactNode }) {
  const Icon = icons[tone];
  return (
    <div className={`ui-notice ${toneClass[tone]}`} role={tone === "err" ? "alert" : "status"}>
      <Icon size={20} aria-hidden="true" />
      <div className="ui-notice-body">
        <strong>{title}</strong>
        {children !== undefined && <p>{children}</p>}
      </div>
    </div>
  );
}
