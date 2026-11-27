// SPDX-License-Identifier: AGPL-3.0-or-later

import { ArrowLeft, Globe, PencilSimple, Plus, Trash } from "@phosphor-icons/react";
import { useCallback, useEffect, useState, type FormEvent } from "react";
import { Trans, useTranslation } from "react-i18next";
import { CertificateCard } from "./Certificate";
import i18n from "./i18n";
import { parkedCertBase } from "./certs";
import { deleteParked, listParked, parkedMessage, setParked, type ParkedDomain, type ParkedTLS } from "./parked";
import { listPages, type StatusPage } from "./pages";
import { formatHash } from "./routes";
import { domainProblem, newSiteHash } from "./sites";
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

type QueryProps = {
  /** The address query: q, page, sort, dir; new=1 or edit=<domain> for one view. */
  query?: URLSearchParams;
  /** Replaces the query in the address without a new history entry. */
  onQuery?: (q: URLSearchParams) => void;
};

const parkedHash = (q?: Record<string, string>) => formatHash({ page: "parked", ...(q ? { query: new URLSearchParams(q) } : {}) });

type HTTPS = { tone: Tone; label: string; rank: number };

function httpsOf(t: ParkedTLS): HTTPS {
  if (!t.enabled) return { tone: "off", label: i18n.t("parked.https.off"), rank: 3 };
  if (t.active) return { tone: "ok", label: i18n.t("parked.https.on"), rank: 2 };
  return t.error
    ? { tone: "err", label: i18n.t("parked.https.failed"), rank: 0 }
    : { tone: "warn", label: i18n.t("parked.https.waiting"), rank: 1 };
}

// Parked lists the parked domains and parks new ones. A parked domain shows
// a parking page on every path; it is dropped when a site takes the domain.
export function Parked({ query = new URLSearchParams(), onQuery = () => undefined }: QueryProps) {
  const { t } = useTranslation();
  const [parked, setList] = useState<ParkedDomain[] | null>(null);
  const [pages, setPages] = useState<StatusPage[]>([]);
  const [loadError, setLoadError] = useState("");
  const [done, setDone] = useState("");
  const [unparking, setUnparking] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");

  const load = useCallback(async () => {
    try {
      const [list, all] = await Promise.all([listParked(), listPages()]);
      setList(list);
      setPages(all.filter((p) => p.kind === "parking"));
      setLoadError("");
    } catch (err) {
      setLoadError(parkedMessage(err));
    }
  }, []);

  useEffect(() => {
    void load();
  }, [load]);

  // A certificate comes in the background: look again while one is pending.
  const pending = parked?.some((p) => p.tls.enabled && !p.tls.active && !p.tls.error) ?? false;
  useEffect(() => {
    if (!pending) return;
    const t = setInterval(() => void load(), 10000);
    return () => clearInterval(t);
  }, [pending, load]);

  const set = (changes: Record<string, string>) => {
    const next = new URLSearchParams(query);
    for (const [k, v] of Object.entries(changes)) {
      if (v) next.set(k, v);
      else next.delete(k);
    }
    onQuery(next);
  };

  const changed = async (msg: string) => {
    setDone(msg);
    set({ new: "", edit: "" });
    await load();
  };

  const unpark = async (domain: string) => {
    setBusy(true);
    setError("");
    setDone("");
    try {
      await deleteParked(domain);
      setUnparking(null);
      await changed(t("parked.unpark.done", { domain }));
    } catch (err) {
      setError(parkedMessage(err));
    } finally {
      setBusy(false);
    }
  };

  const listLink = (
    <a className="ui-btn" href={parkedHash()}>
      <ArrowLeft size={16} aria-hidden="true" />
      {t("parked.allLink")}
    </a>
  );

  if (!parked) {
    return (
      <>
        <PageHead title={t("parked.title")} />
        {loadError ? (
          <Notice tone="err" title={loadError}>
            <Button onClick={() => void load()}>{t("common.actions.tryAgain")}</Button>
          </Notice>
        ) : (
          <p aria-busy="true">{t("common.status.loading")}</p>
        )}
      </>
    );
  }

  const pageName = (id: string) => pages.find((p) => p.id === id)?.name ?? id;

  const unparkDialog = unparking && (
    <section className="ui-card ui-card-body ui-stack" role="alertdialog" aria-labelledby="unpark-title">
      <h2 id="unpark-title" className="ui-flush">
        {t("parked.unpark.title", { domain: unparking })}
      </h2>
      <p className="ui-flush">{t("parked.unpark.text")}</p>
      {error && <Notice tone="err" title={error} />}
      <div className="ui-row">
        <Button variant="danger" disabled={busy} onClick={() => void unpark(unparking)}>
          {t("parked.unpark.confirm", { domain: unparking })}
        </Button>
        <Button disabled={busy} onClick={() => setUnparking(null)}>
          {t("common.actions.cancel")}
        </Button>
      </div>
    </section>
  );

  if (query.get("new") === "1") {
    return (
      <>
        <PageHead title={t("parked.new.title")} description={t("parked.new.description")} actions={listLink} />
        <Card>
          <ParkForm pages={pages} onCancel={() => set({ new: "" })} onParked={changed} />
        </Card>
      </>
    );
  }

  const editDomain = query.get("edit");
  if (editDomain !== null) {
    const p = parked.find((x) => x.domain === editDomain);
    if (!p) {
      return (
        <>
          <PageHead title={t("parked.title")} actions={listLink} />
          <Notice tone="warn" title={t("parked.notParked", { domain: editDomain })} />
        </>
      );
    }
    return (
      <>
        <PageHead title={p.domain} description={<Trans i18nKey="parked.parkedSince" components={{ time: <RelTime iso={p.createdAt} /> }} />} actions={listLink} />
        <TLSNotice tls={p.tls} />
        <Card title={t("parked.edit.card")}>
          <EditForm key={p.domain + p.updatedAt} parked={p} pages={pages} onCancel={() => set({ edit: "" })} onSaved={changed} />
        </Card>
        <CertificateCard
          base={parkedCertBase(p.domain)}
          name={p.domain}
          tls={p.tls}
          offHint={t("parked.edit.certOffHint")}
          onChange={load}
        />
        <Card title={t("parked.edit.makeSite")}>
          <div className="ui-stack">
            <p className="ui-flush">{t("parked.edit.makeSiteText", { domain: p.domain })}</p>
            <div className="ui-row">
              <a className="ui-btn" href={newSiteHash(p.domain, p.tls.enabled)}>
                {t("parked.edit.makeSite")}
              </a>
            </div>
          </div>
        </Card>
        {unparkDialog || (
          <Card title={t("parked.edit.unpark")}>
            <div className="ui-stack">
              <p className="ui-flush">{t("parked.edit.unparkText", { domain: p.domain })}</p>
              <div className="ui-row">
                <Button variant="danger" onClick={() => setUnparking(p.domain)}>
                  {t("parked.unpark.confirm", { domain: p.domain })}
                </Button>
              </div>
            </div>
          </Card>
        )}
      </>
    );
  }

  const columns: Column<ParkedDomain>[] = [
    {
      key: "domain",
      header: t("parked.columns.domain"),
      sortValue: (p) => p.domain,
      cell: (p) => (
        <span className="ui-cell-main">
          <a href={parkedHash({ edit: p.domain })}>{p.domain}</a>
        </span>
      ),
    },
    { key: "page", header: t("parked.columns.page"), sortValue: (p) => pageName(p.page), cell: (p) => pageName(p.page) },
    {
      key: "https",
      header: t("parked.columns.https"),
      sortValue: (p) => httpsOf(p.tls).rank,
      cell: (p) => {
        const h = httpsOf(p.tls);
        return (
          <div className="ui-stack-tight">
            <span className="ui-row">
              <Badge tone={h.tone}>{h.label}</Badge>
            </span>
            {p.tls.active && p.tls.notAfter && (
              <p className="ui-hint ui-flush">
                <Trans i18nKey="parked.https.expires" components={{ time: <RelTime iso={p.tls.notAfter} /> }} />
              </p>
            )}
            {h.rank === 0 && <p className="ui-hint ui-flush">{p.tls.error}</p>}
          </div>
        );
      },
    },
    { key: "created", header: t("parked.columns.created"), sortValue: (p) => p.createdAt, cell: (p) => <RelTime iso={p.createdAt} /> },
    {
      key: "actions",
      header: t("parked.columns.actions"),
      srHeader: true,
      cell: (p) => (
        <RowActions>
          <IconLink label={t("parked.actions.changeLabel", { domain: p.domain })} text={t("parked.actions.change")} icon={<PencilSimple size={16} />} href={parkedHash({ edit: p.domain })} />
          <IconLink label={t("parked.actions.makeSiteLabel", { domain: p.domain })} text={t("parked.actions.makeSite")} icon={<Globe size={16} />} href={newSiteHash(p.domain, p.tls.enabled)} />
          <IconButton
            label={t("parked.actions.unparkLabel", { domain: p.domain })}
            text={t("parked.actions.unpark")} icon={<Trash size={16} />}
            danger
            disabled={busy}
            onClick={() => {
              setError("");
              setUnparking(p.domain);
            }}
          />
        </RowActions>
      ),
    },
  ];

  const q = (query.get("q") ?? "").trim().toLowerCase();
  const pageFilter = query.get("page") ?? "";
  const shown = parked.filter((p) => (!q || p.domain.includes(q)) && (!pageFilter || p.page === pageFilter));
  const sortKey = query.get("sort");
  const sort: Sort | undefined = sortKey ? { key: sortKey, dir: query.get("dir") === "desc" ? "desc" : "asc" } : undefined;

  return (
    <>
      <PageHead
        title={t("parked.title")}
        description={t("parked.description")}
        actions={
          <a className="ui-btn ui-btn-primary" href={parkedHash({ new: "1" })}>
            <Plus size={16} aria-hidden="true" />
            {t("parked.new.button")}
          </a>
        }
      />
      {loadError && <Notice tone="err" title={loadError} />}
      {done && <Notice tone="ok" title={done} />}
      {unparkDialog}
      <Card padded={false}>
        <div className="ui-filters">
          <input
            className="ui-input"
            type="search"
            aria-label={t("parked.filter.label")}
            placeholder={t("parked.filter.placeholder")}
            value={query.get("q") ?? ""}
            onChange={(e) => set({ q: e.target.value })}
          />
          <select className="ui-select" aria-label={t("parked.filter.page")} value={pageFilter} onChange={(e) => set({ page: e.target.value })}>
            <option value="">{t("parked.filter.allPages")}</option>
            {pages.map((p) => (
              <option key={p.id} value={p.id}>
                {p.name}
              </option>
            ))}
          </select>
        </div>
        <Table
          label={t("parked.table.label")}
          actionsBelow
          columns={columns}
          rows={shown}
          rowKey={(p) => p.domain}
          {...(sort ? { sort } : {})}
          onSort={(s) => set({ sort: s.key, dir: s.dir })}
          empty={parked.length === 0 ? t("parked.table.none") : t("parked.table.noMatch")}
        />
      </Card>
    </>
  );
}

function TLSNotice({ tls }: { tls: ParkedTLS }) {
  const { t } = useTranslation();
  if (!tls.enabled || tls.active) return null;
  if (tls.error) {
    return (
      <Notice tone="err" title={t("parked.tls.failed", { error: tls.error })}>
        {t("parked.tls.failedText")}
      </Notice>
    );
  }
  return <Notice tone="warn" title={t("parked.tls.waiting")} />;
}

function PageSelect({ pages, value, onChange }: { pages: StatusPage[]; value: string; onChange: (v: string) => void }) {
  const { t } = useTranslation();
  return (
    <div className="ui-narrow">
      <Field label={t("parked.form.page")} hint={t("parked.form.pageHint")}>
        <select className="ui-select" value={value} onChange={(e) => onChange(e.target.value)}>
          {pages.map((p) => (
            <option key={p.id} value={p.id}>
              {p.name}
            </option>
          ))}
        </select>
      </Field>
    </div>
  );
}

function ParkForm({ pages, onParked, onCancel }: { pages: StatusPage[]; onParked: (msg: string) => Promise<void>; onCancel: () => void }) {
  const { t } = useTranslation();
  const [domain, setDomain] = useState("");
  const [page, setPage] = useState("builtin-parking");
  const [tls, setTls] = useState(true);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");

  async function submit(e: FormEvent) {
    e.preventDefault();
    const d = domain.trim().toLowerCase();
    const problem = d === "" ? t("parked.form.enterDomain") : domainProblem(d);
    if (problem) {
      setError(problem);
      return;
    }
    setBusy(true);
    setError("");
    try {
      const p = await setParked(d, page, tls);
      await onParked(
        p.tls.enabled && !p.tls.active
          ? t("parked.form.parkedHttp", { domain: p.domain })
          : t("parked.form.parked", { domain: p.domain }),
      );
    } catch (err) {
      setError(parkedMessage(err));
      setBusy(false);
    }
  }

  return (
    <form className="ui-stack" onSubmit={(e) => void submit(e)} noValidate aria-label={t("parked.form.label")}>
      <div className="ui-narrow">
        <Field label={t("parked.form.domain")} hint={t("parked.form.domainHint")}>
          <input className="ui-input" value={domain} onChange={(e) => setDomain(e.target.value)} autoComplete="off" spellCheck={false} />
        </Field>
      </div>
      <PageSelect pages={pages} value={page} onChange={setPage} />
      <label className="ui-check">
        <input type="checkbox" checked={tls} onChange={(e) => setTls(e.target.checked)} />
        {t("parked.form.https")}
      </label>
      {error && <Notice tone="err" title={error} />}
      <div className="ui-row">
        <Button type="submit" variant="primary" disabled={busy}>
          {busy ? t("parked.form.parking") : t("parked.form.submit")}
        </Button>
        <Button onClick={onCancel}>{t("common.actions.cancel")}</Button>
      </div>
    </form>
  );
}

type EditProps = { parked: ParkedDomain; pages: StatusPage[]; onSaved: (msg: string) => Promise<void>; onCancel: () => void };

function EditForm({ parked, pages, onSaved, onCancel }: EditProps) {
  const { t } = useTranslation();
  const [page, setPage] = useState(parked.page);
  const [tls, setTls] = useState(parked.tls.enabled);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");

  async function save(e: FormEvent) {
    e.preventDefault();
    setBusy(true);
    setError("");
    try {
      await setParked(parked.domain, page, tls);
      await onSaved(t("parked.form.saved", { domain: parked.domain }));
    } catch (err) {
      setError(parkedMessage(err));
      setBusy(false);
    }
  }

  return (
    <form className="ui-stack" onSubmit={(e) => void save(e)} noValidate>
      <PageSelect pages={pages} value={page} onChange={setPage} />
      <label className="ui-check">
        <input type="checkbox" checked={tls} onChange={(e) => setTls(e.target.checked)} />
        {t("parked.form.https")}
      </label>
      {error && <Notice tone="err" title={error} />}
      <div className="ui-row">
        <Button type="submit" variant="primary" disabled={busy || (page === parked.page && tls === parked.tls.enabled)}>
          {t("common.actions.save")}
        </Button>
        <Button disabled={busy} onClick={onCancel}>
          {t("common.actions.cancel")}
        </Button>
      </div>
    </form>
  );
}
