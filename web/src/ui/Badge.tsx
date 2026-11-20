// SPDX-License-Identifier: AGPL-3.0-or-later

export type Tone = "ok" | "warn" | "err" | "off";

/** A state shown as a tint and always a word, never colour alone. */
export function Badge({ tone, children }: { tone: Tone; children: string }) {
  return <span className={`ui-badge ui-tone-${tone}`}>{children}</span>;
}
