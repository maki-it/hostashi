// SPDX-License-Identifier: AGPL-3.0-or-later

import { Fragment, useCallback, useEffect, useState, type FormEvent } from "react";
import type { Site } from "./sites";
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

const formatDate = (unix: number) => new Date(unix * 1000).toLocaleDateString(undefined, { dateStyle: "medium" });

// SSHSettings lists the public keys that may log in as the site user and
// adds, changes and deletes them. Passwords are never accepted.
export function SSHSettings({ site, host = window.location.hostname }: { site: Site; host?: string }) {
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
    <fieldset>
      <legend>SSH and SFTP of {site.name}</legend>
      <p className="muted">
        Log in as the user {site.name} with a key, never with a password. A key is for SFTP only unless you allow a shell; a shell can run any
        command as the site user. Logins see the server as the site's PHP code does.
      </p>
      {site.disabled && <p className="muted">The site is disabled: nobody can log in until it is enabled. The keys are kept.</p>}
      {!keys ? (
        loadError ? (
          <p role="alert">{loadError}</p>
        ) : (
          <p aria-busy="true">Loading SSH keys…</p>
        )
      ) : (
        <>
          {loadError && <p role="alert">{loadError}</p>}
          {keys.length === 0 ? (
            <p>No keys yet: nobody can log in.</p>
          ) : (
            <>
              <LoginInfo site={site} host={host} shell={shell} />
              <ul className="jobs" aria-label={"SSH keys of " + site.name}>
                {keys.map((k) => (
                  <KeyRow key={k.id} site={site} sshKey={k} onChange={changed} />
                ))}
              </ul>
            </>
          )}
          {done && <p role="status">{done}</p>}
          {keys.length >= maxSSHKeys ? (
            <p className="muted">A site has at most {maxSSHKeys} keys.</p>
          ) : adding ? (
            <AddKeyForm
              onCancel={() => setAdding(false)}
              onSubmit={async (k) => {
                await addSSHKey(site.name, k);
                setAdding(false);
                await changed(k.access === "shell" ? "Key added with shell access." : "Key added for SFTP.");
              }}
            />
          ) : (
            <div className="actions">
              <button
                type="button"
                className="secondary"
                onClick={() => {
                  setDone("");
                  setAdding(true);
                }}
              >
                Add SSH key
              </button>
            </div>
          )}
        </>
      )}
    </fieldset>
  );
}

// LoginInfo shows how to log in and the host keys to compare at the first
// login. The host keys load once; without them the login line still shows.
function LoginInfo({ site, host, shell }: { site: Site; host: string; shell: boolean }) {
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
    <div aria-label={"Login of " + site.name}>
      <p>
        Log in with <code>sftp {site.name + "@" + host}</code>
        {shell && (
          <>
            {" "}
            or <code>ssh {site.name + "@" + host}</code>
          </>
        )}
        .
      </p>
      {keys && keys.length > 0 && (
        <>
          <p className="muted">At the first login, check that the server shows one of these fingerprints:</p>
          <dl className="facts" aria-label="Host key fingerprints">
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
      {failed && <p className="muted">The server's host key fingerprints could not be read right now.</p>}
    </div>
  );
}

function KeyRow({ site, sshKey, onChange }: { site: Site; sshKey: SSHKey; onChange: (msg: string) => Promise<void> }) {
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
      return "Key saved.";
    });
  }

  if (editing) {
    return (
      <li>
        <form onSubmit={(e) => void save(e)} noValidate aria-label={"Change key " + sshKey.label}>
          <label>
            Label
            <input value={label} onChange={(e) => setLabel(e.target.value)} autoComplete="off" maxLength={64} />
          </label>
          <AccessChoice value={access} onChange={setAccess} />
          {error && <p role="alert">{error}</p>}
          <div className="actions">
            <button type="submit" disabled={busy}>
              Save key
            </button>
            <button type="button" className="secondary" disabled={busy} onClick={() => setEditing(false)}>
              Cancel
            </button>
          </div>
        </form>
      </li>
    );
  }

  return (
    <li>
      <div>
        <strong>{sshKey.label}</strong> {sshKey.access === "shell" ? <span className="badge warn">Shell</span> : <span className="badge">SFTP only</span>}
      </div>
      <dl className="facts" aria-label={"Details of key " + sshKey.label}>
        <dt>Fingerprint</dt>
        <dd>
          <code>{sshKey.fingerprint}</code>
        </dd>
        <dt>Type</dt>
        <dd>{sshKey.public.split(" ")[0]}</dd>
        <dt>Added</dt>
        <dd>{formatDate(sshKey.createdAt)}</dd>
      </dl>
      {confirming ? (
        <div className="actions">
          <span>Delete this key? Sessions that are open now keep running.</span>
          <button
            type="button"
            className="danger"
            disabled={busy}
            onClick={() =>
              void act(async () => {
                await deleteSSHKey(site.name, sshKey.id);
                return "Key deleted.";
              })
            }
          >
            Delete key
          </button>
          <button type="button" className="secondary" disabled={busy} onClick={() => setConfirming(false)}>
            Cancel
          </button>
        </div>
      ) : (
        <div className="actions">
          <button
            type="button"
            className="secondary"
            disabled={busy}
            onClick={() => {
              setLabel(sshKey.label);
              setAccess(sshKey.access);
              setError("");
              setEditing(true);
            }}
          >
            Change
          </button>
          <button type="button" className="secondary danger" disabled={busy} onClick={() => setConfirming(true)}>
            Delete
          </button>
        </div>
      )}
      {error && <p role="alert">{error}</p>}
    </li>
  );
}

function AccessChoice({ value, onChange }: { value: SSHAccess; onChange: (a: SSHAccess) => void }) {
  return (
    <fieldset>
      <legend>Access</legend>
      <label className="check">
        <input type="radio" name="access" checked={value === "sftp"} onChange={() => onChange("sftp")} />
        {accessName("sftp")}: upload and download files, no commands
      </label>
      <label className="check">
        <input type="radio" name="access" checked={value === "shell"} onChange={() => onChange("shell")} />
        {accessName("shell")}: also a terminal, for wp-cli, composer or git
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
    <form onSubmit={(e) => void submit(e)} noValidate aria-label="New SSH key">
      <label>
        Public key
        <small>The contents of a .pub file, for example ~/.ssh/id_ed25519.pub. Ed25519, ECDSA or RSA with at least 3072 bits.</small>
        <textarea
          value={key}
          onChange={(e) => {
            setKey(e.target.value);
            if (!labelTouched) setLabel(suggestLabel(e.target.value));
          }}
          rows={3}
          autoComplete="off"
          spellCheck={false}
          className="code"
        />
      </label>
      <label>
        Label
        <small>Shown in the list, so you know whose key it is.</small>
        <input
          value={label}
          onChange={(e) => {
            setLabel(e.target.value);
            setLabelTouched(true);
          }}
          autoComplete="off"
          maxLength={64}
        />
      </label>
      <AccessChoice value={access} onChange={setAccess} />
      {error && <p role="alert">{error}</p>}
      <div className="actions">
        <button type="submit" disabled={busy}>
          {busy ? "Adding…" : "Add key"}
        </button>
        <button type="button" className="secondary" disabled={busy} onClick={onCancel}>
          Cancel
        </button>
      </div>
    </form>
  );
}
