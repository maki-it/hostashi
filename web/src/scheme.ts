// SPDX-License-Identifier: AGPL-3.0-or-later

import { request } from "./api";

// The colour scheme of an account (ADR-0037): "system" follows the
// browser, which is also what the login page does.
export type ColorScheme = "system" | "light" | "dark";

/** applyScheme sets <html data-scheme>, which the tokens read; "system" removes it. */
export function applyScheme(s: ColorScheme) {
  const el = document.documentElement;
  if (s === "system") delete el.dataset.scheme;
  else el.dataset.scheme = s;
}

const systemDark = () => (typeof window.matchMedia === "function" ? window.matchMedia("(prefers-color-scheme: dark)").matches : false);

/** isDark reports whether a scheme shows dark colours on this browser now. */
export const isDark = (s: ColorScheme) => s === "dark" || (s === "system" && systemDark());

/** saveColorScheme stores the scheme for the logged-in account. */
export async function saveColorScheme(colorScheme: ColorScheme): Promise<ColorScheme> {
  const r = await request<{ colorScheme: ColorScheme; theme: string }>("PATCH", "/me", { colorScheme });
  return r.colorScheme;
}
