// SPDX-License-Identifier: AGPL-3.0-or-later

// Numbers, dates and sizes in the panel's language (ADR-0071). Everything
// the UI formats goes through here instead of toLocaleString with a fixed
// or missing locale, so a German account sees "1.234" and "27.11.2026".
import i18n, { currentLang, type Lang } from "./i18n";

// English keeps the day-month order and the 24-hour clock the panel used
// before it had languages.
const locales: Record<Lang, string> = { en: "en-GB", de: "de-DE" };

/** locale is the BCP 47 tag for Intl in the current language. */
export const locale = (): string => locales[currentLang()];

// Intl objects are not cheap to build; keep one per language and options.
const cache = new Map<string, Intl.NumberFormat | Intl.DateTimeFormat | Intl.RelativeTimeFormat | Intl.Collator>();
function cached<T extends Intl.NumberFormat | Intl.DateTimeFormat | Intl.RelativeTimeFormat | Intl.Collator>(kind: string, opts: object, make: (l: string) => T): T {
  const l = locale();
  const key = `${kind}|${l}|${JSON.stringify(opts)}`;
  let f = cache.get(key);
  if (!f) {
    f = make(l);
    cache.set(key, f);
  }
  return f as T;
}

/** formatNumber writes n with the language's separators: "1,234.5" or "1.234,5". */
export const formatNumber = (n: number, opts: Intl.NumberFormatOptions = {}): string =>
  cached("n", opts, (l) => new Intl.NumberFormat(l, opts)).format(n);

/** formatCount writes a whole number, such as a count of files. */
export const formatCount = (n: number): string => formatNumber(n, { maximumFractionDigits: 0 });

const valid = (d: Date) => !Number.isNaN(d.getTime());

/** formatDate: "27 Nov 2026" or "27.11.2026"; "" for an invalid date. */
export const formatDate = (d: Date, opts: Intl.DateTimeFormatOptions = { dateStyle: "medium" }): string =>
  valid(d) ? cached("d", opts, (l) => new Intl.DateTimeFormat(l, opts)).format(d) : "";

/** formatDateTime: "27 Nov 2026, 14:05" or "27.11.2026, 14:05"; "" for an invalid date. */
export const formatDateTime = (d: Date, opts: Intl.DateTimeFormatOptions = { dateStyle: "medium", timeStyle: "short" }): string =>
  formatDate(d, opts);

const units: [Intl.RelativeTimeFormatUnit, number][] = [
  ["year", 365 * 24 * 3600],
  ["month", 30 * 24 * 3600],
  ["week", 7 * 24 * 3600],
  ["day", 24 * 3600],
  ["hour", 3600],
  ["minute", 60],
];

/** formatRelative: "3 days ago", "in 2 hours", "just now"; "" for an invalid date. */
export function formatRelative(date: Date, now: Date = new Date()): string {
  if (!valid(date)) return "";
  const secs = Math.round((date.getTime() - now.getTime()) / 1000);
  const rtf = cached("r", { numeric: "auto" }, (l) => new Intl.RelativeTimeFormat(l, { numeric: "auto" }));
  for (const [unit, size] of units) {
    if (Math.abs(secs) >= size) return rtf.format(Math.trunc(secs / size), unit);
  }
  return i18n.t("common.time.justNow");
}

const KiB = 1024;
const sizes: [string, number][] = [
  ["TiB", KiB ** 4],
  ["GiB", KiB ** 3],
  ["MiB", KiB ** 2],
  ["KiB", KiB],
];

/**
 * formatBytes writes a size in binary units with at most one decimal:
 * "1.5 GiB", in German "1,5 GiB". The units stay the same in every
 * language.
 */
export function formatBytes(n: number): string {
  for (const [unit, size] of sizes) {
    if (Math.abs(n) >= size) return `${formatNumber(n / size, { maximumFractionDigits: 1 })} ${unit}`;
  }
  return `${formatNumber(n, { maximumFractionDigits: 0 })} B`;
}

/** compare sorts text the way the language does, with numbers by value ("site2" before "site10"). */
export const compare = (a: string, b: string): number =>
  cached("c", {}, (l) => new Intl.Collator(l, { numeric: true, sensitivity: "base" })).compare(a, b);
