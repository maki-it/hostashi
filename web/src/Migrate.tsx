// SPDX-License-Identifier: AGPL-3.0-or-later

import { ArrowLeft, Copy, ListChecks, Trash, UploadSimple } from "@phosphor-icons/react";
import { Fragment, useCallback, useEffect, useRef, useState, type FormEvent, type ReactNode } from "react";
import { Trans, useTranslation } from "react-i18next";
import i18n from "./i18n";
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
import { formatBytes, formatCount } from "./format";
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

// typeLabel names the scanned site types; an unknown type keeps its name.
const siteTypes = ["static", "php", "wordpress", "nodejs", "python", "proxy"] as const;
const typeLabel = (type: string): string =>
  (siteTypes as readonly string[]).includes(type) ? i18n.t(`migrate.types.${type as (typeof siteTypes)[number]}`) : type;

const importHash = (q?: Record<string, string>) => formatHash({ page: "import", ...(q ? { query: new URLSearchParams(q) } : {}) });
const siteKey = (s: PlanSite) => s.name || s.source;

// Migrate moves sites from a CloudPanel server to this one: connect the old
// server, upload its scan report, create the sites the plan proposes and
// copy their files and databases (ADR-0026). The setup and the reports are
// one view; a plan (?report=) and one site of it (&site=) are their own.
export function Migrate({ query = new URLSearchParams(), onQuery = () => undefined }: QueryProps) {
  const [source, setInfo] = useState<SourceInfo | null>(null);
  const [error, setError] = useState("");
  const { t } = useTranslation();

  useEffect(() => {
    getSource()
      .then(setInfo)
      .catch((err: unknown) => setError(migrationMessage(err)));
  }, []);

  const head = (
    <PageHead
      title={t("migrate.title")}
      description={t("migrate.description")}
    />
  );

  if (!source) {
    return (
      <>
        {head}
        {error ? <Notice tone="err" title={error} /> : <p aria-busy="true">{t("common.status.loading")}</p>}
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
    return i18n.t("common.status.copied");
  } catch {
    return i18n.t("migrate.copyFailed");
  }
}

function CopyBlock({ label, text }: { label: string; text: string }) {
  const [copied, setCopied] = useState("");
  const { t } = useTranslation();
  return (
    <div className="ui-stack-tight">
      <pre className="ui-code" aria-label={label}>
        {text}
      </pre>
      <div className="ui-row">
        <Button icon={<Copy size={16} aria-hidden="true" />} onClick={() => void copyText(text).then(setCopied)}>
          {t("migrate.copy")}
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
  const { t } = useTranslation();

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

  const state = !info.source ? null : info.ready ? <Badge tone="ok">{t("migrate.source.ready")}</Badge> : <Badge tone="warn">{t("migrate.source.notReady")}</Badge>;

  return (
    <Card title={t("migrate.source.title")} {...(state ? { actions: state } : {})}>
      <div className="ui-stack">
        <p className="ui-flush">
          {t("migrate.source.intro")}
          {info.source && (info.ready ? t("migrate.source.introReady") : t("migrate.source.introNotReady"))}
        </p>
        <form className="ui-stack" onSubmit={(e) => void save(e)} noValidate>
          <div className="ui-host-port">
            <Field label={t("migrate.source.address")} hint={t("migrate.source.addressHint")}>
              <input className="ui-input ui-mono" value={host} onChange={(e) => setHost(e.target.value)} autoComplete="off" spellCheck={false} />
            </Field>
            <Field label={t("migrate.source.port")}>
              <input className="ui-input" value={port} onChange={(e) => setPort(e.target.value)} inputMode="numeric" />
            </Field>
          </div>
          {error && <Notice tone="err" title={error} />}
          {saved && <Notice tone="ok" title={t("migrate.source.saved")} />}
          <div className="ui-row">
            <Button type="submit" variant="primary" disabled={busy}>
              {info.source ? t("common.actions.save") : t("migrate.source.connect")}
            </Button>
          </div>
        </form>
        {info.authorizedKeysLine && (
          <div className="ui-stack-tight">
            <p className="ui-flush">
              <Trans i18nKey="migrate.source.authorizedKeys" components={{ code: <code /> }} />
            </p>
            <CopyBlock label={t("migrate.source.authorizedKeysLabel")} text={info.authorizedKeysLine} />
          </div>
        )}
        {info.source &&
          (confirmRemove ? (
            <div className="ui-stack-tight" role="alertdialog" aria-label={t("migrate.source.forget")}>
              <p className="ui-flush">{t("migrate.source.forgetText")}</p>
              <div className="ui-row">
                <Button variant="danger" disabled={busy} onClick={() => void remove()}>
                  {t("migrate.source.forget")}
                </Button>
                <Button disabled={busy} onClick={() => setConfirmRemove(false)}>
                  {t("common.actions.cancel")}
                </Button>
              </div>
            </div>
          ) : (
            <div className="ui-row">
              <Button variant="danger" onClick={() => setConfirmRemove(true)}>
                {t("migrate.source.forget")}
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
  const { t } = useTranslation();

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
    <Card
      title={t("migrate.hostKey.title")}
      actions={info.hostKey ? <Badge tone="ok">{t("migrate.hostKey.trusted")}</Badge> : <Badge tone="warn">{t("migrate.hostKey.notConfirmed")}</Badge>}
    >
      <div className="ui-stack">
        {info.hostKey ? (
          <p className="ui-flush ui-break">
            <Trans i18nKey="migrate.hostKey.trustedKey" values={{ key: info.hostKey }} components={{ code: <code /> }} />
          </p>
        ) : (
          <p className="ui-flush">{t("migrate.hostKey.missing")}</p>
        )}
        {scanned && (
          <div className="ui-stack-tight">
            <p className="ui-flush">
              <Trans i18nKey="migrate.hostKey.presents" values={{ fingerprint: scanned.fingerprint }} components={{ code: <code /> }} />
            </p>
            <pre className="ui-code" aria-label={t("migrate.hostKey.commandLabel")}>
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
                {t("migrate.hostKey.trust")}
              </Button>
            </div>
          </div>
        )}
        {error && <Notice tone="err" title={error} />}
        <div className="ui-row">
          <Button disabled={busy} onClick={() => void run(async () => setScanned(await scanSourceHostKey()))}>
            {info.hostKey ? t("migrate.hostKey.checkAgain") : t("migrate.hostKey.fetch")}
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
  const { t } = useTranslation();

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
          {t("migrate.db.sqlTitle")}
        </h2>
        <p className="ui-flush">
          <Trans i18nKey="migrate.db.sqlText" values={{ user: creds.user }} components={{ code1: <code />, code2: <code /> }} />
        </p>
        <CopyBlock label={t("migrate.db.sqlLabel")} text={creds.sql} />
        <div className="ui-row">
          <Button variant="primary" onClick={() => setCreds(null)}>
            {t("migrate.db.sqlDone")}
          </Button>
        </div>
      </section>
    );
  }

  return (
    <Card title={t("migrate.db.title")}>
      <div className="ui-stack">
        {info.dbUser ? (
          <p className="ui-flush">
            <Trans i18nKey="migrate.db.readAs" values={{ user: info.dbUser }} components={{ code: <code /> }} />
          </p>
        ) : (
          <p className="ui-flush">{t("migrate.db.needUser")}</p>
        )}
        {error && <Notice tone="err" title={error} />}
        {info.dbUser && confirmNew ? (
          <div className="ui-stack-tight">
            <p className="ui-flush">{t("migrate.db.newPasswordText")}</p>
            <div className="ui-row">
              <Button variant="primary" disabled={busy} onClick={() => void generate()}>
                {t("migrate.db.newPassword")}
              </Button>
              <Button disabled={busy} onClick={() => setConfirmNew(false)}>
                {t("common.actions.cancel")}
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
              {info.dbUser ? t("migrate.db.newPassword") : t("migrate.db.createUser")}
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
  const { t } = useTranslation();

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
      setError(t("migrate.reports.chooseFile"));
      return;
    }
    if (f.size > 16 << 20) {
      setError(t("migrate.errors.too_large"));
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
    { key: "created", header: t("migrate.reports.uploaded"), sortValue: (r) => r.createdAt, cell: (r) => <RelTime iso={r.createdAt} /> },
    { key: "sites", header: t("migrate.reports.sites"), sortValue: (r) => r.sites, cell: (r) => r.sites },
    {
      key: "actions",
      header: t("migrate.reports.actions"),
      srHeader: true,
      cell: (r) => (
        <RowActions>
          <IconLink label={t("migrate.reports.showPlan")} text={t("migrate.reports.showPlan")} icon={<ListChecks size={16} />} href={importHash({ report: r.id })} />
          <IconButton label={t("migrate.reports.deleteReport")} text={t("migrate.reports.delete")} icon={<Trash size={16} />} danger disabled={busy} onClick={() => setConfirmDelete(r.id)} />
        </RowActions>
      ),
    },
  ];

  return (
    <Card title={t("migrate.reports.title")} padded={false}>
      <div className="ui-card-body ui-stack">
        <p className="ui-flush">{t("migrate.reports.intro")}</p>
        <pre className="ui-code" aria-label={t("migrate.reports.commandLabel")}>
          {scanCommand}
        </pre>
        <form className="ui-stack" onSubmit={(e) => void upload(e)} noValidate>
          <Field label={t("migrate.reports.file")}>
            <input className="ui-file" ref={file} type="file" accept="application/json,.json" />
          </Field>
          {error && <Notice tone="err" title={error} />}
          <div className="ui-row">
            <Button type="submit" variant="primary" icon={<UploadSimple size={16} aria-hidden="true" />} disabled={busy}>
              {busy ? t("migrate.reports.uploading") : t("migrate.reports.upload")}
            </Button>
          </div>
        </form>
        {confirmDelete && (
          <div className="ui-stack-tight" role="alertdialog" aria-label={t("migrate.reports.deleteReport")}>
            <p className="ui-flush">{t("migrate.reports.deleteText")}</p>
            <div className="ui-row">
              <Button variant="danger" disabled={busy} onClick={() => void remove(confirmDelete)}>
                {t("migrate.reports.deleteReport")}
              </Button>
              <Button disabled={busy} onClick={() => setConfirmDelete("")}>
                {t("common.actions.cancel")}
              </Button>
            </div>
          </div>
        )}
      </div>
      {reports === null ? (
        <p className="ui-card-body" aria-busy="true">
          {t("common.status.loading")}
        </p>
      ) : (
        <Table
          label={t("migrate.reports.title")}
          columns={columns}
          rows={reports}
          rowKey={(r) => r.id}
          sort={{ key: "created", dir: "desc" }}
          empty={t("migrate.reports.empty")}
        />
      )}
    </Card>
  );
}

const tlsLabel = (tls: PlanSite["tls"]): string => (["none", "reissue", "copy"].includes(tls) ? i18n.t(`migrate.tls.${tls}`) : tls);

type Status = { word: string; tone: Tone; rank: number; value: string };

function statusOf(s: PlanSite): Status {
  if (existsHere(s)) return { word: i18n.t("migrate.status.created"), tone: "ok", rank: 2, value: "created" };
  if (s.action === "import") return { word: i18n.t("migrate.status.ready"), tone: "ok", rank: 1, value: "import" };
  if (s.action === "review") return { word: i18n.t("migrate.status.review"), tone: "warn", rank: 0, value: "review" };
  return { word: i18n.t("migrate.status.skipped"), tone: "off", rank: 3, value: "skip" };
}

const sentence = (m: string) => m.charAt(0).toUpperCase() + m.slice(1) + ".";

type PlanProps = { report: string; ready: boolean; query: URLSearchParams; onQuery: (q: URLSearchParams) => void };

// PlanView shows what the scan report proposes for each site, and one site
// of it with the steps to move it.
function PlanView({ report, ready, query, onQuery }: PlanProps) {
  const [rep, setRep] = useState<Report | null>(null);
  const [php, setPHP] = useState<string[]>([]);
  const [error, setError] = useState("");
  const { t } = useTranslation();

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
      {t("migrate.plan.back")}
    </a>
  );

  if (!rep?.plan) {
    return (
      <>
        <PageHead title={t("migrate.plan.title")} actions={back} />
        {error ? <Notice tone="err" title={error} /> : <p aria-busy="true">{t("common.status.loading")}</p>}
      </>
    );
  }

  const name = query.get("site");
  if (name !== null) {
    const site = rep.plan.sites.find((s) => siteKey(s) === name);
    const planLink = (
      <a className="ui-btn" href={importHash({ report })}>
        <ArrowLeft size={16} aria-hidden="true" />
        {t("migrate.plan.title")}
      </a>
    );
    if (!site) {
      return (
        <>
          <PageHead title={t("migrate.plan.title")} actions={planLink} />
          <Notice tone="warn" title={t("migrate.plan.noSite", { name })} />
        </>
      );
    }
    return <PlanSiteView report={report} site={site} php={php} ready={ready} back={planLink} onChange={load} />;
  }

  const s = rep.plan.summary;
  const columns: Column<PlanSite>[] = [
    {
      key: "site",
      header: t("migrate.plan.site"),
      sortValue: siteKey,
      cell: (p) => (
        <div className="ui-cell-main">
          <a href={importHash({ report, site: siteKey(p) })}>{p.name || p.source}</a>
          <span>{p.domains.length ? p.domains.join(", ") : t("migrate.plan.noDomain")}</span>
        </div>
      ),
    },
    { key: "type", header: t("migrate.plan.type"), sortValue: (p) => p.type, cell: (p) => typeLabel(p.type) },
    { key: "php", header: t("migrate.plan.php"), sortValue: (p) => p.php, cell: (p) => p.php || <span className="ui-hint">{t("migrate.plan.none")}</span> },
    {
      key: "status",
      header: t("migrate.plan.status"),
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
        title={t("migrate.plan.title")}
        description={<Trans i18nKey="migrate.plan.from" components={{ time: <RelTime iso={rep.createdAt} /> }} />}
        actions={back}
      />
      <Notice tone="info" title={t("migrate.plan.summary", { sites: s.sites, ready: s.import, review: s.review, skip: s.skip })}>
        {!ready ? t("migrate.plan.notReady") : t("migrate.plan.open")}
      </Notice>
      {error && <Notice tone="err" title={error} />}
      <Card padded={false}>
        <div className="ui-filters">
          <input
            className="ui-input"
            type="search"
            aria-label={t("migrate.plan.filter")}
            placeholder={t("migrate.plan.filterPlaceholder")}
            value={query.get("q") ?? ""}
            onChange={(e) => set({ q: e.target.value })}
          />
          <select className="ui-select" aria-label={t("migrate.plan.status")} value={status} onChange={(e) => set({ status: e.target.value })}>
            <option value="">{t("migrate.plan.allSites")}</option>
            <option value="review">{t("migrate.status.review")}</option>
            <option value="import">{t("migrate.status.ready")}</option>
            <option value="created">{t("migrate.status.created")}</option>
            <option value="skip">{t("migrate.status.skipped")}</option>
          </select>
        </div>
        <Table
          label={t("migrate.plan.title")}
          columns={columns}
          rows={shown}
          rowKey={(p) => siteKey(p)}
          sort={sort}
          onSort={(x) => set({ sort: x.key, dir: x.dir })}
          empty={t("migrate.plan.empty")}
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
  const { t } = useTranslation();

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
        <Notice tone={site.action === "skip" ? "info" : "warn"} title={site.action === "review" ? t("migrate.site.checkFirst") : t("migrate.site.why")}>
          <ul className="ui-plain-list" aria-label={t("migrate.site.reasons", { name: title })}>
            {site.reasons.map((r) => (
              <li key={r.code + r.msg}>{sentence(r.msg)}</li>
            ))}
          </ul>
        </Notice>
      )}
      <Card title={t("migrate.site.oldServer")}>
        <dl className="ui-facts" aria-label={t("migrate.site.planFor", { name: title })}>
          <dt>{t("migrate.site.domains")}</dt>
          <dd>{site.domains.length ? site.domains.join(", ") : t("migrate.plan.none")}</dd>
          {site.redirectFrom.length > 0 && (
            <>
              <dt>{t("migrate.site.redirects")}</dt>
              <dd>
                {site.redirectFrom.join(", ")}
                {site.wwwRedirect && (
                  <span className="ui-hint ui-flush">
                    {site.wwwRedirect === "to-www" ? t("migrate.site.wwwRedirect") : t("migrate.site.nonWwwRedirect")}
                  </span>
                )}
              </dd>
            </>
          )}
          <dt>{t("migrate.site.type")}</dt>
          <dd>{typeLabel(site.type)}</dd>
          {site.proxy && (
            <>
              <dt>{t("migrate.site.target")}</dt>
              <dd className="ui-mono ui-break">{site.proxy}</dd>
            </>
          )}
          {site.php && (
            <>
              <dt>{t("migrate.plan.php")}</dt>
              <dd>{site.php}</dd>
            </>
          )}
          <dt>{t("migrate.site.https")}</dt>
          <dd>{tlsLabel(site.tls)}</dd>
          {!site.proxy && (
            <>
              <dt>{t("migrate.site.oldFiles")}</dt>
              <dd className="ui-mono ui-break">{site.docRoot}</dd>
            </>
          )}
          {site.customSnippets > 0 && (
            <>
              <dt>{t("migrate.site.customNginx")}</dt>
              <dd>{t("migrate.site.directives", { count: site.customSnippets })}</dd>
            </>
          )}
          {site.cronJobs > 0 && (
            <>
              <dt>{t("migrate.site.cronJobs")}</dt>
              <dd>{cronLabel(site)}</dd>
            </>
          )}
        </dl>
      </Card>
      {(site.action === "import" || site.action === "review") && (
        <Card title={t("migrate.site.createTitle")}>
          <CreateForm report={report} site={site} php={php} onChange={onChange} />
        </Card>
      )}
      {created && site.cronImport > 0 && (
        <Notice tone="info" title={t("migrate.site.cronPaused", { count: site.cronImport })} />
      )}
      {created && site.proxy && (
        <Notice tone="info" title={t("migrate.site.proxyNoAccount")} />
      )}
      {created && site.node && (
        <Notice tone="info" title={t("migrate.site.nodeStopped")} />
      )}
      {created && site.python && (
        <Notice tone="info" title={t("migrate.site.pythonStopped")} />
      )}
      {created && !site.proxy && (
        <Card title={t("migrate.site.filesTitle")}>
          <ImportForm report={report} site={site} ready={ready} />
        </Card>
      )}
      {created && !site.proxy && site.customSnippets > 0 && (
        <Card title={t("migrate.site.customNginx")}>
          <SnippetDraftForm report={report} site={site} />
        </Card>
      )}
      {created && (
        <Card title={t("migrate.site.dnsTitle")}>
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
  const { t } = useTranslation();

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
      setError(t("migrate.create.confirmChecked"));
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
      <p className="ui-flush">{t("migrate.create.intro")}</p>
      {site.php && choices.length > 0 && (
        <div className="ui-narrow">
          <Field label={t("migrate.create.php", { name: site.name })}>
            <select className="ui-select" value={version} onChange={(e) => setVersion(e.target.value)}>
              {choices.map((v) => (
                <option key={v} value={v}>
                  {v}
                  {php.includes(v) ? "" : t("migrate.notInstalled")}
                </option>
              ))}
            </select>
          </Field>
        </div>
      )}
      {site.node && lines.length > 0 && (
        <div className="ui-narrow">
          <Field label={t("migrate.create.node", { name: site.name })} hint={t("migrate.create.nodeHint")}>
            <select className="ui-select" value={line} onChange={(e) => setLine(e.target.value)}>
              {lines.map((v) => (
                <option key={v} value={v}>
                  Node.js {v}
                  {node && !installed.includes(v) ? t("migrate.notInstalled") : ""}
                </option>
              ))}
            </select>
          </Field>
        </div>
      )}
      {site.python && pyLines.length > 0 && (
        <div className="ui-narrow">
          <Field label={t("migrate.create.python", { name: site.name })} hint={t("migrate.create.pythonHint")}>
            <select className="ui-select" value={pyLine} onChange={(e) => setPyLine(e.target.value)}>
              {pyLines.map((v) => (
                <option key={v} value={v}>
                  {t("migrate.create.pythonLine", { line: v })}
                  {python && !installedPy.includes(v) ? t("migrate.notInstalled") : ""}
                </option>
              ))}
            </select>
          </Field>
        </div>
      )}
      {site.proxy && (
        <p className="ui-flush">
          <Trans i18nKey="migrate.create.proxy" values={{ target: site.proxy }} components={{ code: <code /> }} />
        </p>
      )}
      {choosePool && (
        <div className="ui-narrow">
          <Field
            label={t("migrate.create.pool", { name: site.name })}
            hint={site.homeBytes ? t("migrate.create.poolHomeUses", { size: formatBytes(site.homeBytes) }) : t("migrate.create.poolHint")}
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
      {choosePool && tooSmall && <Notice tone="err" title={t("migrate.create.poolTooSmall", { pool })} />}
      {choosePool && !tooSmall && poolNote(chosen) && <Notice tone="warn" title={poolNote(chosen)} />}
      {site.pageCache && (
        <label className="ui-check">
          <input type="checkbox" checked={cache} onChange={(e) => setCache(e.target.checked)} />
          {t("migrate.create.pageCache", { preset: presetLabel[site.pageCache as Preset] ?? site.pageCache })}
        </label>
      )}
      {site.profile === "wordpress" && (
        <p className="ui-hint">
          <Trans i18nKey="migrate.create.wordpress" components={{ code1: <code />, code2: <code />, code3: <code /> }} />
        </p>
      )}
      {review && (
        <label className="ui-check">
          <input type="checkbox" checked={reviewed} onChange={(e) => setReviewed(e.target.checked)} />
          {t("migrate.create.reviewed", { name: site.name })}
        </label>
      )}
      {error && <Notice tone="err" title={error} />}
      <div className="ui-row">
        <Button type="submit" variant="primary" disabled={busy || (choosePool && tooSmall)}>
          {busy ? t("migrate.create.creating") : t("migrate.create.submit", { name: site.name })}
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
  const { t } = useTranslation();

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
        <Trans i18nKey="migrate.import.intro" components={{ code: <code /> }} />
      </p>
      <form className="ui-stack" onSubmit={(e) => void submit(e)} noValidate>
        <Field
          label={t("migrate.import.databases", { name: site.name })}
          hint={
            t("migrate.import.databasesHint") +
            " " +
            (here.length ? t("migrate.import.siteHas", { list: here.join(", ") }) : t("migrate.import.createFirst"))
          }
        >
          <textarea className="ui-input ui-textarea ui-mono" value={dbs} onChange={(e) => setDbs(e.target.value)} rows={2} spellCheck={false} />
        </Field>
        {error && <Notice tone="err" title={error} />}
        <div className="ui-row">
          <Button type="submit" variant="primary" disabled={busy || !ready}>
            {busy ? t("migrate.import.starting") : job ? t("migrate.import.again", { name: site.name }) : t("migrate.import.submit", { name: site.name })}
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
  const { t } = useTranslation();

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

  if (!job) return error ? <Notice tone="err" title={error} /> : <p aria-busy="true">{t("migrate.import.loading")}</p>;

  const state =
    { queued: t("migrate.import.queued"), running: t("migrate.import.running"), done: t("migrate.import.done"), failed: t("migrate.import.failed") }[job.state] ??
    job.state;
  return (
    <section className="ui-stack ui-subsection" aria-label={t("migrate.import.label", { name: job.site })}>
      <dl className="ui-facts">
        <dt>{t("migrate.import.import")}</dt>
        <dd>
          <span className="ui-row">
            <Badge tone={jobTone[job.state] ?? "off"}>{state}</Badge>
            {job.finished && <RelTime iso={job.finished} />}
          </span>
        </dd>
        <dt>{t("migrate.import.copied")}</dt>
        <dd>{t("migrate.import.copiedValue", { count: job.files, size: formatBytes(job.bytes) })}</dd>
        <dt>{t("migrate.import.unchanged")}</dt>
        <dd>{formatCount(job.unchanged)}</dd>
        <dt>{t("migrate.import.deleted")}</dt>
        <dd>{formatCount(job.deleted)}</dd>
        <dt>{t("migrate.import.skipped")}</dt>
        <dd>{formatCount(job.skipped)}</dd>
        {job.dumps && job.dumps.length > 0 && (
          <>
            <dt>{t("migrate.import.dumps")}</dt>
            <dd>
              {job.dumps.map((d, i) => (
                <div key={d}>
                  <span className="ui-mono ui-break">{d}</span>
                  {job.databases?.[i] && t("migrate.import.dumpFor", { database: job.databases[i].to })}
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
          <summary>{t("migrate.import.skippedEntries")}</summary>
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
  if (!l.job) return { word: i18n.t("migrate.loads.notStarted"), tone: "off" };
  if (l.job.state === "running") return { word: i18n.t("migrate.loads.loading"), tone: "warn" };
  if (l.job.state === "done") return { word: i18n.t("migrate.loads.loaded"), tone: "ok" };
  return { word: i18n.t("migrate.loads.failed"), tone: "err" };
}

// DumpLoads shows how the copied dumps are loaded into the site's
// databases. The panel starts the loads by itself once the import is done;
// the button starts those again that did not run or failed.
function DumpLoads({ id }: { id: string }) {
  const [loads, setLoads] = useState<DumpLoad[] | null>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const { t } = useTranslation();

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
    <div className="ui-stack-tight" aria-label={t("migrate.loads.title")} role="group">
      <strong>{t("migrate.loads.title")}</strong>
      {loads === null ? (
        !error && <p aria-busy="true">{t("migrate.loads.loadingList")}</p>
      ) : loads.length === 0 ? (
        <p className="ui-hint ui-flush">{t("migrate.loads.soon")}</p>
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
            {busy ? t("migrate.import.starting") : t("migrate.loads.again")}
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
  const { t } = useTranslation();

  async function activate() {
    setBusy(true);
    setError("");
    try {
      const r = await activateImport(site);
      setDone(t("migrate.activate.done", { site: r.site, previous: r.previous }));
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
        <div className="ui-stack-tight" role="alertdialog" aria-label={t("migrate.activate.label", { site })}>
          <p className="ui-flush">{t("migrate.activate.text", { site })}</p>
          <div className="ui-row">
            <Button variant="primary" disabled={busy} onClick={() => void activate()}>
              {t("migrate.activate.confirm", { site })}
            </Button>
            <Button disabled={busy} onClick={() => setConfirm(false)}>
              {t("common.actions.cancel")}
            </Button>
          </div>
        </div>
      ) : (
        <div className="ui-row">
          <Button onClick={() => setConfirm(true)}>{t("migrate.activate.button")}</Button>
        </div>
      )}
      {error && <Notice tone="err" title={error} />}
    </div>
  );
}

const draftActionLabel = (a: DraftItem["action"]): string => (["kept", "rewritten", "dropped"].includes(a) ? i18n.t(`migrate.snippets.${a}`) : a);

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
  const { t } = useTranslation();

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
      setDone(t("migrate.snippets.saved", { site: site.name }));
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
        <p className="ui-flush">{t("migrate.snippets.intro", { count: site.customSnippets })}</p>
        <div className="ui-row">
          <Button disabled={busy} onClick={() => void load()}>
            {t("migrate.snippets.review")}
          </Button>
        </div>
        {error && <Notice tone="err" title={error} />}
      </div>
    );
  }

  return (
    <form className="ui-stack" onSubmit={(e) => void save(e)} noValidate aria-label={t("migrate.snippets.formLabel", { site: site.name })}>
      <ul className="ui-plain-list ui-stack-tight" aria-label={t("migrate.snippets.listLabel", { site: site.name })}>
        {draft.items.map((it, i) => (
          <li key={String(i)}>
            <Trans
              i18nKey="migrate.snippets.item"
              values={{ context: it.context, line: it.line, action: draftActionLabel(it.action) }}
              components={{ code: <code /> }}
            />
            {it.reason ? ". " + it.reason.charAt(0).toUpperCase() + it.reason.slice(1) + "." : ""}
          </li>
        ))}
      </ul>
      <SnippetField label={t("migrate.snippets.server")} hint={t("migrate.snippets.serverHint")} value={server} onChange={setServer} problems={problems?.server} />
      <SnippetField label={t("migrate.snippets.root")} hint={t("migrate.snippets.rootHint")} value={root} onChange={setRoot} problems={problems?.root} />
      {error && <Notice tone="err" title={error} />}
      {done && <Notice tone="ok" title={done} />}
      <div className="ui-row">
        <Button type="submit" variant="primary" disabled={busy}>
          {t("migrate.snippets.save", { site: site.name })}
        </Button>
      </div>
    </form>
  );
}
