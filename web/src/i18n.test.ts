// SPDX-License-Identifier: AGPL-3.0-or-later

import { describe, expect, it } from "vitest";
import { ApiError } from "./api";
import { message } from "./auth";
import { compare, formatBytes, formatCount, formatDate, formatDateTime, formatNumber, formatRelative } from "./format";
import i18n, { apiErrorText, resolveLanguage, setLanguage } from "./i18n";

describe("resolveLanguage", () => {
  it("keeps a fixed language", () => {
    expect(resolveLanguage("de", ["en-US"])).toBe("de");
    expect(resolveLanguage("en", ["de-DE"])).toBe("en");
  });
  it("follows the first browser language the panel has", () => {
    expect(resolveLanguage("auto", ["de-AT", "en"])).toBe("de");
    expect(resolveLanguage("auto", ["fr-FR", "DE", "en"])).toBe("de");
    expect(resolveLanguage("auto", ["en-GB", "de"])).toBe("en");
  });
  it("falls back to English", () => {
    expect(resolveLanguage("auto", ["fr", "nl"])).toBe("en");
    expect(resolveLanguage("auto", [])).toBe("en");
    expect(resolveLanguage("auto", [""])).toBe("en");
  });
});

describe("setLanguage", () => {
  it("switches the texts and <html lang>", () => {
    setLanguage("de");
    expect(i18n.t("shell.logout")).toBe("Abmelden");
    expect(document.documentElement.lang).toBe("de");
    setLanguage("en");
    expect(i18n.t("shell.logout")).toBe("Log out");
    expect(document.documentElement.lang).toBe("en");
  });
});

describe("API errors", () => {
  it("come from the catalog by code", () => {
    const err = new ApiError(401, "invalid_credentials", "invalid credentials");
    expect(apiErrorText(err)).toBe("Wrong username or password.");
    setLanguage("de");
    expect(apiErrorText(err)).toBe("Benutzername oder Passwort ist falsch.");
    expect(message(err)).toBe("Benutzername oder Passwort ist falsch.");
  });
  it("fall back to the server's English message", () => {
    setLanguage("de");
    expect(apiErrorText(new ApiError(422, "invalid_argument", "name is too long"))).toBe("Name is too long.");
    expect(apiErrorText(new ApiError(409, "site_exists", "A site with this name exists."))).toBe("A site with this name exists.");
    expect(apiErrorText(new ApiError(500, "internal", ""))).toBe("Etwas ist schiefgelaufen (internal). Versuch es noch einmal.");
  });
  it("say when the server is not reachable", () => {
    expect(apiErrorText(new TypeError("Failed to fetch"))).toBe("The server is not reachable.");
    expect(apiErrorText(new Error("x"))).toBe("Something went wrong. Try again.");
  });
  it("name an unknown code on the login form", () => {
    expect(message(new ApiError(500, "internal", "internal error"))).toBe("Something went wrong (internal). Try again.");
    expect(message(new DOMException("closed", "NotAllowedError"))).toBe("The passkey prompt was closed or timed out.");
  });
});

describe("format", () => {
  const d = new Date(2026, 10, 27, 14, 5);
  it("writes numbers, dates and sizes in English", () => {
    expect(formatNumber(1234.5)).toBe("1,234.5");
    expect(formatCount(1234567)).toBe("1,234,567");
    expect(formatDate(d)).toBe("27 Nov 2026");
    expect(formatDateTime(d)).toBe("27 Nov 2026, 14:05");
    expect(formatBytes(1536 * 1024 ** 2)).toBe("1.5 GiB");
    expect(formatBytes(512)).toBe("512 B");
    expect(formatBytes(10 * 1024)).toBe("10 KiB");
    expect(formatBytes(3 * 1024 ** 4)).toBe("3 TiB");
  });
  it("writes them in German", () => {
    setLanguage("de");
    expect(formatNumber(1234.5)).toBe("1.234,5");
    expect(formatCount(1234567)).toBe("1.234.567");
    expect(formatDate(d)).toBe("27.11.2026");
    expect(formatDateTime(d)).toBe("27.11.2026, 14:05");
    expect(formatBytes(1536 * 1024 ** 2)).toBe("1,5 GiB");
  });
  it("writes relative times", () => {
    const now = new Date(2026, 10, 27, 12, 0);
    expect(formatRelative(new Date(2026, 10, 24, 12, 0), now)).toBe("3 days ago");
    expect(formatRelative(new Date(2026, 10, 27, 14, 0), now)).toBe("in 2 hours");
    expect(formatRelative(new Date(2026, 10, 27, 11, 59, 30), now)).toBe("just now");
    setLanguage("de");
    expect(formatRelative(new Date(2026, 10, 24, 12, 0), now)).toBe("vor 3 Tagen");
    expect(formatRelative(new Date(2026, 10, 27, 11, 59, 30), now)).toBe("gerade eben");
    expect(formatRelative(new Date("x"), now)).toBe("");
    expect(formatDate(new Date("x"))).toBe("");
  });
  it("sorts with numbers by value", () => {
    expect(["site10", "Site2", "site1"].sort(compare)).toEqual(["site1", "Site2", "site10"]);
  });
});
