// SPDX-License-Identifier: AGPL-3.0-or-later

// Counts beside the Health and Services entries of the sidebar: how many
// things on that page need someone to look.

import type { DoctorReport } from "./doctor";
import { stateWord, type ServiceRow } from "./services";

export type NavCount = { n: number; tone: "err" | "warn" };

/**
 * healthCount counts the doctor's failed and warning checks. It is red
 * when one failed, amber when there are only warnings.
 */
export function healthCount(r: Pick<DoctorReport, "checks">): NavCount | null {
  const checks = Array.isArray(r?.checks) ? r.checks : [];
  const fail = checks.filter((c) => c.status === "fail").length;
  const warn = checks.filter((c) => c.status === "warn").length;
  if (fail + warn === 0) return null;
  return { n: fail + warn, tone: fail > 0 ? "err" : "warn" };
}

/** servicesCount counts the services that failed. */
export function servicesCount(rows: Pick<ServiceRow, "installed" | "active">[]): NavCount | null {
  const n = (Array.isArray(rows) ? rows : []).filter((r) => stateWord(r).tone === "err").length;
  return n === 0 ? null : { n, tone: "err" };
}

/** countLabel is the accessible text of a count. */
export function countLabel(c: NavCount): string {
  if (c.tone === "err") return c.n === 1 ? "1 problem" : `${c.n} problems`;
  return c.n === 1 ? "1 warning" : `${c.n} warnings`;
}
