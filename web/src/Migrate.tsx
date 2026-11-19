// SPDX-License-Identifier: AGPL-3.0-or-later

import { useCallback, useEffect, useRef, useState, type FormEvent } from "react";
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
  type ImportJob,
  type PlanSite,
  type Report,
  type SourceInfo,
} from "./cloudpanel";
import { listDatabases } from "./databases";
import { phpVersions } from "./sites";

const scanCommand = "hostashi cloudpanel scan -json -full > hostashi-scan.json";
const fingerprintCommand = "for f in /etc/ssh/ssh_host_*_key.pub; do ssh-keygen -lf \"$f\"; done";

// Migrate moves sites from a CloudPanel server to this one: connect the old
// server, upload its scan report, create the sites the plan proposes and
// copy their files and databases (ADR-0026).
export function Migrate() {
  const [source, setInfo] = useState<SourceInfo | null>(null);
  const [error, setError] = useState("");

  useEffect(() => {
    getSource()
      .then(setInfo)
      .catch((err: unknown) => setError(migrationMessage(err)));
  }, []);

  if (!source) {
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
      <SourceCard info={source} onChange={setInfo} />
      {source.source && <HostKeyCard info={source} onChange={setInfo} />}
      {source.source && <DumpUserCard info={source} onChange={setInfo} />}
      <ReportsCard ready={source.ready} />
    </div>
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
    <>
      <pre aria-label={label}>{text}</pre>
      <button type="button" className="secondary" onClick={() => void copyText(text).then(setCopied)}>
        Copy
      </button>
      {copied && <p role="status">{copied}</p>}
    </>
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

  return (
    <section className="card" aria-labelledby="mig-source">
      <h2 id="mig-source">Old server</h2>
      <p className="muted">
        Hostashi reads the sites over SSH from the CloudPanel server. It only reads there and changes nothing.
        {info.source && (info.ready ? " The connection is ready." : " The connection is not ready yet: finish the steps below.")}
      </p>
      <form onSubmit={(e) => void save(e)} noValidate>
        <label>
          Address
          <small>Prefer the IP address: DNS names move during the migration.</small>
          <input value={host} onChange={(e) => setHost(e.target.value)} autoComplete="off" spellCheck={false} />
        </label>
        <label>
          SSH port
          <input value={port} onChange={(e) => setPort(e.target.value)} inputMode="numeric" />
        </label>
        {error && <p role="alert">{error}</p>}
        {saved && <p role="status">Saved.</p>}
        <button type="submit" disabled={busy}>
          {info.source ? "Save" : "Connect old server"}
        </button>
      </form>
      {info.authorizedKeysLine && (
        <div className="keybox">
          <p>
            On the old server, add this line to <code>/root/.ssh/authorized_keys</code>. It only allows reading files over SFTP and a
            tunnel to its database.
          </p>
          <CopyBlock label="authorized_keys line" text={info.authorizedKeysLine} />
        </div>
      )}
      {info.source &&
        (confirmRemove ? (
          <div className="keybox">
            <p>Forget the old server? Its key and host key are deleted here. Remove the line from authorized_keys there as well.</p>
            <div className="actions">
              <button type="button" className="danger" disabled={busy} onClick={() => void remove()}>
                Forget old server
              </button>
              <button type="button" className="secondary" disabled={busy} onClick={() => setConfirmRemove(false)}>
                Cancel
              </button>
            </div>
          </div>
        ) : (
          <div className="keybox">
            <button type="button" className="secondary danger" onClick={() => setConfirmRemove(true)}>
              Forget old server
            </button>
          </div>
        ))}
    </section>
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
    <section className="card" aria-labelledby="mig-hostkey">
      <h2 id="mig-hostkey">Host key of the old server</h2>
      {info.hostKey ? (
        <p>
          Trusted: <code>{info.hostKey}</code>
        </p>
      ) : (
        <p>The old server&apos;s SSH host key is not confirmed yet. Nothing is copied until it is.</p>
      )}
      {scanned && (
        <>
          <p>
            The old server presents <code>{scanned.fingerprint}</code>. On the old server, run this and trust the key only if one line shows
            the same fingerprint:
          </p>
          <pre aria-label="Fingerprint command">{fingerprintCommand}</pre>
          <button
            type="button"
            disabled={busy}
            onClick={() =>
              void run(async () => {
                onChange(await trustSourceHostKey(scanned.key));
                setScanned(null);
              })
            }
          >
            Trust this key
          </button>
        </>
      )}
      {error && <p role="alert">{error}</p>}
      <button type="button" className="secondary" disabled={busy} onClick={() => void run(async () => setScanned(await scanSourceHostKey()))}>
        {info.hostKey ? "Check the host key again" : "Fetch the host key"}
      </button>
    </section>
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
      <section className="card notice" aria-labelledby="mig-dbsql">
        <h2 id="mig-dbsql" ref={heading} tabIndex={-1}>
          Run this SQL on the old server now
        </h2>
        <p>
          Run it as the MariaDB administrator there, for example in <code>mysql</code> as root. It creates the user <code>{creds.user}</code>{" "}
          that may only read. Hostashi shows it only this once.
        </p>
        <CopyBlock label="SQL for the old server" text={creds.sql} />
        <button type="button" onClick={() => setCreds(null)}>
          I have run it
        </button>
      </section>
    );
  }

  return (
    <section className="card" aria-labelledby="mig-dbuser">
      <h2 id="mig-dbuser">Database access</h2>
      {info.dbUser ? (
        <p>
          Databases are read as <code>{info.dbUser}</code> on the old server.
        </p>
      ) : (
        <p>To copy databases, Hostashi needs a read-only database user on the old server. Files are copied without it.</p>
      )}
      {error && <p role="alert">{error}</p>}
      {info.dbUser && confirmNew ? (
        <div>
          <p>A new password replaces the old one. Run the new SQL on the old server, or database copies fail.</p>
          <div className="actions">
            <button type="button" disabled={busy} onClick={() => void generate()}>
              Make a new password
            </button>
            <button type="button" className="secondary" disabled={busy} onClick={() => setConfirmNew(false)}>
              Cancel
            </button>
          </div>
        </div>
      ) : (
        <button
          type="button"
          className={info.dbUser ? "secondary" : undefined}
          disabled={busy}
          onClick={() => (info.dbUser ? setConfirmNew(true) : void generate())}
        >
          {info.dbUser ? "Make a new password" : "Create the database user"}
        </button>
      )}
    </section>
  );
}

function ReportsCard({ ready }: { ready: boolean }) {
  const [reports, setReports] = useState<Report[] | null>(null);
  const [open, setOpen] = useState("");
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
      await load();
      setOpen(r.id);
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
      if (open === id) setOpen("");
      setConfirmDelete("");
      await load();
    } catch (err) {
      setError(migrationMessage(err));
    } finally {
      setBusy(false);
    }
  }

  return (
    <>
      <section className="card" aria-labelledby="mig-reports">
        <h2 id="mig-reports">Scan reports</h2>
        <p>Run the scan on the old server as root. It only reads the CloudPanel configuration.</p>
        <pre aria-label="Scan command">{scanCommand}</pre>
        <form onSubmit={(e) => void upload(e)} noValidate>
          <label>
            Report file
            <input ref={file} type="file" accept="application/json,.json" />
          </label>
          <button type="submit" disabled={busy}>
            {busy ? "Uploading…" : "Upload report"}
          </button>
        </form>
        {error && <p role="alert">{error}</p>}
        {reports === null ? (
          <p aria-busy="true">Loading…</p>
        ) : reports.length === 0 ? (
          <p className="muted">No report is uploaded.</p>
        ) : (
          <ul className="rows">
            {reports.map((r) => (
              <li key={r.id}>
                <span>
                  <strong>{new Date(r.createdAt).toLocaleString()}</strong>
                  <span className="muted">
                    {" "}
                    · {r.sites} {r.sites === 1 ? "site" : "sites"}
                  </span>
                </span>
                <span className="actions">
                  <button type="button" className="secondary" disabled={open === r.id} onClick={() => setOpen(r.id)}>
                    Show plan
                  </button>
                  <button type="button" className="secondary danger" onClick={() => setConfirmDelete(r.id)}>
                    Delete
                  </button>
                </span>
                {confirmDelete === r.id && (
                  <div>
                    <p>Delete this report? Sites created from it stay.</p>
                    <div className="actions">
                      <button type="button" className="danger" disabled={busy} onClick={() => void remove(r.id)}>
                        Delete report
                      </button>
                      <button type="button" className="secondary" disabled={busy} onClick={() => setConfirmDelete("")}>
                        Cancel
                      </button>
                    </div>
                  </div>
                )}
              </li>
            ))}
          </ul>
        )}
      </section>
      {open && <PlanCard key={open} report={open} ready={ready} />}
    </>
  );
}

function PlanCard({ report, ready }: { report: string; ready: boolean }) {
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

  if (!rep?.plan) {
    return error ? (
      <p role="alert" className="card">
        {error}
      </p>
    ) : (
      <p aria-busy="true">Loading…</p>
    );
  }
  const s = rep.plan.summary;

  return (
    <section className="card" aria-labelledby="mig-plan">
      <h2 id="mig-plan">Plan</h2>
      <p>
        {s.sites} sites: {s.import} ready, {s.review} to review, {s.skip} skipped. The plan is worked out again on every view, so sites
        created here show as existing.
      </p>
      {!ready && <p className="muted">Imports can start once the old server and its host key are set up.</p>}
      {error && <p role="alert">{error}</p>}
      <ul className="rows">
        {rep.plan.sites.map((site, i) => (
          <PlanRow key={site.source + "/" + String(i)} report={report} site={site} php={php} ready={ready} onChange={load} />
        ))}
      </ul>
    </section>
  );
}

const tlsLabel: Record<PlanSite["tls"], string> = {
  none: "None on the old server",
  reissue: "New certificate after the DNS switch",
  copy: "Purchased certificate, copied",
};

function actionLabel(s: PlanSite): string {
  if (existsHere(s)) return "Created";
  return s.action === "import" ? "Ready" : s.action === "review" ? "Review" : "Skipped";
}

function PlanRow({
  report,
  site,
  php,
  ready,
  onChange,
}: {
  report: string;
  site: PlanSite;
  php: string[];
  ready: boolean;
  onChange: () => Promise<void>;
}) {
  const created = existsHere(site);
  const label = actionLabel(site);
  const title = site.name || site.source;

  return (
    <li>
      <span>
        <strong>{title}</strong>
        <span className={"badge" + (label === "Skipped" ? "" : label === "Review" ? " warn" : " ok")}>{label}</span>
      </span>
      <dl className="facts" aria-label={"Plan for " + title}>
        <dt>Domains</dt>
        <dd>{site.domains.length ? site.domains.join(", ") : "None"}</dd>
        {site.redirectFrom.length > 0 && (
          <>
            <dt>Redirects</dt>
            <dd>{site.redirectFrom.join(", ")}</dd>
          </>
        )}
        <dt>Type</dt>
        <dd>{site.type}</dd>
        {site.php && (
          <>
            <dt>PHP</dt>
            <dd>{site.php}</dd>
          </>
        )}
        <dt>HTTPS</dt>
        <dd>{tlsLabel[site.tls] ?? site.tls}</dd>
        <dt>Old files</dt>
        <dd>
          <code>{site.docRoot}</code>
        </dd>
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
      {site.reasons.length > 0 && !created && (
        <ul aria-label={"Reasons for " + title}>
          {site.reasons.map((r) => (
            <li key={r.code + r.msg}>{r.msg.charAt(0).toUpperCase() + r.msg.slice(1)}.</li>
          ))}
        </ul>
      )}
      {(site.action === "import" || site.action === "review") && <CreateForm report={report} site={site} php={php} onChange={onChange} />}
      {created && site.cronImport > 0 && (
        <p className="muted">
          {site.cronImport} cron {site.cronImport === 1 ? "job was created paused. Start it" : "jobs were created paused. Start them"} after the DNS switch.
        </p>
      )}
      {created && <ImportForm report={report} site={site} ready={ready} />}
      {created && site.customSnippets > 0 && <SnippetDraftForm report={report} site={site} />}
    </li>
  );
}

function CreateForm({ report, site, php, onChange }: { report: string; site: PlanSite; php: string[]; onChange: () => Promise<void> }) {
  const [reviewed, setReviewed] = useState(false);
  const [version, setVersion] = useState(site.php);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const review = site.action === "review";
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
      await createFromPlan(report, site.name, reviewed, version !== site.php ? version : "");
      await onChange();
    } catch (err) {
      setError(migrationMessage(err));
    } finally {
      setBusy(false);
    }
  }

  return (
    <form onSubmit={(e) => void submit(e)} noValidate>
      {site.php && choices.length > 0 && (
        <label>
          PHP version for {site.name}
          <select value={version} onChange={(e) => setVersion(e.target.value)}>
            {choices.map((v) => (
              <option key={v} value={v}>
                {v}
                {php.includes(v) ? "" : " (not installed)"}
              </option>
            ))}
          </select>
        </label>
      )}
      {review && (
        <label className="check">
          <input type="checkbox" checked={reviewed} onChange={(e) => setReviewed(e.target.checked)} />I checked the points above for{" "}
          {site.name}
        </label>
      )}
      <p className="muted">The site gets the old domains without a certificate. HTTPS follows after the DNS switch.</p>
      {error && <p role="alert">{error}</p>}
      <button type="submit" disabled={busy}>
        {busy ? "Creating…" : "Create " + site.name}
      </button>
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
    <div>
      <p className="muted">
        The site exists here. The import copies the old files to <code>cloudpanel-import</code> in its home and leaves the web root as it is.
        Run it again shortly before the DNS switch for the final sync.
      </p>
      <form onSubmit={(e) => void submit(e)} noValidate>
        <label>
          Databases to copy for {site.name}
          <small>
            One per line: the database on the old server, a space, the database of this site here.{" "}
            {here.length ? "This site has: " + here.join(", ") + "." : "Create the databases on the Databases page first."}
          </small>
          <textarea value={dbs} onChange={(e) => setDbs(e.target.value)} rows={2} spellCheck={false} className="mono" />
        </label>
        {error && <p role="alert">{error}</p>}
        <button type="submit" disabled={busy || !ready}>
          {busy ? "Starting…" : job ? "Import " + site.name + " again" : "Import " + site.name}
        </button>
      </form>
      {job && <ImportStatus id={job} />}
    </div>
  );
}

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

  if (!job) return error ? <p role="alert">{error}</p> : <p aria-busy="true">Loading the import…</p>;

  const state = { queued: "Waiting to start", running: "Copying…", done: "Done", failed: "Failed" }[job.state] ?? job.state;
  return (
    <section aria-label={"Import of " + job.site}>
      <dl className="facts">
        <dt>Import</dt>
        <dd>
          {state}
          {job.finished && " at " + new Date(job.finished).toLocaleString()}
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
                  <code>{d}</code>
                  {job.databases?.[i] && " for " + job.databases[i].to}
                </div>
              ))}
            </dd>
          </>
        )}
      </dl>
      {job.dumps && job.dumps.length > 0 && job.state === "done" && (
        <p className="muted">Load each dump on the Databases page with Import and the path shown.</p>
      )}
      {job.error && <p role="alert">{job.error}</p>}
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
      {error && <p role="alert">{error}</p>}
    </section>
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

  if (done) return <p role="status">{done}</p>;

  return (
    <div className="keybox">
      {confirm ? (
        <>
          <p>
            Make the imported files the web root of {site}? Do this after the final sync, before the DNS switch. The current web root is kept
            and nothing is deleted.
          </p>
          <div className="actions">
            <button type="button" disabled={busy} onClick={() => void activate()}>
              Use imported files for {site}
            </button>
            <button type="button" className="secondary" disabled={busy} onClick={() => setConfirm(false)}>
              Cancel
            </button>
          </div>
        </>
      ) : (
        <button type="button" className="secondary" onClick={() => setConfirm(true)}>
          Use imported files as web root
        </button>
      )}
      {error && <p role="alert">{error}</p>}
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
      <div className="actions">
        <button type="button" className="secondary" disabled={busy} onClick={() => void load()}>
          Review custom nginx
        </button>
        {error && <p role="alert">{error}</p>}
      </div>
    );
  }

  return (
    <form onSubmit={(e) => void save(e)} noValidate aria-label={"Custom nginx of " + site.name}>
      <ul aria-label={"Directives of " + site.name}>
        {draft.items.map((it, i) => (
          <li key={String(i)}>
            <code>{it.context}</code> (line {it.line}): {draftActionLabel[it.action]}
            {it.reason ? ". " + it.reason.charAt(0).toUpperCase() + it.reason.slice(1) + "." : ""}
          </li>
        ))}
      </ul>
      <SnippetField label="Inside the server block" hint="Check every line once before you save." value={server} onChange={setServer} problems={problems?.server} />
      <SnippetField label="Inside location /" hint="From the old site's location /, without its try_files." value={root} onChange={setRoot} problems={problems?.root} />
      <div className="actions">
        <button type="submit" disabled={busy}>
          Save as snippets of {site.name}
        </button>
      </div>
      {error && <p role="alert">{error}</p>}
      {done && <p role="status">{done}</p>}
    </form>
  );
}
