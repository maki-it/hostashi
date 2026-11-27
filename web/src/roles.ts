// SPDX-License-Identifier: AGPL-3.0-or-later

import i18n from "./i18n";

const known = ["admin", "manager", "user"] as const;

/** roleLabel names a role (ADR-0035) in the current language; an unknown role stays as it is. */
export function roleLabel(role: string): string {
  return (known as readonly string[]).includes(role) ? i18n.t(`common.roles.${role as (typeof known)[number]}`) : role;
}
