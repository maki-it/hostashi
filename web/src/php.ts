// SPDX-License-Identifier: AGPL-3.0-or-later

// More PHP versions from sury.org, installed from the panel (ADR-0036).

import { ApiError, request } from "./api";
import type { PHPVersions } from "./sites";

export type PHPInstallState = "idle" | "running" | "succeeded" | "failed";

export type PHPInstall = {
  version?: string;
  state: PHPInstallState;
  log: string[];
  repository: boolean;
  newer: string[];
};

export const getPHPInstall = () => request<PHPInstall>("GET", "/php/install");

export const installPHP = (version: string, confirmEol: boolean) =>
  request<PHPInstall>("POST", "/php/install", { version, confirmEol });

export type Support = "active" | "security" | "eol";

// support says what upstream still does for a version on day today
// (YYYY-MM-DD): bug fixes, security fixes only, or nothing.
export function support(v: PHPVersions, version: string, today: string): Support {
  const sec = v.securityUntil?.[version];
  if (!sec || today > sec) return "eol";
  const act = v.activeUntil?.[version];
  if (act && today <= act) return "active";
  return "security";
}

// daysUntil counts the days from today to day (both YYYY-MM-DD).
export const daysUntil = (day: string, today: string) =>
  Math.round((Date.parse(day + "T00:00:00Z") - Date.parse(today + "T00:00:00Z")) / 86_400_000);

export const todayUTC = () => new Date().toISOString().slice(0, 10);

export function phpMessage(err: unknown): string {
  if (err instanceof ApiError) {
    switch (err.code) {
      case "forbidden":
        return "Only administrators can install PHP versions.";
      case "php_install_refused":
        return err.message.includes("already running")
          ? "Another installation is still running. Wait until it is done."
          : err.message.includes("already installed")
            ? "This version is already installed."
            : "The installation could not start.";
      case "agent_timeout":
      case "agent_unavailable":
        return "The server did not answer. Try again in a moment.";
    }
  }
  return "Something went wrong. Try again.";
}
