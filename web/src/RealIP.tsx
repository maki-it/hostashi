// SPDX-License-Identifier: AGPL-3.0-or-later

import { useCallback, useEffect, useState, type FormEvent } from "react";
import { useTranslation } from "react-i18next";
import {
  fromRealIPForm,
  getPanelRealIP,
  getRealIP,
  realIPMessage,
  setPanelRealIP,
  setRealIP,
  splitRanges,
  toRealIPForm,
  type RealIPForm,
  type RealIPView,
  type WebRealIP,
} from "./realip";
import type { Site } from "./sites";
import { Button, Card, Field, Notice, UnsavedNote } from "./ui";

// RealIPCard sets which proxies a site takes the visitor's address from
// (ADR-0059). Everyone with access to the site may switch Cloudflare and
// the header; only admins add own addresses, because they also go into
// fail2ban's never-ban list for the whole server.
export function RealIPCard({ site, admin }: { site: Site; admin: boolean }) {
  const name = site.name;
  const load = useCallback(() => getRealIP(name), [name]);
  const save = useCallback((r: WebRealIP) => setRealIP(name, r), [name]);
  return <RealIPEditor load={load} save={save} admin={admin} text="site" />;
}

// PanelRealIPCard is the same setting for the panel's own host name, for
// admins only.
export function PanelRealIPCard() {
  return (
    <RealIPEditor
      load={getPanelRealIP}
      save={setPanelRealIP}
      admin
      text="panel"
    />
  );
}

// The texts that differ between a site and the panel: realIp.site.* and
// realIp.panel.*.
type Text = "site" | "panel";

function RealIPEditor({
  load: get,
  save: put,
  admin,
  text,
}: {
  load: () => Promise<RealIPView>;
  save: (r: WebRealIP) => Promise<RealIPView>;
  admin: boolean;
  text: Text;
}) {
  const { t } = useTranslation();
  const [view, setView] = useState<RealIPView | null>(null);
  const [form, setForm] = useState<RealIPForm | null>(null);
  const [loadError, setLoadError] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [done, setDone] = useState("");

  const load = useCallback(async () => {
    try {
      const v = await get();
      setView(v);
      setForm(toRealIPForm(v.realIp));
      setLoadError("");
    } catch (err) {
      setLoadError(realIPMessage(err));
    }
  }, [get]);

  useEffect(() => {
    void load();
  }, [load]);

  if (loadError) {
    return (
      <Card title={t(`realIp.${text}.title`)}>
        <Notice tone="err" title={loadError} />
      </Card>
    );
  }
  if (!view || !form) {
    return (
      <Card title={t(`realIp.${text}.title`)}>
        <p aria-busy="true">{t("common.status.loading")}</p>
      </Card>
    );
  }

  const set = (next: Partial<RealIPForm>) => setForm({ ...form, ...next });
  const dirty = JSON.stringify(form) !== JSON.stringify(toRealIPForm(view.realIp));
  const stored = view.realIp.ranges ?? [];
  // A user without the admin role may keep or remove the stored
  // addresses, never add one.
  const canEditRanges = admin;

  async function submit(e: FormEvent) {
    e.preventDefault();
    const { realIp, error: problem } = fromRealIPForm(form!);
    setError("");
    setDone("");
    if (problem) {
      setError(problem);
      return;
    }
    setBusy(true);
    try {
      const v = await put(realIp);
      setView(v);
      setForm(toRealIPForm(v.realIp));
      setDone(
        realIp.cloudflare || realIp.ranges
          ? t("realIp.saved.on")
          : t("realIp.saved.off"),
      );
    } catch (err) {
      setError(realIPMessage(err));
    } finally {
      setBusy(false);
    }
  }

  return (
    <Card title={t(`realIp.${text}.title`)}>
      <form className="ui-stack" onSubmit={(e) => void submit(e)} noValidate>
        <p className="ui-hint">{t(`realIp.${text}.intro`)}</p>
        <label className="ui-check">
          <input
            type="checkbox"
            checked={form.enabled}
            disabled={busy}
            onChange={(e) => set({ enabled: e.target.checked })}
          />
          {t(`realIp.${text}.behind`)}
        </label>
        {form.enabled && (
          <fieldset className="ui-fieldset ui-stack" disabled={busy}>
            <legend>{t("realIp.trusted")}</legend>
            <label className="ui-check">
              <input
                type="checkbox"
                checked={form.cloudflare}
                onChange={(e) => set({ cloudflare: e.target.checked })}
              />
              {t("realIp.cloudflare", {
                count: view.cloudflare.length,
                date: view.cloudflareDate,
              })}
            </label>
            {form.cloudflare && (
              <details>
                <summary className="ui-hint">{t("realIp.showRanges")}</summary>
                <p className="ui-hint ui-mono">{view.cloudflare.join(" ")}</p>
              </details>
            )}
            <Field
              label={t("realIp.own.label")}
              hint={
                canEditRanges
                  ? t("realIp.own.hint", { max: view.maxRanges })
                  : stored.length > 0
                    ? t("realIp.own.adminsOnlyRemove")
                    : t("realIp.own.adminsOnly")
              }
            >
              <textarea
                className="ui-input ui-textarea ui-mono"
                rows={3}
                value={form.ranges}
                spellCheck={false}
                placeholder={canEditRanges ? "10.0.0.2\n198.51.100.0/24" : ""}
                readOnly={!canEditRanges}
                onChange={(e) => set({ ranges: e.target.value })}
              />
            </Field>
            {!canEditRanges &&
              stored.length > 0 &&
              splitRanges(form.ranges).length > 0 && (
                <Button onClick={() => set({ ranges: "" })}>
                  {t("realIp.own.remove")}
                </Button>
              )}
            <Field
              label={t("realIp.header.label")}
              hint={t("realIp.header.hint")}
            >
              <select
                className="ui-select"
                value={form.header}
                onChange={(e) => set({ header: e.target.value })}
              >
                {view.headers.map((h, i) => (
                  <option key={h} value={i === 0 ? "" : h}>
                    {i === 0 ? t("realIp.header.recommended", { header: h }) : h}
                  </option>
                ))}
              </select>
            </Field>
            <Notice tone="warn" title={t(`realIp.${text}.direct`)} />
            <p className="ui-hint">{t("realIp.fail2ban")}</p>
          </fieldset>
        )}
        {error && <Notice tone="err" title={error} />}
        {done && <Notice tone="ok" title={done} />}
        <div className="ui-row">
          <Button type="submit" variant="primary" disabled={busy || !dirty}>
            {t("common.actions.save")}
          </Button>
          <UnsavedNote dirty={dirty} />
        </div>
      </form>
    </Card>
  );
}
