// SPDX-License-Identifier: AGPL-3.0-or-later

import { useCallback, useEffect, useMemo, useState, type FormEvent } from "react";
import { useTranslation } from "react-i18next";
import {
  fieldOfIni,
  fromForm,
  getPHPSettings,
  memoryWarning,
  numberFields,
  phpSettingsMessage,
  placeholder,
  presetLabels,
  rejectedKey,
  setPHPSettings,
  toForm,
  unitBounds,
  type FormErrors,
  type NumberKey,
  type PHPForm,
  type PHPSettingsView,
} from "./phpsettings";
import type { Site } from "./sites";
import { Button, Card, Field, Notice, UnsavedNote } from "./ui";

// timezones are the IANA names the browser knows, for the datalist; the
// server checks against its own zone database.
function timezones(): string[] {
  try {
    return Intl.supportedValuesOf("timeZone");
  } catch {
    return [];
  }
}

// PHPSettingsCard edits a site's PHP ini settings from the allowlist
// (ADR-0049). Everyone who may change the site's PHP version may change
// these. An empty field is the Hostashi default.
export function PHPSettingsCard({ site }: { site: Site }) {
  const { t } = useTranslation();
  const [view, setView] = useState<PHPSettingsView | null>(null);
  const [form, setForm] = useState<PHPForm | null>(null);
  const [errors, setErrors] = useState<FormErrors>({});
  const [loadError, setLoadError] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [done, setDone] = useState("");
  const zones = useMemo(timezones, []);

  const load = useCallback(async () => {
    try {
      const v = await getPHPSettings(site.name);
      setView(v);
      setForm(toForm(v.settings));
      setLoadError("");
    } catch (err) {
      setLoadError(phpSettingsMessage(err));
    }
  }, [site.name]);

  useEffect(() => {
    void load();
  }, [load]);

  if (loadError) {
    return (
      <Card title={t("siteApp.php.title")}>
        <Notice tone="err" title={loadError} />
      </Card>
    );
  }
  if (!view || !form) {
    return (
      <Card title={t("siteApp.php.title")}>
        <p aria-busy="true">{t("common.status.loading")}</p>
      </Card>
    );
  }

  const d = view.defaults;
  const set = (k: keyof PHPForm, v: string) => {
    setForm({ ...form, [k]: v });
    const { [k]: _, ...rest } = errors;
    setErrors(rest);
  };
  const errProp = (k: keyof PHPForm) => (errors[k] !== undefined ? { error: errors[k] } : {});

  async function save(next: PHPForm, message: string) {
    if (!view) return;
    const { settings, errors: errs } = fromForm(next, view.bounds);
    setErrors(errs);
    setError("");
    setDone("");
    if (Object.keys(errs).length > 0) {
      setError(t("siteApp.php.outOfRange"));
      return;
    }
    setBusy(true);
    try {
      const v = await setPHPSettings(site.name, settings);
      setView(v);
      setForm(toForm(v.settings));
      setDone(message);
    } catch (err) {
      const ini = rejectedKey(err);
      const field = ini ? fieldOfIni(ini) : undefined;
      if (field) setErrors({ [field]: phpSettingsMessage(err) });
      setError(phpSettingsMessage(err));
    } finally {
      setBusy(false);
    }
  }

  function submit(e: FormEvent) {
    e.preventDefault();
    void save(form!, t("siteApp.php.saved"));
  }

  const empty = toForm({});
  const memWarn = memoryWarning(form, view);
  const allDefault = Object.values(toForm(view.settings)).every((v) => v === "");
  const dirty = JSON.stringify(form) !== JSON.stringify(toForm(view.settings));

  const numberField = (key: NumberKey) => {
    const f = numberFields.find((n) => n.key === key)!;
    const b = unitBounds(key, view.bounds);
    return (
      <Field
        key={key}
        label={f.unit ? t("siteApp.php.fieldLabel", { label: f.label, unit: f.unit }) : f.label}
        hint={
          f.unit
            ? t("siteApp.php.numberHintUnit", { ini: f.ini, min: b.min, max: b.max, default: placeholder(key, d), unit: f.unit })
            : t("siteApp.php.numberHint", { ini: f.ini, min: b.min, max: b.max, default: placeholder(key, d) })
        }
        {...errProp(key)}
      >
        <input className="ui-input" inputMode="numeric" value={form[key]} placeholder={placeholder(key, d)} onChange={(e) => set(key, e.target.value)} />
      </Field>
    );
  };

  const onOffField = (key: "displayErrors" | "shortOpenTag", label: string, ini: string) => (
    <Field label={label} hint={ini} {...errProp(key)}>
      <select className="ui-select" value={form[key]} onChange={(e) => set(key, e.target.value)}>
        <option value="">{t("siteApp.php.defaultValue", { value: d[key] ? t("siteApp.php.on") : t("siteApp.php.off") })}</option>
        <option value="on">{t("siteApp.php.on")}</option>
        <option value="off">{t("siteApp.php.off")}</option>
      </select>
    </Field>
  );

  return (
    <Card title={t("siteApp.php.title")}>
      <form className="ui-stack" onSubmit={submit} noValidate>
        <p className="ui-hint">{t("siteApp.php.intro")}</p>
        <fieldset className="ui-fieldset ui-group ui-stack" disabled={busy}>
          <legend>{t("siteApp.php.limits")}</legend>
          <div className="ui-form-grid">
            {numberFields.filter((f) => f.group === "limits").map((f) => numberField(f.key))}
          </div>
          {memWarn && <Notice tone="warn" title={memWarn} />}
        </fieldset>
        <fieldset className="ui-fieldset ui-group ui-stack" disabled={busy}>
          <legend>{t("siteApp.php.behaviour")}</legend>
          <div className="ui-form-grid">
            <Field label={t("siteApp.php.timezone")} hint={t("siteApp.php.timezoneHint", { zone: d.timezone })} {...errProp("timezone")}>
              <input className="ui-input" list="php-timezones" value={form.timezone} placeholder={d.timezone} autoComplete="off" onChange={(e) => set("timezone", e.target.value)} />
            </Field>
            <Field
              label={t("siteApp.php.errorReporting")}
              hint={t("siteApp.php.errorReportingHint")}
              {...errProp("errorReporting")}
            >
              <select className="ui-select" value={form.errorReporting} onChange={(e) => set("errorReporting", e.target.value)}>
                <option value="">{t("siteApp.php.defaultValue", { value: presetLabels[d.errorReporting] ?? d.errorReporting })}</option>
                {view.errorReportingPresets.map((p) => (
                  <option key={p} value={p}>
                    {presetLabels[p] ?? p}
                  </option>
                ))}
              </select>
            </Field>
            {onOffField("displayErrors", t("siteApp.php.displayErrors"), t("siteApp.php.displayErrorsHint"))}
            {onOffField("shortOpenTag", t("siteApp.php.shortOpenTag"), "short_open_tag")}
            {numberField("sessionGcMaxlifetime")}
          </div>
          <datalist id="php-timezones">
            {zones.map((z) => (
              <option key={z} value={z} />
            ))}
          </datalist>
        </fieldset>
        {error && <Notice tone="err" title={error} />}
        {done && <Notice tone="ok" title={done} />}
        <div className="ui-row">
          <Button type="submit" variant="primary" disabled={busy || !dirty}>
            {t("siteApp.php.save")}
          </Button>
          <Button disabled={busy || allDefault} onClick={() => void save(empty, t("siteApp.php.resetDone"))}>
            {t("siteApp.php.reset")}
          </Button>
          <UnsavedNote dirty={dirty} />
        </div>
      </form>
    </Card>
  );
}
