// SPDX-License-Identifier: AGPL-3.0-or-later

import { ArrowLeft, Database as DatabaseIcon, Gear, Plus, Table as TableIcon } from "@phosphor-icons/react";
import { useCallback, useEffect, useRef, useState, type FormEvent } from "react";
import { listSites, type Site } from "./backups";
import {
  createDatabase,
  createUser,
  databaseMessage,
  deleteDatabase,
  defaultDumpPath,
  dumpFormats,
  formatOf,
  withFormat,
  type DumpFormat,
  deleteUser,
  getDumpJob,
  isPostgres,
  LOCAL_SERVER,
  listDatabases,
  listServers,
  nameProblem,
  pgSSLMode,
  serverAddress,
  pathProblem,
  resetPassword,
  startDump,
  suggestUser,
  type Credentials,
  type Database,
  type DBServer,
  type DumpJob,
} from "./databases";
import { DBServersView, serversHref } from "./DBServers";
import { poll } from "./backups";
import { OpenToolDialog } from "./DBTool";
import { ToolSessions } from "./DBToolSessions";
import { getDBTool, type DBToolStatus } from "./dbtool";
import { formatBytes } from "./limits";
import { formatHash } from "./routes";
import { Button, Card, Field, IconButton, IconLink, Notice, PageHead, RelTime, RowActions, Table, type Column, type Sort } from "./ui";

type QueryProps = {
  /** The role of the logged-in user: admins manage the database servers. */
  role?: string;
  /** The address query: q, site, sort, dir; new=1 for the form; db=<name> for one database; view=servers for the servers. */
  query: URLSearchParams;
  /** Replaces the query in the address without a new history entry. */
  onQuery: (q: URLSearchParams) => void;
};

const dbHref = (name: string) => formatHash({ page: "databases", query: new URLSearchParams({ db: name }) });

// serverColumn names the server and, for PostgreSQL, the engine (ADR-0051).
const serverColumn = (servers: DBServer[]): Column<Database> => ({
  key: "server",
  header: "Server",
  sortValue: (d) => d.server,
  cell: (d) => (
    <span>
      <span className="ui-mono">{d.server}</span>
      {isPostgres(servers.find((v) => v.name === d.server)) && <span className="ui-hint"> PostgreSQL</span>}
    </span>
  ),
});

// columns builds the table; onOpen is set when the database tool can be
// opened (ADR-0038), for every database, PostgreSQL ones too (ADR-0051).
const columns = (onOpen?: (db: string) => void): Column<Database>[] => [
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
    cell: (d) => (d.users.length === 0 ? <span className="ui-hint">No users</span> : <span className="ui-mono">{d.users.join(", ")}</span>),
  },
  {
    key: "size",
    header: "Size",
    sortValue: (d) => d.sizeBytes ?? -1,
    cell: (d) => (d.sizeBytes === undefined ? <span className="ui-hint">Not measured</span> : formatBytes(d.sizeBytes)),
  },
  { key: "created", header: "Created", sortValue: (d) => d.createdAt, cell: (d) => <RelTime iso={d.createdAt} /> },
  {
    key: "actions",
    header: "Actions",
    srHeader: true,
    cell: (d) => (
      <RowActions>
        {onOpen && <IconButton label={`Open ${d.name} in Adminer`} icon={<TableIcon size={16} />} onClick={() => onOpen(d.name)} />}
        <IconLink label={`Manage ${d.name}`} icon={<Gear size={16} />} href={dbHref(d.name)} />
      </RowActions>
    ),
  },
];

// Databases is the database page (ADR-0037): a table of all databases that
// can be sorted and filtered, the form for a new one (?new=1) and the view
// of one database (?db=<name>) with its users, import/export and delete.
// A generated password is shown once, above whatever view is open.
export function Databases({ role = "user", query, onQuery }: QueryProps) {
  const [dbs, setDbs] = useState<Database[] | null>(null);
  const [sites, setSites] = useState<Site[] | null>(null);
  const [servers, setServers] = useState<DBServer[] | null>(null);
  const [error, setError] = useState("");
  const [secret, setSecret] = useState<Credentials | null>(null);
  const [tool, setTool] = useState<DBToolStatus | null>(null);
  const [opening, setOpening] = useState<string | null>(null);
  const [toolClosed, setToolClosed] = useState(0);

  // Without the tool (or without the status) there is no Adminer button.
  useEffect(() => {
    getDBTool().then(setTool, () => setTool(null));
  }, []);

  const load = useCallback(async () => {
    try {
      const [d, s, v] = await Promise.all([listDatabases(), listSites(), listServers()]);
      setDbs(d);
      setSites(s);
      setServers(v);
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

  const serverOf = (database: string) => servers?.find((v) => v.name === dbs?.find((d) => d.name === database)?.server);
  const notice = secret && <PasswordNotice creds={secret} server={serverOf(secret.database)} onClose={() => setSecret(null)} />;
  const toolMinutes = tool?.available ? tool.minutes : null;
  const onOpen = toolMinutes ? setOpening : undefined;
  const closeTool = () => {
    setOpening(null);
    setToolClosed((n) => n + 1);
  };
  const toolDialog = opening && toolMinutes && <OpenToolDialog key={opening} db={opening} server={dbs?.find((d) => d.name === opening)?.server ?? LOCAL_SERVER} minutes={toolMinutes} onClose={closeTool} />;
  const listLink = (
    <a className="ui-btn" href={formatHash({ page: "databases" })}>
      <ArrowLeft size={16} aria-hidden="true" />
      All databases
    </a>
  );

  if (!dbs || !sites || !servers) {
    return (
      <>
        <PageHead title="Databases" />
        {error ? <Notice tone="err" title={error} /> : <p aria-busy="true">Loading…</p>}
      </>
    );
  }

  const admin = role === "admin";
  const hostOf = (server: string) => serverAddress(servers.find((v) => v.name === server));

  if (admin && query.get("view") === "servers") {
    return <DBServersView servers={servers} query={query} onChange={load} />;
  }

  if (query.get("new") === "1") {
    return (
      <>
        <PageHead title="New database" description="A database belongs to one site." actions={listLink} />
        {notice}
        <CreateCard
          sites={sites}
          servers={servers}
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
        <PageHead
          title={one}
          description={
            db && (
              <>
                Database of site <a href={formatHash({ page: "sites", site: { name: db.site } })}>{db.site}</a>
                {db.server !== LOCAL_SERVER && (
                  <>
                    {" "}
                    on server <span className="ui-mono">{db.server}</span> ({hostOf(db.server)})
                  </>
                )}
              </>
            )
          }
          actions={
            <>
              {db && onOpen && (
                <Button icon={<TableIcon size={16} aria-hidden="true" />} onClick={() => onOpen(db.name)}>
                  Open in Adminer
                </Button>
              )}
              {listLink}
            </>
          }
        />
        {notice}
        {toolDialog}
        {db && toolMinutes && <ToolSessions db={db.name} reload={toolClosed} />}
        {error && <Notice tone="err" title={error} />}
        {db ? (
          <DatabaseView key={db.name} db={db} host={hostOf(db.server)} postgres={isPostgres(serverOf(db.name))} onSecret={setSecret} onChange={load} onDeleted={() => set({ db: "" })} />
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
  // The server column only matters once there is more than one server.
  const base = columns(onOpen);
  const tableColumns = servers.length > 1 ? [...base.slice(0, 2), serverColumn(servers), ...base.slice(2)] : base;
  const shown = dbs.filter((d) => (!site || d.site === site) && (!q || d.name.includes(q) || d.users.some((u) => u.includes(q))));

  return (
    <>
      <PageHead
        title="Databases"
        description="Databases of your sites. Hostashi stores no database password."
        actions={
          <>
            {admin && (
              <a className="ui-btn" href={serversHref()}>
                <DatabaseIcon size={16} aria-hidden="true" />
                Servers
              </a>
            )}
            {sites.length > 0 && (
              <a className="ui-btn ui-btn-primary" href={formatHash({ page: "databases", query: new URLSearchParams({ new: "1" }) })}>
                <Plus size={16} aria-hidden="true" />
                New database
              </a>
            )}
          </>
        }
      />
      {notice}
      {toolDialog}
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
            columns={tableColumns}
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
function PasswordNotice({ creds, server, onClose }: { creds: Credentials; server: DBServer | undefined; onClose: () => void }) {
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
    <section className="ui-card ui-card-body ui-stack ui-attention" aria-labelledby="db-secret">
      <h2 id="db-secret" ref={heading} tabIndex={-1}>
        Save this password now
      </h2>
      <p className="ui-flush">Hostashi does not store it and cannot show it again. If it is lost, set a new one.</p>
      <dl className="ui-facts">
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
          <code>{creds.host}</code>
        </dd>
        {server && isPostgres(server) && (
          <>
            <dt>Engine</dt>
            <dd>PostgreSQL</dd>
            <dt>SSL mode</dt>
            <dd>
              <code>{pgSSLMode(server)}</code>
            </dd>
          </>
        )}
      </dl>
      <Field label="Password">
        <input
          readOnly
          value={creds.password}
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

function CreateCard({
  sites,
  servers,
  onCreated,
  onChange,
}: {
  sites: Site[];
  servers: DBServer[];
  onCreated: (c: Credentials | null) => void;
  onChange: () => Promise<void>;
}) {
  const [site, setSite] = useState(sites[0]?.name ?? "");
  const [server, setServer] = useState(() => (servers.find((v) => v.default) ?? servers[0])?.name ?? LOCAL_SERVER);
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
      const d = await createDatabase(site, name, withUser ? userName : "", servers.length > 1 ? server : "");
      const host = serverAddress(servers.find((v) => v.name === d.server));
      onCreated(d.password ? { database: d.name, user: userName, password: d.password, host } : null);
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
      <form className="ui-stack" onSubmit={(e) => void submit(e)} noValidate>
        <Field label="Site">
          <select className="ui-select" value={site} onChange={(e) => setSite(e.target.value)}>
            {sites.map((s) => (
              <option key={s.name} value={s.name}>
                {s.name}
              </option>
            ))}
          </select>
        </Field>
        {servers.length > 1 && (
          <Field label="Server" hint="A database stays on the server it is created on.">
            <select className="ui-select" value={server} onChange={(e) => setServer(e.target.value)}>
              {servers.map((v) => (
                <option key={v.name} value={v.name}>
                  {v.name + (v.local ? " (this machine)" : " (" + serverAddress(v) + ")") + (v.state === "error" ? ", not reachable" : "")}
                </option>
              ))}
            </select>
          </Field>
        )}
        <Field label="Database name" hint="Lowercase letters, digits and _, starting with a letter.">
          <input className="ui-input" value={name} onChange={(e) => setName(e.target.value.trim())} required autoComplete="off" spellCheck={false} />
        </Field>
        <label className="ui-check">
          <input type="checkbox" checked={withUser} onChange={(e) => setWithUser(e.target.checked)} />
          Create a user for it with a generated password
        </label>
        {withUser && (
          <Field label="User name">
            <input
              className="ui-input"
              value={userName}
              onChange={(e) => {
                setUserTouched(true);
                setUser(e.target.value.trim());
              }}
              required
              autoComplete="off"
              spellCheck={false}
            />
          </Field>
        )}
        {error && <Notice tone="err" title={error} />}
        <Button variant="primary" type="submit" disabled={busy}>
          Create database
        </Button>
      </form>
    </Card>
  );
}

// DatabaseView is the page of one database: its users, import and export,
// and delete.
function DatabaseView({
  db,
  host,
  postgres,
  onSecret,
  onChange,
  onDeleted,
}: {
  db: Database;
  host: string;
  postgres: boolean;
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
          <p className="ui-hint ui-flush">No users.</p>
        ) : (
          <ul className="ui-plain-list ui-stack-tight" aria-label={"Users of " + db.name}>
            {db.users.map((u) => (
              <UserRow key={u} db={db.name} host={host} user={u} onSecret={onSecret} onChange={onChange} />
            ))}
          </ul>
        )}
        <AddUser db={db.name} host={host} onSecret={onSecret} onChange={onChange} />
      </Card>
      <Card title="Import or export">
        <DumpForm db={db.name} site={db.site} server={db.server} postgres={postgres} />
      </Card>
      <Card title="Delete database">
        <form
          className="ui-stack"
          onSubmit={(e) => {
            e.preventDefault();
            void drop();
          }}
        >
          <div className="ui-narrow">
            <Field
              label={
                <>
                  Type <code className="ui-confirm-phrase">{db.name}</code> to confirm
                </>
              }
              hint="The database is deleted with its users. This cannot be undone."
            >
              <input className="ui-input" value={typed} onChange={(e) => setTyped(e.target.value)} autoComplete="off" spellCheck={false} />
            </Field>
          </div>
          {error && <Notice tone="err" title={error} />}
          <div className="ui-row">
            <Button variant="danger" type="submit" disabled={busy || typed !== db.name}>
              Delete {db.name}
            </Button>
          </div>
        </form>
      </Card>
    </>
  );
}

function UserRow({
  db,
  host,
  user,
  onSecret,
  onChange,
}: {
  db: string;
  host: string;
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
        onSecret({ database: db, user, password: r.password, host });
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
    <li className="ui-list-row">
      <code>{user}</code>
      {confirm === "" ? (
        <div className="ui-row">
          <Button disabled={busy} onClick={() => setConfirm("password")}>
            New password
          </Button>
          <Button variant="danger" disabled={busy} onClick={() => setConfirm("delete")}>
            Delete user
          </Button>
        </div>
      ) : (
        <div className="ui-row">
          <span>
            {confirm === "password"
              ? "The old password stops working at once. Update the site's configuration afterwards."
              : "The site can no longer log in with this user."}
          </span>
          <Button variant={confirm === "delete" ? "danger" : "primary"} disabled={busy} onClick={() => void run()}>
            {confirm === "password" ? "Set new password" : "Delete user " + user}
          </Button>
          <Button disabled={busy} onClick={() => setConfirm("")}>
            Cancel
          </Button>
        </div>
      )}
      {error && <Notice tone="err" title={error} />}
    </li>
  );
}

function AddUser({
  db,
  host,
  onSecret,
  onChange,
}: {
  db: string;
  host: string;
  onSecret: (c: Credentials) => void;
  onChange: () => Promise<void>;
}) {
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
      onSecret({ database: db, user, password: r.password, host });
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
      <div className="ui-row">
        <Button onClick={() => setOpen(true)}>
          Add user
        </Button>
      </div>
    );
  }

  return (
    <form className="ui-stack" onSubmit={(e) => void submit(e)} noValidate>
      <Field label={`New user for ${db}`}>
        <input className="ui-input" value={user} onChange={(e) => setUser(e.target.value.trim())} required autoComplete="off" spellCheck={false} />
      </Field>
      {error && <Notice tone="err" title={error} />}
      <div className="ui-row">
        <Button variant="primary" type="submit" disabled={busy}>
          Add user
        </Button>
        <Button disabled={busy} onClick={() => setOpen(false)}>
          Cancel
        </Button>
      </div>
    </form>
  );
}

// DumpForm imports a dump file from the site's home into the database or
// exports the database into a new file there (ADR-0025).
// For a database on an external server, the job connects to that server
// with a temporary user (ADR-0041 addendum 1).
function DumpForm({ db, site, server, postgres }: { db: string; site: string; server: string; postgres: boolean }) {
  const [kind, setKind] = useState<"export" | "import">("export");
  const [path, setPath] = useState(() => defaultDumpPath(db, new Date(), postgres));
  const [job, setJob] = useState<DumpJob | null>(null);
  const [error, setError] = useState("");

  const running = job?.state === "running";

  async function submit(e: FormEvent) {
    e.preventDefault();
    const problem = pathProblem(path, postgres);
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
    <form className="ui-stack" onSubmit={(e) => void submit(e)} noValidate>
      <fieldset className="ui-fieldset ui-stack-tight">
        <legend>Direction</legend>
        <label className="ui-check">
          <input type="radio" name={"dump-" + db} checked={kind === "export"} onChange={() => setKind("export")} />
          Export into a new file
        </label>
        <label className="ui-check">
          <input type="radio" name={"dump-" + db} checked={kind === "import"} onChange={() => setKind("import")} />
          Import a file
        </label>
      </fieldset>
      {kind === "export" && (
        <Field label="Format">
          <select
            className="ui-select"
            value={formatOf(path)}
            onChange={(e) => e.target.value && setPath(withFormat(path, e.target.value as DumpFormat))}
          >
            {formatOf(path) === "" && <option value="">Other (see the file name)</option>}
            {dumpFormats(postgres).map((f) => (
              <option key={f.value} value={f.value}>
                {f.label}
              </option>
            ))}
          </select>
        </Field>
      )}
      <Field
        label={`File in the home of site ${site}`}
        hint={
          (postgres
            ? "Relative to the home, ending in .dump (pg_dump custom format), .sql or .sql.gz. "
            : "Relative to the home, ending in .sql or .sql.gz. ") +
          (kind === "export" ? "An existing file is never replaced." : "Upload it first, for example over SFTP.")
        }
      >
        <input className="ui-input ui-mono" value={path} onChange={(e) => setPath(e.target.value.trim())} autoComplete="off" spellCheck={false} />
      </Field>
      {server !== LOCAL_SERVER && (
        <p className="ui-hint ui-flush">
          The data goes between this machine and {server} with a temporary database user that the panel removes after the job.
        </p>
      )}
      {kind === "import" &&
        (postgres ? (
          <Notice
            tone="warn"
            title={`The file runs in ${db} as one transaction: a failed import changes nothing. A .dump file replaces the tables it contains.`}
          />
        ) : (
          <Notice tone="warn" title={`The statements in the file run in ${db}. Import into an empty database: a failed import is not rolled back.`} />
        ))}
      {running && <p aria-busy="true">{job.kind === "import" ? "Importing…" : "Exporting…"}</p>}
      {job?.state === "done" && (
        <Notice tone="ok" title={job.kind === "import" ? `Imported ${job.path} into ${db}.` : `Exported ${db} into ${job.path}.`} />
      )}
      {job?.state === "failed" && <Notice tone="err" title={(job.kind === "import" ? "Import" : "Export") + " failed: " + (job.error ?? "unknown error")} />}
      {error && <Notice tone="err" title={error} />}
      <div className="ui-row">
        <Button variant="primary" type="submit" disabled={running}>
          {kind === "import" ? "Start import" : "Start export"}
        </Button>
      </div>
    </form>
  );
}
