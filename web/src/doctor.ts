// SPDX-License-Identifier: AGPL-3.0-or-later

// The read-only checks of hostashi doctor, run by the agent on request.

import { request } from "./api";
import i18n, { areaErrorText } from "./i18n";

export type DoctorStatus = "ok" | "warn" | "fail";

export type DoctorCheck = {
  id: string;
  site?: string;
  status: DoctorStatus;
  summary: string;
  hint?: string;
};

export type DoctorReport = {
  status: DoctorStatus;
  checkedAt: string;
  checks: DoctorCheck[];
  counts: Record<DoctorStatus, number>;
};

export const runDoctor = () => request<DoctorReport>("GET", "/doctor");

/** checkTitle is the name of a check in the current language, its ID when it has none. */
export function checkTitle(id: string): string {
  const key = `server.doctor.checks.${id.replaceAll(".", "_")}`;
  return i18n.exists(key) ? (i18n.t as (k: string) => string)(key) : id;
}

export const doctorMessage = (err: unknown): string => areaErrorText("server.doctor.errors", err);
