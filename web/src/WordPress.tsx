// SPDX-License-Identifier: AGPL-3.0-or-later

import { useCallback, useEffect, useRef, useState, type FormEvent } from "react";
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
        <p aria-busy="true">Loading…</p>
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
        {status.state === "running" && <Notice tone="info" title="WordPress is being installed. This takes about a minute." />}
        {finished && status.installed && (
          <Notice tone="ok" title="WordPress is installed.">
            Log in at{" "}
            <a href={url + "/wp-admin/"} target="_blank" rel="noreferrer noopener">
              {url}/wp-admin/
            </a>
            .
          </Notice>
        )}
        {status.state === "failed" && (
          <Notice tone="err" title={`The install failed${status.error ? `: ${status.error}` : ""}.`}>
            Its database stays.
          </Notice>
        )}
        {finished && !status.installed && status.empty === false && (
          <Notice tone="info" title="The document root holds files, but no WordPress.">
            WordPress installs only into an empty folder. Reinstall replaces the files with a new WordPress.
          </Notice>
        )}
        {status.log && status.state !== "none" && (
          <details className="ui-job-log" open={status.state === "failed"}>
            <summary>{running ? "Show progress" : "Show log"}</summary>
            <pre className="ui-code" aria-label="Install log">
              {status.log}
            </pre>
          </details>
        )}
        {finished && !occupied && <InstallForm site={site} onStarted={started} />}
        {occupied && !reinstalling && (
          <div className="ui-row">
            <Button variant="danger" onClick={() => setReinstalling(true)}>
              Reinstall WordPress
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
  const [typed, setTyped] = useState("");
  const [input, setInput] = useState<WordPressInput>({
    locale: "en_US",
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
        <Notice tone="warn" title="Reinstall replaces the site with a new WordPress.">
          Everything in the document root is deleted: themes, plugins, uploads and any other files.
          {reinstall.database ? (
            <>
              {" "}
              The database <code>{reinstall.database}</code> is deleted as well.
            </>
          ) : (
            " The site's databases stay."
          )}{" "}
          Back up the site first under Backups; this cannot be undone.
        </Notice>
      ) : (
        <p className="ui-hint">
          Installs the latest WordPress into the empty document root, with a new database and user for it. WP-CLI runs as the site user;
          file editing in the dashboard stays off.
        </p>
      )}
      <fieldset className="ui-fieldset ui-stack" disabled={busy}>
        <div className="ui-form-grid">
          <Field label="Site title">
            <input className="ui-input" value={input.title} onChange={(e) => set({ title: e.target.value })} maxLength={100} />
          </Field>
          <Field label="Language">
            <select className="ui-select" value={input.locale} onChange={(e) => set({ locale: e.target.value })}>
              {wpLocales.map((l) => (
                <option key={l.value} value={l.value}>
                  {l.label}
                </option>
              ))}
            </select>
          </Field>
          <Field label="Admin user" hint="Not admin, which bots try first.">
            <input
              className="ui-input"
              value={input.adminUser}
              autoComplete="off"
              spellCheck={false}
              onChange={(e) => set({ adminUser: e.target.value })}
              maxLength={60}
            />
          </Field>
          <Field label="Admin email">
            <input className="ui-input" type="email" value={input.adminEmail} onChange={(e) => set({ adminEmail: e.target.value })} />
          </Field>
        </div>
        <label className="ui-check">
          <input type="radio" name={`${site.name}-wp-password`} checked={!own} onChange={() => setOwn(false)} />
          Generate a strong admin password (shown once)
        </label>
        <label className="ui-check">
          <input type="radio" name={`${site.name}-wp-password`} checked={own} onChange={() => setOwn(true)} />
          Set my own password
        </label>
        {own && (
          <Field label="Admin password" hint="12 to 128 characters.">
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
          Discourage search engines in WordPress (Settings, Reading)
        </label>
      </fieldset>
      {reinstall && (
        <div className="ui-narrow">
          <Field
            label={
              <>
                Type <code className="ui-confirm-phrase">{site.name}</code> to confirm
              </>
            }
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
              Reinstall WordPress
            </Button>
            <Button disabled={busy} onClick={onCancel}>
              Cancel
            </Button>
          </>
        ) : (
          <Button type="submit" variant="primary" disabled={busy}>
            Install WordPress
          </Button>
        )}
      </div>
    </form>
  );
}

// PasswordBox sits at the top of the WordPress card, where the person
// just pressed Install, so the one-time password is not missed.
function PasswordBox({ secret, onClose }: { secret: Secret; onClose: () => void }) {
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
      setCopied("Copied.");
    } catch {
      setCopied("Copying is not possible here. Select the password and copy it by hand.");
    }
  }

  return (
    <div ref={box} className="ui-secret-box ui-stack" role="region" aria-labelledby="wp-secret">
      <h3 id="wp-secret" ref={heading} tabIndex={-1}>
        Save the WordPress admin password now
      </h3>
      <p className="ui-flush">Hostashi does not keep it and cannot show it again. If it is lost, reset it from the WordPress login page.</p>
      <dl className="ui-facts">
        <dt>Login</dt>
        <dd>
          <code>{secret.url}/wp-admin/</code>
        </dd>
        <dt>User</dt>
        <dd>
          <code>{secret.user}</code>
        </dd>
        <dt>Database</dt>
        <dd>
          <code>{secret.database}</code>
        </dd>
      </dl>
      <div className="ui-field">
        <label htmlFor="wp-secret-password">Admin password</label>
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
          <Button onClick={() => void copy()}>Copy password</Button>
        </div>
      </div>
      {copied && <Notice tone="ok" title={copied} />}
      <div className="ui-row">
        <Button variant="primary" onClick={onClose}>
          I have saved it
        </Button>
      </div>
    </div>
  );
}
