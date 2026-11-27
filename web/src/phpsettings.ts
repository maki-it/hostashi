// SPDX-License-Identifier: AGPL-3.0-or-later

// The PHP settings of a site (ADR-0049): a closed allowlist with typed
// values. The pool sets them as defaults (php_value), so a .user.ini in
// the site may still change them; nginx and the site's limits stay the
// boundary. A field left out means the Hostashi default.

import { ApiError, request } from "./api";
import i18n from "./i18n";

export type PHPSettings = {
  memoryLimit?: number; // bytes
  uploadMaxFilesize?: number; // bytes
  postMaxSize?: number; // bytes
  maxExecutionTime?: number; // seconds
  maxInputTime?: number; // seconds
  maxInputVars?: number;
  maxFileUploads?: number;
  timezone?: string;
  displayErrors?: boolean;
  errorReporting?: string;
  sessionGcMaxlifetime?: number; // seconds
  shortOpenTag?: boolean;
};

export type NumberKey = "memoryLimit" | "uploadMaxFilesize" | "postMaxSize" | "maxExecutionTime" | "maxInputTime" | "maxInputVars" | "maxFileUploads" | "sessionGcMaxlifetime";

export type PHPSettingsView = {
  settings: PHPSettings;
  effective: Required<PHPSettings>;
  defaults: Required<PHPSettings>;
  bounds: Record<NumberKey, { min: number; max: number }>;
  errorReportingPresets: string[];
  updatedAt?: string;
  /** The site's memory limit in bytes, 0 for none (ADR-0015). */
  memoryLimitBytes: number;
  phpWorkers: number;
};

const settingsPath = (site: string) => "/sites/" + encodeURIComponent(site) + "/php/settings";

export const getPHPSettings = (site: string) => request<PHPSettingsView>("GET", settingsPath(site));
export const setPHPSettings = (site: string, s: PHPSettings) => request<PHPSettingsView>("PUT", settingsPath(site), s);

export const MiB = 1 << 20;

const fieldLabel = (k: NumberKey) => i18n.t(`siteApp.php.fields.${k}`);

// The number fields of the form, with their ini key and unit. Sizes are
// entered in MiB and sent in bytes.
export const numberFields: { key: NumberKey; ini: string; label: string; unit: "MiB" | "s" | ""; group: "limits" | "behaviour" }[] = [
  { key: "memoryLimit", ini: "memory_limit", get label() { return fieldLabel("memoryLimit"); }, unit: "MiB", group: "limits" },
  { key: "uploadMaxFilesize", ini: "upload_max_filesize", get label() { return fieldLabel("uploadMaxFilesize"); }, unit: "MiB", group: "limits" },
  { key: "postMaxSize", ini: "post_max_size", get label() { return fieldLabel("postMaxSize"); }, unit: "MiB", group: "limits" },
  { key: "maxExecutionTime", ini: "max_execution_time", get label() { return fieldLabel("maxExecutionTime"); }, unit: "s", group: "limits" },
  { key: "maxInputTime", ini: "max_input_time", get label() { return fieldLabel("maxInputTime"); }, unit: "s", group: "limits" },
  { key: "maxInputVars", ini: "max_input_vars", get label() { return fieldLabel("maxInputVars"); }, unit: "", group: "limits" },
  { key: "maxFileUploads", ini: "max_file_uploads", get label() { return fieldLabel("maxFileUploads"); }, unit: "", group: "limits" },
  { key: "sessionGcMaxlifetime", ini: "session.gc_maxlifetime", get label() { return fieldLabel("sessionGcMaxlifetime"); }, unit: "s", group: "behaviour" },
];

// The form keeps every field as text; "" is the default.
export type PHPForm = Record<NumberKey | "timezone" | "displayErrors" | "errorReporting" | "shortOpenTag", string>;

const isSize = (k: NumberKey) => k === "memoryLimit" || k === "uploadMaxFilesize" || k === "postMaxSize";

const toUnit = (k: NumberKey, v: number) => (isSize(k) ? v / MiB : v);

const onOff = (b: boolean | undefined) => (b === undefined ? "" : b ? "on" : "off");

export function toForm(s: PHPSettings): PHPForm {
  const f = { timezone: s.timezone ?? "", displayErrors: onOff(s.displayErrors), errorReporting: s.errorReporting ?? "", shortOpenTag: onOff(s.shortOpenTag) } as PHPForm;
  for (const { key } of numberFields) {
    const v = s[key];
    f[key] = v ? String(toUnit(key, v)) : "";
  }
  return f;
}

// placeholder shows the default of a number field in the form's unit.
export const placeholder = (k: NumberKey, d: Required<PHPSettings>) => String(toUnit(k, d[k]));

// unitBounds are the bounds of a number field in the form's unit.
export function unitBounds(k: NumberKey, b: PHPSettingsView["bounds"]): { min: number; max: number } {
  return { min: toUnit(k, b[k].min), max: toUnit(k, b[k].max) };
}

export type FormErrors = Partial<Record<keyof PHPForm, string>>;

// fromForm checks the form against the bounds and returns the settings
// to send, or the errors per field.
export function fromForm(f: PHPForm, b: PHPSettingsView["bounds"]): { settings: PHPSettings; errors: FormErrors } {
  const settings: PHPSettings = {};
  const errors: FormErrors = {};
  for (const { key } of numberFields) {
    const text = f[key].trim();
    if (text === "") continue;
    const { min, max } = unitBounds(key, b);
    if (!/^\d+$/.test(text) || Number(text) < min || Number(text) > max) {
      errors[key] = i18n.t("siteApp.php.numberProblem", { min, max });
      continue;
    }
    settings[key] = isSize(key) ? Number(text) * MiB : Number(text);
  }
  const tz = f.timezone.trim();
  if (tz !== "") settings.timezone = tz;
  if (f.displayErrors !== "") settings.displayErrors = f.displayErrors === "on";
  if (f.errorReporting !== "") settings.errorReporting = f.errorReporting;
  if (f.shortOpenTag !== "") settings.shortOpenTag = f.shortOpenTag === "on";
  return { settings, errors };
}

// fieldOfIni maps the ini key of a rejected setting to its form field.
export function fieldOfIni(ini: string): keyof PHPForm | undefined {
  const n = numberFields.find((f) => f.ini === ini);
  if (n) return n.key;
  return ({ "date.timezone": "timezone", display_errors: "displayErrors", error_reporting: "errorReporting", short_open_tag: "shortOpenTag" } as Record<string, keyof PHPForm>)[ini];
}

// rejectedKey returns the ini key a 422 php_setting_rejected names.
export function rejectedKey(err: unknown): string | undefined {
  if (err instanceof ApiError && err.code === "php_setting_rejected") {
    const key = (err.body as { key?: unknown } | undefined)?.key;
    if (typeof key === "string") return key;
  }
  return undefined;
}

export function phpSettingsMessage(err: unknown): string {
  if (err instanceof ApiError) {
    if (err.code === "php_setting_rejected") return i18n.t("siteApp.php.errors.rejected", { reason: err.message.replace(/^php setting /, "") });
    if (err.code === "not_found") return i18n.t("siteApp.php.errors.notFound");
    return err.message;
  }
  return i18n.t("siteApp.php.errors.failed");
}

// memoryWarning warns when memory_limit times the PHP workers exceeds the
// site's memory limit (ADR-0049). It does not refuse: workers rarely all
// use their maximum at once.
export function memoryWarning(form: Pick<PHPForm, "memoryLimit">, v: Pick<PHPSettingsView, "defaults" | "memoryLimitBytes" | "phpWorkers">): string {
  if (v.memoryLimitBytes <= 0 || v.phpWorkers <= 0) return "";
  const text = form.memoryLimit.trim();
  const mem = /^\d+$/.test(text) ? Number(text) * MiB : v.defaults.memoryLimit;
  if (mem * v.phpWorkers <= v.memoryLimitBytes) return "";
  return i18n.t("siteApp.php.memoryWarning", { workers: v.phpWorkers, each: mem / MiB, total: (mem * v.phpWorkers) / MiB, limit: Math.floor(v.memoryLimitBytes / MiB) });
}

export const presetLabels: Record<string, string> = {
  get production() { return i18n.t("siteApp.php.presets.production"); },
  get development() { return i18n.t("siteApp.php.presets.development"); },
  get errors() { return i18n.t("siteApp.php.presets.errors"); },
};
