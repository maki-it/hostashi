// SPDX-License-Identifier: AGPL-3.0-or-later

import { useCallback, useEffect, useState, type FormEvent } from "react";
import { useTranslation } from "react-i18next";
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

// Header names and values read the same in every language.
const noSniff = "nosniff";
const permissionsPolicy = "Permissions-Policy";

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
  const { t } = useTranslation();
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
      <Card title={t("siteHeaders.title")}>
        <Notice tone="err" title={loadError} />
      </Card>
    );
  }
  if (!view || !form) {
    return (
      <Card title={t("siteHeaders.title")}>
        <p aria-busy="true">{t("common.status.loading")}</p>
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
    void save(form!, t("siteHeaders.saved"));
  }

  return (
    <Card title={t("siteHeaders.title")}>
      <form className="ui-stack" onSubmit={submit} noValidate>
        <p className="ui-hint">{t("siteHeaders.intro")}</p>
        <fieldset className="ui-fieldset ui-group ui-stack" disabled={busy}>
          <legend>{t("siteHeaders.basic")}</legend>
          <div className="ui-form-grid">
            <Field
              label={t("siteHeaders.sniff.label")}
              hint={
                <>
                  <Doc header="X-Content-Type-Options" />.{" "}
                  {t("siteHeaders.sniff.hint")}
                </>
              }
            >
              <select
                className="ui-select"
                value={form.noSniff ? "on" : ""}
                onChange={(e) => set({ noSniff: e.target.value === "on" })}
              >
                <option value="">{t("siteHeaders.off")}</option>
                <option value="on">{noSniff}</option>
              </select>
            </Field>
            <Field
              label={t("siteHeaders.frames.label")}
              hint={
                <>
                  <Doc header="X-Frame-Options" />.{" "}
                  {t("siteHeaders.frames.hint")}
                </>
              }
            >
              <select
                className="ui-select"
                value={form.frameOptions}
                onChange={(e) => set({ frameOptions: e.target.value })}
              >
                <option value="">{t("siteHeaders.off")}</option>
                <option value="SAMEORIGIN">
                  {t("siteHeaders.frames.sameOrigin")}
                </option>
                <option value="DENY">{t("siteHeaders.frames.deny")}</option>
              </select>
            </Field>
            <Field
              label={t("siteHeaders.referrer.label")}
              hint={
                <>
                  <Doc header="Referrer-Policy" />.{" "}
                  {t("siteHeaders.referrer.hint")}
                </>
              }
            >
              <select
                className="ui-select"
                value={form.referrerPolicy}
                onChange={(e) => set({ referrerPolicy: e.target.value })}
              >
                <option value="">{t("siteHeaders.off")}</option>
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
          <legend>{t("siteHeaders.permissions.legend")}</legend>
          <Field
            label={t("siteHeaders.permissions.label")}
            hint={
              <>
                <Doc header="Permissions-Policy" />.{" "}
                {t("siteHeaders.permissions.hint")}
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
              <option value="off">{t("siteHeaders.off")}</option>
              <option value="strict">
                {t("siteHeaders.permissions.strict")}
              </option>
              <option value="own">{t("siteHeaders.permissions.own")}</option>
            </select>
          </Field>
          {form.permissions === "strict" && (
            <p className="ui-hint ui-mono">{strict}</p>
          )}
          {form.permissions === "own" && (
            <Field
              label={permissionsPolicy}
              hint={t("siteHeaders.permissions.ownHint", {
                max: view.maxValueBytes,
              })}
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
          <legend>{t("siteHeaders.csp.legend")}</legend>
          <Field
            label={t("siteHeaders.csp.mode")}
            hint={
              <>
                <Doc header="Content-Security-Policy" />.{" "}
                {t("siteHeaders.csp.modeHint")}
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
              <option value="off">{t("siteHeaders.off")}</option>
              <option value="report">{t("siteHeaders.csp.report")}</option>
              <option value="enforce">{t("siteHeaders.csp.enforce")}</option>
            </select>
          </Field>
          {form.csp !== "off" && (
            <Field
              label={t("siteHeaders.csp.policy")}
              hint={t("siteHeaders.csp.policyHint", { max: view.maxValueBytes })}
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
              title={t("siteHeaders.csp.enforceWarning")}
            />
          )}
        </fieldset>
        {error && <Notice tone="err" title={error} />}
        {done && <Notice tone="ok" title={done} />}
        <div className="ui-row">
          <Button type="submit" variant="primary" disabled={busy}>
            {t("siteHeaders.save")}
          </Button>
          <Button
            disabled={busy}
            onClick={() =>
              void save(
                { ...toForm(view.defaults, strict), noIndex: form.noIndex },
                t("siteHeaders.recommendedOn"),
              )
            }
          >
            {t("siteHeaders.recommended")}
          </Button>
        </div>
      </form>
    </Card>
  );
}
