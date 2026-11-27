// SPDX-License-Identifier: AGPL-3.0-or-later

// The panel's languages (ADR-0071): English, the default and fallback,
// and German. The catalogs ship with the bundle, so i18next starts
// synchronously and the first frame is already in the right language.
import i18n from "i18next";
import { initReactI18next } from "react-i18next";
import { ApiError, request } from "./api";
import de from "./locales/de";
import en, { type Catalog } from "./locales/en";

/** The languages of the UI. */
export const languages = ["en", "de"] as const;
export type Lang = (typeof languages)[number];

/** An account's setting: "auto" follows the browser. */
export type Language = "auto" | Lang;

declare module "i18next" {
  interface CustomTypeOptions {
    defaultNS: "translation";
    resources: { translation: Catalog };
  }
}

const isLang = (s: string): s is Lang => (languages as readonly string[]).includes(s);

/**
 * resolveLanguage turns a setting into the language shown. "auto" takes the
 * first browser language the panel has, by its primary subtag ("de-AT"
 * is German), and English when there is none.
 */
export function resolveLanguage(setting: Language, browser: readonly string[] = browserLanguages()): Lang {
  if (setting !== "auto") return setting;
  for (const tag of browser) {
    const primary = tag.toLowerCase().split("-")[0] ?? "";
    if (isLang(primary)) return primary;
  }
  return "en";
}

function browserLanguages(): readonly string[] {
  if (typeof navigator === "undefined") return [];
  if (Array.isArray(navigator.languages) && navigator.languages.length > 0) return navigator.languages;
  return navigator.language ? [navigator.language] : [];
}

void i18n.use(initReactI18next).init({
  resources: { en: { translation: en }, de: { translation: de } },
  lng: resolveLanguage("auto"),
  fallbackLng: "en",
  supportedLngs: [...languages],
  // The catalogs are in the bundle: no loading, no waiting.
  initAsync: false,
  // React escapes what it renders; escaping here would show "&amp;".
  interpolation: { escapeValue: false },
  returnNull: false,
});
document.documentElement.lang = i18n.language;

/** currentLang is the language shown now. */
export const currentLang = (): Lang => (isLang(i18n.language) ? i18n.language : "en");

/** setLanguage shows the panel in the language of a setting and returns it. */
export function setLanguage(setting: Language): Lang {
  const lang = resolveLanguage(setting);
  if (i18n.language !== lang) void i18n.changeLanguage(lang);
  document.documentElement.lang = lang;
  return lang;
}

/** saveLanguage stores the language setting of the logged-in account. */
export async function saveLanguage(language: Language): Promise<Language> {
  const r = await request<{ language: Language }>("PATCH", "/me", { language });
  return r.language;
}

/**
 * apiErrorText is the sentence for an error of the API in the current
 * language: errors.<code> when the catalog has it, else the server's
 * English message (ADR-0071). Other errors say the server is not
 * reachable (fetch failed) or that something went wrong.
 */
export function apiErrorText(err: unknown): string {
  if (err instanceof ApiError) {
    const key = `errors.${err.code}`;
    if (i18n.exists(key)) return i18n.t(key as "errors.busy");
    const m = err.message.trim();
    return m === "" ? i18n.t("common.errors.unknown", { code: err.code }) : m.charAt(0).toUpperCase() + m.slice(1) + (/[.!?]$/.test(m) ? "" : ".");
  }
  if (err instanceof TypeError) return i18n.t("common.errors.unreachable");
  return i18n.t("common.errors.generic");
}

export default i18n;
