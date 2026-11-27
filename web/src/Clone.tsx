// SPDX-License-Identifier: AGPL-3.0-or-later

import { cardId, CloseButton } from "./SiteStatus";
import { useCallback, useEffect, useRef, useState, type FormEvent } from "react";
import { Trans, useTranslation } from "react-i18next";
import { formatBytes, formatCount, formatDateTime } from "./format";
import i18n from "./i18n";
import { cloneMessage, cloneSite, cloneSuggestion, configuredProfiles, getClone, refreshClone, type Clone, type CloneStart } from "./clones";
import { getSiteProfile } from "./profiles";
import { formatHash } from "./routes";
import { domainProblem, hasAccount, listPools, siteMessage, siteNameProblem, siteType, type Pool, type Site } from "./sites";
import { Badge, Button, Card, Field, Notice } from "./ui";

/** canClone: only sites with a Linux account can be copied (ADR-0070). */
export const canClone = (site: Site) => site.state === "active" && hasAccount(site) && !["redirect", "proxy"].includes(siteType(site));

/**
 * useClone loads where the site was copied from, and polls while a copy
 * runs. null: the site is no clone; undefined: not loaded yet.
 */
export function useClone(site: string): { clone: Clone | null | undefined; reload: () => void } {
  const [clone, setClone] = useState<Clone | null | undefined>(undefined);
  const load = useCallback(() => {
    getClone(site)
      .then(setClone)
      .catch(() => setClone(null));
  }, [site]);

  useEffect(load, [load]);

  const copying = clone?.state === "copying";
  useEffect(() => {
    if (!copying) return;
    const t = setInterval(load, 3000);
    return () => clearInterval(t);
  }, [copying, load]);

  return { clone, reload: load };
}

/** CloneBadge marks a copy in the page head. */
export function CloneBadge({ clone }: { clone: Clone | null | undefined }) {
  const { t } = useTranslation();
  if (!clone) return null;
  if (clone.state === "copying") return <Badge tone="warn">{clone.refresh ? t("sites.clone.refreshing") : t("sites.clone.copying")}</Badge>;
  return <Badge tone="warn">{clone.staging ? t("sites.clone.staging") : t("sites.clone.copy")}</Badge>;
}

// CloneCard creates a new site as a copy of this one: files, local
// databases and settings. A staging copy is protected and not indexed.
export function CloneCard({ site, onClose }: { site: Site; onClose?: () => void }) {
  const { t } = useTranslation();
  const suggestion = cloneSuggestion(site);
  const [open, setOpen] = useState(onClose !== undefined);
  const [name, setName] = useState(suggestion.name);
  const [domain, setDomain] = useState(suggestion.domain);
  const [pool, setPool] = useState("");
  const [pools, setPools] = useState<Pool[]>([]);
  const [staging, setStaging] = useState(true);
  const [maintenance, setMaintenance] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [started, setStarted] = useState<CloneStart | null>(null);
  const [tried, setTried] = useState(false);

  useEffect(() => {
    if (!open) return;
    listPools()
      .then(setPools)
      .catch(() => setPools([]));
  }, [open]);

  const nameErr = siteNameProblem(name.trim());
  const domainErr = domain.trim() === "" ? t("sites.check.domainEmpty") : domainProblem(domain.trim().toLowerCase());

  async function submit(e: FormEvent) {
    e.preventDefault();
    setTried(true);
    if (nameErr || domainErr) return;
    setBusy(true);
    setError("");
    try {
      const out = await cloneSite(site.name, { name: name.trim(), domain: domain.trim().toLowerCase(), staging, ...(pool && { pool }), ...(maintenance && { maintenance }) });
      setStarted(out);
      setOpen(false);
    } catch (err) {
      setError(cloneMessage(err) ?? siteMessage(err));
    } finally {
      setBusy(false);
    }
  }

  return (
    <Card title={t("sites.clone.title")} id={cardId(site.name, "clone")} actions={onClose && <CloseButton onClose={onClose} />}>
      <div className="ui-stack">
        <p className="ui-hint ui-flush">{t("sites.clone.intro")}</p>
        {started && <CloneStarted started={started} onClose={() => setStarted(null)} />}
        {!open ? (
          !started && (
            <div className="ui-row">
              <Button onClick={() => setOpen(true)}>{t("sites.clone.open")}</Button>
            </div>
          )
        ) : (
          <form className="ui-stack" onSubmit={(e) => void submit(e)} noValidate>
            <div className="ui-narrow ui-stack">
              <Field label={t("sites.clone.name")} hint={t("sites.clone.nameHint")} {...(tried && nameErr && { error: nameErr })}>
                <input className="ui-input" value={name} onChange={(e) => setName(e.target.value)} autoComplete="off" spellCheck={false} />
              </Field>
              <Field label={t("sites.clone.domain")} hint={t("sites.clone.domainHint")} {...(tried && domainErr && { error: domainErr })}>
                <input className="ui-input" value={domain} onChange={(e) => setDomain(e.target.value)} autoComplete="off" spellCheck={false} />
              </Field>
              {pools.length > 1 && (
                <Field label={t("sites.clone.pool")}>
                  <select className="ui-select" value={pool} onChange={(e) => setPool(e.target.value)}>
                    <option value="">{t("sites.clone.samePool", { pool: site.pool })}</option>
                    {pools
                      .filter((p) => p.name !== site.pool)
                      .map((p) => (
                        <option key={p.name} value={p.name}>
                          {p.name}
                        </option>
                      ))}
                  </select>
                </Field>
              )}
            </div>
            <label className="ui-check">
              <input type="checkbox" checked={staging} onChange={(e) => setStaging(e.target.checked)} />
              {t("sites.clone.stagingOf", { site: site.name })}
            </label>
            {!site.maintenance.enabled && <MaintenanceCheck source={site.name} checked={maintenance} onChange={setMaintenance} />}
            <Notice tone="warn" title={t("sites.clone.mailWarning")}>
              {t("sites.clone.mailWarningText")}
            </Notice>
            {error && <Notice tone="err" title={error} />}
            <div className="ui-row">
              <Button type="submit" variant="primary" disabled={busy}>
                {busy ? t("sites.clone.starting") : staging ? t("sites.clone.createStaging") : t("sites.clone.create")}
              </Button>
              <Button disabled={busy} onClick={() => setOpen(false)}>
                {t("common.actions.cancel")}
              </Button>
            </div>
          </form>
        )}
      </div>
    </Card>
  );
}

// CloneStarted links to the new site and shows its generated password
// once: Hostashi keeps only its hash.
function CloneStarted({ started, onClose }: { started: CloneStart; onClose: () => void }) {
  const { t } = useTranslation();
  const [copied, setCopied] = useState("");
  const heading = useRef<HTMLHeadingElement>(null);
  const link = formatHash({ page: "sites", site: { name: started.site.name } });

  useEffect(() => {
    heading.current?.focus();
  }, []);

  async function copy(password: string) {
    try {
      await navigator.clipboard.writeText(password);
      setCopied(t("sites.clone.copied"));
    } catch {
      setCopied(t("sites.clone.copyFailed"));
    }
  }

  if (!started.access) {
    return (
      <Notice tone="ok" title={t("sites.clone.beingMade", { site: started.site.name })}>
        <Trans i18nKey="sites.clone.follow" values={{ site: started.site.name }} components={{ a: <a href={link} /> }} />
      </Notice>
    );
  }

  return (
    <section className="ui-card ui-card-body ui-stack ui-attention" aria-labelledby="clone-secret">
      <h2 id="clone-secret" ref={heading} tabIndex={-1}>
        {t("sites.clone.saveTitle")}
      </h2>
      <p className="ui-flush">
        <Trans i18nKey="sites.clone.saveText" values={{ site: started.site.name }} components={{ a: <a href={link} /> }} />
      </p>
      <dl className="ui-facts">
        <dt>{t("sites.clone.address")}</dt>
        <dd>
          <code>{started.site.domains[0]}</code>
        </dd>
        <dt>{t("sites.clone.user")}</dt>
        <dd>
          <code>{started.access.user}</code>
        </dd>
      </dl>
      <Field label={t("sites.clone.password")}>
        <input readOnly value={started.access.password} onFocus={(e) => e.currentTarget.select()} autoComplete="off" spellCheck={false} className="ui-input ui-mono" />
      </Field>
      <div className="ui-row">
        <Button onClick={() => void copy(started.access?.password ?? "")}>{t("sites.clone.copyPassword")}</Button>
        <Button variant="primary" onClick={onClose}>
          {t("sites.clone.saved")}
        </Button>
      </div>
      {copied && <Notice tone="ok" title={copied} />}
    </section>
  );
}

const steps = (c: Clone) => {
  const dbs = c.databases.filter((d) => d.done).length;
  return [
    { label: i18n.t("sites.clone.stepFiles"), done: c.filesDone },
    ...(c.databases.length > 0 ? [{ label: i18n.t("sites.clone.stepDatabases", { done: dbs, total: c.databases.length }), done: dbs === c.databases.length }] : []),
    { label: i18n.t("sites.clone.stepConfig"), done: c.state === "done" },
  ];
};

// CloneStatusCard shows where a copy came from and how far it is, and
// offers "refresh from source" for a staging copy.
export function CloneStatusCard({ site, clone, staff, onRefresh }: { site: Site; clone: Clone; staff: boolean; onRefresh: () => void }) {
  const { t } = useTranslation();
  const [profile, setProfile] = useState<string | null>(null);
  useEffect(() => {
    getSiteProfile(site.name)
      .then((p) => setProfile(p.profile))
      .catch(() => setProfile(null));
  }, [site.name]);

  const kind = clone.staging ? t("sites.clone.stagingCopy") : t("sites.clone.copy");
  // A deleted source shows as words, not as a link.
  const src = clone.source ? <a href={formatHash({ page: "sites", site: { name: clone.source } })} /> : <span />;
  const values = { kind, source: clone.source || t("sites.clone.deletedSource"), date: clone.finishedAt ? formatDateTime(new Date(clone.finishedAt)) : "" };

  return (
    <Card title={kind}>
      <div className="ui-stack">
        <p className="ui-flush">
          <Trans i18nKey={clone.finishedAt ? "sites.clone.ofAt" : "sites.clone.of"} values={values} components={{ src }} />
        </p>
        {clone.state === "copying" && (
          <div className="ui-stack" aria-live="polite">
            <ul className="ui-flush" aria-label={t("sites.clone.progress")}>
              {steps(clone).map((s) => (
                <li key={s.label}>
                  {s.done ? "✓" : "…"} {s.label}
                </li>
              ))}
            </ul>
            {clone.files && !clone.filesDone && (
              <p className="ui-hint ui-flush">
                {t("sites.clone.soFar", { files: formatCount(clone.files.files), bytes: formatBytes(clone.files.bytes) })}
              </p>
            )}
          </div>
        )}
        {clone.state === "failed" && (
          <Notice tone="err" title={t("sites.clone.failed")}>
            {clone.error}
          </Notice>
        )}
        {clone.files && clone.files.skipped > 0 && (
          <>
            <Notice tone="warn" title={t("sites.clone.skipped", { count: clone.files.skipped, n: formatCount(clone.files.skipped) })} />
            <ul className="ui-flush" aria-label={t("sites.clone.skippedLabel")}>
              {clone.files.skippedPaths.map((s) => (
                <li key={s.path}>
                  <span className="ui-mono">{s.path}</span>: {s.reason}
                </li>
              ))}
            </ul>
          </>
        )}
        {clone.state !== "copying" && profile !== null && !configuredProfiles.includes(profile) && <Checklist site={site} clone={clone} />}
        <Notice tone="warn" title={t("sites.clone.beforeTest")}>
          {t("sites.clone.beforeTestText")}
        </Notice>
        {clone.staging && staff && clone.source && <RefreshForm site={site} clone={clone} onDone={onRefresh} />}
      </div>
    </Card>
  );
}

// Checklist lists what to change by hand in an application Hostashi does
// not configure itself.
function Checklist({ site, clone }: { site: Site; clone: Clone }) {
  const { t } = useTranslation();
  const dbs = (
    <>
      {clone.databases.map((d, i) => (
        <span key={d.target}>
          {i > 0 && ", "}
          <code>{d.target}</code>
        </span>
      ))}
    </>
  );
  return (
    <div className="ui-stack">
      <p className="ui-flush">{t("sites.clone.unknownConfig")}</p>
      <ul className="ui-flush" aria-label={t("sites.clone.whatToChange")}>
        {clone.databases.length > 0 && (
          <li>
            <Trans i18nKey="sites.clone.dbConnection" components={{ dbs }} />
          </li>
        )}
        <li>
          <Trans i18nKey={clone.source ? "sites.clone.ownAddressSource" : "sites.clone.ownAddress"} values={{ domain: site.domains[0] ?? "" }} components={{ code: <code /> }} />
        </li>
        <li>{t("sites.clone.liveServices")}</li>
      </ul>
    </div>
  );
}

// MaintenanceCheck offers to put the source in maintenance while its files
// and databases are copied, so the copy gets one consistent state.
function MaintenanceCheck({ source, checked, onChange }: { source: string; checked: boolean; onChange: (v: boolean) => void }) {
  const { t } = useTranslation();
  return (
    <label className="ui-check ui-check-hint">
      <input type="checkbox" checked={checked} onChange={(e) => onChange(e.target.checked)} />
      <span>
        {t("sites.clone.maintenance", { site: source })}
        <span className="ui-hint ui-flush">{t("sites.clone.maintenanceHint")}</span>
      </span>
    </label>
  );
}

// RefreshForm copies the source's files and databases into the staging
// copy again, after the copy's name is typed.
function RefreshForm({ site, clone, onDone }: { site: Site; clone: Clone; onDone: () => void }) {
  const { t } = useTranslation();
  const [open, setOpen] = useState(false);
  const [typed, setTyped] = useState("");
  const [maintenance, setMaintenance] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");

  async function run(e: FormEvent) {
    e.preventDefault();
    setBusy(true);
    setError("");
    try {
      await refreshClone(site.name, maintenance);
      setOpen(false);
      setTyped("");
      onDone();
    } catch (err) {
      setError(cloneMessage(err) ?? siteMessage(err));
    } finally {
      setBusy(false);
    }
  }

  if (clone.state === "copying") return null;

  return (
    <div className="ui-stack">
      {!open ? (
        <div className="ui-row">
          <Button onClick={() => setOpen(true)}>{t("sites.clone.refresh", { source: clone.source })}</Button>
        </div>
      ) : (
        <form className="ui-stack" onSubmit={(e) => void run(e)} data-unsaved="ignore">
          <div className="ui-narrow">
            <Field
              label={<Trans i18nKey="sites.confirm.type" values={{ name: site.name }} components={{ code: <code className="ui-confirm-phrase" /> }} />}
              hint={t("sites.clone.refreshHint", { site: site.name, source: clone.source })}
            >
              <input className="ui-input" value={typed} onChange={(e) => setTyped(e.target.value)} autoComplete="off" spellCheck={false} />
            </Field>
          </div>
          {clone.source && <MaintenanceCheck source={clone.source} checked={maintenance} onChange={setMaintenance} />}
          <div className="ui-row">
            <Button type="submit" variant="danger" disabled={busy || typed !== site.name}>
              {busy ? t("sites.clone.starting") : t("sites.clone.replace", { site: site.name, source: clone.source })}
            </Button>
            <Button
              disabled={busy}
              onClick={() => {
                setOpen(false);
                setTyped("");
                setError("");
              }}
            >
              {t("common.actions.cancel")}
            </Button>
          </div>
        </form>
      )}
      {error && <Notice tone="err" title={error} />}
    </div>
  );
}
