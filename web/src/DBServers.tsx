// SPDX-License-Identifier: AGPL-3.0-or-later

import { ArrowLeft, ArrowsClockwise, Plus, Star, Trash } from "@phosphor-icons/react";
import { useState, type FormEvent } from "react";
import { Trans, useTranslation } from "react-i18next";
import {
  addServer,
  checkServer,
  databaseMessage,
  defaultPort,
  deleteServer,
  engineLabel,
  POSTGRESQL,
  serverAddress,
  serverNameProblem,
  setDefaultServer,
  type DBEngine,
  type DBServer,
  type NewDBServer,
} from "./databases";
import { formatCount } from "./format";
import { formatHash } from "./routes";
import { Badge, Button, Card, Field, IconButton, Notice, PageHead, RelTime, RowActions, Table, type Column, type Tone } from "./ui";

export const serversHref = (q: Record<string, string> = {}) =>
  formatHash({ page: "databases", query: new URLSearchParams({ view: "servers", ...q }) });

// The label of a state is databases.dbServers.states.<state>.
const stateTone: Record<DBServer["state"], Tone> = { ok: "ok", error: "err", unchecked: "off" };

// tlsKey names how the panel talks to the server: databases.dbServers.tls.<key>.
const tlsKey = (s: DBServer) => ({ socket: "socket", verify: "verify", "verify-ca": "verifyCa" } as const)[s.tlsMode as "socket"] ?? "none";

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
  const { t: tr } = useTranslation();
  const [busy, setBusy] = useState("");
  const [error, setError] = useState("");
  const [result, setResult] = useState("");
  const [confirm, setConfirm] = useState("");
  // What adding a server warned about, shown on the list after it.
  const [warnings, setWarnings] = useState<string[]>([]);

  const back = (
    <a className="ui-btn" href={formatHash({ page: "databases" })}>
      <ArrowLeft size={16} aria-hidden="true" />
      {tr("databases.allDatabases")}
    </a>
  );

  if (query.get("new") === "1") {
    return (
      <>
        <PageHead
          title={tr("databases.dbServers.new.title")}
          description={tr("databases.dbServers.new.description")}
          actions={
            <a className="ui-btn" href={serversHref()}>
              <ArrowLeft size={16} aria-hidden="true" />
              {tr("databases.dbServers.allServers")}
            </a>
          }
        />
        <AddServerCard
          onAdded={async (s) => {
            setError("");
            setResult(tr("databases.dbServers.added", { name: s.name, engine: engineLabel(s) }));
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
        setResult(s.state === "ok" ? tr("databases.dbServers.reachable", { name, engine: engineLabel(s) }) : "");
        if (s.state !== "ok") setError(tr("databases.dbServers.notReachable", { name, reason: s.lastError ?? tr("databases.dbServers.unknownError") }));
      } else if (what === "default") {
        await setDefaultServer(name);
        setResult(tr("databases.dbServers.nowDefault", { name }));
      } else {
        await deleteServer(name);
        setResult(tr("databases.dbServers.removed", { name }));
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
      header: tr("databases.dbServers.columns.server"),
      sortValue: (s) => s.name,
      cell: (s) => (
        <span className="ui-row">
          <span className="ui-mono">{s.name}</span>
          {s.default && <Badge tone="ok">{tr("databases.dbServers.default")}</Badge>}
        </span>
      ),
    },
    { key: "engine", header: tr("databases.dbServers.columns.engine"), sortValue: (s) => engineLabel(s), cell: (s) => engineLabel(s) },
    { key: "address", header: tr("databases.dbServers.columns.address"), sortValue: (s) => serverAddress(s), cell: (s) => <span className="ui-mono">{serverAddress(s)}</span> },
    {
      key: "tls",
      header: tr("databases.dbServers.columns.connection"),
      cell: (s) => (
        <span>
          {tr(`databases.dbServers.tls.${tlsKey(s)}`)}
          {s.certExpires && (
            <>
              <br />
              <span className="ui-hint">
                <Trans i18nKey="databases.dbServers.certEnds" components={{ time: <RelTime iso={s.certExpires} /> }} />
              </span>
            </>
          )}
        </span>
      ),
    },
    {
      key: "state",
      header: tr("databases.dbServers.columns.state"),
      sortValue: (s) => s.state,
      cell: (s) => {
        return (
          <span>
            <Badge tone={stateTone[s.state]}>{tr(`databases.dbServers.states.${s.state}`)}</Badge>
            {s.checkedAt && !s.local && (
              <>
                <br />
                <span className="ui-hint">
                  <Trans i18nKey="databases.dbServers.checked" components={{ time: <RelTime iso={s.checkedAt} /> }} />
                </span>
              </>
            )}
          </span>
        );
      },
    },
    { key: "databases", header: tr("databases.dbServers.columns.databases"), sortValue: (s) => s.databases, cell: (s) => formatCount(s.databases) },
    {
      key: "actions",
      header: tr("databases.dbServers.columns.actions"),
      srHeader: true,
      cell: (s) => (
        <RowActions>
          {!s.local && (
            <IconButton label={tr("databases.dbServers.checkLabel", { name: s.name })} text={tr("databases.dbServers.check")} icon={<ArrowsClockwise size={16} />} disabled={busy !== ""} onClick={() => void act(s.name, "check")} />
          )}
          {!s.default && (
            <IconButton label={tr("databases.dbServers.makeDefaultLabel", { name: s.name })} text={tr("databases.dbServers.makeDefault")} icon={<Star size={16} />} disabled={busy !== ""} onClick={() => void act(s.name, "default")} />
          )}
          {!s.local && (
            <IconButton
              label={tr("databases.dbServers.removeLabel", { name: s.name })}
              text={tr("databases.dbServers.remove")}
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
        title={tr("databases.dbServers.title")}
        description={tr("databases.dbServers.description")}
        actions={
          <>
            {back}
            <a className="ui-btn ui-btn-primary" href={serversHref({ new: "1" })}>
              <Plus size={16} aria-hidden="true" />
              {tr("databases.dbServers.add")}
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
        <Card title={tr("databases.dbServers.removeTitle", { name: confirm })}>
          <p className="ui-flush">{tr("databases.dbServers.removeText")}</p>
          <div className="ui-row">
            <Button variant="danger" disabled={busy !== ""} onClick={() => void act(confirm, "delete")}>
              {tr("databases.dbServers.removeSubmit")}
            </Button>
            <Button disabled={busy !== ""} onClick={() => setConfirm("")}>
              {tr("common.actions.cancel")}
            </Button>
          </div>
        </Card>
      )}
      <Card padded={false}>
        <Table label={tr("databases.dbServers.title")} columns={columns} rows={servers} rowKey={(s) => s.name} empty={tr("databases.dbServers.empty")} />
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


// AddServerCard adds a server. The panel connects, checks the version and
// creates and drops a test database and user before it saves anything.
function AddServerCard({ onAdded }: { onAdded: (s: DBServer & { warnings?: string[] }) => Promise<void> }) {
  const { t: tr } = useTranslation();
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
      (s.host === "" ? tr("databases.problems.host") : "") ||
      (!Number.isInteger(s.port) || s.port < 1 || s.port > 65535 ? tr("databases.problems.port") : "") ||
      (s.adminUser === "" ? tr("databases.problems.adminUser") : "") ||
      (s.adminPassword === "" ? tr("databases.problems.adminPassword") : "") ||
      (s.tlsMode === "verify-ca" && !s.caCertificate.includes("BEGIN CERTIFICATE") ? tr("databases.problems.ca") : "");
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
          <legend>{tr("databases.dbServers.new.engine")}</legend>
          <label className="ui-check">
            <input type="radio" name="db-server-engine" checked={s.engine === "mariadb"} onChange={() => setEngine("mariadb")} />
            {tr("databases.dbServers.new.mariadb")}
          </label>
          <label className="ui-check">
            <input type="radio" name="db-server-engine" checked={s.engine === "postgresql"} onChange={() => setEngine("postgresql")} />
            {POSTGRESQL}
          </label>
        </fieldset>
        <Field label={tr("databases.dbServers.new.name")} hint={tr("databases.dbServers.new.nameHint")}>
          <input className="ui-input" value={s.name} onChange={(e) => set({ name: e.target.value.trim() })} autoComplete="off" spellCheck={false} />
        </Field>
        <div className="ui-row">
          <Field label={tr("databases.dbServers.new.host")} hint={tr("databases.dbServers.new.hostHint")}>
            <input className="ui-input ui-mono" value={s.host} onChange={(e) => set({ host: e.target.value.trim() })} autoComplete="off" spellCheck={false} />
          </Field>
          <Field label={tr("databases.dbServers.new.port")} hint={tr("databases.dbServers.new.portHint", { port: defaultPort(s.engine) })}>
            <input className="ui-input" type="number" min={1} max={65535} value={s.port} onChange={(e) => set({ port: Number(e.target.value) })} />
          </Field>
        </div>
        <Field label={tr("databases.dbServers.new.admin")} hint={tr(s.engine === "postgresql" ? "databases.dbServers.new.adminHintPostgresql" : "databases.dbServers.new.adminHintMariadb")}>
          <input className="ui-input ui-mono" value={s.adminUser} onChange={(e) => set({ adminUser: e.target.value.trim() })} autoComplete="off" spellCheck={false} />
        </Field>
        <Field label={tr("databases.dbServers.new.adminPassword")}>
          <input
            className="ui-input"
            type="password"
            value={s.adminPassword}
            onChange={(e) => set({ adminPassword: e.target.value })}
            autoComplete="new-password"
          />
        </Field>
        <fieldset className="ui-fieldset ui-stack-tight">
          <legend>{tr("databases.dbServers.new.connection")}</legend>
          <label className="ui-check">
            <input type="radio" name="db-server-tls" checked={s.tlsMode === "verify"} onChange={() => set({ tlsMode: "verify" })} />
            {tr("databases.dbServers.new.verify")}
          </label>
          <label className="ui-check">
            <input type="radio" name="db-server-tls" checked={s.tlsMode === "verify-ca"} onChange={() => set({ tlsMode: "verify-ca" })} />
            {tr("databases.dbServers.new.verifyCa")}
          </label>
          <label className="ui-check">
            <input type="radio" name="db-server-tls" checked={s.tlsMode === "none"} onChange={() => set({ tlsMode: "none" })} />
            {tr("databases.dbServers.new.none")}
          </label>
        </fieldset>
        {s.tlsMode === "verify-ca" && (
          <Field label={tr("databases.dbServers.new.ca")} hint={tr("databases.dbServers.new.caHint")}>
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
          <Notice tone="info" title={tr("databases.dbServers.new.pgHba")} />
        )}
        {s.tlsMode === "none" && (
          <Notice tone="warn" title={tr("databases.dbServers.new.noTls")} />
        )}
        {error && <Notice tone="err" title={error} />}
        {busy && <p aria-busy="true">{tr("databases.dbServers.new.checking")}</p>}
        <div className="ui-row">
          <Button variant="primary" type="submit" disabled={busy}>
            {tr("databases.dbServers.add")}
          </Button>
        </div>
      </form>
    </Card>
  );
}
