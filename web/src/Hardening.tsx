// SPDX-License-Identifier: AGPL-3.0-or-later

import { Fragment, useCallback, useEffect, useMemo, useState, type FormEvent } from "react";
import { Trans, useTranslation } from "react-i18next";
import {
  fromHardeningForm,
  getSiteProfile,
  previewSiteProfile,
  profileMessage,
  setSiteProfile,
  toHardeningForm,
  type HardeningForm,
  type SiteProfileView,
} from "./profiles";
import type { Site } from "./sites";
import { Badge, Button, Card, Field, Notice } from "./ui";

// HardeningCard chooses a site's application profile and its hardening
// rules (ADR-0069, ADR-0017 Part 4). Only what differs from the profile is
// stored, so later changes of the profile reach the rules the site kept.
export function HardeningCard({ site }: { site: Site }) {
  const { t } = useTranslation();
  const [view, setView] = useState<SiteProfileView | null>(null);
  const [profile, setProfile] = useState("");
  const [form, setForm] = useState<HardeningForm | null>(null);
  const [loadError, setLoadError] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [done, setDone] = useState("");

  const show = useCallback((v: SiteProfileView) => {
    setView(v);
    setProfile(v.profile);
    setForm(toHardeningForm(v.hardening));
  }, []);

  const load = useCallback(async () => {
    try {
      show(await getSiteProfile(site.name));
      setLoadError("");
    } catch (err) {
      setLoadError(profileMessage(err));
    }
  }, [site.name, show]);

  useEffect(() => {
    void load();
  }, [load]);

  const title = t("hardening.title");
  if (loadError) {
    return (
      <Card title={title}>
        <Notice tone="err" title={loadError} />
      </Card>
    );
  }
  if (!view || !form) {
    return (
      <Card title={title}>
        <p aria-busy="true">{t("hardening.loading")}</p>
      </Card>
    );
  }

  const php = site.php !== "";
  const choices = view.choices;
  const chosen = choices.profiles.find((p) => p.name === profile);
  const set = (next: Partial<HardeningForm>) => setForm({ ...form, ...next });

  // A new profile brings its own rules into the form; saving keeps them.
  function choose(name: string) {
    setProfile(name);
    const p = choices.profiles.find((c) => c.name === name);
    if (p) setForm(toHardeningForm(p.hardening));
    setDone("");
  }

  async function save(useProfile: boolean) {
    setError("");
    setDone("");
    setBusy(true);
    try {
      const v = await setSiteProfile(site.name, profile, useProfile ? undefined : fromHardeningForm(form!, php));
      show(v);
      setDone(useProfile ? t("hardening.savedProfile") : t("hardening.savedRules"));
    } catch (err) {
      setError(profileMessage(err));
    } finally {
      setBusy(false);
    }
  }

  function submit(e: FormEvent) {
    e.preventDefault();
    void save(false);
  }

  const pathHint = t("hardening.pathHint", { max: choices.maxPaths });

  return (
    <Card title={title}>
      <form className="ui-stack" onSubmit={submit} noValidate>
        <p className="ui-hint">
          <Trans i18nKey="hardening.intro" components={{ code1: <code />, code2: <code />, code3: <code /> }} />
        </p>
        <Field
          label={t("hardening.profile")}
          hint={
            chosen?.minPhp
              ? t("hardening.minPhp", { version: chosen.minPhp }) + (chosen.webDir ? " " + t("hardening.webDir", { dir: chosen.webDir }) : "")
              : t("hardening.profileHint")
          }
        >
          <select className="ui-select" value={profile} disabled={busy} onChange={(e) => choose(e.target.value)}>
            {choices.profiles.map((p) => (
              <option key={p.name} value={p.name}>
                {p.title}
              </option>
            ))}
          </select>
        </Field>
        {chosen?.checklist && chosen.checklist.length > 0 && (
          <ul className="ui-hint">
            {chosen.checklist.map((c) => (
              <li key={c}>{c}</li>
            ))}
          </ul>
        )}
        <fieldset className="ui-fieldset ui-stack" disabled={busy}>
          <legend>
            {t("hardening.rules")} {view.changed && profile === view.profile && <Badge tone="warn">{t("hardening.changed")}</Badge>}
          </legend>
          <label className="ui-check">
            <input type="checkbox" checked={form.configFiles} onChange={(e) => set({ configFiles: e.target.checked })} />
            {t("hardening.configFiles")}
          </label>
          <p className="ui-hint">
            {t("hardening.suffixes")}{" "}
            {choices.configFileSuffixes.map((s, i) => (
              <Fragment key={s}>
                {i > 0 && " "}
                <code>{s}</code>
              </Fragment>
            ))}

          </p>
          <div className="ui-form-grid">
            <Field label={t("hardening.denyDirs")} hint={t("hardening.denyDirsHint", { pathHint })}>
              <textarea className="ui-input ui-textarea ui-mono" rows={3} spellCheck={false} value={form.denyDirs} onChange={(e) => set({ denyDirs: e.target.value })} />
            </Field>
            <Field label={t("hardening.denyFiles")} hint={t("hardening.denyFilesHint")}>
              <textarea className="ui-input ui-textarea ui-mono" rows={3} spellCheck={false} value={form.denyFiles} onChange={(e) => set({ denyFiles: e.target.value })} />
            </Field>
            {php && (
              <>
                <Field label={t("hardening.noPhpDirs")} hint={t("hardening.noPhpDirsHint")}>
                  <textarea className="ui-input ui-textarea ui-mono" rows={3} spellCheck={false} value={form.noPhpDirs} onChange={(e) => set({ noPhpDirs: e.target.value })} />
                </Field>
                <Field
                  label={t("hardening.limitFiles")}
                  hint={t("hardening.limitFilesHint", { perMinute: choices.limitPerMinute, burst: choices.limitBurst })}
                >
                  <textarea className="ui-input ui-textarea ui-mono" rows={3} spellCheck={false} value={form.limitFiles} onChange={(e) => set({ limitFiles: e.target.value })} />
                </Field>
              </>
            )}
          </div>
        </fieldset>
        <ServerBlock site={site.name} profile={profile} form={form} php={php} />
        {error && <Notice tone="err" title={error} />}
        {done && <Notice tone="ok" title={done} />}
        <div className="ui-row">
          <Button type="submit" variant="primary" disabled={busy}>
            {t("hardening.save")}
          </Button>
          <Button disabled={busy} onClick={() => void save(true)}>
            {t("hardening.useProfile")}
          </Button>
        </div>
      </form>
    </Card>
  );
}

// ServerBlock shows the nginx server block the site gets with the chosen
// profile and the rules in the form, before they are saved (ADR-0069). It
// loads only while open, and again shortly after the form changes.
function ServerBlock({ site, profile, form, php }: { site: string; profile: string; form: HardeningForm; php: boolean }) {
  const { t } = useTranslation();
  const [open, setOpen] = useState(false);
  const [config, setConfig] = useState("");
  const [error, setError] = useState("");
  const hardening = useMemo(() => fromHardeningForm(form, php), [form, php]);

  useEffect(() => {
    if (!open) return;
    let stale = false;
    const run = async () => {
      try {
        const p = await previewSiteProfile(site, profile, hardening);
        if (!stale) {
          setConfig(p.config);
          setError("");
        }
      } catch (err) {
        if (!stale) setError(profileMessage(err));
      }
    };
    const timer = setTimeout(() => void run(), 300);
    return () => {
      stale = true;
      clearTimeout(timer);
    };
  }, [open, site, profile, hardening]);

  return (
    <details onToggle={(e) => setOpen(e.currentTarget.open)}>
      <summary>{t("hardening.block.show")}</summary>
      <div className="ui-stack">
        <p className="ui-hint">
          <Trans i18nKey="hardening.block.hint" components={{ code: <code /> }} />
        </p>
        {error ? (
          <Notice tone="err" title={error} />
        ) : config ? (
          <pre className="ui-code ui-code-scroll" aria-label={t("hardening.block.label")}>
            {config}
          </pre>
        ) : (
          <p aria-busy="true">{t("hardening.block.loading")}</p>
        )}
      </div>
    </details>

  );
}
