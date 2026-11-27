// SPDX-License-Identifier: AGPL-3.0-or-later

import { useCallback, useEffect, useRef, useState, type FormEvent } from "react";
import { Trans, useTranslation } from "react-i18next";
import i18n from "./i18n";
import {
  accessMessage,
  addAccessUser,
  deleteAccessUser,
  fromAccessForm,
  getAccess,
  lockout,
  setAccess,
  setAccessUserPassword,
  toAccessForm,
  type AccessForm,
  type AccessUser,
  type AccessView,
} from "./access";
import type { Site } from "./sites";
import { formatHash } from "./routes";
import { Key, Trash } from "@phosphor-icons/react";
import { Button, Card, Field, IconButton, Notice, RelTime, RowActions, Table, UnsavedNote, type Column } from "./ui";

// Examples in the fields, the same in every language.
const ipExample = "203.0.113.7\n2001:db8::/48";
const pathExample = "/wp-admin\n/wp-login.php";

// SiteAccess is the "Access" tab of a site (ADR-0066): an IP rule and
// password protection for the whole site or some paths, with its users.
export function SiteAccess({ site }: { site: Site }) {
  const { t } = useTranslation();
  const name = site.name;
  const [view, setView] = useState<AccessView | null>(null);
  const [loadError, setLoadError] = useState("");
  const [secret, setSecret] = useState<{ user: string; password: string } | null>(null);

  const load = useCallback(async () => {
    try {
      setView(await getAccess(name));
      setLoadError("");
    } catch (err) {
      setLoadError(accessMessage(err));
    }
  }, [name]);

  useEffect(() => {
    void load();
  }, [load]);

  if (loadError) return <Notice tone="err" title={loadError} />;
  if (!view) return <p aria-busy="true">{t("common.status.loading")}</p>;

  return (
    <>
      {secret && <PasswordNotice site={site} secret={secret} onClose={() => setSecret(null)} />}
      <SettingsCard site={name} view={view} onSaved={setView} />
      <UsersCard site={name} view={view} onChange={load} onSecret={setSecret} />
    </>
  );
}

function SettingsCard({ site, view, onSaved }: { site: string; view: AccessView; onSaved: (v: AccessView) => void }) {
  const { t } = useTranslation();
  const [form, setForm] = useState<AccessForm>(() => toAccessForm(view));
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [done, setDone] = useState("");
  const set = (next: Partial<AccessForm>) => setForm({ ...form, ...next });

  async function submit(e: FormEvent) {
    e.preventDefault();
    const { settings, error: problem } = fromAccessForm(form, view.users.length);
    setError("");
    setDone("");
    if (problem) {
      setError(problem);
      return;
    }
    setBusy(true);
    try {
      const v = await setAccess(site, settings);
      onSaved(v);
      setForm(toAccessForm(v));
      setDone(summary(v));
    } catch (err) {
      setError(accessMessage(err));
    } finally {
      setBusy(false);
    }
  }

  const locked = lockout(form, view.clientAddress);
  const dirty = JSON.stringify(form) !== JSON.stringify(toAccessForm(view));

  return (
    <Card title={t("siteAccess.settings.title")}>
      <form className="ui-stack" onSubmit={(e) => void submit(e)} noValidate>
        <p className="ui-hint">
          <Trans i18nKey="siteAccess.settings.intro" components={{ a: <a href={formatHash({ page: "sites", site: { name: site, tab: "realip" } })} /> }} />
        </p>
        <fieldset className="ui-fieldset ui-stack" disabled={busy}>
          <legend>{t("siteAccess.settings.ipRule")}</legend>
          <label className="ui-check">
            <input type="radio" name={site + "-ipmode"} checked={form.ipMode === ""} onChange={() => set({ ipMode: "" })} />
            {t("siteAccess.settings.everybody")}
          </label>
          <label className="ui-check">
            <input type="radio" name={site + "-ipmode"} checked={form.ipMode === "allow"} onChange={() => set({ ipMode: "allow" })} />
            {t("siteAccess.settings.allow")}
          </label>
          <label className="ui-check">
            <input type="radio" name={site + "-ipmode"} checked={form.ipMode === "deny"} onChange={() => set({ ipMode: "deny" })} />
            {t("siteAccess.settings.deny")}
          </label>
          {form.ipMode !== "" && (
            <Field label={t("siteAccess.settings.addresses")} hint={t("siteAccess.settings.addressesHint", { max: view.limits.ips })}>
              <textarea
                className="ui-input ui-textarea ui-mono"
                rows={4}
                value={form.ips}
                spellCheck={false}
                placeholder={ipExample}
                onChange={(e) => set({ ips: e.target.value })}
              />
            </Field>
          )}
          {form.ipMode !== "" && view.clientAddress && (
            <p className="ui-hint">
              <Trans i18nKey="siteAccess.settings.yourAddress" values={{ address: view.clientAddress }} components={{ code: <code /> }} />
            </p>
          )}
          {locked && <Notice tone="warn" title={t("siteAccess.settings.lockout")} />}
        </fieldset>
        <fieldset className="ui-fieldset ui-stack" disabled={busy}>
          <legend>{t("siteAccess.settings.password")}</legend>
          <label className="ui-check">
            <input type="checkbox" checked={form.password} onChange={(e) => set({ password: e.target.checked })} />
            {t("siteAccess.settings.askPassword")}
          </label>
          {form.password && view.users.length === 0 && <Notice tone="warn" title={t("siteAccess.settings.addUserFirst")} />}
          {form.password && (
            <>
              <label className="ui-check">
                <input type="radio" name={site + "-scope"} checked={form.scope === "site"} onChange={() => set({ scope: "site" })} />
                {t("siteAccess.settings.wholeSite")}
              </label>
              <label className="ui-check">
                <input type="radio" name={site + "-scope"} checked={form.scope === "paths"} onChange={() => set({ scope: "paths" })} />
                {t("siteAccess.settings.onlyPaths")}
              </label>
              {form.scope === "paths" && (
                <Field
                  label={t("siteAccess.settings.paths")}
                  hint={t("siteAccess.settings.pathsHint", { max: view.limits.paths })}
                >
                  <textarea
                    className="ui-input ui-textarea ui-mono"
                    rows={3}
                    value={form.paths}
                    spellCheck={false}
                    placeholder={pathExample}
                    onChange={(e) => set({ paths: e.target.value })}
                  />
                </Field>
              )}
              <Field label={t("siteAccess.settings.bypass")} hint={t("siteAccess.settings.bypassHint", { max: view.limits.bypass })}>
                <textarea
                  className="ui-input ui-textarea ui-mono"
                  rows={2}
                  value={form.bypass}
                  spellCheck={false}
                  placeholder="198.51.100.0/24"
                  onChange={(e) => set({ bypass: e.target.value })}
                />
              </Field>
              <p className="ui-hint">{t("siteAccess.settings.passwordNote")}</p>
            </>
          )}
        </fieldset>
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

function summary(v: AccessView): string {
  const parts: string[] = [];
  if (v.ipMode === "allow") parts.push(i18n.t("siteAccess.summary.allow", { count: v.ips.length }));
  if (v.ipMode === "deny") parts.push(i18n.t("siteAccess.summary.deny", { count: v.ips.length }));
  if (v.password) parts.push(v.paths.length > 0 ? i18n.t("siteAccess.summary.paths", { count: v.paths.length }) : i18n.t("siteAccess.summary.site"));
  if (parts.length === 0) return i18n.t("siteAccess.summary.open");
  return i18n.t("siteAccess.summary.saved", { parts: parts.join(", ") });
}

function UsersCard({
  site,
  view,
  onChange,
  onSecret,
}: {
  site: string;
  view: AccessView;
  onChange: () => Promise<void>;
  onSecret: (s: { user: string; password: string }) => void;
}) {
  const { t } = useTranslation();
  const [name, setName] = useState("");
  const [own, setOwn] = useState(false);
  const [password, setPassword] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [done, setDone] = useState("");
  const [confirm, setConfirm] = useState("");

  async function run(fn: () => Promise<string>) {
    setBusy(true);
    setError("");
    setDone("");
    try {
      setDone(await fn());
      await onChange();
    } catch (err) {
      setError(accessMessage(err));
    } finally {
      setBusy(false);
      setConfirm("");
    }
  }

  function add(e: FormEvent) {
    e.preventDefault();
    const user = name.trim();
    if (!user) {
      setError(t("siteAccess.users.enterName"));
      return;
    }
    if (own && password.length < view.limits.minPassword) {
      setError(t("siteAccess.users.tooShort", { min: view.limits.minPassword }));
      return;
    }
    void run(async () => {
      const r = await addAccessUser(site, user, own ? password : "");
      setName("");
      setPassword("");
      if (r.password) onSecret({ user, password: r.password });
      return t("siteAccess.users.added", { user });
    });
  }

  const renew = (user: string) =>
    run(async () => {
      const r = await setAccessUserPassword(site, user, "");
      if (r.password) onSecret({ user, password: r.password });
      return t("siteAccess.users.renewed", { user });
    });

  const remove = (user: string) =>
    run(async () => {
      await deleteAccessUser(site, user);
      return t("siteAccess.users.removed", { user });
    });

  const last = view.password && view.users.length === 1;

  const columns: Column<AccessUser>[] = [
    { key: "name", header: t("siteAccess.users.user"), cell: (u) => <code>{u.name}</code>, sortValue: (u) => u.name },
    { key: "updated", header: t("siteAccess.users.passwordSet"), cell: (u) => <RelTime iso={u.updatedAt} /> },
    {
      key: "actions",
      header: t("siteAccess.users.actions"),
      srHeader: true,
      cell: (u) => (
        <RowActions>
          <IconButton label={t("siteAccess.users.newPasswordFor", { user: u.name })} text={t("siteAccess.users.newPassword")} icon={<Key size={16} aria-hidden="true" />} tipBelow={u === view.users[0]} disabled={busy} onClick={() => void renew(u.name)} />
          {confirm === u.name ? (
            <Button variant="danger" disabled={busy} onClick={() => void remove(u.name)}>
              {t("siteAccess.users.removeConfirm", { user: u.name })}
            </Button>
          ) : (
            <IconButton
              label={last ? t("siteAccess.users.lastStays") : t("siteAccess.users.removeUser", { user: u.name })}
              text={t("siteAccess.users.remove")} icon={<Trash size={16} aria-hidden="true" />}
              danger
              tipBelow={u === view.users[0]}
              disabled={busy || last}
              onClick={() => setConfirm(u.name)}
            />
          )}
        </RowActions>
      ),
    },
  ];

  return (
    <Card title={t("siteAccess.users.title")}>
      <div className="ui-stack">
        <Table label={t("siteAccess.users.title")} actionsBelow columns={columns} rows={view.users} rowKey={(u) => u.name} empty={t("siteAccess.users.none")} />
        <form className="ui-stack" onSubmit={add} noValidate>
          <fieldset className="ui-fieldset ui-stack" disabled={busy || view.users.length >= view.limits.users}>
            <legend>{t("siteAccess.users.addLegend")}</legend>
            <Field label={t("siteAccess.users.name")} hint={t("siteAccess.users.nameHint")}>
              <input className="ui-input" value={name} autoComplete="off" spellCheck={false} onChange={(e) => setName(e.target.value)} />
            </Field>
            <label className="ui-check">
              <input type="radio" name={site + "-pw"} checked={!own} onChange={() => setOwn(false)} />
              {t("siteAccess.users.generate")}
            </label>
            <label className="ui-check">
              <input type="radio" name={site + "-pw"} checked={own} onChange={() => setOwn(true)} />
              {t("siteAccess.users.choose")}
            </label>
            {own && (
              <Field label={t("siteAccess.settings.password")} hint={t("siteAccess.users.passwordHint", { min: view.limits.minPassword, max: view.limits.maxPassword })}>
                <input className="ui-input" type="password" value={password} autoComplete="new-password" onChange={(e) => setPassword(e.target.value)} />
              </Field>
            )}
            <div className="ui-row">
              <Button type="submit" variant="primary">
                {t("siteAccess.users.add")}
              </Button>
            </div>
          </fieldset>
        </form>
        {error && <Notice tone="err" title={error} />}
        {done && <Notice tone="ok" title={done} />}
      </div>
    </Card>
  );
}

// PasswordNotice shows a generated password once. The panel cannot show it
// again; closing the notice drops it from memory.
function PasswordNotice({ site, secret, onClose }: { site: Site; secret: { user: string; password: string }; onClose: () => void }) {
  const { t } = useTranslation();
  const [copied, setCopied] = useState("");
  const heading = useRef<HTMLHeadingElement>(null);

  useEffect(() => {
    setCopied("");
    heading.current?.scrollIntoView?.({ block: "start" });
    heading.current?.focus();
  }, [secret]);

  async function copy() {
    try {
      await navigator.clipboard.writeText(secret.password);
      setCopied(t("common.status.copied"));
    } catch {
      setCopied(t("siteAccess.secret.copyFailed"));
    }
  }

  return (
    <section className="ui-card ui-card-body ui-stack ui-attention" aria-labelledby="access-secret">
      <h2 id="access-secret" ref={heading} tabIndex={-1}>
        {t("siteAccess.secret.title")}
      </h2>
      <p className="ui-flush">{t("siteAccess.secret.text")}</p>
      <dl className="ui-facts">
        <dt>{t("siteAccess.secret.site")}</dt>
        <dd>
          <code>{site.domains[0]}</code>
        </dd>
        <dt>{t("siteAccess.users.user")}</dt>
        <dd>
          <code>{secret.user}</code>
        </dd>
      </dl>
      <Field label={t("siteAccess.settings.password")}>
        <input
          readOnly
          value={secret.password}
          onFocus={(e) => e.currentTarget.select()}
          autoComplete="off"
          spellCheck={false}
          className="ui-input ui-mono"
        />
      </Field>
      <div className="ui-row">
        <Button onClick={() => void copy()}>{t("siteAccess.secret.copy")}</Button>
        <Button variant="primary" onClick={onClose}>
          {t("siteAccess.secret.saved")}
        </Button>
      </div>
      {copied && <Notice tone="ok" title={copied} />}
    </section>
  );
}
