// SPDX-License-Identifier: AGPL-3.0-or-later

import { ArrowLeft, Copy, ListChecks, Trash, UploadSimple } from "@phosphor-icons/react";
import { Fragment, useCallback, useEffect, useRef, useState, type FormEvent, type ReactNode } from "react";
import { Cutover } from "./Cutover";
import { SnippetField } from "./Snippets";
import { setSnippets, sizeProblem, snippetDraft, snippetMessage, snippetProblems, type DraftItem, type SnippetDraft, type SnippetProblems } from "./snippets";
import {
  activateImport,
  cronLabel,
  createFromPlan,
  deleteReport,
  deleteSource,
  existsHere,
  formatBytes,
  getImport,
  listLoads,
  startLoads,
  getReport,
  getSource,
  listImports,
  hostProblem,
  listReports,
  migrationMessage,
  newDBPassword,
  parseDBMap,
  scanSourceHostKey,
  setSource,
  startImport,
  trustSourceHostKey,
  uploadReport,
  type DBCredentials,
  type DumpLoad,
  type ImportJob,
  type PlanSite,
  type Report,
  type SourceInfo,
} from "./cloudpanel";
import { listDatabases } from "./databases";
import { formatHash } from "./routes";
import { getNode, type NodeStatus } from "./node";
import { getPython, type PythonStatus } from "./python";
import { presetLabel, type Preset } from "./pagecache";
import { defaultPool, listPools, phpVersions, poolLabel, poolNote, type Pool } from "./sites";
import {
  Badge,
  Button,
  Card,
  Field,
  IconButton,
  IconLink,
  Notice,
  PageHead,
  RelTime,
  RowActions,
  Table,
  type Column,
  type Sort,
  type Tone,
} from "./ui";

const scanCommand = "hostashi cloudpanel scan -json -full > hostashi-scan.json";
const fingerprintCommand = "for f in /etc/ssh/ssh_host_*_key.pub; do ssh-keygen -lf \"$f\"; done";

type QueryProps = {
  /** The address query: report=<id> for a plan, and site=<name> for one site of it; q, status, sort, dir for the plan table. */
  query?: URLSearchParams;
  /** Replaces the query in the address without a new history entry. */
  onQuery?: (q: URLSearchParams) => void;
};

// typeLabel names the scanned site types.
const typeLabel: Record<string, string> = {
  static: "Static",
  php: "PHP",
  wordpress: "WordPress",
  nodejs: "Node.js",
  python: "Python",
  proxy: "Reverse proxy",
};

const importHash = (q?: Record<string, string>) => formatHash({ page: "import", ...(q ? { query: new URLSearchParams(q) } : {}) });
const siteKey = (s: PlanSite) => s.name || s.source;

// Migrate moves sites from a CloudPanel server to this one: connect the old
// server, upload its scan report, create the sites the plan proposes and
// copy their files and databases (ADR-0026). The setup and the reports are
// one view; a plan (?report=) and one site of it (&site=) are their own.
export function Migrate({ query = new URLSearchParams(), onQuery = () => undefined }: QueryProps) {
  const [source, setInfo] = useState<SourceInfo | null>(null);
  const [error, setError] = useState("");

  useEffect(() => {
    getSource()
      .then(setInfo)
      .catch((err: unknown) => setError(migrationMessage(err)));
  }, []);

  const head = (
    <PageHead
      title="CloudPanel import"
      description="Move sites from a CloudPanel server to this one: connect the old server, upload its scan report, then create and copy the sites."
    />
  );

  if (!source) {
    return (
      <>
        {head}
        {error ? <Notice tone="err" title={error} /> : <p aria-busy="true">Loading…</p>}
      </>
    );
  }

  const report = query.get("report");
  if (report) {
    return <PlanView key={report} report={report} ready={source.ready} query={query} onQuery={onQuery} />;
  }

  return (
    <>
      {head}
      <SourceCard info={source} onChange={setInfo} />
      {source.source && <HostKeyCard info={source} onChange={setInfo} />}
      {source.source && <DumpUserCard info={source} onChange={setInfo} />}
      <ReportsCard
        onOpen={(id) => {
          window.location.hash = importHash({ report: id });
        }}
      />
    </>
  );
}

async function copyText(text: string): Promise<string> {
  try {
    await navigator.clipboard.writeText(text);
    return "Copied.";
  } catch {
    return "Copying is not possible here. Select the text and copy it by hand.";
  }
}

function CopyBlock({ label, text }: { label: string; text: string }) {
  const [copied, setCopied] = useState("");
  return (
    <div className="ui-stack-tight">
      <pre className="ui-code" aria-label={label}>
        {text}
      </pre>
      <div className="ui-row">
        <Button icon={<Copy size={16} aria-hidden="true" />} onClick={() => void copyText(text).then(setCopied)}>
          Copy
        </Button>
        {copied && (
          <span role="status" className="ui-hint">
            {copied}
          </span>
        )}
      </div>
    </div>
  );
}

function SourceCard({ info, onChange }: { info: SourceInfo; onChange: (i: SourceInfo) => void }) {
  const [host, setHost] = useState(info.source?.host ?? "");
  const [port, setPort] = useState(String(info.source?.port ?? 22));
  const [confirmRemove, setConfirmRemove] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [saved, setSaved] = useState(false);

  async function save(e: FormEvent) {
    e.preventDefault();
    const h = host.trim().toLowerCase();
    const problem = hostProblem(h, port.trim());
    setSaved(false);
    if (problem) {
      setError(problem);
      return;
    }
    setBusy(true);
    setError("");
    try {
      onChange(await setSource(h, Number(port)));
      setSaved(true);
    } catch (err) {
      setError(migrationMessage(err));
    } finally {
      setBusy(false);
    }
  }

  async function remove() {
    setBusy(true);
    setError("");
    try {
      const i = await deleteSource();
      setHost("");
      setPort("22");
      setConfirmRemove(false);
      onChange(i);
    } catch (err) {
      setError(migrationMessage(err));
    } finally {
      setBusy(false);
    }
  }

  const state = !info.source ? null : info.ready ? <Badge tone="ok">Ready</Badge> : <Badge tone="warn">Not ready</Badge>;

  return (
    <Card title="Old server" {...(state ? { actions: state } : {})}>
      <div className="ui-stack">
        <p className="ui-flush">
          Hostashi reads the sites over SSH from the CloudPanel server. It only reads there and changes nothing.
          {info.source && (info.ready ? " The connection is ready." : " The connection is not ready yet: finish the steps below.")}
        </p>
        <form className="ui-stack" onSubmit={(e) => void save(e)} noValidate>
          <div className="ui-host-port">
            <Field label="Address" hint="Prefer the IP address: DNS names move during the migration.">
              <input className="ui-input ui-mono" value={host} onChange={(e) => setHost(e.target.value)} autoComplete="off" spellCheck={false} />
            </Field>
            <Field label="SSH port">
              <input className="ui-input" value={port} onChange={(e) => setPort(e.target.value)} inputMode="numeric" />
            </Field>
          </div>
          {error && <Notice tone="err" title={error} />}
          {saved && <Notice tone="ok" title="Saved." />}
          <div className="ui-row">
            <Button type="submit" variant="primary" disabled={busy}>
              {info.source ? "Save" : "Connect old server"}
            </Button>
          </div>
        </form>
        {info.authorizedKeysLine && (
          <div className="ui-stack-tight">
            <p className="ui-flush">
              On the old server, add this line to <code>/root/.ssh/authorized_keys</code>. It only allows reading files over SFTP and a tunnel
              to its database.
            </p>
            <CopyBlock label="authorized_keys line" text={info.authorizedKeysLine} />
          </div>
        )}
        {info.source &&
          (confirmRemove ? (
            <div className="ui-stack-tight" role="alertdialog" aria-label="Forget old server">
              <p className="ui-flush">Forget the old server? Its key and host key are deleted here. Remove the line from authorized_keys there as well.</p>
              <div className="ui-row">
                <Button variant="danger" disabled={busy} onClick={() => void remove()}>
                  Forget old server
                </Button>
                <Button disabled={busy} onClick={() => setConfirmRemove(false)}>
                  Cancel
                </Button>
              </div>
            </div>
          ) : (
            <div className="ui-row">
              <Button variant="danger" onClick={() => setConfirmRemove(true)}>
                Forget old server
              </Button>
            </div>
          ))}
      </div>
    </Card>
  );
}

function HostKeyCard({ info, onChange }: { info: SourceInfo; onChange: (i: SourceInfo) => void }) {
  const [scanned, setScanned] = useState<{ key: string; fingerprint: string } | null>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");

  async function run(f: () => Promise<void>) {
    setBusy(true);
    setError("");
    try {
      await f();
    } catch (err) {
      setError(migrationMessage(err));
    } finally {
      setBusy(false);
    }
  }

  return (
    <Card title="Host key of the old server" actions={info.hostKey ? <Badge tone="ok">Trusted</Badge> : <Badge tone="warn">Not confirmed</Badge>}>
      <div className="ui-stack">
        {info.hostKey ? (
          <p className="ui-flush ui-break">
            Trusted: <code>{info.hostKey}</code>
          </p>
        ) : (
          <p className="ui-flush">The old server&apos;s SSH host key is not confirmed yet. Nothing is copied until it is.</p>
        )}
        {scanned && (
          <div className="ui-stack-tight">
            <p className="ui-flush">
              The old server presents <code>{scanned.fingerprint}</code>. On the old server, run this and trust the key only if one line shows
              the same fingerprint:
            </p>
            <pre className="ui-code" aria-label="Fingerprint command">
              {fingerprintCommand}
            </pre>
            <div className="ui-row">
              <Button
                variant="primary"
                disabled={busy}
                onClick={() =>
                  void run(async () => {
                    onChange(await trustSourceHostKey(scanned.key));
                    setScanned(null);
                  })
                }
              >
                Trust this key
              </Button>
            </div>
          </div>
        )}
        {error && <Notice tone="err" title={error} />}
        <div className="ui-row">
          <Button disabled={busy} onClick={() => void run(async () => setScanned(await scanSourceHostKey()))}>
            {info.hostKey ? "Check the host key again" : "Fetch the host key"}
          </Button>
        </div>
      </div>
    </Card>
  );
}

function DumpUserCard({ info, onChange }: { info: SourceInfo; onChange: (i: SourceInfo) => void }) {
  const [creds, setCreds] = useState<DBCredentials | null>(null);
  const [confirmNew, setConfirmNew] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const heading = useRef<HTMLHeadingElement>(null);

  useEffect(() => {
    if (creds) heading.current?.focus();
  }, [creds]);

  async function generate() {
    setBusy(true);
    setError("");
    try {
      const c = await newDBPassword();
      setCreds(c);
      setConfirmNew(false);
      onChange({ ...info, dbUser: c.user });
    } catch (err) {
      setError(migrationMessage(err));
    } finally {
      setBusy(false);
    }
  }

  if (creds) {
    return (
      <section className="ui-card ui-card-body ui-stack ui-attention" aria-labelledby="mig-dbsql">
        <h2 id="mig-dbsql" className="ui-flush" ref={heading} tabIndex={-1}>
          Run this SQL on the old server now
        </h2>
        <p className="ui-flush">
          Run it as the MariaDB administrator there, for example in <code>mysql</code> as root. It creates the user <code>{creds.user}</code>{" "}
          that may only read. Hostashi shows it only this once.
        </p>
        <CopyBlock label="SQL for the old server" text={creds.sql} />
        <div className="ui-row">
          <Button variant="primary" onClick={() => setCreds(null)}>
            I have run it
          </Button>
        </div>
      </section>
    );
  }

  return (
    <Card title="Database access">
      <div className="ui-stack">
        {info.dbUser ? (
          <p className="ui-flush">
            Databases are read as <code>{info.dbUser}</code> on the old server.
          </p>
        ) : (
          <p className="ui-flush">To copy databases, Hostashi needs a read-only database user on the old server. Files are copied without it.</p>
        )}
        {error && <Notice tone="err" title={error} />}
        {info.dbUser && confirmNew ? (
          <div className="ui-stack-tight">
            <p className="ui-flush">A new password replaces the old one. Run the new SQL on the old server, or database copies fail.</p>
            <div className="ui-row">
              <Button variant="primary" disabled={busy} onClick={() => void generate()}>
                Make a new password
              </Button>
              <Button disabled={busy} onClick={() => setConfirmNew(false)}>
                Cancel
              </Button>
            </div>
          </div>
        ) : (
          <div className="ui-row">
            <Button
              variant={info.dbUser ? "secondary" : "primary"}
              disabled={busy}
              onClick={() => (info.dbUser ? setConfirmNew(true) : void generate())}
            >
              {info.dbUser ? "Make a new password" : "Create the database user"}
            </Button>
          </div>
        )}
      </div>
    </Card>
  );
}

function ReportsCard({ onOpen }: { onOpen: (id: string) => void }) {
  const [reports, setReports] = useState<Report[] | null>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [confirmDelete, setConfirmDelete] = useState("");
  const file = useRef<HTMLInputElement>(null);

  const load = useCallback(async () => {
    try {
      setReports(await listReports());
    } catch (err) {
      setError(migrationMessage(err));
    }
  }, []);

  useEffect(() => {
    void load();
  }, [load]);

  async function upload(e: FormEvent) {
    e.preventDefault();
    const f = file.current?.files?.[0];
    if (!f) {
      setError("Choose the report file.");
      return;
    }
    if (f.size > 16 << 20) {
      setError("The report is larger than 16 MiB.");
      return;
    }
    setBusy(true);
    setError("");
    try {
      const r = await uploadReport(await f.text());
      if (file.current) file.current.value = "";
      onOpen(r.id);
    } catch (err) {
      setError(migrationMessage(err));
    } finally {
      setBusy(false);
    }
  }

  async function remove(id: string) {
    setBusy(true);
    setError("");
    try {
      await deleteReport(id);
      setConfirmDelete("");
      await load();
    } catch (err) {
      setError(migrationMessage(err));
    } finally {
      setBusy(false);
    }
  }

  const columns: Column<Report>[] = [
    { key: "created", header: "Uploaded", sortValue: (r) => r.createdAt, cell: (r) => <RelTime iso={r.createdAt} /> },
    { key: "sites", header: "Sites", sortValue: (r) => r.sites, cell: (r) => r.sites },
    {
      key: "actions",
      header: "Actions",
      srHeader: true,
      cell: (r) => (
        <RowActions>
          <IconLink label="Show plan" icon={<ListChecks size={16} />} href={importHash({ report: r.id })} />
          <IconButton label="Delete report" icon={<Trash size={16} />} danger disabled={busy} onClick={() => setConfirmDelete(r.id)} />
        </RowActions>
      ),
    },
  ];

  return (
    <Card title="Scan reports" padded={false}>
      <div className="ui-card-body ui-stack">
        <p className="ui-flush">Run the scan on the old server as root. It only reads the CloudPanel configuration.</p>
        <pre className="ui-code" aria-label="Scan command">
          {scanCommand}
        </pre>
        <form className="ui-stack" onSubmit={(e) => void upload(e)} noValidate>
          <Field label="Report file">
            <input className="ui-file" ref={file} type="file" accept="application/json,.json" />
          </Field>
          {error && <Notice tone="err" title={error} />}
          <div className="ui-row">
            <Button type="submit" variant="primary" icon={<UploadSimple size={16} aria-hidden="true" />} disabled={busy}>
              {busy ? "Uploading…" : "Upload report"}
            </Button>
          </div>
        </form>
        {confirmDelete && (
          <div className="ui-stack-tight" role="alertdialog" aria-label="Delete report">
            <p className="ui-flush">Delete this report? Sites created from it stay.</p>
            <div className="ui-row">
              <Button variant="danger" disabled={busy} onClick={() => void remove(confirmDelete)}>
                Delete report
              </Button>
              <Button disabled={busy} onClick={() => setConfirmDelete("")}>
                Cancel
              </Button>
            </div>
          </div>
        )}
      </div>
      {reports === null ? (
        <p className="ui-card-body" aria-busy="true">
          Loading…
        </p>
      ) : (
        <Table
          label="Scan reports"
          columns={columns}
          rows={reports}
          rowKey={(r) => r.id}
          sort={{ key: "created", dir: "desc" }}
          empty="No report is uploaded."
        />
      )}
    </Card>
  );
}

const tlsLabel: Record<PlanSite["tls"], string> = {
  none: "None on the old server",
  reissue: "New certificate after the DNS switch",
  copy: "Purchased certificate, copied",
};

type Status = { word: string; tone: Tone; rank: number; value: string };

function statusOf(s: PlanSite): Status {
  if (existsHere(s)) return { word: "Created", tone: "ok", rank: 2, value: "created" };
  if (s.action === "import") return { word: "Ready", tone: "ok", rank: 1, value: "import" };
  if (s.action === "review") return { word: "Review", tone: "warn", rank: 0, value: "review" };
  return { word: "Skipped", tone: "off", rank: 3, value: "skip" };
}

const sentence = (m: string) => m.charAt(0).toUpperCase() + m.slice(1) + ".";

type PlanProps = { report: string; ready: boolean; query: URLSearchParams; onQuery: (q: URLSearchParams) => void };

// PlanView shows what the scan report proposes for each site, and one site
// of it with the steps to move it.
function PlanView({ report, ready, query, onQuery }: PlanProps) {
  const [rep, setRep] = useState<Report | null>(null);
  const [php, setPHP] = useState<string[]>([]);
  const [error, setError] = useState("");

  const load = useCallback(async () => {
    try {
      setRep(await getReport(report));
      setError("");
    } catch (err) {
      setError(migrationMessage(err));
    }
  }, [report]);

  useEffect(() => {
    void load();
    phpVersions()
      .then((v) => setPHP(v.installed))
      .catch(() => setPHP([]));
  }, [load]);

  const set = (changes: Record<string, string>) => {
    const next = new URLSearchParams(query);
    for (const [k, v] of Object.entries(changes)) {
      if (v) next.set(k, v);
      else next.delete(k);
    }
    onQuery(next);
  };

  const back = (
    <a className="ui-btn" href={importHash()}>
      <ArrowLeft size={16} aria-hidden="true" />
      Old server and reports
    </a>
  );

  if (!rep?.plan) {
    return (
      <>
        <PageHead title="Plan" actions={back} />
        {error ? <Notice tone="err" title={error} /> : <p aria-busy="true">Loading…</p>}
      </>
    );
  }

  const name = query.get("site");
  if (name !== null) {
    const site = rep.plan.sites.find((s) => siteKey(s) === name);
    const planLink = (
      <a className="ui-btn" href={importHash({ report })}>
        <ArrowLeft size={16} aria-hidden="true" />
        Plan
      </a>
    );
    if (!site) {
      return (
        <>
          <PageHead title="Plan" actions={planLink} />
          <Notice tone="warn" title={`The plan has no site ${name}.`} />
        </>
      );
    }
    return <PlanSiteView report={report} site={site} php={php} ready={ready} back={planLink} onChange={load} />;
  }

  const s = rep.plan.summary;
  const columns: Column<PlanSite>[] = [
    {
      key: "site",
      header: "Site",
      sortValue: siteKey,
      cell: (p) => (
        <div className="ui-cell-main">
          <a href={importHash({ report, site: siteKey(p) })}>{p.name || p.source}</a>
          <span>{p.domains.length ? p.domains.join(", ") : "No domain"}</span>
        </div>
      ),
    },
    { key: "type", header: "Type", sortValue: (p) => p.type, cell: (p) => typeLabel[p.type] ?? p.type },
    { key: "php", header: "PHP", sortValue: (p) => p.php, cell: (p) => p.php || <span className="ui-hint">None</span> },
    {
      key: "status",
      header: "Status",
      sortValue: (p) => statusOf(p).rank,
      cell: (p) => {
        const st = statusOf(p);
        return (
          <div className="ui-stack-tight">
            <span className="ui-row">
              <Badge tone={st.tone}>{st.word}</Badge>
            </span>
            {p.reasons.length > 0 && st.value !== "created" && <p className="ui-hint ui-flush">{sentence(p.reasons[0]!.msg)}</p>}
          </div>
        );
      },
    },
  ];

  const q = (query.get("q") ?? "").trim().toLowerCase();
  const status = query.get("status") ?? "";
  const shown = rep.plan.sites.filter(
    (p) => (!q || siteKey(p).toLowerCase().includes(q) || p.domains.some((d) => d.includes(q))) && (!status || statusOf(p).value === status),
  );
  const sortKey = query.get("sort");
  const sort: Sort = sortKey ? { key: sortKey, dir: query.get("dir") === "desc" ? "desc" : "asc" } : { key: "status", dir: "asc" };

  return (
    <>
      <PageHead
        title="Plan"
        description={
          <>
            From the report uploaded <RelTime iso={rep.createdAt} />. It is worked out again on every view, so sites created here show as
            created.
          </>
        }
        actions={back}
      />
      <Notice tone="info" title={`${s.sites} sites: ${s.import} ready, ${s.review} to review, ${s.skip} skipped.`}>
        {!ready ? "Imports can start once the old server and its host key are set up." : "Open a site to create it here and copy it."}
      </Notice>
      {error && <Notice tone="err" title={error} />}
      <Card padded={false}>
        <div className="ui-filters">
          <input
            className="ui-input"
            type="search"
            aria-label="Filter sites"
            placeholder="Filter by site or domain"
            value={query.get("q") ?? ""}
            onChange={(e) => set({ q: e.target.value })}
          />
          <select className="ui-select" aria-label="Status" value={status} onChange={(e) => set({ status: e.target.value })}>
            <option value="">All sites</option>
            <option value="review">Review</option>
            <option value="import">Ready</option>
            <option value="created">Created</option>
            <option value="skip">Skipped</option>
          </select>
        </div>
        <Table
          label="Plan"
          columns={columns}
          rows={shown}
          rowKey={(p) => siteKey(p)}
          sort={sort}
          onSort={(x) => set({ sort: x.key, dir: x.dir })}
          empty="No site matches the filter."
        />
      </Card>
    </>
  );
}

type SiteViewProps = { report: string; site: PlanSite; php: string[]; ready: boolean; back: ReactNode; onChange: () => Promise<void> };

function PlanSiteView({ report, site, php, ready, back, onChange }: SiteViewProps) {
  const created = existsHere(site);
  const st = statusOf(site);
  const title = site.name || site.source;

  return (
    <>
      <PageHead
        title={title}
        description={
          <span className="ui-row">
            <Badge tone={st.tone}>{st.word}</Badge>
          </span>
        }
        actions={back}
      />
      {site.reasons.length > 0 && !created && (
        <Notice tone={site.action === "skip" ? "info" : "warn"} title={site.action === "review" ? "Check these points first" : "Why"}>
          <ul className="ui-plain-list" aria-label={"Reasons for " + title}>
            {site.reasons.map((r) => (
              <li key={r.code + r.msg}>{sentence(r.msg)}</li>
            ))}
          </ul>
        </Notice>
      )}
      <Card title="On the old server">
        <dl className="ui-facts" aria-label={"Plan for " + title}>
          <dt>Domains</dt>
          <dd>{site.domains.length ? site.domains.join(", ") : "None"}</dd>
          {site.redirectFrom.length > 0 && (
            <>
              <dt>Redirects</dt>
              <dd>
                {site.redirectFrom.join(", ")}
                {site.wwwRedirect && (
                  <span className="ui-hint ui-flush">
                    {" "}
                    · taken over as the site's www redirect ({site.wwwRedirect === "to-www" ? "to the www name" : "to the name without www"})
                  </span>
                )}
              </dd>
            </>
          )}
          <dt>Type</dt>
          <dd>{typeLabel[site.type] ?? site.type}</dd>
          {site.proxy && (
            <>
              <dt>Target</dt>
              <dd className="ui-mono ui-break">{site.proxy}</dd>
            </>
          )}
          {site.php && (
            <>
              <dt>PHP</dt>
              <dd>{site.php}</dd>
            </>
          )}
          <dt>HTTPS</dt>
          <dd>{tlsLabel[site.tls] ?? site.tls}</dd>
          {!site.proxy && (
            <>
              <dt>Old files</dt>
              <dd className="ui-mono ui-break">{site.docRoot}</dd>
            </>
          )}
          {site.customSnippets > 0 && (
            <>
              <dt>Custom nginx</dt>
              <dd>{site.customSnippets} directives</dd>
            </>
          )}
          {site.cronJobs > 0 && (
            <>
              <dt>Cron jobs</dt>
              <dd>{cronLabel(site)}</dd>
            </>
          )}
        </dl>
      </Card>
      {(site.action === "import" || site.action === "review") && (
        <Card title="Create the site">
          <CreateForm report={report} site={site} php={php} onChange={onChange} />
        </Card>
      )}
      {created && site.cronImport > 0 && (
        <Notice
          tone="info"
          title={`${site.cronImport} cron ${site.cronImport === 1 ? "job was created paused. Start it" : "jobs were created paused. Start them"} after the DNS switch.`}
        />
      )}
      {created && site.proxy && (
        <Notice tone="info" title="A reverse proxy site has no Linux account here: there are no files or databases to copy." />
      )}
      {created && site.node && (
        <Notice tone="info" title="The app stays stopped. After the final sync, set its start command on the site's Web tab." />
      )}
      {created && site.python && (
        <Notice tone="info" title="The app stays stopped. After the final sync, install its packages into ~/.venv over SSH and set its start command on the site's Web tab." />
      )}
      {created && !site.proxy && (
        <Card title="Files and databases">
          <ImportForm report={report} site={site} ready={ready} />
        </Card>
      )}
      {created && !site.proxy && site.customSnippets > 0 && (
        <Card title="Custom nginx">
          <SnippetDraftForm report={report} site={site} />
        </Card>
      )}
      {created && (
        <Card title="DNS switch">
          <Cutover site={site.name} />
        </Card>
      )}
    </>
  );
}

function CreateForm({ report, site, php, onChange }: { report: string; site: PlanSite; php: string[]; onChange: () => Promise<void> }) {
  const [reviewed, setReviewed] = useState(false);
  const [version, setVersion] = useState(site.php);
  const [line, setLine] = useState(site.node ?? "");
  const [node, setNode] = useState<NodeStatus | null>(null);
  const [pyLine, setPyLine] = useState(site.python ?? "");
  const [python, setPython] = useState<PythonStatus | null>(null);
  const [cache, setCache] = useState(true);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [pools, setPools] = useState<Pool[]>([]);
  const [pool, setPool] = useState("default");
  const review = site.action === "review";

  // The pool choice shows only with a second pool (ADR-0016); a reverse
  // proxy site has no home.
  useEffect(() => {
    if (site.proxy) return;
    listPools()
      .then((list) => {
        setPools(list);
        setPool(defaultPool(list));
      })
      .catch(() => setPools([]));
  }, [site.proxy]);
  useEffect(() => {
    if (!site.node) return;
    getNode()
      .then(setNode)
      .catch(() => setNode(null));
  }, [site.node]);
  useEffect(() => {
    if (!site.python) return;
    getPython()
      .then(setPython)
      .catch(() => setPython(null));
  }, [site.python]);
  const installedPy = python?.installed.map((i) => i.line) ?? [];
  const pyLines = python?.releases.map((r) => r.line) ?? (site.python ? [site.python] : []);
  const installed = node?.installed.map((i) => i.major) ?? [];
  const lines = node?.releases.map((r) => r.major) ?? (site.node ? [site.node] : []);
  const choosePool = pools.length > 1;
  const chosen = pools.find((p) => p.name === pool);
  const tooSmall = !!(chosen?.available && site.homeBytes && chosen.freeBytes !== undefined && site.homeBytes > chosen.freeBytes);
  const choices = site.php && !php.includes(site.php) ? [site.php, ...php] : php;

  async function submit(e: FormEvent) {
    e.preventDefault();
    if (review && !reviewed) {
      setError("Confirm that you checked the points above.");
      return;
    }
    setBusy(true);
    setError("");
    try {
      await createFromPlan(
        report,
        site.name,
        reviewed,
        version !== site.php ? version : "",
        site.pageCache ? cache : undefined,
        choosePool ? pool : "",
        line !== site.node ? line : "",
        pyLine !== site.python ? pyLine : "",
      );
      await onChange();
    } catch (err) {
      setError(migrationMessage(err));
    } finally {
      setBusy(false);
    }
  }

  return (
    <form className="ui-stack" onSubmit={(e) => void submit(e)} noValidate>
      <p className="ui-flush">The site gets the old domains without a certificate. HTTPS follows after the DNS switch.</p>
      {site.php && choices.length > 0 && (
        <div className="ui-narrow">
          <Field label={"PHP version for " + site.name}>
            <select className="ui-select" value={version} onChange={(e) => setVersion(e.target.value)}>
              {choices.map((v) => (
                <option key={v} value={v}>
                  {v}
                  {php.includes(v) ? "" : " (not installed)"}
                </option>
              ))}
            </select>
          </Field>
        </div>
      )}
      {site.node && lines.length > 0 && (
        <div className="ui-narrow">
          <Field label={"Node.js for " + site.name} hint="The old Node.js version is not known. The app stays stopped until you set its start command.">
            <select className="ui-select" value={line} onChange={(e) => setLine(e.target.value)}>
              {lines.map((v) => (
                <option key={v} value={v}>
                  Node.js {v}
                  {node && !installed.includes(v) ? " (not installed)" : ""}
                </option>
              ))}
            </select>
          </Field>
        </div>
      )}
      {site.python && pyLines.length > 0 && (
        <div className="ui-narrow">
          <Field label={"Python for " + site.name} hint="The old Python version is not known. Hostashi makes the environment ~/.venv; install the packages into it after the copy. The app stays stopped until you set its start command.">
            <select className="ui-select" value={pyLine} onChange={(e) => setPyLine(e.target.value)}>
              {pyLines.map((v) => (
                <option key={v} value={v}>
                  Python {v}
                  {python && !installedPy.includes(v) ? " (not installed)" : ""}
                </option>
              ))}
            </select>
          </Field>
        </div>
      )}
      {site.proxy && (
        <p className="ui-flush">
          It passes every request on to <code>{site.proxy}</code>, without a Linux account, files or cron jobs.
        </p>
      )}
      {choosePool && (
        <div className="ui-narrow">
          <Field
            label={"Storage pool for " + site.name}
            hint={site.homeBytes ? `The old home uses ${formatBytes(site.homeBytes)}.` : "Where the site's home lives. A site stays on its pool."}
          >
            <select className="ui-select" value={pool} onChange={(e) => setPool(e.target.value)}>
              {pools.map((p) => (
                <option key={p.name} value={p.name} disabled={!p.available}>
                  {poolLabel(p)}
                </option>
              ))}
            </select>
          </Field>
        </div>
      )}
      {choosePool && tooSmall && <Notice tone="err" title={`Pool ${pool} has less free space than the old home uses. Choose another pool.`} />}
      {choosePool && !tooSmall && poolNote(chosen) && <Notice tone="warn" title={poolNote(chosen)} />}
      {site.pageCache && (
        <label className="ui-check">
          <input type="checkbox" checked={cache} onChange={(e) => setCache(e.target.checked)} />
          Switch on the page cache ({presetLabel[site.pageCache as Preset] ?? site.pageCache} preset)
        </label>
      )}
      {review && (
        <label className="ui-check">
          <input type="checkbox" checked={reviewed} onChange={(e) => setReviewed(e.target.checked)} />I checked the points above for {site.name}
        </label>
      )}
      {error && <Notice tone="err" title={error} />}
      <div className="ui-row">
        <Button type="submit" variant="primary" disabled={busy || (choosePool && tooSmall)}>
          {busy ? "Creating…" : "Create " + site.name}
        </Button>
      </div>
    </form>
  );
}

function ImportForm({ report, site, ready }: { report: string; site: PlanSite; ready: boolean }) {
  const [dbs, setDbs] = useState("");
  const [here, setHere] = useState<string[]>([]);
  const [job, setJob] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");

  useEffect(() => {
    listImports(site.name)
      .then((list) => setJob((cur) => cur || (list[0]?.id ?? "")))
      .catch(() => undefined);
  }, [site.name]);

  useEffect(() => {
    listDatabases()
      .then((all) => setHere(all.filter((d) => d.site === site.name).map((d) => d.name)))
      .catch(() => setHere([]));
  }, [site.name]);

  async function submit(e: FormEvent) {
    e.preventDefault();
    const { maps, problem } = parseDBMap(dbs);
    if (problem) {
      setError(problem);
      return;
    }
    setBusy(true);
    setError("");
    try {
      const j = await startImport(report, site.name, maps);
      setJob(j.id);
    } catch (err) {
      setError(migrationMessage(err));
    } finally {
      setBusy(false);
    }
  }

  return (
    <div className="ui-stack">
      <p className="ui-flush">
        The site exists here. The import copies the old files to <code>cloudpanel-import</code> in its home and leaves the web root as it is.
        Run it again shortly before the DNS switch for the final sync.
      </p>
      <form className="ui-stack" onSubmit={(e) => void submit(e)} noValidate>
        <Field
          label={"Databases to copy for " + site.name}
          hint={
            "One per line: the database on the old server, a space, the database of this site here. " +
            (here.length ? "This site has: " + here.join(", ") + "." : "Create the databases on the Databases page first.")
          }
        >
          <textarea className="ui-input ui-textarea ui-mono" value={dbs} onChange={(e) => setDbs(e.target.value)} rows={2} spellCheck={false} />
        </Field>
        {error && <Notice tone="err" title={error} />}
        <div className="ui-row">
          <Button type="submit" variant="primary" disabled={busy || !ready}>
            {busy ? "Starting…" : job ? "Import " + site.name + " again" : "Import " + site.name}
          </Button>
        </div>
      </form>
      {job && <ImportStatus id={job} />}
    </div>
  );
}

const jobTone: Record<ImportJob["state"], Tone> = { queued: "off", running: "warn", done: "ok", failed: "err" };

function ImportStatus({ id }: { id: string }) {
  const [job, setJob] = useState<ImportJob | null>(null);
  const [error, setError] = useState("");

  const load = useCallback(async () => {
    try {
      setJob(await getImport(id));
      setError("");
    } catch (err) {
      setError(migrationMessage(err));
    }
  }, [id]);

  useEffect(() => {
    setJob(null);
    void load();
  }, [load]);

  const active = job?.state === "queued" || job?.state === "running";
  useEffect(() => {
    if (!active) return;
    const t = setInterval(() => void load(), 3000);
    return () => clearInterval(t);
  }, [active, load]);

  if (!job) return error ? <Notice tone="err" title={error} /> : <p aria-busy="true">Loading the import…</p>;

  const state = { queued: "Waiting to start", running: "Copying…", done: "Done", failed: "Failed" }[job.state] ?? job.state;
  return (
    <section className="ui-stack ui-subsection" aria-label={"Import of " + job.site}>
      <dl className="ui-facts">
        <dt>Import</dt>
        <dd>
          <span className="ui-row">
            <Badge tone={jobTone[job.state] ?? "off"}>{state}</Badge>
            {job.finished && <RelTime iso={job.finished} />}
          </span>
        </dd>
        <dt>Copied</dt>
        <dd>
          {job.files} files, {formatBytes(job.bytes)}
        </dd>
        <dt>Unchanged</dt>
        <dd>{job.unchanged}</dd>
        <dt>Deleted</dt>
        <dd>{job.deleted}</dd>
        <dt>Skipped</dt>
        <dd>{job.skipped}</dd>
        {job.dumps && job.dumps.length > 0 && (
          <>
            <dt>Database dumps</dt>
            <dd>
              {job.dumps.map((d, i) => (
                <div key={d}>
                  <span className="ui-mono ui-break">{d}</span>
                  {job.databases?.[i] && " for " + job.databases[i].to}
                </div>
              ))}
            </dd>
          </>
        )}
      </dl>
      {job.dumps && job.dumps.length > 0 && job.state === "done" && <DumpLoads id={job.id} />}
      {job.error && <Notice tone="err" title={job.error} />}
      {job.skippedPaths && job.skippedPaths.length > 0 && (
        <details>
          <summary>Skipped entries</summary>
          <ul>
            {job.skippedPaths.map((p) => (
              <li key={p}>
                <code>{p}</code>
              </li>
            ))}
          </ul>
        </details>
      )}
      {job.state === "done" && <Activate site={job.site} />}
      {error && <Notice tone="err" title={error} />}
    </section>
  );
}

function loadLabel(l: DumpLoad): { word: string; tone: Tone } {
  if (!l.job) return { word: "Not started", tone: "off" };
  if (l.job.state === "running") return { word: "Loading…", tone: "warn" };
  if (l.job.state === "done") return { word: "Loaded", tone: "ok" };
  return { word: "Failed", tone: "err" };
}

// DumpLoads shows how the copied dumps are loaded into the site's
// databases. The panel starts the loads by itself once the import is done;
// the button starts those again that did not run or failed.
function DumpLoads({ id }: { id: string }) {
  const [loads, setLoads] = useState<DumpLoad[] | null>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");

  const load = useCallback(async () => {
    try {
      setLoads(await listLoads(id));
      setError("");
    } catch (err) {
      setError(migrationMessage(err));
    }
  }, [id]);

  useEffect(() => {
    void load();
  }, [load]);

  const running = loads?.some((l) => l.job?.state === "running") ?? false;
  useEffect(() => {
    if (!running) return;
    const t = setInterval(() => void load(), 3000);
    return () => clearInterval(t);
  }, [running, load]);

  async function retry() {
    setBusy(true);
    setError("");
    try {
      setLoads(await startLoads(id));
    } catch (err) {
      setError(migrationMessage(err));
    } finally {
      setBusy(false);
    }
  }

  const unfinished = loads?.some((l) => !l.job || l.job.state === "failed") ?? false;

  return (
    <div className="ui-stack-tight" aria-label="Database loads" role="group">
      <strong>Database loads</strong>
      {loads === null ? (
        !error && <p aria-busy="true">Loading the database loads…</p>
      ) : loads.length === 0 ? (
        <p className="ui-hint ui-flush">The dumps are loaded into the databases shortly.</p>
      ) : (
        <dl className="ui-facts">
          {loads.map((l) => {
            const s = loadLabel(l);
            const why = l.job?.error ?? l.error;
            return (
              <Fragment key={l.database + l.path}>
                <dt>{l.database}</dt>
                <dd>
                  <span className="ui-row">
                    <Badge tone={s.tone}>{s.word}</Badge>
                    {l.job?.state === "done" && l.job.finished && <RelTime iso={l.job.finished} />}
                  </span>
                  {why && <p className="ui-hint ui-flush">{why}</p>}
                </dd>
              </Fragment>
            );
          })}
        </dl>
      )}
      {error && <Notice tone="err" title={error} />}
      {(unfinished || loads?.length === 0) && (
        <div className="ui-row">
          <Button disabled={busy} onClick={() => void retry()}>
            {busy ? "Starting…" : "Load the dumps again"}
          </Button>
        </div>
      )}
    </div>
  );
}

// Activate swaps the imported files in as the web root, after the final
// sync and before the DNS switch. The old web root is kept.
function Activate({ site }: { site: string }) {
  const [confirm, setConfirm] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [done, setDone] = useState("");

  async function activate() {
    setBusy(true);
    setError("");
    try {
      const r = await activateImport(site);
      setDone(`The imported files are the web root of ${r.site} now. The old web root is kept as ${r.previous} in its home.`);
      setConfirm(false);
    } catch (err) {
      setError(migrationMessage(err));
    } finally {
      setBusy(false);
    }
  }

  if (done) return <Notice tone="ok" title={done} />;

  return (
    <div className="ui-stack-tight">
      {confirm ? (
        <div className="ui-stack-tight" role="alertdialog" aria-label={"Use imported files for " + site}>
          <p className="ui-flush">
            Make the imported files the web root of {site}? Do this after the final sync, before the DNS switch. The current web root is kept and
            nothing is deleted.
          </p>
          <div className="ui-row">
            <Button variant="primary" disabled={busy} onClick={() => void activate()}>
              Use imported files for {site}
            </Button>
            <Button disabled={busy} onClick={() => setConfirm(false)}>
              Cancel
            </Button>
          </div>
        </div>
      ) : (
        <div className="ui-row">
          <Button onClick={() => setConfirm(true)}>Use imported files as web root</Button>
        </div>
      )}
      {error && <Notice tone="err" title={error} />}
    </div>
  );
}

const draftActionLabel: Record<DraftItem["action"], string> = { kept: "Kept", rewritten: "Kept, paths changed", dropped: "Left out" };

// SnippetDraftForm reviews the custom nginx directives of an imported site
// once (ADR-0017): the draft drops what Hostashi does itself, points paths
// at this server, and the person edits and saves it as the site's snippets.
function SnippetDraftForm({ report, site }: { report: string; site: PlanSite }) {
  const [draft, setDraft] = useState<SnippetDraft | null>(null);
  const [server, setServer] = useState("");
  const [root, setRoot] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [done, setDone] = useState("");
  const [problems, setProblems] = useState<SnippetProblems | null>(null);

  async function load() {
    setBusy(true);
    setError("");
    try {
      const d = await snippetDraft(report, site.name);
      setDraft(d);
      setServer(d.server);
      setRoot(d.root);
    } catch (err) {
      setError(snippetMessage(err));
    } finally {
      setBusy(false);
    }
  }

  async function save(e: FormEvent) {
    e.preventDefault();
    const problem = sizeProblem({ server, root });
    if (problem) {
      setError(problem);
      return;
    }
    setBusy(true);
    setError("");
    setDone("");
    setProblems(null);
    try {
      await setSnippets(site.name, { server, root });
      setDone("Snippets saved and active on " + site.name + ".");
    } catch (err) {
      setProblems(snippetProblems(err));
      setError(snippetMessage(err));
    } finally {
      setBusy(false);
    }
  }

  if (!draft) {
    return (
      <div className="ui-stack">
        <p className="ui-flush">
          The old site has {site.customSnippets} custom nginx directives. Review them once: the draft leaves out what Hostashi does itself and
          points paths at this server.
        </p>
        <div className="ui-row">
          <Button disabled={busy} onClick={() => void load()}>
            Review custom nginx
          </Button>
        </div>
        {error && <Notice tone="err" title={error} />}
      </div>
    );
  }

  return (
    <form className="ui-stack" onSubmit={(e) => void save(e)} noValidate aria-label={"Custom nginx of " + site.name}>
      <ul className="ui-plain-list ui-stack-tight" aria-label={"Directives of " + site.name}>
        {draft.items.map((it, i) => (
          <li key={String(i)}>
            <code>{it.context}</code> (line {it.line}): {draftActionLabel[it.action]}
            {it.reason ? ". " + it.reason.charAt(0).toUpperCase() + it.reason.slice(1) + "." : ""}
          </li>
        ))}
      </ul>
      <SnippetField label="Inside the server block" hint="Check every line once before you save." value={server} onChange={setServer} problems={problems?.server} />
      <SnippetField label="Inside location /" hint="From the old site's location /, without its try_files." value={root} onChange={setRoot} problems={problems?.root} />
      {error && <Notice tone="err" title={error} />}
      {done && <Notice tone="ok" title={done} />}
      <div className="ui-row">
        <Button type="submit" variant="primary" disabled={busy}>
          Save as snippets of {site.name}
        </Button>
      </div>
    </form>
  );
}
