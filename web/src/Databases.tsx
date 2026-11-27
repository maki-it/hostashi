// SPDX-License-Identifier: AGPL-3.0-or-later

import { ArrowLeft, Database as DatabaseIcon, Gear, Plus, Table as TableIcon } from "@phosphor-icons/react";
import { useCallback, useEffect, useRef, useState, type FormEvent } from "react";
import { Trans, useTranslation } from "react-i18next";
import type { TFunction } from "i18next";
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
  POSTGRESQL,
  serverAddress,
  pathProblem,
  resetPassword,
  startDump,
  suggestUser,
  uploadDir,
  uploadPath,
  type Credentials,
  type Database,
  type DBServer,
  type DumpJob,
} from "./databases";
import { DBServersView, serversHref } from "./DBServers";
import { poll } from "./backups";
import { OpenToolDialog } from "./DBTool";
import { ToolActionDialog, toolAction, ToolSessions } from "./DBToolSessions";
import { getDBTool, type DBToolStatus } from "./dbtool";
import { formatBytes } from "./limits";
import { remove as removeFiles, uploadFile } from "./files";
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
const serverColumn = (tr: TFunction, servers: DBServer[]): Column<Database> => ({
  key: "server",
  header: tr("databases.columns.server"),
  sortValue: (d) => d.server,
  cell: (d) => (
    <span>
      <span className="ui-mono">{d.server}</span>
      {isPostgres(servers.find((v) => v.name === d.server)) && <span className="ui-hint">{" " + POSTGRESQL}</span>}
    </span>
  ),
});

// serverOption is a server in the list of the new-database form.
const serverOption = (tr: TFunction, v: DBServer) => {
  const name = tr(v.local ? "databases.create.thisMachine" : "databases.create.remote", { name: v.name, address: serverAddress(v) });
  return v.state === "error" ? tr("databases.create.unreachable", { server: name }) : name;
};

// columns builds the table; onOpen is set when the database tool can be
// opened (ADR-0038), for every database, PostgreSQL ones too (ADR-0051).
const columns = (tr: TFunction, onOpen?: (db: string) => void): Column<Database>[] => [
  {
    key: "name",
    header: tr("databases.columns.database"),
    sortValue: (d) => d.name,
    cell: (d) => (
      <a className="ui-mono" href={dbHref(d.name)}>
        {d.name}
      </a>
    ),
  },
  {
    key: "site",
    header: tr("databases.columns.site"),
    sortValue: (d) => d.site,
    cell: (d) => <a href={formatHash({ page: "sites", site: { name: d.site } })}>{d.site}</a>,
  },
  {
    key: "users",
    header: tr("databases.columns.users"),
    sortValue: (d) => d.users.length,
    cell: (d) => (d.users.length === 0 ? <span className="ui-hint">{tr("databases.noUsers")}</span> : <span className="ui-mono">{d.users.join(", ")}</span>),
  },
  {
    key: "size",
    header: tr("databases.columns.size"),
    sortValue: (d) => d.sizeBytes ?? -1,
    cell: (d) => (d.sizeBytes === undefined ? <span className="ui-hint">{tr("databases.notMeasured")}</span> : formatBytes(d.sizeBytes)),
  },
  { key: "created", header: tr("databases.columns.created"), sortValue: (d) => d.createdAt, cell: (d) => <RelTime iso={d.createdAt} /> },
  {
    key: "actions",
    header: tr("databases.columns.actions"),
    srHeader: true,
    cell: (d) => (
      <RowActions>
        {onOpen && <IconButton label={tr("databases.openLabel", { name: d.name })} text={tr("databases.open")} icon={<TableIcon size={16} />} onClick={() => onOpen(d.name)} />}
        <IconLink label={tr("databases.manageLabel", { name: d.name })} text={tr("databases.manage")} icon={<Gear size={16} />} href={dbHref(d.name)} />
      </RowActions>
    ),
  },
];

// Databases is the database page (ADR-0037): a table of all databases that
// can be sorted and filtered, the form for a new one (?new=1) and the view
// of one database (?db=<name>) with its users, import/export and delete.
// A generated password is shown once, above whatever view is open.
export function Databases({ role = "user", query, onQuery }: QueryProps) {
  const { t: tr } = useTranslation();
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
      {tr("databases.allDatabases")}
    </a>
  );

  if (!dbs || !sites || !servers) {
    return (
      <>
        <PageHead title={tr("databases.title")} />
        {error ? <Notice tone="err" title={error} /> : <p aria-busy="true">{tr("common.status.loading")}</p>}
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
        <PageHead title={tr("databases.create.title")} description={tr("databases.create.description")} actions={listLink} />
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
    const asked = toolAction(query);
    return (
      <>
        <PageHead
          title={one}
          description={
            db && (
              <Trans
                i18nKey={db.server === LOCAL_SERVER ? "databases.view.ofSite" : "databases.view.onServer"}
                values={{ site: db.site, server: db.server, host: hostOf(db.server) }}
                components={{ a: <a href={formatHash({ page: "sites", site: { name: db.site } })} />, code: <span className="ui-mono" /> }}
              />
            )
          }
          actions={
            <>
              {db && onOpen && (
                <Button icon={<TableIcon size={16} aria-hidden="true" />} onClick={() => onOpen(db.name)}>
                  {tr("databases.view.openInAdminer")}
                </Button>
              )}
              {listLink}
            </>
          }
        />
        {notice}
        {toolDialog}
        {db && asked && (
          <ToolActionDialog
            key={asked.action + asked.session}
            db={db.name}
            action={asked.action}
            session={asked.session}
            onClose={() => {
              set({ tool: "", session: "" });
              setToolClosed((n) => n + 1);
            }}
          />
        )}
        {db && toolMinutes && <ToolSessions db={db.name} reload={toolClosed} />}
        {error && <Notice tone="err" title={error} />}
        {db ? (
          <DatabaseView key={db.name} db={db} host={hostOf(db.server)} postgres={isPostgres(serverOf(db.name))} onSecret={setSecret} onChange={load} onDeleted={() => set({ db: "" })} />
        ) : (
          <Notice tone="warn" title={tr("databases.notFound", { name: one })} />
        )}
      </>
    );
  }

  const q = (query.get("q") ?? "").trim().toLowerCase();
  const site = query.get("site") ?? "";
  const sortKey = query.get("sort");
  const sort: Sort | undefined = sortKey ? { key: sortKey, dir: query.get("dir") === "desc" ? "desc" : "asc" } : undefined;
  // The server column only matters once there is more than one server.
  const base = columns(tr, onOpen);
  const tableColumns = servers.length > 1 ? [...base.slice(0, 2), serverColumn(tr, servers), ...base.slice(2)] : base;
  const shown = dbs.filter((d) => (!site || d.site === site) && (!q || d.name.includes(q) || d.users.some((u) => u.includes(q))));

  return (
    <>
      <PageHead
        title={tr("databases.title")}
        description={tr("databases.description")}
        actions={
          <>
            {admin && (
              <a className="ui-btn" href={serversHref()}>
                <DatabaseIcon size={16} aria-hidden="true" />
                {tr("databases.servers")}
              </a>
            )}
            {sites.length > 0 && (
              <a className="ui-btn ui-btn-primary" href={formatHash({ page: "databases", query: new URLSearchParams({ new: "1" }) })}>
                <Plus size={16} aria-hidden="true" />
                {tr("databases.new")}
              </a>
            )}
          </>
        }
      />
      {notice}
      {toolDialog}
      {error && <Notice tone="err" title={error} />}
      {sites.length === 0 && dbs.length === 0 ? (
        <Notice tone="info" title={tr("databases.needSite")} />
      ) : (
        <Card padded={false}>
          <div className="ui-filters">
            <input
              className="ui-input"
              type="search"
              aria-label={tr("databases.filter.label")}
              placeholder={tr("databases.filter.placeholder")}
              value={query.get("q") ?? ""}
              onChange={(e) => set({ q: e.target.value })}
            />
            <select className="ui-select" aria-label={tr("databases.filter.site")} value={site} onChange={(e) => set({ site: e.target.value })}>
              <option value="">{tr("databases.filter.allSites")}</option>
              {sites.map((s) => (
                <option key={s.name} value={s.name}>
                  {s.name}
                </option>
              ))}
            </select>
          </div>
          <Table
            label={tr("databases.title")}
            columns={tableColumns}
            rows={shown}
            rowKey={(d) => d.name}
            sort={sort}
            onSort={(s) => set({ sort: s.key, dir: s.dir })}
            empty={dbs.length === 0 ? tr("databases.empty") : tr("databases.noMatch")}
          />
        </Card>
      )}
    </>
  );
}

// PasswordNotice shows a generated password once. The panel cannot show it
// again; closing the notice drops it from memory.
function PasswordNotice({ creds, server, onClose }: { creds: Credentials; server: DBServer | undefined; onClose: () => void }) {
  const { t: tr } = useTranslation();
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
      setCopied(tr("common.status.copied"));
    } catch {
      setCopied(tr("databases.secret.copyFailed"));
    }
  }

  return (
    <section className="ui-card ui-card-body ui-stack ui-attention" aria-labelledby="db-secret">
      <h2 id="db-secret" ref={heading} tabIndex={-1}>
        {tr("databases.secret.title")}
      </h2>
      <p className="ui-flush">{tr("databases.secret.text")}</p>
      <dl className="ui-facts">
        <dt>{tr("databases.secret.database")}</dt>
        <dd>
          <code>{creds.database}</code>
        </dd>
        <dt>{tr("databases.secret.user")}</dt>
        <dd>
          <code>{creds.user}</code>
        </dd>
        <dt>{tr("databases.secret.host")}</dt>
        <dd>
          <code>{creds.host}</code>
        </dd>
        {server && isPostgres(server) && (
          <>
            <dt>{tr("databases.secret.engine")}</dt>
            <dd>{POSTGRESQL}</dd>
            <dt>{tr("databases.secret.sslMode")}</dt>
            <dd>
              <code>{pgSSLMode(server)}</code>
            </dd>
          </>
        )}
      </dl>
      <Field label={tr("databases.secret.password")}>
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
        <Button onClick={() => void copy()}>{tr("databases.secret.copy")}</Button>
        <Button variant="primary" onClick={onClose}>
          {tr("databases.secret.saved")}
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
  const { t: tr } = useTranslation();
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
      <Notice tone="info" title={tr("databases.needSite")} />
    );
  }

  return (
    <Card>
      <form className="ui-stack" onSubmit={(e) => void submit(e)} noValidate>
        <Field label={tr("databases.create.site")}>
          <select className="ui-select" value={site} onChange={(e) => setSite(e.target.value)}>
            {sites.map((s) => (
              <option key={s.name} value={s.name}>
                {s.name}
              </option>
            ))}
          </select>
        </Field>
        {servers.length > 1 && (
          <Field label={tr("databases.create.server")} hint={tr("databases.create.serverHint")}>
            <select className="ui-select" value={server} onChange={(e) => setServer(e.target.value)}>
              {servers.map((v) => (
                <option key={v.name} value={v.name}>
                  {serverOption(tr, v)}
                </option>
              ))}
            </select>
          </Field>
        )}
        <Field label={tr("databases.create.name")} hint={tr("databases.create.nameHint")}>
          <input className="ui-input" value={name} onChange={(e) => setName(e.target.value.trim())} required autoComplete="off" spellCheck={false} />
        </Field>
        <label className="ui-check">
          <input type="checkbox" checked={withUser} onChange={(e) => setWithUser(e.target.checked)} />
          {tr("databases.create.withUser")}
        </label>
        {withUser && (
          <Field label={tr("databases.create.user")}>
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
          {tr("databases.create.submit")}
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
  const { t: tr } = useTranslation();
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
      <Card title={tr("databases.users.title")}>
        {db.users.length === 0 ? (
          <p className="ui-hint ui-flush">{tr("databases.users.none")}</p>
        ) : (
          <ul className="ui-plain-list ui-stack-tight" aria-label={tr("databases.users.listLabel", { db: db.name })}>
            {db.users.map((u) => (
              <UserRow key={u} db={db.name} host={host} user={u} onSecret={onSecret} onChange={onChange} />
            ))}
          </ul>
        )}
        <AddUser db={db.name} host={host} onSecret={onSecret} onChange={onChange} />
      </Card>
      <Card title={tr("databases.dump.title")}>
        <DumpForm db={db.name} site={db.site} server={db.server} postgres={postgres} />
      </Card>
      <Card title={tr("databases.delete.title")}>
        <form
          className="ui-stack"
          onSubmit={(e) => {
            e.preventDefault();
            void drop();
          }}
        >
          <div className="ui-narrow">
            <Field
              label={<Trans i18nKey="databases.delete.confirm" values={{ name: db.name }} components={{ code: <code className="ui-confirm-phrase" /> }} />}
              hint={tr("databases.delete.hint")}
            >
              <input className="ui-input" value={typed} onChange={(e) => setTyped(e.target.value)} autoComplete="off" spellCheck={false} />
            </Field>
          </div>
          {error && <Notice tone="err" title={error} />}
          <div className="ui-row">
            <Button variant="danger" type="submit" disabled={busy || typed !== db.name}>
              {tr("databases.delete.submit", { name: db.name })}
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
  const { t: tr } = useTranslation();
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
            {tr("databases.users.newPassword")}
          </Button>
          <Button variant="danger" disabled={busy} onClick={() => setConfirm("delete")}>
            {tr("databases.users.delete")}
          </Button>
        </div>
      ) : (
        <div className="ui-row">
          <span>
            {confirm === "password"
              ? tr("databases.users.passwordWarning")
              : tr("databases.users.deleteWarning")}
          </span>
          <Button variant={confirm === "delete" ? "danger" : "primary"} disabled={busy} onClick={() => void run()}>
            {confirm === "password" ? tr("databases.users.setPassword") : tr("databases.users.deleteSubmit", { user })}
          </Button>
          <Button disabled={busy} onClick={() => setConfirm("")}>
            {tr("common.actions.cancel")}
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
  const { t: tr } = useTranslation();
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
        <Button onClick={() => setOpen(true)}>{tr("databases.users.add")}</Button>
      </div>
    );
  }

  return (
    <form className="ui-stack" onSubmit={(e) => void submit(e)} noValidate>
      <Field label={tr("databases.users.newFor", { db })}>
        <input className="ui-input" value={user} onChange={(e) => setUser(e.target.value.trim())} required autoComplete="off" spellCheck={false} />
      </Field>
      {error && <Notice tone="err" title={error} />}
      <div className="ui-row">
        <Button variant="primary" type="submit" disabled={busy}>
          {tr("databases.users.add")}
        </Button>
        <Button disabled={busy} onClick={() => setOpen(false)}>
          {tr("common.actions.cancel")}
        </Button>
      </div>
    </form>
  );
}

// DumpForm imports a dump into the database, from the user's computer or
// from a file in the site's home, or exports the database into a new file
// there (ADR-0025). An uploaded dump goes through the file manager into
// hostashi-imports/ in the site's home and is removed after the import.
// For a database on an external server, the job connects to that server
// with a temporary user (ADR-0041 addendum 1).
function DumpForm({ db, site, server, postgres }: { db: string; site: string; server: string; postgres: boolean }) {
  const { t: tr } = useTranslation();
  const [kind, setKind] = useState<"export" | "import">("export");
  const [source, setSource] = useState<"upload" | "home">("upload");
  const [file, setFile] = useState<File | null>(null);
  const [sent, setSent] = useState<number | null>(null);
  const [path, setPath] = useState(() => defaultDumpPath(db, new Date(), postgres));
  const [job, setJob] = useState<DumpJob | null>(null);
  const [error, setError] = useState("");

  const uploading = sent !== null;
  const running = uploading || job?.state === "running";
  const fromComputer = kind === "import" && source === "upload";

  async function submit(e: FormEvent) {
    e.preventDefault();
    let target = path;
    if (fromComputer) {
      if (!file) {
        setError(tr("databases.dump.chooseFile"));
        return;
      }
      target = uploadPath(file.name, new Date(), postgres);
      if (target === "") {
        setError(tr(postgres ? "databases.problems.endingPg" : "databases.problems.ending"));
        return;
      }
    } else {
      const problem = pathProblem(path, postgres);
      if (problem) {
        setError(problem);
        return;
      }
    }
    setError("");
    setJob(null);
    let uploaded = false;
    try {
      if (fromComputer && file) {
        setSent(0);
        await uploadFile(site, target, file, { parents: true, onProgress: setSent });
        uploaded = true;
        setSent(null);
      }
      const started = await startDump(db, kind, target);
      setJob(started);
      setJob(await poll(() => getDumpJob(db, started.id), (j) => j.state !== "running"));
    } catch (err) {
      setJob(null);
      setError(databaseMessage(err));
    } finally {
      setSent(null);
      // The uploaded copy has done its job, or is of no use after a failure.
      if (uploaded) await removeFiles(site, [target]).catch(() => undefined);
    }
  }

  return (
    <form className="ui-stack" onSubmit={(e) => void submit(e)} noValidate>
      <fieldset className="ui-fieldset ui-stack-tight">
        <legend>{tr("databases.dump.direction")}</legend>
        <label className="ui-check">
          <input type="radio" name={"dump-" + db} checked={kind === "export"} onChange={() => setKind("export")} />
          {tr("databases.dump.export")}
        </label>
        <label className="ui-check">
          <input type="radio" name={"dump-" + db} checked={kind === "import"} onChange={() => setKind("import")} />
          {tr("databases.dump.import")}
        </label>
      </fieldset>
      {kind === "import" && (
        <fieldset className="ui-fieldset ui-stack-tight">
          <legend>{tr("databases.dump.from")}</legend>
          <label className="ui-check">
            <input type="radio" name={"dump-src-" + db} checked={source === "upload"} onChange={() => setSource("upload")} />
            {tr("databases.dump.computer")}
          </label>
          <label className="ui-check">
            <input type="radio" name={"dump-src-" + db} checked={source === "home"} onChange={() => setSource("home")} />
            {tr("databases.dump.home", { site })}
          </label>
        </fieldset>
      )}
      {kind === "export" && (
        <Field label={tr("databases.dump.format")}>
          <select
            className="ui-select"
            value={formatOf(path)}
            onChange={(e) => e.target.value && setPath(withFormat(path, e.target.value as DumpFormat))}
          >
            {formatOf(path) === "" && <option value="">{tr("databases.dump.otherFormat")}</option>}
            {dumpFormats(postgres).map((f) => (
              <option key={f.value} value={f.value}>
                {f.label}
              </option>
            ))}
          </select>
        </Field>
      )}
      {fromComputer ? (
        <Field
          label={tr("databases.dump.file")}
          hint={tr(postgres ? "databases.dump.fileHintPg" : "databases.dump.fileHint") + " " + tr("databases.dump.uploadHint", { site })}
        >
          <input
            className="ui-input"
            type="file"
            accept={postgres ? ".sql,.gz,.dump" : ".sql,.gz"}
            onChange={(e) => setFile(e.target.files?.[0] ?? null)}
          />
        </Field>
      ) : (
        <Field
          label={tr("databases.dump.path", { site })}
          hint={
            tr(postgres ? "databases.dump.pathHintPg" : "databases.dump.pathHint") +
            " " +
            tr(kind === "export" ? "databases.dump.neverReplaced" : "databases.dump.uploadFirst")
          }
        >
          <input className="ui-input ui-mono" value={path} onChange={(e) => setPath(e.target.value.trim())} autoComplete="off" spellCheck={false} />
        </Field>
      )}
      {server !== LOCAL_SERVER && (
        <p className="ui-hint ui-flush">{tr("databases.dump.remote", { server })}</p>
      )}
      {kind === "import" &&
        (postgres ? (
          <Notice
            tone="warn"
            title={tr("databases.dump.importWarnPg", { db })}
          />
        ) : (
          <Notice tone="warn" title={tr("databases.dump.importWarn", { db })} />
        ))}
      {uploading && file && (
        <p aria-busy="true">{tr("databases.dump.uploading", { percent: Math.floor(((sent ?? 0) / Math.max(file.size, 1)) * 100) })}</p>
      )}
      {!uploading && job?.state === "running" && <p aria-busy="true">{tr(job.kind === "import" ? "databases.dump.importing" : "databases.dump.exporting")}</p>}
      {job?.state === "done" && (
        <Notice
          tone="ok"
          title={
            job.kind === "import"
              ? tr("databases.dump.imported", { file: file && job.path.startsWith(uploadDir + "/") ? file.name : job.path, db })
              : tr("databases.dump.exported", { db, path: job.path })
          }
        />
      )}
      {job?.state === "failed" && <Notice tone="err" title={tr(job.kind === "import" ? "databases.dump.importFailed" : "databases.dump.exportFailed", { reason: job.error ?? tr("databases.dump.unknownError") })} />}
      {error && <Notice tone="err" title={error} />}
      <div className="ui-row">
        <Button variant="primary" type="submit" disabled={running}>
          {tr(kind === "import" ? "databases.dump.startImport" : "databases.dump.startExport")}
        </Button>
      </div>
    </form>
  );
}
