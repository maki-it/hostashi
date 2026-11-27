// SPDX-License-Identifier: AGPL-3.0-or-later

import { Fragment, useCallback, useEffect, useState, type FormEvent } from "react";
import { useTranslation } from "react-i18next";
import { formatDate as formatDay } from "./format";
import type { Site } from "./sites";
import { Badge, Button, Card, CopyValue, Field, Notice } from "./ui";
import {
  accessName,
  addSSHKey,
  changeSSHKey,
  deleteSSHKey,
  hostKeys,
  keyProblem,
  labelProblem,
  listSSHKeys,
  maxSSHKeys,
  sshMessage,
  suggestLabel,
  type SSHAccess,
  type SSHHostKey,
  type SSHKey,
} from "./ssh";

const formatDate = (unix: number) => formatDay(new Date(unix * 1000));

// SSHSettings lists the public keys that may log in as the site user and
// adds, changes and deletes them. Passwords are never accepted.
export function SSHSettings({ site, host = window.location.hostname }: { site: Site; host?: string }) {
  const { t } = useTranslation();
  const [keys, setKeys] = useState<SSHKey[] | null>(null);
  const [loadError, setLoadError] = useState("");
  const [adding, setAdding] = useState(false);
  const [done, setDone] = useState("");

  const load = useCallback(async () => {
    try {
      setKeys((await listSSHKeys(site.name)).keys);
      setLoadError("");
    } catch (err) {
      setLoadError(sshMessage(err));
    }
  }, [site.name]);

  useEffect(() => {
    void load();
  }, [load]);

  const changed = async (msg: string) => {
    setDone(msg);
    await load();
  };

  const shell = keys?.some((k) => k.access === "shell") ?? false;

  return (
    <Card title={t("ssh.title")}>
      <div className="ui-stack">
        <p className="ui-flush">{t("ssh.intro", { user: site.name })}</p>
        {site.disabled && <Notice tone="warn" title={t("ssh.disabled")} />}
        {!keys ? (
          loadError ? (
            <Notice tone="err" title={loadError} />
          ) : (
            <p aria-busy="true">{t("ssh.loading")}</p>
          )
        ) : (
          <>
            {loadError && <Notice tone="err" title={loadError} />}
            {keys.length === 0 ? (
              <p className="ui-hint ui-flush">{t("ssh.none")}</p>
            ) : (
              <>
                <LoginInfo site={site} host={host} shell={shell} />
                <ul className="ui-plain-list ui-stack" aria-label={t("ssh.list", { site: site.name })}>
                  {keys.map((k) => (
                    <KeyRow key={k.id} site={site} sshKey={k} onChange={changed} />
                  ))}
                </ul>
              </>
            )}
            {done && <Notice tone="ok" title={done} />}
            {keys.length >= maxSSHKeys ? (
              <p className="ui-hint ui-flush">{t("ssh.max", { max: maxSSHKeys })}</p>
            ) : adding ? (
              <AddKeyForm
                onCancel={() => setAdding(false)}
                onSubmit={async (k) => {
                  await addSSHKey(site.name, k);
                  setAdding(false);
                  await changed(k.access === "shell" ? t("ssh.addedShell") : t("ssh.addedSftp"));
                }}
              />
            ) : (
              <div className="ui-row">
                <Button
                  onClick={() => {
                    setDone("");
                    setAdding(true);
                  }}
                >
                  {t("ssh.add")}
                </Button>
              </div>
            )}
          </>
        )}
      </div>
    </Card>
  );
}

// LoginInfo shows how to log in and the host keys to compare at the first
// login. The host keys load once; without them the login line still shows.
function LoginInfo({ site, host, shell }: { site: Site; host: string; shell: boolean }) {
  const { t } = useTranslation();
  const [keys, setKeys] = useState<SSHHostKey[] | null>(null);
  const [failed, setFailed] = useState(false);

  useEffect(() => {
    let live = true;
    hostKeys()
      .then((r) => live && setKeys(r.keys))
      .catch(() => live && setFailed(true));
    return () => {
      live = false;
    };
  }, []);

  return (
    <div className="ui-stack-tight" role="group" aria-label={t("ssh.login.label", { site: site.name })}>
      <p className="ui-flush ui-row">
        {t("ssh.login.with")} <CopyValue value={`sftp ${site.name}@${host}`} label={t("ssh.login.copySftp")} />
        {shell && (
          <>
            {t("ssh.login.or")} <CopyValue value={`ssh ${site.name}@${host}`} label={t("ssh.login.copySsh")} />
          </>
        )}
      </p>
      {keys && keys.length > 0 && (
        <>
          <p className="ui-hint ui-flush">{t("ssh.login.fingerprints")}</p>
          <dl className="ui-facts" aria-label={t("ssh.login.fingerprintsLabel")}>
            {keys.map((k) => (
              <Fragment key={k.type}>
                <dt>{k.type}</dt>
                <dd>
                  <code>{k.fingerprint}</code>
                </dd>
              </Fragment>
            ))}
          </dl>
        </>
      )}
      {failed && <p className="ui-hint ui-flush">{t("ssh.login.failed")}</p>}
    </div>
  );
}

function KeyRow({ site, sshKey, onChange }: { site: Site; sshKey: SSHKey; onChange: (msg: string) => Promise<void> }) {
  const { t } = useTranslation();
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [editing, setEditing] = useState(false);
  const [confirming, setConfirming] = useState(false);
  const [label, setLabel] = useState(sshKey.label);
  const [access, setAccess] = useState<SSHAccess>(sshKey.access);

  async function act(f: () => Promise<string>) {
    setBusy(true);
    setError("");
    try {
      await onChange(await f());
    } catch (err) {
      setError(sshMessage(err));
    } finally {
      setBusy(false);
    }
  }

  async function save(e: FormEvent) {
    e.preventDefault();
    const problem = labelProblem(label);
    if (problem) {
      setError(problem);
      return;
    }
    await act(async () => {
      await changeSSHKey(site.name, sshKey.id, { label, access });
      setEditing(false);
      return t("ssh.key.saved");
    });
  }

  if (editing) {
    return (
      <li className="ui-subsection">
        <form className="ui-stack" onSubmit={(e) => void save(e)} noValidate aria-label={t("ssh.key.changeLabel", { label: sshKey.label })}>
          <Field label={t("ssh.key.label")}>
            <input className="ui-input" value={label} onChange={(e) => setLabel(e.target.value)} autoComplete="off" maxLength={64} />
          </Field>
          <AccessChoice value={access} onChange={setAccess} />
          {error && <Notice tone="err" title={error} />}
          <div className="ui-row">
            <Button variant="primary" type="submit" disabled={busy}>
              {t("ssh.key.save")}
            </Button>
            <Button disabled={busy} onClick={() => setEditing(false)}>
              {t("ssh.key.cancel")}
            </Button>
          </div>
        </form>
      </li>
    );
  }

  return (
    <li className="ui-subsection ui-stack-tight">
      <div className="ui-row">
        <strong>{sshKey.label}</strong> {sshKey.access === "shell" ? <Badge tone="warn">{t("ssh.key.shell")}</Badge> : <Badge tone="off">{t("ssh.key.sftpOnly")}</Badge>}
      </div>
      <dl className="ui-facts" aria-label={t("ssh.key.details", { label: sshKey.label })}>
        <dt>{t("ssh.key.fingerprint")}</dt>
        <dd>
          <code>{sshKey.fingerprint}</code>
        </dd>
        <dt>{t("ssh.key.type")}</dt>
        <dd>{sshKey.public.split(" ")[0]}</dd>
        <dt>{t("ssh.key.added")}</dt>
        <dd>{formatDate(sshKey.createdAt)}</dd>
      </dl>
      {confirming ? (
        <div className="ui-row" role="alertdialog" aria-label={t("ssh.key.deleteLabel", { label: sshKey.label })}>
          <span>{t("ssh.key.deleteText")}</span>
          <Button
            variant="danger"
            disabled={busy}
            onClick={() =>
              void act(async () => {
                await deleteSSHKey(site.name, sshKey.id);
                return t("ssh.key.deleted");
              })
            }
          >
            {t("ssh.key.delete")}
          </Button>
          <Button disabled={busy} onClick={() => setConfirming(false)}>
            {t("ssh.key.cancel")}
          </Button>
        </div>
      ) : (
        <div className="ui-row">
          <Button
            disabled={busy}
            onClick={() => {
              setLabel(sshKey.label);
              setAccess(sshKey.access);
              setError("");
              setEditing(true);
            }}
          >
            {t("ssh.key.change")}
          </Button>
          <Button variant="danger" disabled={busy} onClick={() => setConfirming(true)}>
            {t("ssh.key.deleteShort")}
          </Button>
        </div>
      )}
      {error && <Notice tone="err" title={error} />}
    </li>
  );
}

function AccessChoice({ value, onChange }: { value: SSHAccess; onChange: (a: SSHAccess) => void }) {
  const { t } = useTranslation();
  return (
    <fieldset className="ui-fieldset ui-stack-tight">
      <legend>{t("ssh.access.legend")}</legend>
      <label className="ui-check">
        <input type="radio" name="access" checked={value === "sftp"} onChange={() => onChange("sftp")} />
        {t("ssh.access.sftpText", { name: accessName("sftp") })}
      </label>
      <label className="ui-check">
        <input type="radio" name="access" checked={value === "shell"} onChange={() => onChange("shell")} />
        {t("ssh.access.shellText", { name: accessName("shell") })}
      </label>
    </fieldset>
  );
}

function AddKeyForm({
  onSubmit,
  onCancel,
}: {
  onSubmit: (k: { label: string; access: SSHAccess; public: string }) => Promise<void>;
  onCancel: () => void;
}) {
  const { t } = useTranslation();
  const [key, setKey] = useState("");
  const [label, setLabel] = useState("");
  const [labelTouched, setLabelTouched] = useState(false);
  const [access, setAccess] = useState<SSHAccess>("sftp");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");

  async function submit(e: FormEvent) {
    e.preventDefault();
    const problem = keyProblem(key) || labelProblem(label);
    if (problem) {
      setError(problem);
      return;
    }
    setBusy(true);
    setError("");
    try {
      await onSubmit({ label, access, public: key.trim() });
    } catch (err) {
      setError(sshMessage(err));
      setBusy(false);
    }
  }

  return (
    <form className="ui-stack ui-subsection" onSubmit={(e) => void submit(e)} noValidate aria-label={t("ssh.new.label")}>
      <strong>{t("ssh.new.label")}</strong>
      <Field label={t("ssh.new.public")} hint={t("ssh.new.publicHint")}>
        <textarea
          value={key}
          onChange={(e) => {
            setKey(e.target.value);
            if (!labelTouched) setLabel(suggestLabel(e.target.value));
          }}
          rows={3}
          autoComplete="off"
          spellCheck={false}
          className="ui-input ui-textarea ui-mono"
        />
      </Field>
      <Field label={t("ssh.new.keyLabel")} hint={t("ssh.new.keyLabelHint")}>
        <input
          className="ui-input"
          value={label}
          onChange={(e) => {
            setLabel(e.target.value);
            setLabelTouched(true);
          }}
          autoComplete="off"
          maxLength={64}
        />
      </Field>
      <AccessChoice value={access} onChange={setAccess} />
      {error && <Notice tone="err" title={error} />}
      <div className="ui-row">
        <Button variant="primary" type="submit" disabled={busy}>
          {busy ? t("ssh.new.adding") : t("ssh.new.submit")}
        </Button>
        <Button disabled={busy} onClick={onCancel}>
          {t("ssh.new.cancel")}

        </Button>
      </div>
    </form>
  );
}
