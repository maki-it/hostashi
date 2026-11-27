// SPDX-License-Identifier: AGPL-3.0-or-later

// Redirect sites (ADR-0054): every address of the site goes to one target.

import { useState, type FormEvent } from "react";
import { useTranslation } from "react-i18next";
import { redirectProblem, redirectStatuses, setRedirect, siteMessage, type Site, type SiteRedirect } from "./sites";
import { Button, Card, Field, Notice } from "./ui";

export const newRedirect = (): SiteRedirect => ({ target: "", status: 301, keepPath: true });

/** RedirectFields edits a redirect, for New site and the Redirect card. */
export function RedirectFields({ value, onChange, disabled = false }: { value: SiteRedirect; onChange: (r: SiteRedirect) => void; disabled?: boolean }) {
  const { t } = useTranslation();
  return (
    <>
      <Field label={t("siteDomains.redirect.target")} hint={t("siteDomains.redirect.targetHint")}>
        <input
          className="ui-input ui-mono"
          value={value.target}
          onChange={(e) => onChange({ ...value, target: e.target.value.trim() })}
          placeholder="https://www.example.com"
          disabled={disabled}
          autoComplete="off"
          spellCheck={false}
          inputMode="url"
        />
      </Field>
      <Field label={t("siteDomains.redirect.status")} hint={t("siteDomains.redirect.statusHint")}>
        <select className="ui-select" value={value.status} disabled={disabled} onChange={(e) => onChange({ ...value, status: Number(e.target.value) })}>
          {redirectStatuses.map((s) => (
            <option key={s} value={s}>
              {t(`siteDomains.redirect.statuses.${s}`)}
            </option>
          ))}
        </select>
      </Field>
      <label className="ui-check ui-check-hint">
        <input type="checkbox" checked={value.keepPath} disabled={disabled} onChange={(e) => onChange({ ...value, keepPath: e.target.checked })} />
        <span>
          {t("siteDomains.redirect.keepPath")}
          <span className="ui-hint ui-flush">{t("siteDomains.redirect.keepPathHint")}</span>
        </span>
      </label>
    </>
  );
}

/** RedirectCard changes the target of a redirect site. */
export function RedirectCard({ site, onChange }: { site: Site; onChange: () => Promise<void> }) {
  const { t } = useTranslation();
  const saved = site.redirect ?? newRedirect();
  const [value, setValue] = useState<SiteRedirect>(saved);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [done, setDone] = useState("");
  const unchanged = value.target === saved.target && value.status === saved.status && value.keepPath === saved.keepPath;

  async function save(e: FormEvent) {
    e.preventDefault();
    const problem = redirectProblem(value.target, site.domains);
    if (problem) {
      setError(problem);
      return;
    }
    setBusy(true);
    setError("");
    setDone("");
    try {
      await setRedirect(site.name, value);
      await onChange();
      setDone(t("siteDomains.redirect.saved", { target: value.target }));
    } catch (err) {
      setError(siteMessage(err));
    } finally {
      setBusy(false);
    }
  }

  return (
    <Card title={t("siteDomains.redirect.title")}>
      <form className="ui-stack" onSubmit={(e) => void save(e)} noValidate>
        <RedirectFields value={value} onChange={setValue} disabled={busy} />
        {error && <Notice tone="err" title={error} />}
        {done && <Notice tone="ok" title={done} />}
        <div className="ui-row">
          <Button type="submit" variant="primary" disabled={busy || unchanged}>
            {t("siteDomains.redirect.save")}
          </Button>
        </div>
      </form>
    </Card>
  );
}
