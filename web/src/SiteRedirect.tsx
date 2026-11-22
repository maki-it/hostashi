// SPDX-License-Identifier: AGPL-3.0-or-later

// Redirect sites (ADR-0054): every address of the site goes to one target.

import { useState, type FormEvent } from "react";
import { redirectProblem, redirectStatuses, setRedirect, siteMessage, type Site, type SiteRedirect } from "./sites";
import { Button, Card, Field, Notice } from "./ui";

export const newRedirect = (): SiteRedirect => ({ target: "", status: 301, keepPath: true });

/** RedirectFields edits a redirect, for New site and the Redirect card. */
export function RedirectFields({ value, onChange, disabled = false }: { value: SiteRedirect; onChange: (r: SiteRedirect) => void; disabled?: boolean }) {
  return (
    <>
      <Field label="Redirect to" hint="The full address, for example https://www.example.com or https://example.com/new-shop.">
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
      <Field label="Status code" hint="301 for a move that stays. Browsers remember 301 and 308; use 302 while you test.">
        <select className="ui-select" value={value.status} disabled={disabled} onChange={(e) => onChange({ ...value, status: Number(e.target.value) })}>
          {redirectStatuses.map((s) => (
            <option key={s.value} value={s.value}>
              {s.label}
            </option>
          ))}
        </select>
      </Field>
      <label className="ui-check ui-check-hint">
        <input type="checkbox" checked={value.keepPath} disabled={disabled} onChange={(e) => onChange({ ...value, keepPath: e.target.checked })} />
        <span>
          Keep the path
          <span className="ui-hint ui-flush">
            On: /shop/cart?id=1 goes to the same path on the target. Off: every address goes to the target as it is.
          </span>
        </span>
      </label>
    </>
  );
}

/** RedirectCard changes the target of a redirect site. */
export function RedirectCard({ site, onChange }: { site: Site; onChange: () => Promise<void> }) {
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
      setDone(`Every address of the site now goes to ${value.target}.`);
    } catch (err) {
      setError(siteMessage(err));
    } finally {
      setBusy(false);
    }
  }

  return (
    <Card title="Redirect">
      <form className="ui-stack" onSubmit={(e) => void save(e)} noValidate>
        <RedirectFields value={value} onChange={setValue} disabled={busy} />
        {error && <Notice tone="err" title={error} />}
        {done && <Notice tone="ok" title={done} />}
        <div className="ui-row">
          <Button type="submit" variant="primary" disabled={busy || unchanged}>
            Save redirect
          </Button>
        </div>
      </form>
    </Card>
  );
}
