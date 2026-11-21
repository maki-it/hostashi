// SPDX-License-Identifier: AGPL-3.0-or-later

import { ArrowLeft, ArrowsClockwise, Plus, Star, Trash } from "@phosphor-icons/react";
import { useState, type FormEvent } from "react";
import {
  addServer,
  checkServer,
  databaseMessage,
  defaultPort,
  deleteServer,
  engineLabel,
  serverAddress,
  serverNameProblem,
  setDefaultServer,
  type DBEngine,
  type DBServer,
  type NewDBServer,
} from "./databases";
import { formatHash } from "./routes";
import { Badge, Button, Card, Field, IconButton, Notice, PageHead, RelTime, RowActions, Table, type Column, type Tone } from "./ui";

export const serversHref = (q: Record<string, string> = {}) =>
  formatHash({ page: "databases", query: new URLSearchParams({ view: "servers", ...q }) });

const stateBadge: Record<DBServer["state"], [Tone, string]> = {
  ok: ["ok", "Reachable"],
  error: ["err", "Not reachable"],
  unchecked: ["off", "Not checked"],
};

// tlsLabel says how the panel talks to the server.
function tlsLabel(s: DBServer): string {
  switch (s.tlsMode) {
    case "socket":
      return "Local socket";
    case "verify":
      return "TLS, system CAs";
    case "verify-ca":
      return "TLS, own CA";
    default:
      return "No TLS";
  }
}

// DBServersView lists the database servers for administrators (ADR-0041,
// ADR-0051): the local MariaDB and the external servers, with check,
// default and delete, and the form for a new server (new=1).
export function DBServersView({
  servers,
  query,
  onChange,
}: {
  servers: DBServer[];
  query: URLSearchParams;
  onChange: () => Promise<void>;
}) {
  const [busy, setBusy] = useState("");
  const [error, setError] = useState("");
  const [result, setResult] = useState("");
  const [confirm, setConfirm] = useState("");
  // What adding a server warned about, shown on the list after it.
  const [warnings, setWarnings] = useState<string[]>([]);

  const back = (
    <a className="ui-btn" href={formatHash({ page: "databases" })}>
      <ArrowLeft size={16} aria-hidden="true" />
      All databases
    </a>
  );

  if (query.get("new") === "1") {
    return (
      <>
        <PageHead
          title="Add database server"
          description="A MariaDB, MySQL or PostgreSQL server that the panel creates databases on."
          actions={
            <a className="ui-btn" href={serversHref()}>
              <ArrowLeft size={16} aria-hidden="true" />
              All servers
            </a>
          }
        />
        <AddServerCard
          onAdded={async (s) => {
            setError("");
            setResult(`Added ${s.name} (${engineLabel(s)}).`);
            setWarnings(s.warnings ?? []);
            await onChange();
            window.location.hash = serversHref();
          }}
        />
      </>
    );
  }

  async function act(name: string, what: "check" | "default" | "delete") {
    setBusy(name);
    setError("");
    setResult("");
    setWarnings([]);
    try {
      if (what === "check") {
        const s = await checkServer(name);
        setResult(s.state === "ok" ? `${name} is reachable (${engineLabel(s)}).` : "");
        if (s.state !== "ok") setError(`${name} is not reachable: ${s.lastError ?? "unknown error"}.`);
      } else if (what === "default") {
        await setDefaultServer(name);
        setResult(`New databases now go to ${name}.`);
      } else {
        await deleteServer(name);
        setResult(`Removed ${name}. Its databases were not touched.`);
        setConfirm("");
      }
      await onChange();
    } catch (err) {
      setError(databaseMessage(err));
    } finally {
      setBusy("");
    }
  }

  const columns: Column<DBServer>[] = [
    {
      key: "name",
      header: "Server",
      sortValue: (s) => s.name,
      cell: (s) => (
        <span className="ui-row">
          <span className="ui-mono">{s.name}</span>
          {s.default && <Badge tone="ok">Default</Badge>}
        </span>
      ),
    },
    { key: "engine", header: "Engine", sortValue: (s) => engineLabel(s), cell: (s) => engineLabel(s) },
    { key: "address", header: "Address", sortValue: (s) => serverAddress(s), cell: (s) => <span className="ui-mono">{serverAddress(s)}</span> },
    {
      key: "tls",
      header: "Connection",
      cell: (s) => (
        <span>
          {tlsLabel(s)}
          {s.certExpires && (
            <>
              <br />
              <span className="ui-hint">
                Certificate ends <RelTime iso={s.certExpires} />
              </span>
            </>
          )}
        </span>
      ),
    },
    {
      key: "state",
      header: "State",
      sortValue: (s) => s.state,
      cell: (s) => {
        const [tone, label] = stateBadge[s.state];
        return (
          <span>
            <Badge tone={tone}>{label}</Badge>
            {s.checkedAt && !s.local && (
              <>
                <br />
                <span className="ui-hint">
                  Checked <RelTime iso={s.checkedAt} />
                </span>
              </>
            )}
          </span>
        );
      },
    },
    { key: "databases", header: "Databases", sortValue: (s) => s.databases, cell: (s) => String(s.databases) },
    {
      key: "actions",
      header: "Actions",
      srHeader: true,
      cell: (s) => (
        <RowActions>
          {!s.local && (
            <IconButton label={`Check ${s.name}`} icon={<ArrowsClockwise size={16} />} disabled={busy !== ""} onClick={() => void act(s.name, "check")} />
          )}
          {!s.default && (
            <IconButton label={`Make ${s.name} the default`} icon={<Star size={16} />} disabled={busy !== ""} onClick={() => void act(s.name, "default")} />
          )}
          {!s.local && (
            <IconButton
              label={`Remove ${s.name}`}
              icon={<Trash size={16} />}
              danger
              disabled={busy !== "" || s.databases > 0}
              onClick={() => setConfirm(s.name)}
            />
          )}
        </RowActions>
      ),
    },
  ];

  const failing = servers.filter((s) => s.state === "error" && s.lastError);

  return (
    <>
      <PageHead
        title="Database servers"
        description="New databases go to the default server. A database stays on the server it was created on."
        actions={
          <>
            {back}
            <a className="ui-btn ui-btn-primary" href={serversHref({ new: "1" })}>
              <Plus size={16} aria-hidden="true" />
              Add server
            </a>
          </>
        }
      />
      {result && <Notice tone="ok" title={result} />}
      {warnings.map((w) => (
        <Notice key={w} tone="warn" title={w.charAt(0).toUpperCase() + w.slice(1) + "."} />
      ))}
      {error && <Notice tone="err" title={error} />}
      {failing.map((s) => (
        <Notice key={s.name} tone="warn" title={`${s.name}: ${s.lastError ?? ""}`} />
      ))}
      {confirm && (
        <Card title={`Remove ${confirm}?`}>
          <p className="ui-flush">The panel forgets the server and its administrator password. Nothing on the server changes.</p>
          <div className="ui-row">
            <Button variant="danger" disabled={busy !== ""} onClick={() => void act(confirm, "delete")}>
              Remove server
            </Button>
            <Button disabled={busy !== ""} onClick={() => setConfirm("")}>
              Cancel
            </Button>
          </div>
        </Card>
      )}
      <Card padded={false}>
        <Table label="Database servers" columns={columns} rows={servers} rowKey={(s) => s.name} empty="No servers." />
      </Card>
    </>
  );
}

const emptyServer: NewDBServer = {
  engine: "mariadb",
  name: "",
  host: "",
  port: 3306,
  adminUser: "",
  adminPassword: "",
  tlsMode: "verify",
  caCertificate: "",
};

const engineHints: Record<DBEngine, { port: string; admin: string }> = {
  mariadb: {
    port: "Usually 3306.",
    admin: "Needs CREATE, DROP, CREATE USER and GRANT OPTION. The panel stores the password encrypted.",
  },
  postgresql: {
    port: "Usually 5432.",
    admin: "A role with CREATEDB and CREATEROLE, not a superuser. The panel stores the password encrypted.",
  },
};

// AddServerCard adds a server. The panel connects, checks the version and
// creates and drops a test database and user before it saves anything.
function AddServerCard({ onAdded }: { onAdded: (s: DBServer & { warnings?: string[] }) => Promise<void> }) {
  const [s, setS] = useState<NewDBServer>(emptyServer);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");

  const set = (changes: Partial<NewDBServer>) => setS((prev) => ({ ...prev, ...changes }));
  // A port left at the old engine's default follows the new engine.
  const setEngine = (engine: DBEngine) =>
    setS((prev) => ({ ...prev, engine, port: prev.port === defaultPort(prev.engine) ? defaultPort(engine) : prev.port }));

  async function submit(e: FormEvent) {
    e.preventDefault();
    const problem =
      serverNameProblem(s.name) ||
      (s.host === "" ? "Enter the server's host name or IP address." : "") ||
      (!Number.isInteger(s.port) || s.port < 1 || s.port > 65535 ? "Enter a port from 1 to 65535." : "") ||
      (s.adminUser === "" ? "Enter the administrator's user name." : "") ||
      (s.adminPassword === "" ? "Enter the administrator's password." : "") ||
      (s.tlsMode === "verify-ca" && !s.caCertificate.includes("BEGIN CERTIFICATE") ? "Paste the CA certificate in PEM format." : "");
    if (problem) {
      setError(problem);
      return;
    }
    setBusy(true);
    setError("");
    try {
      const added = await addServer(s);
      setS(emptyServer);
      await onAdded(added);
    } catch (err) {
      setError(databaseMessage(err));
    } finally {
      setBusy(false);
    }
  }

  return (
    <Card>
      <form className="ui-stack" onSubmit={(e) => void submit(e)} noValidate>
        <fieldset className="ui-fieldset ui-stack-tight">
          <legend>Engine</legend>
          <label className="ui-check">
            <input type="radio" name="db-server-engine" checked={s.engine === "mariadb"} onChange={() => setEngine("mariadb")} />
            MariaDB or MySQL
          </label>
          <label className="ui-check">
            <input type="radio" name="db-server-engine" checked={s.engine === "postgresql"} onChange={() => setEngine("postgresql")} />
            PostgreSQL
          </label>
        </fieldset>
        <Field label="Name" hint="How the panel shows the server, for example db1.">
          <input className="ui-input" value={s.name} onChange={(e) => set({ name: e.target.value.trim() })} autoComplete="off" spellCheck={false} />
        </Field>
        <div className="ui-row">
          <Field label="Host" hint="Host name or IP address. Sites use it as DB_HOST.">
            <input className="ui-input ui-mono" value={s.host} onChange={(e) => set({ host: e.target.value.trim() })} autoComplete="off" spellCheck={false} />
          </Field>
          <Field label="Port" hint={engineHints[s.engine].port}>
            <input className="ui-input" type="number" min={1} max={65535} value={s.port} onChange={(e) => set({ port: Number(e.target.value) })} />
          </Field>
        </div>
        <Field label="Administrator" hint={engineHints[s.engine].admin}>
          <input className="ui-input ui-mono" value={s.adminUser} onChange={(e) => set({ adminUser: e.target.value.trim() })} autoComplete="off" spellCheck={false} />
        </Field>
        <Field label="Administrator password">
          <input
            className="ui-input"
            type="password"
            value={s.adminPassword}
            onChange={(e) => set({ adminPassword: e.target.value })}
            autoComplete="new-password"
          />
        </Field>
        <fieldset className="ui-fieldset ui-stack-tight">
          <legend>Connection</legend>
          <label className="ui-check">
            <input type="radio" name="db-server-tls" checked={s.tlsMode === "verify"} onChange={() => set({ tlsMode: "verify" })} />
            TLS with a certificate from a public CA (recommended)
          </label>
          <label className="ui-check">
            <input type="radio" name="db-server-tls" checked={s.tlsMode === "verify-ca"} onChange={() => set({ tlsMode: "verify-ca" })} />
            TLS with a certificate from my own CA
          </label>
          <label className="ui-check">
            <input type="radio" name="db-server-tls" checked={s.tlsMode === "none"} onChange={() => set({ tlsMode: "none" })} />
            No TLS (only in a private network)
          </label>
        </fieldset>
        {s.tlsMode === "verify-ca" && (
          <Field label="CA certificate" hint="PEM format. The panel also checks the server's host name.">
            <textarea
              className="ui-input ui-mono"
              rows={6}
              value={s.caCertificate}
              onChange={(e) => set({ caCertificate: e.target.value })}
              spellCheck={false}
            />
          </Field>
        )}
        {s.engine === "postgresql" && (
          <Notice
            tone="info"
            title="PostgreSQL decides in its pg_hba.conf which hosts may log in. Allow this machine's address there, and only it."
          />
        )}
        {s.tlsMode === "none" && (
          <Notice tone="warn" title="Passwords and data cross the network unencrypted. Use this only inside a private network you trust." />
        )}
        {error && <Notice tone="err" title={error} />}
        {busy && <p aria-busy="true">Connecting and checking the privileges…</p>}
        <div className="ui-row">
          <Button variant="primary" type="submit" disabled={busy}>
            Add server
          </Button>
        </div>
      </form>
    </Card>
  );
}
