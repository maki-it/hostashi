// SPDX-License-Identifier: AGPL-3.0-or-later

import { useCallback, useEffect, useRef, useState, type FormEvent } from "react";
import { listSites, type Site } from "./backups";
import {
  createDatabase,
  createUser,
  databaseMessage,
  deleteDatabase,
  defaultDumpPath,
  deleteUser,
  getDumpJob,
  listDatabases,
  nameProblem,
  pathProblem,
  resetPassword,
  startDump,
  suggestUser,
  type Credentials,
  type Database,
  type DumpJob,
} from "./databases";
import { poll } from "./backups";

// Databases is the database page: create a database for a site, manage its
// users and passwords, and delete it. A generated password is shown once.
export function Databases() {
  const [dbs, setDbs] = useState<Database[] | null>(null);
  const [sites, setSites] = useState<Site[] | null>(null);
  const [error, setError] = useState("");
  const [secret, setSecret] = useState<Credentials | null>(null);

  const load = useCallback(async () => {
    try {
      const [d, s] = await Promise.all([listDatabases(), listSites()]);
      setDbs(d);
      setSites(s);
      setError("");
    } catch (err) {
      setError(databaseMessage(err));
    }
  }, []);

  useEffect(() => {
    void load();
  }, [load]);

  if (!dbs || !sites) {
    return error ? (
      <p role="alert" className="card">
        {error}
      </p>
    ) : (
      <p aria-busy="true">Loading…</p>
    );
  }

  return (
    <div className="stack">
      {secret && <PasswordNotice creds={secret} onClose={() => setSecret(null)} />}
      <CreateCard sites={sites} onCreated={(c) => (c ? setSecret(c) : undefined)} onChange={load} />
      <section className="card" aria-labelledby="db-list">
        <h2 id="db-list">Databases</h2>
        {error && <p role="alert">{error}</p>}
        {dbs.length === 0 ? (
          <p>No databases yet.</p>
        ) : (
          <ul className="rows">
            {dbs.map((d) => (
              <DatabaseRow key={d.name} db={d} onSecret={setSecret} onChange={load} />
            ))}
          </ul>
        )}
      </section>
    </div>
  );
}

// PasswordNotice shows a generated password once. The panel cannot show it
// again; closing the notice drops it from memory.
function PasswordNotice({ creds, onClose }: { creds: Credentials; onClose: () => void }) {
  const [copied, setCopied] = useState("");
  const heading = useRef<HTMLHeadingElement>(null);

  // Bring the notice into view: the action that caused it may be far down.
  useEffect(() => {
    setCopied("");
    heading.current?.scrollIntoView?.({ block: "start" });
    heading.current?.focus();
  }, [creds]);

  async function copy() {
    try {
      await navigator.clipboard.writeText(creds.password);
      setCopied("Copied.");
    } catch {
      setCopied("Copying is not possible here. Select the password and copy it by hand.");
    }
  }

  return (
    <section className="card notice" aria-labelledby="db-secret">
      <h2 id="db-secret" ref={heading} tabIndex={-1}>Save this password now</h2>
      <p>Hostashi does not store it and cannot show it again. If it is lost, set a new one.</p>
      <dl className="facts">
        <dt>Database</dt>
        <dd>
          <code>{creds.database}</code>
        </dd>
        <dt>User</dt>
        <dd>
          <code>{creds.user}</code>
        </dd>
        <dt>Host</dt>
        <dd>
          <code>localhost</code>
        </dd>
      </dl>
      <label>
        Password
        <input
          readOnly
          value={creds.password}
          onFocus={(e) => e.currentTarget.select()}
          autoComplete="off"
          spellCheck={false}
          className="mono"
        />
      </label>
      <button type="button" className="secondary" onClick={() => void copy()}>
        Copy password
      </button>
      {copied && <p role="status">{copied}</p>}
      <button type="button" onClick={onClose}>
        I have saved it
      </button>
    </section>
  );
}

function CreateCard({
  sites,
  onCreated,
  onChange,
}: {
  sites: Site[];
  onCreated: (c: Credentials | null) => void;
  onChange: () => Promise<void>;
}) {
  const [site, setSite] = useState(sites[0]?.name ?? "");
  const [name, setName] = useState("");
  const [withUser, setWithUser] = useState(true);
  const [user, setUser] = useState("");
  const [userTouched, setUserTouched] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");

  const userName = userTouched ? user : suggestUser(name);

  async function submit(e: FormEvent) {
    e.preventDefault();
    const problem = nameProblem("database", name) || (withUser ? nameProblem("user", userName) : "");
    if (problem) {
      setError(problem);
      return;
    }
    setBusy(true);
    setError("");
    try {
      const d = await createDatabase(site, name, withUser ? userName : "");
      onCreated(d.password ? { database: d.name, user: userName, password: d.password } : null);
      setName("");
      setUser("");
      setUserTouched(false);
      await onChange();
    } catch (err) {
      setError(databaseMessage(err));
    } finally {
      setBusy(false);
    }
  }

  if (sites.length === 0) {
    return (
      <section className="card" aria-labelledby="db-create">
        <h2 id="db-create">New database</h2>
        <p>Create a site first. Every database belongs to a site.</p>
      </section>
    );
  }

  return (
    <section className="card" aria-labelledby="db-create">
      <h2 id="db-create">New database</h2>
      <form onSubmit={(e) => void submit(e)} noValidate>
        <label>
          Site
          <select value={site} onChange={(e) => setSite(e.target.value)}>
            {sites.map((s) => (
              <option key={s.name} value={s.name}>
                {s.name}
              </option>
            ))}
          </select>
        </label>
        <label>
          Database name
          <small>Lowercase letters, digits and _, starting with a letter.</small>
          <input value={name} onChange={(e) => setName(e.target.value.trim())} required autoComplete="off" spellCheck={false} />
        </label>
        <label className="check">
          <input type="checkbox" checked={withUser} onChange={(e) => setWithUser(e.target.checked)} />
          Create a user for it with a generated password
        </label>
        {withUser && (
          <label>
            User name
            <input
              value={userName}
              onChange={(e) => {
                setUserTouched(true);
                setUser(e.target.value.trim());
              }}
              required
              autoComplete="off"
              spellCheck={false}
            />
          </label>
        )}
        {error && <p role="alert">{error}</p>}
        <button type="submit" disabled={busy}>
          Create database
        </button>
      </form>
    </section>
  );
}

function DatabaseRow({
  db,
  onSecret,
  onChange,
}: {
  db: Database;
  onSecret: (c: Credentials) => void;
  onChange: () => Promise<void>;
}) {
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const [confirming, setConfirming] = useState(false);
  const [typed, setTyped] = useState("");

  async function act(f: () => Promise<void>) {
    setBusy(true);
    setError("");
    try {
      await f();
    } catch (err) {
      setError(databaseMessage(err));
    } finally {
      setBusy(false);
    }
  }

  const drop = () =>
    act(async () => {
      await deleteDatabase(db.name);
      await onChange();
    });

  return (
    <li>
      <span>
        <strong>{db.name}</strong> <span className="muted">site {db.site}</span>
      </span>
      {!confirming && (
        <button type="button" className="secondary danger" disabled={busy} onClick={() => setConfirming(true)}>
          Delete
        </button>
      )}
      {db.users.length === 0 ? (
        <p className="muted">No users.</p>
      ) : (
        <ul className="users" aria-label={"Users of " + db.name}>
          {db.users.map((u) => (
            <UserRow key={u} db={db.name} user={u} onSecret={onSecret} onChange={onChange} />
          ))}
        </ul>
      )}
      <AddUser db={db.name} onSecret={onSecret} onChange={onChange} />
      <DumpForm db={db.name} site={db.site} />
      {confirming && (
        <form
          onSubmit={(e) => {
            e.preventDefault();
            void drop();
          }}
        >
          <label>
            <span>
              Type <code>{db.name}</code> to delete the database and its users. This cannot be undone.
            </span>
            <input value={typed} onChange={(e) => setTyped(e.target.value)} autoComplete="off" spellCheck={false} />
          </label>
          <div className="actions">
            <button type="submit" className="danger" disabled={busy || typed !== db.name}>
              Delete {db.name}
            </button>
            <button
              type="button"
              className="secondary"
              onClick={() => {
                setConfirming(false);
                setTyped("");
              }}
            >
              Cancel
            </button>
          </div>
        </form>
      )}
      {error && <p role="alert">{error}</p>}
    </li>
  );
}

function UserRow({
  db,
  user,
  onSecret,
  onChange,
}: {
  db: string;
  user: string;
  onSecret: (c: Credentials) => void;
  onChange: () => Promise<void>;
}) {
  const [busy, setBusy] = useState(false);
  const [confirm, setConfirm] = useState<"" | "password" | "delete">("");
  const [error, setError] = useState("");

  async function run() {
    setBusy(true);
    setError("");
    try {
      if (confirm === "password") {
        const r = await resetPassword(db, user);
        onSecret({ database: db, user, password: r.password });
      } else {
        await deleteUser(db, user);
        await onChange();
      }
      setConfirm("");
    } catch (err) {
      setError(databaseMessage(err));
    } finally {
      setBusy(false);
    }
  }

  return (
    <li>
      <code>{user}</code>
      {confirm === "" ? (
        <div className="actions">
          <button type="button" className="secondary" disabled={busy} onClick={() => setConfirm("password")}>
            New password
          </button>
          <button type="button" className="secondary danger" disabled={busy} onClick={() => setConfirm("delete")}>
            Delete user
          </button>
        </div>
      ) : (
        <div className="actions">
          <span>
            {confirm === "password"
              ? "The old password stops working at once. Update the site's configuration afterwards."
              : "The site can no longer log in with this user."}
          </span>
          <button type="button" className={confirm === "delete" ? "danger" : ""} disabled={busy} onClick={() => void run()}>
            {confirm === "password" ? "Set new password" : "Delete " + user}
          </button>
          <button type="button" className="secondary" disabled={busy} onClick={() => setConfirm("")}>
            Cancel
          </button>
        </div>
      )}
      {error && <p role="alert">{error}</p>}
    </li>
  );
}

function AddUser({ db, onSecret, onChange }: { db: string; onSecret: (c: Credentials) => void; onChange: () => Promise<void> }) {
  const [open, setOpen] = useState(false);
  const [user, setUser] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");

  async function submit(e: FormEvent) {
    e.preventDefault();
    const problem = nameProblem("user", user);
    if (problem) {
      setError(problem);
      return;
    }
    setBusy(true);
    setError("");
    try {
      const r = await createUser(db, user);
      onSecret({ database: db, user, password: r.password });
      setUser("");
      setOpen(false);
      await onChange();
    } catch (err) {
      setError(databaseMessage(err));
    } finally {
      setBusy(false);
    }
  }

  if (!open) {
    return (
      <button type="button" className="secondary" onClick={() => setOpen(true)}>
        Add user
      </button>
    );
  }

  return (
    <form onSubmit={(e) => void submit(e)} noValidate>
      <label>
        New user for {db}
        <input value={user} onChange={(e) => setUser(e.target.value.trim())} required autoComplete="off" spellCheck={false} />
      </label>
      {error && <p role="alert">{error}</p>}
      <div className="actions">
        <button type="submit" disabled={busy}>
          Add user
        </button>
        <button type="button" className="secondary" disabled={busy} onClick={() => setOpen(false)}>
          Cancel
        </button>
      </div>
    </form>
  );
}

// DumpForm imports a dump file from the site's home into the database or
// exports the database into a new file there (ADR-0025).
function DumpForm({ db, site }: { db: string; site: string }) {
  const [open, setOpen] = useState(false);
  const [kind, setKind] = useState<"export" | "import">("export");
  const [path, setPath] = useState(() => defaultDumpPath(db));
  const [job, setJob] = useState<DumpJob | null>(null);
  const [error, setError] = useState("");

  const running = job?.state === "running";

  async function submit(e: FormEvent) {
    e.preventDefault();
    const problem = pathProblem(path);
    if (problem) {
      setError(problem);
      return;
    }
    setError("");
    setJob(null);
    try {
      const started = await startDump(db, kind, path);
      setJob(started);
      setJob(await poll(() => getDumpJob(db, started.id), (j) => j.state !== "running"));
    } catch (err) {
      setJob(null);
      setError(databaseMessage(err));
    }
  }

  if (!open) {
    return (
      <button type="button" className="secondary" onClick={() => setOpen(true)}>
        Import or export
      </button>
    );
  }

  return (
    <form onSubmit={(e) => void submit(e)} noValidate>
      <fieldset>
        <legend>Import or export {db}</legend>
        <label className="check">
          <input type="radio" name={"dump-" + db} checked={kind === "export"} onChange={() => setKind("export")} />
          Export into a new file
        </label>
        <label className="check">
          <input type="radio" name={"dump-" + db} checked={kind === "import"} onChange={() => setKind("import")} />
          Import a file
        </label>
        <label>
          File in the home of site {site}
          <small>
            {kind === "export"
              ? "Relative to the home, ending in .sql or .sql.gz. An existing file is never replaced."
              : "Relative to the home, ending in .sql or .sql.gz. Upload it first, for example over SFTP."}
          </small>
          <input value={path} onChange={(e) => setPath(e.target.value.trim())} autoComplete="off" spellCheck={false} />
        </label>
        {kind === "import" && (
          <p className="muted">The statements in the file run in {db}. Import into an empty database: a failed import is not rolled back.</p>
        )}
      </fieldset>
      {running && <p aria-busy="true">{job.kind === "import" ? "Importing…" : "Exporting…"}</p>}
      {job?.state === "done" && (
        <p role="status">
          {job.kind === "import" ? (
            <>
              Imported <code>{job.path}</code> into {db}.
            </>
          ) : (
            <>
              Exported {db} into <code>{job.path}</code>.
            </>
          )}
        </p>
      )}
      {job?.state === "failed" && <p role="alert">{(job.kind === "import" ? "Import" : "Export") + " failed: " + (job.error ?? "unknown error")}</p>}
      {error && <p role="alert">{error}</p>}
      <div className="actions">
        <button type="submit" disabled={running}>
          {kind === "import" ? "Start import" : "Start export"}
        </button>
        <button type="button" className="secondary" disabled={running} onClick={() => setOpen(false)}>
          Close
        </button>
      </div>
    </form>
  );
}
