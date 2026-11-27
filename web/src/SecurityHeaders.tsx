// SPDX-License-Identifier: AGPL-3.0-or-later

import { useCallback, useEffect, useState, type FormEvent } from "react";
import {
  fromForm,
  getHeaders,
  headersMessage,
  setHeaders,
  toForm,
  type HeadersForm,
  type HeadersView,
} from "./headers";
import type { Site } from "./sites";
import { Button, Card, Field, Notice } from "./ui";

const mdn = "https://developer.mozilla.org/docs/Web/HTTP/Reference/Headers/";

function Doc({ header }: { header: string }) {
  return (
    <a href={mdn + header} target="_blank" rel="noreferrer noopener">
      {header}
    </a>
  );
}

// SecurityHeadersCard switches a site's security headers (ADR-0056).
// Everyone who may change the site's HSTS may change these.
export function SecurityHeadersCard({ site }: { site: Site }) {
  const [view, setView] = useState<HeadersView | null>(null);
  const [form, setForm] = useState<HeadersForm | null>(null);
  const [loadError, setLoadError] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [done, setDone] = useState("");

  const load = useCallback(async () => {
    try {
      const v = await getHeaders(site.name);
      setView(v);
      setForm(toForm(v.headers, v.permissionsPolicyStrict));
      setLoadError("");
    } catch (err) {
      setLoadError(headersMessage(err));
    }
  }, [site.name]);

  useEffect(() => {
    void load();
  }, [load]);

  if (loadError) {
    return (
      <Card title="Security headers">
        <Notice tone="err" title={loadError} />
      </Card>
    );
  }
  if (!view || !form) {
    return (
      <Card title="Security headers">
        <p aria-busy="true">Loading…</p>
      </Card>
    );
  }

  const strict = view.permissionsPolicyStrict;
  const set = (next: Partial<HeadersForm>) => setForm({ ...form, ...next });

  async function save(next: HeadersForm, message: string) {
    const { headers, error: problem } = fromForm(next, strict);
    setError("");
    setDone("");
    if (problem) {
      setError(problem);
      return;
    }
    setBusy(true);
    try {
      const v = await setHeaders(site.name, headers);
      setView(v);
      setForm(toForm(v.headers, strict));
      setDone(message);
    } catch (err) {
      setError(headersMessage(err));
    } finally {
      setBusy(false);
    }
  }

  function submit(e: FormEvent) {
    e.preventDefault();
    void save(form!, "Saved. nginx sends the new headers now.");
  }

  return (
    <Card title="Security headers">
      <form className="ui-stack" onSubmit={submit} noValidate>
        <p className="ui-hint">
          Sent with every answer of the site, error pages included. A header
          switched on here replaces the same header from the application.
          Switched off, the application's own header passes through unchanged.
        </p>
        <fieldset className="ui-fieldset ui-group ui-stack" disabled={busy}>
          <legend>Basic</legend>
          <div className="ui-form-grid">
            <Field
              label="MIME type sniffing"
              hint={
                <>
                  <Doc header="X-Content-Type-Options" />. Browsers trust the
                  declared type of every file.
                </>
              }
            >
              <select
                className="ui-select"
                value={form.noSniff ? "on" : ""}
                onChange={(e) => set({ noSniff: e.target.value === "on" })}
              >
                <option value="">Off</option>
                <option value="on">nosniff</option>
              </select>
            </Field>
            <Field
              label="Show in frames"
              hint={
                <>
                  <Doc header="X-Frame-Options" />. Protects against
                  clickjacking.
                </>
              }
            >
              <select
                className="ui-select"
                value={form.frameOptions}
                onChange={(e) => set({ frameOptions: e.target.value })}
              >
                <option value="">Off</option>
                <option value="SAMEORIGIN">
                  SAMEORIGIN: only on this site
                </option>
                <option value="DENY">DENY: never</option>
              </select>
            </Field>
            <Field
              label="Referrer"
              hint={
                <>
                  <Doc header="Referrer-Policy" />. What other sites learn about
                  the page a visitor came from.
                </>
              }
            >
              <select
                className="ui-select"
                value={form.referrerPolicy}
                onChange={(e) => set({ referrerPolicy: e.target.value })}
              >
                <option value="">Off</option>
                {view.referrerPolicies.map((p) => (
                  <option key={p} value={p}>
                    {p}
                  </option>
                ))}
              </select>
            </Field>
          </div>
        </fieldset>
        <fieldset className="ui-fieldset ui-group ui-stack" disabled={busy}>
          <legend>Permissions</legend>
          <Field
            label="Browser features"
            hint={
              <>
                <Doc header="Permissions-Policy" />. Which features the site and
                embedded frames may use.
              </>
            }
          >
            <select
              className="ui-select"
              value={form.permissions}
              onChange={(e) =>
                set({
                  permissions: e.target.value as HeadersForm["permissions"],
                })
              }
            >
              <option value="off">Off</option>
              <option value="strict">
                Strict: camera, microphone, location, payment and USB off
              </option>
              <option value="own">Own policy</option>
            </select>
          </Field>
          {form.permissions === "strict" && (
            <p className="ui-hint ui-mono">{strict}</p>
          )}
          {form.permissions === "own" && (
            <Field
              label="Permissions-Policy"
              hint={`Comma-separated, for example camera=(), geolocation=(self "https://maps.example.com"). At most ${view.maxValueBytes} bytes.`}
            >
              <textarea
                className="ui-input ui-textarea ui-mono"
                rows={2}
                value={form.permissionsValue}
                spellCheck={false}
                onChange={(e) => set({ permissionsValue: e.target.value })}
              />
            </Field>
          )}
        </fieldset>
        <fieldset className="ui-fieldset ui-group ui-stack" disabled={busy}>
          <legend>Content Security Policy</legend>
          <Field
            label="Mode"
            hint={
              <>
                <Doc header="Content-Security-Policy" />. Report only shows
                violations in the browser console without blocking anything.
              </>
            }
          >
            <select
              className="ui-select"
              value={form.csp}
              onChange={(e) =>
                set({ csp: e.target.value as HeadersForm["csp"] })
              }
            >
              <option value="off">Off</option>
              <option value="report">Report only (test first)</option>
              <option value="enforce">Enforce</option>
            </select>
          </Field>
          {form.csp !== "off" && (
            <Field
              label="Policy"
              hint={`Directives separated by ";", for example default-src 'self'; img-src 'self' data:. At most ${view.maxValueBytes} bytes.`}
            >
              <textarea
                className="ui-input ui-textarea ui-mono"
                rows={3}
                value={form.cspValue}
                spellCheck={false}
                onChange={(e) => set({ cspValue: e.target.value })}
              />
            </Field>
          )}
          {form.csp === "enforce" && (
            <Notice
              tone="warn"
              title="A policy that is too strict breaks the site at once: blocked scripts, styles or fonts. Test it with report only first."
            />
          )}
        </fieldset>
        {error && <Notice tone="err" title={error} />}
        {done && <Notice tone="ok" title={done} />}
        <div className="ui-row">
          <Button type="submit" variant="primary" disabled={busy}>
            Save security headers
          </Button>
          <Button
            disabled={busy}
            onClick={() =>
              void save(
                { ...toForm(view.defaults, strict), noIndex: form.noIndex },
                "The recommended headers are on.",
              )
            }
          >
            Use recommended
          </Button>
        </div>
      </form>
    </Card>
  );
}
