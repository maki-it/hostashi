// SPDX-License-Identifier: AGPL-3.0-or-later

const units: [Intl.RelativeTimeFormatUnit, number][] = [
  ["year", 365 * 24 * 3600],
  ["month", 30 * 24 * 3600],
  ["week", 7 * 24 * 3600],
  ["day", 24 * 3600],
  ["hour", 3600],
  ["minute", 60],
];

const rtf = new Intl.RelativeTimeFormat("en", { numeric: "auto" });
const exactFormat = new Intl.DateTimeFormat("en-GB", { dateStyle: "medium", timeStyle: "short" });

/** "3 days ago", "in 2 hours", "just now". Invalid input gives "". */
export function relativeTime(date: Date, now: Date = new Date()): string {
  const t = date.getTime();
  if (Number.isNaN(t)) return "";
  const secs = Math.round((t - now.getTime()) / 1000);
  if (Math.abs(secs) < 60) return "just now";
  for (const [unit, size] of units) {
    if (Math.abs(secs) >= size) return rtf.format(Math.trunc(secs / size), unit);
  }
  return "just now";
}

/** The exact local time, for example "19 Nov 2026, 09:00". */
export const exactTime = (date: Date) => (Number.isNaN(date.getTime()) ? "" : exactFormat.format(date));

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
