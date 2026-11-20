// SPDX-License-Identifier: AGPL-3.0-or-later

import { ArrowLeft, Gear, Plus } from "@phosphor-icons/react";
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
import { formatHash } from "./routes";
import { Card, IconLink, Notice, PageHead, RelTime, RowActions, Table, type Column, type Sort } from "./ui";

type QueryProps = {
  /** The address query: q, site, sort, dir; new=1 for the form; db=<name> for one database. */
  query: URLSearchParams;
  /** Replaces the query in the address without a new history entry. */
  onQuery: (q: URLSearchParams) => void;
};

const dbHref = (name: string) => formatHash({ page: "databases", query: new URLSearchParams({ db: name }) });

const columns: Column<Database>[] = [
  {
    key: "name",
    header: "Database",
    sortValue: (d) => d.name,
    cell: (d) => (
      <a className="ui-mono" href={dbHref(d.name)}>
        {d.name}
      </a>
    ),
  },
  {
    key: "site",
    header: "Site",
    sortValue: (d) => d.site,
    cell: (d) => <a href={formatHash({ page: "sites", site: { name: d.site } })}>{d.site}</a>,
  },
  {
    key: "users",
    header: "Users",
    sortValue: (d) => d.users.length,
    cell: (d) => (d.users.length === 0 ? <span className="muted">No users</span> : <span className="ui-mono">{d.users.join(", ")}</span>),
  },
  { key: "created", header: "Created", sortValue: (d) => d.createdAt, cell: (d) => <RelTime iso={d.createdAt} /> },
  {
    key: "actions",
    header: "Actions",
    srHeader: true,
    cell: (d) => (
      <RowActions>
        <IconLink label={`Manage ${d.name}`} icon={<Gear size={16} />} href={dbHref(d.name)} />
      </RowActions>
    ),
  },
];

// Databases is the database page (ADR-0037): a table of all databases that
// can be sorted and filtered, the form for a new one (?new=1) and the view
// of one database (?db=<name>) with its users, import/export and delete.
// A generated password is shown once, above whatever view is open.
export function Databases({ query, onQuery }: QueryProps) {
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

  const set = (changes: Record<string, string>) => {
    const next = new URLSearchParams(query);
    for (const [k, v] of Object.entries(changes)) {
      if (v) next.set(k, v);
      else next.delete(k);
    }
    onQuery(next);
  };

  const notice = secret && <PasswordNotice creds={secret} onClose={() => setSecret(null)} />;
  const listLink = (
    <a className="ui-btn" href={formatHash({ page: "databases" })}>
      <ArrowLeft size={16} aria-hidden="true" />
      All databases
    </a>
  );

  if (!dbs || !sites) {
    return (
      <>
        <PageHead title="Databases" />
        {error ? <Notice tone="err" title={error} /> : <p aria-busy="true">Loading…</p>}
      </>
    );
  }

  if (query.get("new") === "1") {
    return (
      <>
        <PageHead title="New database" description="A MariaDB database belongs to one site." actions={listLink} />
        {notice}
        <CreateCard
          sites={sites}
          onCreated={(c) => {
            if (c) setSecret(c);
            set({ new: "" });
          }}
          onChange={load}
        />
      </>
    );
  }

  const one = query.get("db");
  if (one !== null) {
    const db = dbs.find((d) => d.name === one);
    return (
      <>
        <PageHead title={one} description={db && <>Database of site <a href={formatHash({ page: "sites", site: { name: db.site } })}>{db.site}</a></>} actions={listLink} />
        {notice}
        {error && <Notice tone="err" title={error} />}
        {db ? (
          <DatabaseView key={db.name} db={db} onSecret={setSecret} onChange={load} onDeleted={() => set({ db: "" })} />
        ) : (
          <Notice tone="warn" title={`There is no database named ${one}.`} />
        )}
      </>
    );
  }

  const q = (query.get("q") ?? "").trim().toLowerCase();
  const site = query.get("site") ?? "";
  const sortKey = query.get("sort");
  const sort: Sort | undefined = sortKey ? { key: sortKey, dir: query.get("dir") === "desc" ? "desc" : "asc" } : undefined;
  const shown = dbs.filter((d) => (!site || d.site === site) && (!q || d.name.includes(q) || d.users.some((u) => u.includes(q))));

  return (
    <>
      <PageHead
        title="Databases"
        description="MariaDB databases of your sites. Hostashi stores no database password."
        actions={
          sites.length > 0 && (
            <a className="ui-btn ui-btn-primary" href={formatHash({ page: "databases", query: new URLSearchParams({ new: "1" }) })}>
              <Plus size={16} aria-hidden="true" />
              New database
            </a>
          )
        }
      />
      {notice}
      {error && <Notice tone="err" title={error} />}
      {sites.length === 0 && dbs.length === 0 ? (
        <Notice tone="info" title="Create a site first. Every database belongs to a site." />
      ) : (
        <Card padded={false}>
          <div className="ui-filters">
            <input
              className="ui-input"
              type="search"
              aria-label="Filter databases"
              placeholder="Filter by database or user"
              value={query.get("q") ?? ""}
              onChange={(e) => set({ q: e.target.value })}
            />
            <select className="ui-select" aria-label="Site" value={site} onChange={(e) => set({ site: e.target.value })}>
              <option value="">All sites</option>
              {sites.map((s) => (
                <option key={s.name} value={s.name}>
                  {s.name}
                </option>
              ))}
            </select>
          </div>
          <Table
            label="Databases"
            columns={columns}
            rows={shown}
            rowKey={(d) => d.name}
            sort={sort}
            onSort={(s) => set({ sort: s.key, dir: s.dir })}
            empty={dbs.length === 0 ? "No databases yet." : "No database matches the filter."}
          />
        </Card>
      )}
    </>
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
      <Notice tone="info" title="Create a site first. Every database belongs to a site." />
    );
  }

  return (
    <Card>
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
    </Card>
  );
}

// DatabaseView is the page of one database: its users, import and export,
// and delete.
function DatabaseView({
  db,
  onSecret,
  onChange,
  onDeleted,
}: {
  db: Database;
  onSecret: (c: Credentials) => void;
  onChange: () => Promise<void>;
  onDeleted: () => void;
}) {
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const [typed, setTyped] = useState("");

  async function drop() {
    setBusy(true);
    setError("");
    try {
      await deleteDatabase(db.name);
      onDeleted();
      await onChange();
    } catch (err) {
      setError(databaseMessage(err));
      setBusy(false);
    }
  }

  return (
    <>
      <Card title="Users">
        {db.users.length === 0 ? (
          <p className="muted">No users.</p>
        ) : (
          <ul className="users" aria-label={"Users of " + db.name}>
            {db.users.map((u) => (
              <UserRow key={u} db={db.name} user={u} onSecret={onSecret} onChange={onChange} />
            ))}
          </ul>
        )}
        <div className="keybox">
          <AddUser db={db.name} onSecret={onSecret} onChange={onChange} />
        </div>
      </Card>
      <Card title="Import or export">
        <DumpForm db={db.name} site={db.site} />
      </Card>
      <Card title="Delete database">
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
          {error && <p role="alert">{error}</p>}
          <div className="actions">
            <button type="submit" className="danger" disabled={busy || typed !== db.name}>
              Delete {db.name}
            </button>
          </div>
        </form>
      </Card>
    </>
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
            {confirm === "password" ? "Set new password" : "Delete user " + user}
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
      <div className="actions">
        <button type="button" className="secondary" onClick={() => setOpen(true)}>
          Add user
        </button>
      </div>
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

  return (
    <form onSubmit={(e) => void submit(e)} noValidate>
      <fieldset>
        <legend>Direction</legend>
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
      </div>
    </form>
  );
}
