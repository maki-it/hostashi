// SPDX-License-Identifier: AGPL-3.0-or-later

// WordPress quick install of a PHP site (ADR-0069): types and API calls.

import { ApiError, request } from "./api";
import i18n from "./i18n";

export type WordPressState = "none" | "running" | "succeeded" | "failed";

export type WordPressStatus = {
  state: WordPressState;
  error?: string;
  log?: string;
  // The document root now: whether it holds WordPress, whether it is
  // empty, and the database its wp-config.php names.
  installed?: boolean;
  empty?: boolean;
  database?: string;
};

export type WordPressInput = {
  locale: string;
  title: string;
  adminUser: string;
  adminEmail: string;
  adminPassword?: string;
  discourage?: boolean;
};

export type WordPressStart = WordPressStatus & {
  database: string;
  // Set only when the panel generated it; shown once.
  adminPassword?: string;
  // The old WordPress database a reinstall dropped.
  droppedDatabase?: string;
};

export const wpLocales: { value: string; label: string }[] = [
  { value: "en_US", label: "English (United States)" },
  { value: "de_DE", label: "Deutsch" },
];

const path = (site: string) => `/sites/${encodeURIComponent(site)}/wordpress`;

export function getWordPress(site: string): Promise<WordPressStatus> {
  return request<WordPressStatus>("GET", path(site));
}

export function installWordPress(site: string, input: WordPressInput): Promise<WordPressStart> {
  return request<WordPressStart>("POST", path(site), input);
}

// reinstallWordPress empties the document root and installs a new
// WordPress; confirm is the site's name.
export function reinstallWordPress(site: string, input: WordPressInput, confirm: string): Promise<WordPressStart> {
  return request<WordPressStart>("POST", path(site) + "/reinstall", {
    ...input,
    confirm,
  });
}

export function wordPressMessage(err: unknown): string {
  if (err instanceof ApiError) {
    if (err.code === "wordpress_unavailable" || err.code === "not_found") return i18n.t(`sites.wordpress.errors.${err.code}`);
    if (err.code === "invalid_argument")
      return err.message
        .replace(/^hosting: invalid input: (wordpress: )?/, "")
        .replace(/^./, (c) => c.toUpperCase())
        .replace(/\.?$/, ".");
    if (/not empty/.test(err.message)) return i18n.t("sites.wordpress.errors.notEmpty");
    return err.message.replace(/^wpunit: /, "").replace(/^./, (c) => c.toUpperCase());
  }
  return i18n.t("sites.wordpress.errors.generic");
}
