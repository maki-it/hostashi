// SPDX-License-Identifier: AGPL-3.0-or-later

// More PHP versions from sury.org, installed from the panel (ADR-0036).

import { ApiError, request } from "./api";
import { formatDate } from "./format";
import i18n from "./i18n";
import type { PHPVersions } from "./sites";

export type PHPInstallState = "idle" | "running" | "succeeded" | "failed";

export type PHPInstall = {
  version?: string;
  /** What the last run did; missing while idle. */
  action?: "install" | "remove";
  state: PHPInstallState;
  log: string[];
  repository: boolean;
  newer: string[];
};

export const getPHPInstall = () => request<PHPInstall>("GET", "/php/install");

export const installPHP = (version: string, confirmEol: boolean) =>
  request<PHPInstall>("POST", "/php/install", { version, confirmEol });

export const removePHP = (version: string) => request<PHPInstall>("POST", "/php/remove", { version });

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
  const e = (k: "forbidden" | "running" | "alreadyInstalled" | "inUse" | "notInstalled" | "notStarted" | "php_default" | "timeout") =>
    i18n.t(`server.runtimes.php.errors.${k}`);
  if (err instanceof ApiError) {
    switch (err.code) {
      case "forbidden":
        return e("forbidden");
      case "php_install_refused":
        return err.message.includes("already running")
          ? e("running")
          : err.message.includes("already installed")
            ? e("alreadyInstalled")
            : err.message.includes("still run")
              ? e("inUse")
              : err.message.includes("not installed")
                ? e("notInstalled")
                : e("notStarted");
      case "php_in_use":
        return e("inUse");
      case "php_default":
        return e("php_default");
      case "agent_timeout":
      case "agent_unavailable":
        return e("timeout");
    }
  }
  return i18n.t("common.errors.generic");
}

/** formatDay writes a day of the form YYYY-MM-DD in the current language; anything else stays as it is. */
export const formatDay = (day: string): string =>
  /^\d{4}-\d{2}-\d{2}$/.test(day) ? formatDate(new Date(day + "T00:00:00Z"), { dateStyle: "medium", timeZone: "UTC" }) || day : day;
