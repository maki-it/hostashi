// SPDX-License-Identifier: AGPL-3.0-or-later

// Reverse proxy sites (ADR-0054): every request of the site goes to
// another web server.

import { useState, type FormEvent } from "react";
import { proxyProblem, setProxy, siteMessage, type Site, type SiteProxy } from "./sites";
import { Button, Card, Field, Notice } from "./ui";

export const newProxy = (): SiteProxy => ({ target: "", targetHost: false, insecure: false });

/** ProxyFields edits a reverse proxy, for New site and the Reverse proxy card. */
export function ProxyFields({ value, onChange, disabled = false }: { value: SiteProxy; onChange: (p: SiteProxy) => void; disabled?: boolean }) {
  const https = value.target.startsWith("https://");
  return (
    <>
      <Field
        label="Pass requests to"
        hint="The address of the other web server, for example http://10.0.0.5:8080 or https://app.internal.example.com. Private networks are fine; this server's own addresses are not."
      >
        <input
          className="ui-input ui-mono"
          value={value.target}
          onChange={(e) => onChange({ ...value, target: e.target.value.trim() })}
          placeholder="http://10.0.0.5:8080"
          disabled={disabled}
          autoComplete="off"
          spellCheck={false}
          inputMode="url"
        />
      </Field>
      <label className="ui-check ui-check-hint">
        <input type="checkbox" checked={value.targetHost} disabled={disabled} onChange={(e) => onChange({ ...value, targetHost: e.target.checked })} />
        <span>
          Send the target's host name
          <span className="ui-hint ui-flush">
            Off: the other server sees the visitor's domain as Host, as most apps expect. On: it sees the host of the address above.
          </span>
        </span>
      </label>
      {https && (
        <label className="ui-check ui-check-hint">
          <input type="checkbox" checked={value.insecure} disabled={disabled} onChange={(e) => onChange({ ...value, insecure: e.target.checked })} />
          <span>
            Do not verify the target's certificate
            <span className="ui-hint ui-flush">Only for a target with a self-signed certificate in a network you trust.</span>
          </span>
        </label>
      )}
      {https && value.insecure && (
        <Notice tone="warn" title="The connection to the target is encrypted but not verified. Anyone between this server and the target could read it." />
      )}
    </>
  );
}

/** ProxyCard changes the target of a reverse proxy site. */
export function ProxyCard({ site, onChange }: { site: Site; onChange: () => Promise<void> }) {
  const saved = site.proxy ?? newProxy();
  const [value, setValue] = useState<SiteProxy>(saved);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [done, setDone] = useState("");
  const send = { ...value, insecure: value.target.startsWith("https://") && value.insecure };
  const unchanged = send.target === saved.target && send.targetHost === saved.targetHost && send.insecure === saved.insecure;

  async function save(e: FormEvent) {
    e.preventDefault();
    const problem = proxyProblem(value.target, site.domains);
    if (problem) {
      setError(problem);
      return;
    }
    setBusy(true);
    setError("");
    setDone("");
    try {
      await setProxy(site.name, send);
      await onChange();
      setDone(`Requests to the site now go to ${send.target}.`);
    } catch (err) {
      setError(siteMessage(err));
    } finally {
      setBusy(false);
    }
  }

  return (
    <Card title="Reverse proxy">
      <form className="ui-stack" onSubmit={(e) => void save(e)} noValidate>
        <ProxyFields value={value} onChange={setValue} disabled={busy} />
        {error && <Notice tone="err" title={error} />}
        {done && <Notice tone="ok" title={done} />}
        <div className="ui-row">
          <Button type="submit" variant="primary" disabled={busy || unchanged}>
            Save reverse proxy
          </Button>
        </div>
      </form>
    </Card>
  );
}
