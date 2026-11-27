// SPDX-License-Identifier: AGPL-3.0-or-later

import { useCallback, useEffect, useRef, useState, type FormEvent } from "react";
import { Trans, useTranslation } from "react-i18next";
import { currentLang } from "./i18n";
import type { Site } from "./sites";
import { Button, Card, Field, Notice } from "./ui";
import {
  getWordPress,
  installWordPress,
  reinstallWordPress,
  wordPressMessage,
  wpLocales,
  type WordPressInput,
  type WordPressStart,
  type WordPressStatus,
} from "./wordpress";

// How often the card asks for the state of a running install.
const pollMs = 3000;

type Secret = { url: string; user: string; password: string; database: string };

// WordPressCard installs WordPress into the empty document root of a PHP
// site (ADR-0069): the panel creates a database, WP-CLI does the rest as
// the site user. The admin password is shown once.
export function WordPressCard({ site }: { site: Site }) {
  const { t } = useTranslation();
  const [status, setStatus] = useState<WordPressStatus | null>(null);
  const [loadError, setLoadError] = useState("");
  const [secret, setSecret] = useState<Secret | null>(null);
  const [reinstalling, setReinstalling] = useState(false);

  const load = useCallback(async () => {
    try {
      setStatus(await getWordPress(site.name));
      setLoadError("");
    } catch (err) {
      setLoadError(wordPressMessage(err));
    }
  }, [site.name]);

  useEffect(() => {
    void load();
  }, [load]);

  const running = status?.state === "running";
  useEffect(() => {
    if (!running) return;
    const t = setInterval(() => void load(), pollMs);
    return () => clearInterval(t);
  }, [running, load]);

  const title = "WordPress";
  const url = (site.tls.enabled ? "https://" : "http://") + site.domains[0];

  if (loadError) {
    return (
      <Card title={title}>
        <Notice tone="err" title={loadError} />
      </Card>
    );
  }
  if (!status) {
    return (
      <Card title={title}>
        <p aria-busy="true">{t("common.status.loading")}</p>
      </Card>
    );
  }

  const started = (st: WordPressStart, input: WordPressInput) => {
    setReinstalling(false);
    setStatus(st);
    if (st.adminPassword)
      setSecret({
        url,
        user: input.adminUser,
        password: st.adminPassword,
        database: st.database,
      });
  };
  const finished = status.state !== "running";
  // WordPress is there, or other files are: only a reinstall can install.
  const occupied = finished && (status.installed === true || status.empty === false);

  return (
    <>
      <Card title={title}>
        {secret && <PasswordBox secret={secret} onClose={() => setSecret(null)} />}
        {status.state === "running" && <Notice tone="info" title={t("sites.wordpress.running")} />}
        {finished && status.installed && (
          <Notice tone="ok" title={t("sites.wordpress.installed")}>
            <Trans i18nKey="sites.wordpress.loginAt" values={{ url }} components={{ a: <a href={url + "/wp-admin/"} target="_blank" rel="noreferrer noopener" /> }} />
          </Notice>
        )}
        {status.state === "failed" && (
          <Notice tone="err" title={status.error ? t("sites.wordpress.failedWith", { error: status.error }) : t("sites.wordpress.failed")}>
            {t("sites.wordpress.dbStays")}
          </Notice>
        )}
        {finished && !status.installed && status.empty === false && (
          <Notice tone="info" title={t("sites.wordpress.occupied")}>
            {t("sites.wordpress.occupiedText")}
          </Notice>
        )}
        {status.log && status.state !== "none" && (
          <details className="ui-job-log" open={status.state === "failed"}>
            <summary>{running ? t("sites.wordpress.progress") : t("sites.wordpress.log")}</summary>
            <pre className="ui-code" aria-label={t("sites.wordpress.logLabel")}>
              {status.log}
            </pre>
          </details>
        )}
        {finished && !occupied && <InstallForm site={site} onStarted={started} />}
        {occupied && !reinstalling && (
          <div className="ui-row">
            <Button variant="danger" onClick={() => setReinstalling(true)}>
              {t("sites.wordpress.reinstall")}
            </Button>
          </div>
        )}
        {occupied && reinstalling && (
          <InstallForm site={site} reinstall={{ database: status.database }} onStarted={started} onCancel={() => setReinstalling(false)} />
        )}
      </Card>
    </>
  );
}

// InstallForm installs into the empty document root, or with reinstall
// replaces everything in it: the person types the site's name to confirm.
function InstallForm({
  site,
  reinstall,
  onStarted,
  onCancel,
}: {
  site: Site;
  reinstall?: { database?: string | undefined };
  onStarted: (st: WordPressStart, input: WordPressInput) => void;
  onCancel?: () => void;
}) {
  const { t } = useTranslation();
  const [typed, setTyped] = useState("");
  const [input, setInput] = useState<WordPressInput>({
    // WordPress in the panel's language, which the person can change.
    locale: currentLang() === "de" ? "de_DE" : "en_US",
    title: site.title || site.domains[0] || "",
    adminUser: "",
    adminEmail: "",
  });
  const [own, setOwn] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const set = (next: Partial<WordPressInput>) => setInput({ ...input, ...next });

  async function submit(e: FormEvent) {
    e.preventDefault();
    setError("");
    setBusy(true);
    try {
      const { adminPassword, ...rest } = input;
      const body: WordPressInput = own && adminPassword ? { ...rest, adminPassword } : rest;
      onStarted(reinstall ? await reinstallWordPress(site.name, body, typed) : await installWordPress(site.name, body), body);
    } catch (err) {
      setError(wordPressMessage(err));
    } finally {
      setBusy(false);
    }
  }

  return (
    <form className="ui-stack" onSubmit={submit} noValidate>
      {reinstall ? (
        <Notice tone="warn" title={t("sites.wordpress.reinstallTitle")}>
          {t("sites.wordpress.reinstallText")}{" "}
          {reinstall.database ? (
            <Trans i18nKey="sites.wordpress.reinstallDb" values={{ database: reinstall.database }} components={{ code: <code /> }} />
          ) : (
            t("sites.wordpress.reinstallDbStays")
          )}{" "}
          {t("sites.wordpress.reinstallBackup")}
        </Notice>
      ) : (
        <p className="ui-hint">{t("sites.wordpress.intro")}</p>
      )}
      <fieldset className="ui-fieldset ui-stack" disabled={busy}>
        <div className="ui-form-grid">
          <Field label={t("sites.wordpress.siteTitle")}>
            <input className="ui-input" value={input.title} onChange={(e) => set({ title: e.target.value })} maxLength={100} />
          </Field>
          <Field label={t("sites.wordpress.language")}>
            <select className="ui-select" value={input.locale} onChange={(e) => set({ locale: e.target.value })}>
              {wpLocales.map((l) => (
                <option key={l.value} value={l.value}>
                  {l.label}
                </option>
              ))}
            </select>
          </Field>
          <Field label={t("sites.wordpress.adminUser")} hint={t("sites.wordpress.adminUserHint")}>
            <input
              className="ui-input"
              value={input.adminUser}
              autoComplete="off"
              spellCheck={false}
              onChange={(e) => set({ adminUser: e.target.value })}
              maxLength={60}
            />
          </Field>
          <Field label={t("sites.wordpress.adminEmail")}>
            <input className="ui-input" type="email" value={input.adminEmail} onChange={(e) => set({ adminEmail: e.target.value })} />
          </Field>
        </div>
        <label className="ui-check">
          <input type="radio" name={`${site.name}-wp-password`} checked={!own} onChange={() => setOwn(false)} />
          {t("sites.wordpress.generate")}
        </label>
        <label className="ui-check">
          <input type="radio" name={`${site.name}-wp-password`} checked={own} onChange={() => setOwn(true)} />
          {t("sites.wordpress.own")}
        </label>
        {own && (
          <Field label={t("sites.wordpress.adminPassword")} hint={t("sites.wordpress.passwordHint")}>
            <input
              className="ui-input"
              type="password"
              autoComplete="new-password"
              value={input.adminPassword ?? ""}
              onChange={(e) => set({ adminPassword: e.target.value })}
            />
          </Field>
        )}
        <label className="ui-check">
          <input type="checkbox" checked={input.discourage === true} onChange={(e) => set({ discourage: e.target.checked })} />
          {t("sites.wordpress.discourage")}
        </label>
      </fieldset>
      {reinstall && (
        <div className="ui-narrow">
          <Field
            label={<Trans i18nKey="sites.confirm.type" values={{ name: site.name }} components={{ code: <code className="ui-confirm-phrase" /> }} />}
          >
            <input className="ui-input" value={typed} onChange={(e) => setTyped(e.target.value)} autoComplete="off" spellCheck={false} />
          </Field>
        </div>
      )}
      {error && <Notice tone="err" title={error} />}
      <div className="ui-row">
        {reinstall ? (
          <>
            <Button type="submit" variant="danger" disabled={busy || typed !== site.name}>
              {t("sites.wordpress.reinstall")}
            </Button>
            <Button disabled={busy} onClick={onCancel}>
              {t("common.actions.cancel")}
            </Button>
          </>
        ) : (
          <Button type="submit" variant="primary" disabled={busy}>
            {t("sites.wordpress.install")}
          </Button>
        )}
      </div>
    </form>
  );
}

// PasswordBox sits at the top of the WordPress card, where the person
// just pressed Install, so the one-time password is not missed.
function PasswordBox({ secret, onClose }: { secret: Secret; onClose: () => void }) {
  const { t } = useTranslation();
  const [copied, setCopied] = useState("");
  const box = useRef<HTMLDivElement>(null);
  const heading = useRef<HTMLHeadingElement>(null);

  useEffect(() => {
    box.current?.scrollIntoView?.({ block: "center" });
    heading.current?.focus({ preventScroll: true });
  }, []);

  async function copy() {
    try {
      await navigator.clipboard.writeText(secret.password);
      setCopied(t("sites.wordpress.copied"));
    } catch {
      setCopied(t("sites.wordpress.copyFailed"));
    }
  }

  return (
    <div ref={box} className="ui-secret-box ui-stack" role="region" aria-labelledby="wp-secret">
      <h3 id="wp-secret" ref={heading} tabIndex={-1}>
        {t("sites.wordpress.saveTitle")}
      </h3>
      <p className="ui-flush">{t("sites.wordpress.saveText")}</p>
      <dl className="ui-facts">
        <dt>{t("sites.wordpress.login")}</dt>
        <dd>
          <a href={secret.url + "/wp-admin/"} target="_blank" rel="noreferrer noopener">
            <code>{secret.url}/wp-admin/</code>
          </a>
        </dd>
        <dt>{t("sites.wordpress.user")}</dt>
        <dd>
          <code>{secret.user}</code>
        </dd>
        <dt>{t("sites.wordpress.database")}</dt>
        <dd>
          <code>{secret.database}</code>
        </dd>
      </dl>
      <div className="ui-field">
        <label htmlFor="wp-secret-password">{t("sites.wordpress.adminPassword")}</label>
        <div className="ui-secret-row">
          <input
            id="wp-secret-password"
            readOnly
            value={secret.password}
            onFocus={(e) => e.currentTarget.select()}
            autoComplete="off"
            spellCheck={false}
            className="ui-input ui-secret-value"
          />
          <Button onClick={() => void copy()}>{t("sites.wordpress.copyPassword")}</Button>
        </div>
      </div>
      {copied && <Notice tone="ok" title={copied} />}
      <div className="ui-row">
        <Button variant="primary" onClick={onClose}>
          {t("sites.wordpress.saved")}
        </Button>
      </div>
    </div>
  );
}
