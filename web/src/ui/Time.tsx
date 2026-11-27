// SPDX-License-Identifier: AGPL-3.0-or-later

import { formatDateTime, formatRelative } from "../format";

/** "3 days ago", "in 2 hours", "just now", in the panel's language. Invalid input gives "". */
export const relativeTime = (date: Date, now: Date = new Date()): string => formatRelative(date, now);

/** The exact local time, for example "19 Nov 2026, 09:00" or "19.11.2026, 09:00". */
export const exactTime = (date: Date) => formatDateTime(date);

/**
 * A relative time with the exact time in a tooltip (ADR-0037). The tooltip
 * shows on hover and on keyboard focus; screen readers get the exact time
 * from the label.
 */
export function RelTime({ iso, now }: { iso: string; now?: Date }) {
  const d = new Date(iso);
  const rel = relativeTime(d, now);
  if (rel === "") return <span>{iso || "—"}</span>;
  const exact = exactTime(d);
  return (
    <time className="ui-time" dateTime={iso} data-tip={exact} tabIndex={0} aria-label={`${rel}, ${exact}`}>
      {rel}
    </time>
  );
}
