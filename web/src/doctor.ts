// SPDX-License-Identifier: AGPL-3.0-or-later

// The read-only checks of hostashi doctor, run by the agent on request.

import { ApiError, request } from "./api";

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

const titles: Record<string, string> = {
  "system.os": "Operating system",
  "system.disk": "Free space",
  "system.quota": "Disk limits",
  "system.proc": "Process privacy",
  "system.pool": "Home directories",
  "service.panel": "Panel",
  "service.nginx": "Web server",
  "service.mariadb": "MariaDB",
  "service.fail2ban": "Brute-force protection",
  "site.home": "Home directory",
  "site.disk": "Disk use",
  "site.limits": "Resource limits",
  "site.tls": "Certificate",
};

export const checkTitle = (id: string) => titles[id] ?? id;

export function doctorMessage(err: unknown): string {
  if (err instanceof ApiError) {
    switch (err.code) {
      case "forbidden":
        return "Only administrators can run the checks.";
      case "agent_timeout":
        return "The checks took too long. Try again.";
      case "agent_unavailable":
        return "The checks could not be run. Try again.";
      default:
        return err.message ? err.message.charAt(0).toUpperCase() + err.message.slice(1) + "." : "Something went wrong (" + err.code + ").";
    }
  }
  if (err instanceof TypeError) return "The server is not reachable.";
  return "Something went wrong. Try again.";
}
