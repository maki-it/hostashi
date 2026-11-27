// SPDX-License-Identifier: AGPL-3.0-or-later

import { request } from "./api";

// The colour scheme of an account (ADR-0037): "system" follows the
// browser, which is also what the login page does.
export type ColorScheme = "system" | "light" | "dark";

/** schemes lists the choices in the order the account page and the top bar show them. */
export const schemes: readonly ColorScheme[] = ["system", "light", "dark"];

/** applyScheme sets <html data-scheme>, which the tokens read; "system" removes it. */
export function applyScheme(s: ColorScheme) {
  const el = document.documentElement;
  if (s === "system") delete el.dataset.scheme;
  else el.dataset.scheme = s;
}

/** saveColorScheme stores the scheme for the logged-in account. */
export async function saveColorScheme(colorScheme: ColorScheme): Promise<ColorScheme> {
  const r = await request<{ colorScheme: ColorScheme; theme: string }>("PATCH", "/me", { colorScheme });
  return r.colorScheme;
}
