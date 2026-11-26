// SPDX-License-Identifier: AGPL-3.0-or-later

import { useCallback, useEffect, useMemo, useState, type FormEvent } from "react";
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

const unitWord = { MiB: " MiB", s: " s", "": "" };

// PHPSettingsCard edits a site's PHP ini settings from the allowlist
// (ADR-0049). Everyone who may change the site's PHP version may change
// these. An empty field is the Hostashi default.
export function PHPSettingsCard({ site }: { site: Site }) {
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
      <Card title="PHP settings">
        <Notice tone="err" title={loadError} />
      </Card>
    );
  }
  if (!view || !form) {
    return (
      <Card title="PHP settings">
        <p aria-busy="true">Loading…</p>
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
      setError("Some values are out of range. Check the marked fields.");
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
    void save(form!, "Saved. PHP and nginx use the new values now.");
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
        label={`${f.label}${f.unit ? ` (${f.unit})` : ""}`}
        hint={`${f.ini}, ${b.min} to ${b.max}${unitWord[f.unit]}. Default ${placeholder(key, d)}${unitWord[f.unit]}.`}
        {...errProp(key)}
      >
        <input className="ui-input" inputMode="numeric" value={form[key]} placeholder={placeholder(key, d)} onChange={(e) => set(key, e.target.value)} />
      </Field>
    );
  };

  const onOffField = (key: "displayErrors" | "shortOpenTag", label: string, ini: string) => (
    <Field label={label} hint={ini} {...errProp(key)}>
      <select className="ui-select" value={form[key]} onChange={(e) => set(key, e.target.value)}>
        <option value="">Default ({d[key] ? "On" : "Off"})</option>
        <option value="on">On</option>
        <option value="off">Off</option>
      </select>
    </Field>
  );

  return (
    <Card title="PHP settings">
      <form className="ui-stack" onSubmit={submit} noValidate>
        <p className="ui-hint">
          Leave a field empty for the Hostashi default. These are defaults for the site: its own .user.ini or ini_set() can still
          change them. The server's limits still apply: nginx takes no request larger than the largest request and waits no longer
          than the execution time, and the site's memory limit stays the ceiling.
        </p>
        <fieldset className="ui-fieldset ui-group ui-stack" disabled={busy}>
          <legend>Limits</legend>
          <div className="ui-form-grid">
            {numberFields.filter((f) => f.group === "limits").map((f) => numberField(f.key))}
          </div>
          {memWarn && <Notice tone="warn" title={memWarn} />}
        </fieldset>
        <fieldset className="ui-fieldset ui-group ui-stack" disabled={busy}>
          <legend>Behaviour</legend>
          <div className="ui-form-grid">
            <Field label="Time zone" hint={`date.timezone, an IANA name. Default ${d.timezone}.`} {...errProp("timezone")}>
              <input className="ui-input" list="php-timezones" value={form.timezone} placeholder={d.timezone} autoComplete="off" onChange={(e) => set("timezone", e.target.value)} />
            </Field>
            <Field
              label="Error reporting"
              hint="error_reporting. Production reports all but deprecations, development everything."
              {...errProp("errorReporting")}
            >
              <select className="ui-select" value={form.errorReporting} onChange={(e) => set("errorReporting", e.target.value)}>
                <option value="">Default ({presetLabels[d.errorReporting] ?? d.errorReporting})</option>
                {view.errorReportingPresets.map((p) => (
                  <option key={p} value={p}>
                    {presetLabels[p] ?? p}
                  </option>
                ))}
              </select>
            </Field>
            {onOffField("displayErrors", "Show errors in pages", "display_errors. Keep it off on a live site.")}
            {onOffField("shortOpenTag", "Short open tag", "short_open_tag")}
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
            Save PHP settings
          </Button>
          <Button disabled={busy || allDefault} onClick={() => void save(empty, "All PHP settings are back to the defaults.")}>
            Reset all to defaults
          </Button>
          <UnsavedNote dirty={dirty} />
        </div>
      </form>
    </Card>
  );
}
