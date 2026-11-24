// SPDX-License-Identifier: AGPL-3.0-or-later

import { useCallback, useEffect, useRef, useState, type FormEvent } from "react";
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
import { Key, Trash } from "@phosphor-icons/react";
import { Button, Card, Field, IconButton, Notice, RelTime, RowActions, Table, type Column } from "./ui";

// SiteAccess is the "Access" tab of a site (ADR-0066): an IP rule and
// password protection for the whole site or some paths, with its users.
export function SiteAccess({ site }: { site: Site }) {
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
  if (!view) return <p aria-busy="true">Loading…</p>;

  return (
    <>
      {secret && <PasswordNotice site={site} secret={secret} onClose={() => setSecret(null)} />}
      <SettingsCard site={name} view={view} onSaved={setView} />
      <UsersCard site={name} view={view} onChange={load} onSecret={setSecret} />
    </>
  );
}

function SettingsCard({ site, view, onSaved }: { site: string; view: AccessView; onSaved: (v: AccessView) => void }) {
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

  return (
    <Card title="Access protection">
      <form className="ui-stack" onSubmit={(e) => void submit(e)} noValidate>
        <p className="ui-hint">
          nginx checks these rules before the site sees a request, for every file and page. Let's Encrypt still reaches the site, so certificates keep
          renewing. Behind a CDN such as Cloudflare, switch on "Real visitor address" in the Domains & HTTPS tab first, or the rules see the CDN instead of
          the visitor.
        </p>
        <fieldset className="ui-fieldset ui-stack" disabled={busy}>
          <legend>IP rule</legend>
          <label className="ui-check">
            <input type="radio" name={site + "-ipmode"} checked={form.ipMode === ""} onChange={() => set({ ipMode: "" })} />
            Everybody may visit
          </label>
          <label className="ui-check">
            <input type="radio" name={site + "-ipmode"} checked={form.ipMode === "allow"} onChange={() => set({ ipMode: "allow" })} />
            Only these addresses may visit
          </label>
          <label className="ui-check">
            <input type="radio" name={site + "-ipmode"} checked={form.ipMode === "deny"} onChange={() => set({ ipMode: "deny" })} />
            Block these addresses
          </label>
          {form.ipMode !== "" && (
            <Field label="Addresses" hint={`Addresses or ranges such as 203.0.113.0/24, one per line, at most ${view.limits.ips}. Others get 403 Forbidden.`}>
              <textarea
                className="ui-input ui-textarea ui-mono"
                rows={4}
                value={form.ips}
                spellCheck={false}
                placeholder={"203.0.113.7\n2001:db8::/48"}
                onChange={(e) => set({ ips: e.target.value })}
              />
            </Field>
          )}
          {form.ipMode !== "" && view.clientAddress && (
            <p className="ui-hint">
              Your address as the panel sees it: <code>{view.clientAddress}</code>
            </p>
          )}
          {locked && <Notice tone="warn" title="Your own address is not on the list. You will not be able to open the site from here after saving." />}
        </fieldset>
        <fieldset className="ui-fieldset ui-stack" disabled={busy}>
          <legend>Password</legend>
          <label className="ui-check">
            <input type="checkbox" checked={form.password} onChange={(e) => set({ password: e.target.checked })} />
            Ask for a user name and password
          </label>
          {form.password && view.users.length === 0 && <Notice tone="warn" title="Add a user below first." />}
          {form.password && (
            <>
              <label className="ui-check">
                <input type="radio" name={site + "-scope"} checked={form.scope === "site"} onChange={() => set({ scope: "site" })} />
                For the whole site
              </label>
              <label className="ui-check">
                <input type="radio" name={site + "-scope"} checked={form.scope === "paths"} onChange={() => set({ scope: "paths" })} />
                Only for these paths
              </label>
              {form.scope === "paths" && (
                <Field
                  label="Paths"
                  hint={`One per line, at most ${view.limits.paths}. A path covers everything below it, in any upper or lower case: /wp-admin covers /wp-admin/ and /WP-Admin/users.php, not /wp-administrator.`}
                >
                  <textarea
                    className="ui-input ui-textarea ui-mono"
                    rows={3}
                    value={form.paths}
                    spellCheck={false}
                    placeholder={"/wp-admin\n/wp-login.php"}
                    onChange={(e) => set({ paths: e.target.value })}
                  />
                </Field>
              )}
              <Field label="No password from these addresses" hint={`Optional, for example your office or an uptime monitor, one per line, at most ${view.limits.bypass}.`}>
                <textarea
                  className="ui-input ui-textarea ui-mono"
                  rows={2}
                  value={form.bypass}
                  spellCheck={false}
                  placeholder="198.51.100.0/24"
                  onChange={(e) => set({ bypass: e.target.value })}
                />
              </Field>
              <p className="ui-hint">
                The site's application no longer gets the browser's Authorization header on protected paths. Each protected request costs nginx a few
                milliseconds; on a busy site, protect only the admin paths. After 10 wrong passwords in 10 minutes, fail2ban blocks the address for an hour.
              </p>
            </>
          )}
        </fieldset>
        {error && <Notice tone="err" title={error} />}
        {done && <Notice tone="ok" title={done} />}
        <div className="ui-row">
          <Button type="submit" variant="primary" disabled={busy}>
            Save
          </Button>
        </div>
      </form>
    </Card>
  );
}

function summary(v: AccessView): string {
  const parts: string[] = [];
  if (v.ipMode === "allow") parts.push(`only ${v.ips.length === 1 ? "1 address" : `${v.ips.length} addresses`} may visit`);
  if (v.ipMode === "deny") parts.push(`${v.ips.length === 1 ? "1 address is" : `${v.ips.length} addresses are`} blocked`);
  if (v.password) parts.push(v.paths.length > 0 ? `a password is asked on ${v.paths.length === 1 ? "1 path" : `${v.paths.length} paths`}` : "a password is asked for the whole site");
  if (parts.length === 0) return "Saved. Everybody may visit without a password.";
  return "Saved: " + parts.join(", ") + ".";
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
      setError("Enter a user name.");
      return;
    }
    if (own && password.length < view.limits.minPassword) {
      setError(`The password needs at least ${view.limits.minPassword} characters.`);
      return;
    }
    void run(async () => {
      const r = await addAccessUser(site, user, own ? password : "");
      setName("");
      setPassword("");
      if (r.password) onSecret({ user, password: r.password });
      return `User ${user} added.`;
    });
  }

  const renew = (user: string) =>
    run(async () => {
      const r = await setAccessUserPassword(site, user, "");
      if (r.password) onSecret({ user, password: r.password });
      return `User ${user} has a new password.`;
    });

  const remove = (user: string) =>
    run(async () => {
      await deleteAccessUser(site, user);
      return `User ${user} removed.`;
    });

  const last = view.password && view.users.length === 1;

  const columns: Column<AccessUser>[] = [
    { key: "name", header: "User", cell: (u) => <code>{u.name}</code>, sortValue: (u) => u.name },
    { key: "updated", header: "Password set", cell: (u) => <RelTime iso={u.updatedAt} /> },
    {
      key: "actions",
      header: "Actions",
      srHeader: true,
      cell: (u) => (
        <RowActions>
          <IconButton label={`New password for ${u.name}`} icon={<Key size={16} aria-hidden="true" />} tipBelow={u === view.users[0]} disabled={busy} onClick={() => void renew(u.name)} />
          {confirm === u.name ? (
            <Button variant="danger" disabled={busy} onClick={() => void remove(u.name)}>
              Remove {u.name}?
            </Button>
          ) : (
            <IconButton
              label={last ? "The last user stays while the password is on" : `Remove ${u.name}`}
              icon={<Trash size={16} aria-hidden="true" />}
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
    <Card title="Users">
      <div className="ui-stack">
        <Table label="Users" columns={columns} rows={view.users} rowKey={(u) => u.name} empty="No users yet." />
        <form className="ui-stack" onSubmit={add} noValidate>
          <fieldset className="ui-fieldset ui-stack" disabled={busy || view.users.length >= view.limits.users}>
            <legend>Add a user</legend>
            <Field label="User name" hint="Letters, digits and . _ @ -, at most 64 characters.">
              <input className="ui-input" value={name} autoComplete="off" spellCheck={false} onChange={(e) => setName(e.target.value)} />
            </Field>
            <label className="ui-check">
              <input type="radio" name={site + "-pw"} checked={!own} onChange={() => setOwn(false)} />
              Generate a strong password (shown once)
            </label>
            <label className="ui-check">
              <input type="radio" name={site + "-pw"} checked={own} onChange={() => setOwn(true)} />
              Choose a password
            </label>
            {own && (
              <Field label="Password" hint={`${view.limits.minPassword} to ${view.limits.maxPassword} characters.`}>
                <input className="ui-input" type="password" value={password} autoComplete="new-password" onChange={(e) => setPassword(e.target.value)} />
              </Field>
            )}
            <div className="ui-row">
              <Button type="submit" variant="primary">
                Add user
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
      setCopied("Copied.");
    } catch {
      setCopied("Copying is not possible here. Select the password and copy it by hand.");
    }
  }

  return (
    <section className="ui-card ui-card-body ui-stack ui-attention" aria-labelledby="access-secret">
      <h2 id="access-secret" ref={heading} tabIndex={-1}>
        Save this password now
      </h2>
      <p className="ui-flush">Hostashi stores only a hash and cannot show it again. If it is lost, set a new one.</p>
      <dl className="ui-facts">
        <dt>Site</dt>
        <dd>
          <code>{site.domains[0]}</code>
        </dd>
        <dt>User</dt>
        <dd>
          <code>{secret.user}</code>
        </dd>
      </dl>
      <Field label="Password">
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
        <Button onClick={() => void copy()}>Copy password</Button>
        <Button variant="primary" onClick={onClose}>
          I have saved it
        </Button>
      </div>
      {copied && <Notice tone="ok" title={copied} />}
    </section>
  );
}
