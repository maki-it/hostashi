// SPDX-License-Identifier: AGPL-3.0-or-later

// Resetting the file permissions of a site (ADR-0067): every file below the
// home goes back to the site user, with the default modes.

import { ApiError, request } from "./api";
import i18n from "./i18n";

export type PermissionsSkip = { path: string; reason: string };

export type PermissionsReset = {
  checked: number;
  changed: number;
  skipped: number;
  /** At most 50 entries, relative to the home. */
  skippedPaths: PermissionsSkip[];
};

export const resetPermissions = (site: string) =>
  request<PermissionsReset>("POST", "/sites/" + encodeURIComponent(site) + "/permissions/reset");

/** resetMessage explains the errors that are special to a reset. */
export function resetMessage(err: unknown): string | null {
  if (!(err instanceof ApiError)) return null;
  switch (err.code) {
    case "reset_incomplete":
    case "permissions_unavailable":
      return i18n.t(`permissions.errors.${err.code}`);
    default:
      return null;
  }

}
