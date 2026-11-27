// SPDX-License-Identifier: AGPL-3.0-or-later

import { ArrowSquareOut } from "@phosphor-icons/react";
import { useEffect, useState, type FormEvent, type ReactNode } from "react";
import { Trans, useTranslation } from "react-i18next";
import { formatDate } from "./format";
import { CertificateCard } from "./Certificate";
import { canClone, CloneBadge, CloneCard, CloneStatusCard, useClone } from "./Clone";
import type { Clone } from "./clones";
import { siteCertBase } from "./certs";
import { CronSettings } from "./Cron";
import { cardId, CloseButton, QuickActions, SiteActions, siteUrl, StatusTiles, type OverviewCard } from "./SiteStatus";
import { cronFailing, listCron } from "./cron";
import { getSiteScan } from "./scan";
import { FileManager } from "./Files";
import { GitSettings } from "./Git";
import { LimitSettings } from "./Limits";
import { SiteMalware } from "./Malware";
import { SiteLogs } from "./Logs";
import { ObjectCacheSettings } from "./ObjectCache";
import { allowIpsProblem, clientIp, listPages, pageMessage, parseAllowIps, setMaintenance, type StatusPage } from "./pages";
import { formatHash, type SiteTab } from "./routes";
import { PHPOptions, useSites } from "./Sites";
import {
  deleteAndPark,
  deleteSite,
  hasAccount,
  httpsLabel,
  httpsTone,
  setDisabled,
  setHSTS,
  setPHP,
  setPHPPrograms,
  setTLS,
  siteMessage,
  siteStatus,
  runtimeWord,
  siteType,
  siteTypeLabel,
  maxTitleLength,
  setTitle,
  siteLabel,
  type PHPVersions,
  type Site,
} from "./sites";
import { PageCacheSettings } from "./PageCache";
import i18n from "./i18n";
import { PHPSettingsCard } from "./PHPSettings";
import { Services } from "./Services";
import { programCommand, startCommand } from "./node";
import { pythonCommand } from "./python";
import { SiteWebSettings } from "./SiteWeb";
import { RemoveAccountCard } from "./SiteType";
import { SnippetSettings } from "./Snippets";
import { SiteAccess } from "./SiteAccess";
import { RealIPCard } from "./RealIP";
import { SecurityHeadersCard } from "./SecurityHeaders";
import { HardeningCard } from "./Hardening";
import { SSHSettings } from "./SSH";
import { PermissionsResetCard } from "./PermissionsReset";
import { AppliesAtOnce, Badge, Button, Card, Field, Notice, Switch } from "./ui";
import { groupsForSite, SiteNav, type SiteGroup } from "./SiteNav";
import { useUnsaved } from "./Unsaved";
import { SiteUsageCard } from "./Usage";
import { WWWCard } from "./WWW";
import { DomainsCard, PendingDomainsNotice } from "./Domains";

type Props = {
  name: string;
  tab: SiteTab;
  /** Opens another tab: a new entry in the browser history. */
  onTab: (tab: SiteTab) => void;
  /** The role of the logged-in account; hides what it cannot change. */
  role?: string;
  /** The folder of the Files tab (?path=), "" for the home. */
  path?: string;
  /** Opens another folder in the Files tab: a new history entry. */
  onPath?: (path: string) => void;
};

/**
 * SiteAlerts shows, beside the site's name, what needs a look: open virus
 * finds and cron jobs whose last run failed. Each badge opens its tab.
 */
function SiteAlerts({ site, groups, tab }: { site: string; groups: SiteGroup[]; tab: SiteTab }) {
  const { t } = useTranslation();
  const [finds, setFinds] = useState(0);
  const [failing, setFailing] = useState(0);
  const has = (id: SiteTab) => groups.some((g) => g.tabs.some((t) => t.id === id));
  const malware = has("malware");
  const cron = has("cron");

  useEffect(() => {
    let live = true;
    if (malware) {
      getSiteScan(site)
        .then((s) => live && setFinds(s.findings.filter((f) => f.state === "open").length))
        .catch(() => undefined);
    }
    if (cron) {
      listCron(site)
        .then((l) => live && setFailing(l.jobs.filter((j) => !j.paused && cronFailing(j.status)).length))
        .catch(() => undefined);
    }
    return () => {
      live = false;
    };
  }, [site, tab, malware, cron]);

  return (
    <>
      {finds > 0 && (
        <a href={formatHash({ page: "sites", site: { name: site, tab: "malware" } })}>
          <Badge tone="err">{t("sites.alerts.virusFinds", { count: finds })}</Badge>
        </a>
      )}
      {failing > 0 && (
        <a href={formatHash({ page: "sites", site: { name: site, tab: "cron" } })}>
          <Badge tone="warn">{t("sites.alerts.cronFails", { count: failing })}</Badge>
        </a>
      )}
    </>
  );
}

// SitePage shows one site with its settings in tabs (#/sites/<name>/<tab>).
export function SitePage({ name, tab, onTab, role = "user", path = "", onPath = () => undefined }: Props) {
  const { t } = useTranslation();
  const { sites, php, error, load } = useSites();
  const site = sites?.find((s) => s.name === name);
  const { clone, reload: reloadClone } = useClone(name);
  // card is the overview card the Actions menu opened (C1 of the UX review).
  const [card, setCard] = useState<OverviewCard | null>(null);
  useEffect(() => setCard(null), [name]);

  if (!sites) return error ? <Notice tone="err" title={error} /> : <p aria-busy="true">{t("common.status.loading")}</p>;
  if (!site) {
    return (
      <Notice tone="err" title={t("sites.page.missing", { name })}>
        <a href={formatHash({ page: "sites" })}>{t("sites.page.back")}</a>
      </Notice>
    );
  }

  const status = siteStatus(site);
  const ready = site.state === "active";
  const staff = role === "admin" || role === "manager";
  const groups = groupsForSite(site);
  const active: SiteTab = ready && groups.some((g) => g.tabs.some((t) => t.id === tab)) ? tab : "overview";

  const panel = (
    <>
      {active === "overview" && (
        <Overview site={site} groups={groups} onChange={load} staff={staff} admin={role === "admin"} clone={clone ?? null} onCloneChange={reloadClone} card={card} onCard={setCard} />
      )}
      {active === "domains" && (
        <ReadOnly when={!staff}>
          <DomainsTab site={site} onChange={load} />
        </ReadOnly>
      )}
      {active === "https" && <HTTPSTab site={site} onChange={load} staff={staff} />}
      {active === "headers" && <SecurityHeadersCard site={site} />}
      {active === "hardening" && <HardeningCard site={site} />}
      {active === "realip" && <RealIPCard site={site} admin={role === "admin"} />}
      {active === "access" && <SiteAccess site={site} />}
      {active === "web" && <SiteWebSettings site={site} php={php} onChange={load} admin={role === "admin"} staff={staff} />}
      {active === "files" && <FileManager site={site} path={path} onPath={onPath} />}
      {active === "files" && site.state === "active" && <PermissionsResetCard site={site} />}
      {active === "logs" && <SiteLogs site={site} admin={role === "admin"} />}
      {active === "php" && (
        <>
          <PHPTab site={site} php={php} onChange={load} staff={staff} />
          {site.php && <PHPSettingsCard site={site} />}
        </>
      )}
      {active === "cache" && (
        <>
          <PageCacheSettings site={site} canSize={staff} />
          <ObjectCacheSettings site={site} canSize={staff} />
        </>
      )}
      {active === "git" && <GitSettings site={site} canEdit={staff} />}
      {active === "malware" && <SiteMalware site={site} canManage={staff} admin={role === "admin"} />}
      {active === "cron" && <CronSettings site={site} />}
      {active === "ssh" && <SSHSettings site={site} />}
      {active === "limits" && (
        <ReadOnly when={!staff}>
          <LimitSettings site={site} />
        </ReadOnly>
      )}
      {active === "usage" && <SiteUsageCard site={site.name} />}
      {active === "nginx" && (
        <ReadOnly when={role !== "admin"} adminOnly>
          <SnippetSettings site={site} />
        </ReadOnly>
      )}
    </>
  );

  return (
    <>
      <div className="ui-page-head">
        <div>
          <h1>{siteLabel(site)}</h1>
          <p className="ui-row">
            {site.title && (
              <span title={t("sites.page.systemNameTip")}>
                <Trans i18nKey="sites.page.systemName" values={{ name: site.name }} components={{ code: <code /> }} />
              </span>
            )}
            {site.domains[0] && (
              <a className="ui-mono ui-ext" href={siteUrl(site)} target="_blank" rel="noopener noreferrer" title={t("sites.page.openTip")}>
                {site.domains[0]}
                <ArrowSquareOut size={14} aria-hidden="true" />
              </a>
            )}
            <Badge tone={status.tone}>{status.word}</Badge>
            <Badge tone={httpsTone(site.tls)}>{httpsLabel(site.tls)}</Badge>
            <Badge tone="off">{siteTypeLabel(site)}</Badge>
            <CloneBadge clone={clone} />
            {ready && <SiteAlerts site={site.name} groups={groups} tab={active} />}
          </p>
        </div>
        {ready && (
          <div className="ui-page-actions">
            <SiteActions site={site} groups={groups} staff={staff} admin={role === "admin"} onTab={onTab} onCard={setCard} />
          </div>
        )}
      </div>
      {error && <Notice tone="err" title={error} />}
      {site.tls.enabled && !site.tls.active && site.tls.error && (
        <Notice tone="err" title={t("sites.page.certFailed")}>
          {t("sites.page.certFailedText", { error: site.tls.error })}
        </Notice>
      )}
      {ready ? (
        <SitePanel site={site.name} groups={groups} active={active} onTab={onTab}>
          {panel}
        </SitePanel>
      ) : (
        <div className="ui-stack">{panel}</div>
      )}
    </>
  );
}

// SitePanel is the tab navigation with the unsaved-changes guard around it.
function SitePanel({ site, groups, active, onTab, children }: { site: string; groups: SiteGroup[]; active: SiteTab; onTab: (t: SiteTab) => void; children: ReactNode }) {
  const u = useUnsaved(site + "/" + active);
  return (
    <SiteNav site={site} groups={groups} active={active} onTab={(t) => u.guard(() => onTab(t))}>
      <div className="ui-stack" {...u.watch}>
        {children}
      </div>
      {u.bar}
    </SiteNav>
  );
}

// useAction runs one change at a time and keeps its result for the card.
function useAction(onChange: () => Promise<void>) {
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [done, setDone] = useState("");

  async function act(f: () => Promise<string>) {
    setBusy(true);
    setError("");
    setDone("");
    try {
      const msg = await f();
      await onChange();
      setDone(msg);
    } catch (err) {
      setError(siteMessage(err));
    } finally {
      setBusy(false);
    }
  }

  const result: ReactNode = (
    <>
      {error && <Notice tone="err" title={error} />}
      {done && <Notice tone="ok" title={done} />}
    </>
  );
  return { busy, act, setError, setDone, result };
}

type TabProps = { site: Site; onChange: () => Promise<void> };

function Overview({
  site,
  groups,
  onChange,
  staff,
  admin,
  clone,
  onCloneChange,
  card,
  onCard,
}: TabProps & { groups: SiteGroup[]; staff: boolean; admin: boolean; clone: Clone | null; onCloneChange: () => void; card: OverviewCard | null; onCard: (c: OverviewCard | null) => void }) {
  const { t } = useTranslation();
  const [primary, ...aliases] = site.domains;
  const close = () => onCard(null);
  return (
    <>
      {card === "rename" && site.state !== "deleting" && <RenameCard site={site} onChange={onChange} onClose={close} />}
      {card === "clone" && staff && canClone(site) && clone?.state !== "copying" && <CloneCard site={site} onClose={close} />}
      {card === "services" && admin && hasAccount(site) && <Services site={site.name} id={cardId(site.name, "services")} onClose={close} />}
      {site.state === "active" && (
        <>
          <StatusTiles site={site} groups={groups} admin={admin} />
          <QuickActions site={site} groups={groups} />
        </>
      )}
      <div className="ui-two">
        <Card title={t("sites.details.title")}>
          <dl className="ui-facts" aria-label={t("sites.details.label", { site: site.name })}>
            <dt>{t("sites.details.systemName")}</dt>
            <dd>
              <code>{site.name}</code>
              {hasAccount(site) ? t("sites.details.accountNote") : ""}
            </dd>
            {site.title && (
              <>
                <dt>{t("sites.details.displayName")}</dt>
                <dd>{site.title}</dd>
              </>
            )}
            <dt>{t("sites.details.state")}</dt>
            <dd>
              {site.state === "deleting"
                ? t("sites.state.deleting")
                : site.state !== "active"
                  ? t("sites.state.creating")
                  : site.disabled
                    ? t(runtimeWord(site) === "PHP" ? "sites.details.disabledPhp" : runtimeWord(site) === "app" ? "sites.details.disabledApp" : "sites.details.disabled")
                    : site.maintenance.enabled
                      ? site.maintenance.allowIps.length > 0
                        ? t("sites.details.maintenanceExcept", { ips: site.maintenance.allowIps.join(", ") })
                        : t("sites.details.maintenance")
                      : t("sites.state.active")}
            </dd>
            <dt>{t("sites.details.primaryDomain")}</dt>
            <dd className="ui-mono">{primary}</dd>
            {aliases.length > 0 && (
              <>
                <dt>{t("sites.details.otherDomains")}</dt>
                <dd className="ui-mono">{aliases.join(", ")}</dd>
              </>
            )}
            {site.node && (
              <>
                <dt>{t("sites.details.nodeApp")}</dt>
                <dd>
                  {site.node.startKind ? <span className="ui-mono">{startCommand(site.node)}</span> : t("sites.details.noStart")}
                  {t("sites.details.onNode", { version: site.node.version })}
                  {site.node.stopped ? t("sites.details.stopped") : ""}
                </dd>
              </>
            )}
            {site.python && (
              <>
                <dt>{t("sites.details.pythonApp")}</dt>
                <dd>
                  {site.python.startKind ? <span className="ui-mono">{pythonCommand(site.python)}</span> : t("sites.details.noStart")}
                  {t("sites.details.onPython", { version: site.python.version })}
                  {site.python.stopped ? t("sites.details.stopped") : ""}
                </dd>
              </>
            )}
            {site.app && (
              <>
                <dt>{t("sites.details.app")}</dt>
                <dd>
                  {site.app.program ? <span className="ui-mono">{programCommand(site.app)}</span> : t("sites.details.noProgram")}
                  {site.app.stopped ? t("sites.details.stopped") : ""}
                </dd>
              </>
            )}
            {site.proxy ? (
              <>
                <dt>{t("sites.details.proxy")}</dt>
                <dd>
                  <span className="ui-mono">{site.proxy.target}</span>
                  {site.proxy.insecure ? t("sites.details.insecure") : ""}
                </dd>
              </>
            ) : site.redirect ? (
              <>
                <dt>{t("sites.details.redirect")}</dt>
                <dd>
                  <span className="ui-mono">{site.redirect.target}</span>
                  {t(site.redirect.keepPath ? "sites.details.redirectKeepsPath" : "sites.details.redirectStatus", { status: site.redirect.status })}
                </dd>
              </>
            ) : (
              <>
                <dt>{t("sites.details.php")}</dt>
                <dd>{site.php ? t(site.disabled ? "sites.details.phpStopped" : "sites.details.phpVersion", { version: site.php }) : t("sites.details.noPhp")}</dd>
              </>
            )}
            <dt>{t("sites.details.https")}</dt>
            <dd>
              {httpsLabel(site.tls)}
              {site.tls.active && site.tls.hsts ? t("sites.details.hsts") : ""}
              {site.tls.active && site.tls.notAfter ? t("sites.details.validUntil", { date: formatDate(new Date(site.tls.notAfter)) }) : ""}
            </dd>
            {hasAccount(site) && (
              <>
                <dt>{t("sites.details.pool")}</dt>
                <dd className="ui-mono">{site.pool}</dd>
              </>
            )}
          </dl>
        </Card>
        {site.state === "active" && (
          <Card title={t("sites.details.maintenanceCard")} actions={<AppliesAtOnce />}>
            <MaintenanceSettings site={site} onChange={onChange} />
          </Card>
        )}
      </div>
      {clone && <CloneStatusCard site={site} clone={clone} staff={staff} onRefresh={onCloneChange} />}
      {/* Disabling and deleting are for administrators and managers (ADR-0035). */}
      {site.state === "active" && staff && (
        <section className="ui-danger-zone" aria-labelledby={`danger-${site.name}`}>
          <h2 id={`danger-${site.name}`}>{t("sites.details.danger")}</h2>
          <Availability site={site} onChange={onChange} />
          {hasAccount(site) && ["redirect", "proxy"].includes(siteType(site)) && <RemoveAccountCard site={site} onChange={onChange} />}
          <DeleteSite site={site} canPark />
        </section>
      )}
    </>
  );
}

// RenameCard sets the display name (ADR-0072). The system name stays, and
// the card says so with the name in code style.
function RenameCard({ site, onChange, onClose }: TabProps & { onClose: () => void }) {
  const { t } = useTranslation();
  const [title, setTitleText] = useState(site.title ?? "");
  const { busy, act, result } = useAction(onChange);
  const current = site.title ?? "";
  const next = title.trim();

  useEffect(() => setTitleText(site.title ?? ""), [site.title]);

  function save(e: FormEvent, value: string) {
    e.preventDefault();
    void act(async () => {
      await setTitle(site.name, value);
      return value ? t("sites.rename.shownAs", { title: value }) : t("sites.rename.removed");
    });
  }

  return (
    <Card title={t("sites.rename.title")} id={cardId(site.name, "rename")} actions={<CloseButton onClose={onClose} />}>
      <form className="ui-stack" onSubmit={(e) => save(e, next)} noValidate>
        <Field
          label={t("sites.rename.label")}
          hint={<Trans i18nKey={hasAccount(site) ? "sites.rename.hintAccount" : "sites.rename.hint"} values={{ max: maxTitleLength, name: site.name }} components={{ code: <code /> }} />}
        >
          <input className="ui-input" value={title} onChange={(e) => setTitleText(e.target.value)} maxLength={maxTitleLength} placeholder={site.name} autoComplete="off" />
        </Field>
        {result}
        <div className="ui-row">
          <Button type="submit" variant="primary" disabled={busy || next === current}>
            {t("sites.rename.save")}
          </Button>
          {current && (
            <Button type="button" variant="secondary" disabled={busy} onClick={(e) => save(e, "")}>
              {t("sites.rename.remove")}
            </Button>
          )}
        </div>
      </form>
    </Card>
  );
}

// useMaintenancePages lists the maintenance pages to choose from; the current
// choice stays in the list even when the list could not be loaded.
function useMaintenancePages(current: string): { id: string; name: string }[] {
  const [pages, setPages] = useState<StatusPage[] | null>(null);

  useEffect(() => {
    listPages()
      .then((all) => setPages(all.filter((p) => p.kind === "maintenance")))
      .catch(() => setPages([]));
  }, []);

  const choices: { id: string; name: string }[] = [...(pages ?? [])];
  if (!choices.some((p) => p.id === current)) {
    choices.unshift({ id: current, name: current === "builtin-maintenance" ? i18n.t("sites.availability.builtin") : current });
  }
  return choices;
}

function PageSelect({ label, value, choices, onChange }: { label: string; value: string; choices: { id: string; name: string }[]; onChange: (v: string) => void }) {
  return (
    <Field label={label}>
      <select className="ui-select" value={value} onChange={(e) => onChange(e.target.value)}>
        {choices.map((p) => (
          <option key={p.id} value={p.id}>
            {p.name}
          </option>
        ))}
      </select>
    </Field>
  );
}

function Availability({ site, onChange }: TabProps) {
  const { t } = useTranslation();
  const { busy, act, result } = useAction(onChange);
  const [confirm, setConfirm] = useState(false);
  const [page, setPage] = useState(site.disabledPage);
  const choices = useMaintenancePages(page);

  const toggle = (on: boolean) =>
    act(async () => {
      await setDisabled(site.name, on, page);
      setConfirm(false);
      return t(on ? "sites.availability.disabledDone" : "sites.availability.enabledDone", { site: site.name });
    });

  const changePage = () =>
    act(async () => {
      await setDisabled(site.name, true, page);
      return t("sites.availability.pageChanged");
    });

  return (
    <Card title={t("sites.availability.title")}>
      <div className="ui-stack">
        {site.disabled ? (
          <>
            <p className="ui-hint">{t("sites.availability.disabledText")}</p>
            <PageSelect label={t("sites.availability.page")} value={page} choices={choices} onChange={setPage} />
            <div className="ui-row">
              <Button disabled={busy || page === site.disabledPage} onClick={() => void changePage()}>
                {t("sites.availability.changePage")}
              </Button>
              <Button disabled={busy} onClick={() => void toggle(false)}>
                {t("sites.availability.enable", { site: site.name })}
              </Button>
            </div>
          </>
        ) : confirm ? (
          <>
            <p>{t("sites.availability.confirmText")}</p>
            <PageSelect label={t("sites.availability.page")} value={page} choices={choices} onChange={setPage} />
            <div className="ui-row">
              <Button variant="danger" disabled={busy} onClick={() => void toggle(true)}>
                {t("sites.availability.disable", { site: site.name })}
              </Button>
              <Button disabled={busy} onClick={() => setConfirm(false)}>
                {t("common.actions.cancel")}
              </Button>
            </div>
          </>
        ) : (
          <>
            <p className="ui-hint">
              {t(runtimeWord(site) === "PHP" ? "sites.availability.hintPhp" : runtimeWord(site) === "app" ? "sites.availability.hintApp" : "sites.availability.hint")}
            </p>
            <div className="ui-row">
              <Button variant="danger" disabled={busy} onClick={() => setConfirm(true)}>
                {t("sites.availability.disableSite")}
              </Button>
            </div>
          </>
        )}
        {result}
      </div>
    </Card>
  );
}

function DeleteSite({ site, canPark }: { site: Site; canPark: boolean }) {
  const { t } = useTranslation();
  const [mode, setMode] = useState<"" | "delete" | "park">("");
  const [typed, setTyped] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  // Delete and park (ADR-0048): every domain is checked at first.
  const [chosen, setChosen] = useState<string[]>(site.domains);
  const [pages, setPages] = useState<StatusPage[]>([]);
  const [page, setPage] = useState("");
  const [tls, setTls] = useState(site.tls.enabled);

  useEffect(() => {
    if (mode !== "park") return;
    listPages()
      .then((all) => setPages(all.filter((p) => p.kind === "parking")))
      .catch(() => setPages([]));
  }, [mode]);

  async function drop(e: FormEvent) {
    e.preventDefault();
    setBusy(true);
    setError("");
    try {
      if (mode === "park") {
        await deleteAndPark(site.name, { domains: chosen, page, tls, confirm: typed });
        window.location.hash = formatHash({ page: "parked" });
      } else {
        await deleteSite(site.name);
        window.location.hash = formatHash({ page: "sites" });
      }
    } catch (err) {
      setError(siteMessage(err));
      setBusy(false);
    }
  }

  function cancel() {
    setMode("");
    setTyped("");
    setError("");
    setChosen(site.domains);
  }

  const toggle = (d: string, on: boolean) => setChosen((c) => (on ? site.domains.filter((x) => x === d || c.includes(x)) : c.filter((x) => x !== d)));

  return (
    <Card title={t("sites.delete.title")}>
      {mode === "" ? (
        <div className="ui-stack">
          <p className="ui-hint">
            {t(hasAccount(site) ? "sites.delete.hintAccount" : site.proxy ? "sites.delete.hintProxy" : "sites.delete.hintRedirect")}
          </p>
          {canPark && <p className="ui-hint ui-flush">{t("sites.delete.parkHint")}</p>}
          <div className="ui-row">
            <Button variant="danger" onClick={() => setMode("delete")}>
              {t("sites.delete.delete")}
            </Button>
            {canPark && (
              <Button variant="danger" onClick={() => setMode("park")}>
                {t("sites.delete.deleteAndPark")}
              </Button>
            )}
          </div>
        </div>
      ) : (
        <form className="ui-stack" onSubmit={(e) => void drop(e)}>
          {mode === "park" && (
            <>
              <fieldset className="ui-fieldset ui-stack-tight">
                <legend>{t("sites.delete.domainsToPark")}</legend>
                {site.domains.map((d) => (
                  <label key={d} className="ui-check">
                    <input type="checkbox" checked={chosen.includes(d)} onChange={(e) => toggle(d, e.target.checked)} />
                    {d}
                  </label>
                ))}
                <p className="ui-hint ui-flush">{t("sites.delete.leftOut")}</p>
              </fieldset>
              <div className="ui-narrow">
                <Field label={t("sites.delete.parkingPage")}>
                  <select className="ui-select" value={page} onChange={(e) => setPage(e.target.value)}>
                    <option value="">{t("sites.delete.builtinParking")}</option>
                    {pages
                      .filter((p) => !p.builtin)
                      .map((p) => (
                        <option key={p.id} value={p.id}>
                          {p.name}
                        </option>
                      ))}
                  </select>
                </Field>
              </div>
              <label className="ui-check">
                <input type="checkbox" checked={tls} onChange={(e) => setTls(e.target.checked)} />
                {t("sites.delete.tls")}
              </label>
            </>
          )}
          <div className="ui-narrow">
            <Field
              label={<Trans i18nKey="sites.confirm.type" values={{ name: site.name }} components={{ code: <code className="ui-confirm-phrase" /> }} />}
              hint={t("sites.delete.confirmHint")}
            >
              <input className="ui-input" value={typed} onChange={(e) => setTyped(e.target.value)} autoComplete="off" spellCheck={false} />
            </Field>
          </div>
          <div className="ui-row">
            <Button type="submit" variant="danger" disabled={busy || typed !== site.name || (mode === "park" && chosen.length === 0)}>
              {t(mode === "park" ? "sites.delete.submitPark" : "sites.delete.submit", { site: site.name })}
            </Button>
            <Button disabled={busy} onClick={cancel}>
              {t("common.actions.cancel")}
            </Button>
          </div>
          {error && <Notice tone="err" title={error} />}
        </form>
      )}
    </Card>
  );
}

function DomainsTab({ site, onChange }: TabProps) {
  return (
    <>
      <PendingDomainsNotice site={site} onChange={onChange} />
      <DomainsCard site={site} onChange={onChange} />
      {siteType(site) !== "redirect" && <WWWCard site={site} onChange={onChange} onDomains={() => undefined} />}
    </>
  );
}

function HTTPSTab({ site, onChange, staff }: TabProps & { staff: boolean }) {
  const { t } = useTranslation();
  const h = useAction(onChange);
  // Turning HTTPS off and HSTS on are asked again: the first serves the
  // site over plain HTTP, the second sticks in browsers for a long time.
  const [ask, setAsk] = useState<"tls-off" | "hsts-on" | null>(null);

  const toggleTLS = (on: boolean) =>
    h.act(async () => {
      const s = await setTLS(site.name, on);
      if (!on) return t("sites.httpsTab.off");
      return s.tls.active ? t("sites.httpsTab.on") : t("sites.httpsTab.onWaiting");
    });

  const toggleHSTS = (on: boolean) =>
    h.act(async () => {
      await setHSTS(site.name, on);
      return on ? t("sites.httpsTab.hstsOn") : t("sites.httpsTab.hstsOff");
    });

  return (
    <>
      <Card title={t("sites.httpsTab.title")} actions={<AppliesAtOnce />}>
        <div className="ui-stack">
          <Switch
            checked={site.tls.enabled}
            disabled={h.busy || ask !== null || !staff}
            hint={staff ? t("sites.httpsTab.tlsHint") : t("sites.httpsTab.tlsHintReadOnly")}
            onChange={(on) => (on ? void toggleTLS(true) : setAsk("tls-off"))}
          >
            {site.tls.source === "custom" ? t("sites.httpsTab.own") : t("sites.httpsTab.letsEncrypt")}
          </Switch>
          <Switch
            checked={site.tls.hsts}
            disabled={h.busy || ask !== null}
            hint={t("sites.httpsTab.hstsHint")}
            onChange={(on) => (on ? setAsk("hsts-on") : void toggleHSTS(false))}
          >
            {t("sites.httpsTab.hsts")}
          </Switch>
          {ask === "tls-off" && (
            <Notice tone="warn" title={t("sites.httpsTab.askOff")}>
              <p className="ui-flush">{t("sites.httpsTab.askOffText")}</p>
              <div className="ui-row">
                <Button variant="danger" disabled={h.busy} onClick={() => void toggleTLS(false).finally(() => setAsk(null))}>
                  {t("sites.httpsTab.turnOff")}
                </Button>
                <Button disabled={h.busy} onClick={() => setAsk(null)}>
                  {t("common.actions.cancel")}
                </Button>
              </div>
            </Notice>
          )}
          {ask === "hsts-on" && (
            <Notice tone="warn" title={t("sites.httpsTab.askHsts")}>
              <p className="ui-flush">{t("sites.httpsTab.askHstsText")}</p>
              <div className="ui-row">
                <Button variant="primary" disabled={h.busy} onClick={() => void toggleHSTS(true).finally(() => setAsk(null))}>
                  {t("sites.httpsTab.turnHstsOn")}
                </Button>
                <Button disabled={h.busy} onClick={() => setAsk(null)}>
                  {t("common.actions.cancel")}
                </Button>
              </div>
            </Notice>
          )}
          {site.tls.hsts && !site.tls.active && <p className="ui-hint">{t("sites.httpsTab.hstsOnlyHttps")}</p>}
          {h.result}
        </div>
      </Card>
      <ReadOnly when={!staff}>
        <CertificateCard base={siteCertBase(site.name)} name={site.name} tls={site.tls} onChange={onChange} />
      </ReadOnly>
    </>
  );
}

// ReadOnly shows settings a role may see but not change: a note on top and
// every control below it switched off. The server refuses the change anyway.
function ReadOnly({ when, adminOnly = false, children }: { when: boolean; adminOnly?: boolean; children: ReactNode }) {
  const { t } = useTranslation();
  if (!when) return <>{children}</>;
  return (
    <>
      <Notice tone="info" title={t(adminOnly ? "sites.page.readOnlyAdmin" : "sites.page.readOnlyStaff")} />
      <fieldset className="ui-fieldset ui-stack" disabled aria-label={t("sites.page.readOnly")}>
        {children}
      </fieldset>
    </>
  );
}

function PHPTab({ site, php, onChange, staff }: TabProps & { php: PHPVersions | null; staff: boolean }) {
  const { t } = useTranslation();
  const [version, setVersion] = useState(site.php);
  const v = useAction(onChange);
  const p = useAction(onChange);

  function savePHP(e: FormEvent) {
    e.preventDefault();
    void v.act(async () => {
      await setPHP(site.name, version);
      return version ? t("sites.phpTab.runsOn", { version }) : t("sites.phpTab.off");
    });
  }

  const togglePrograms = (on: boolean) =>
    p.act(async () => {
      await setPHPPrograms(site.name, on);
      return on ? t("sites.phpTab.programsOn") : t("sites.phpTab.programsOff");
    });

  return (
    <>
      <Card title={t("sites.phpTab.title")}>
        <form className="ui-stack" onSubmit={savePHP} noValidate>
          <Field label={t("sites.phpTab.label", { site: site.name })}>
            <select className="ui-select" value={version} onChange={(e) => setVersion(e.target.value)}>
              <PHPOptions php={php} current={site.php} withoutNone />
            </select>
          </Field>
          {php?.legacy.includes(version) && version !== site.php && (
            <p className="ui-hint">{t("sites.create.legacy", { version })}</p>
          )}
          <p className="ui-hint">
            <Trans
              i18nKey="sites.phpTab.onlyInstalled"
              components={{ php: <a href={formatHash({ page: "php" })} />, web: <a href={formatHash({ page: "sites", site: { name: site.name, tab: "web" } })} /> }}
            />
          </p>
          {v.result}
          <div className="ui-row">
            <Button type="submit" variant="primary" disabled={v.busy || version === site.php}>
              {t("sites.phpTab.save")}
            </Button>
          </div>
        </form>
      </Card>
      {site.php && (
        <Card title={t("sites.phpTab.programs")} actions={<AppliesAtOnce />}>
          <div className="ui-stack">
            <Switch
              checked={site.phpPrograms}
              disabled={p.busy || !staff}
              onChange={(on) => void togglePrograms(on)}
              hint={(staff ? "" : t("sites.phpTab.programsReadOnly") + " ") + t("sites.phpTab.programsHint")}
            >
              {t("sites.phpTab.programsSwitch")}
            </Switch>
            {p.result}
          </div>
        </Card>
      )}
    </>
  );
}

// MaintenanceSettings switches the maintenance page of a site: addresses
// outside the list get the page with 503, the listed ones see the site.
function MaintenanceSettings({ site, onChange }: { site: Site; onChange: () => Promise<void> }) {
  const { t } = useTranslation();
  const m = site.maintenance;
  const [page, setPage] = useState(m.page);
  const choices = useMaintenancePages(page);
  const [allow, setAllow] = useState(m.allowIps.join("\n"));
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [done, setDone] = useState("");
  const [editing, setEditing] = useState(false);

  async function save(enabled: boolean, e?: FormEvent) {
    e?.preventDefault();
    const list = parseAllowIps(allow);
    const problem = allowIpsProblem(list);
    if (problem) {
      setError(problem);
      setDone("");
      return;
    }
    setBusy(true);
    setError("");
    setDone("");
    try {
      const s = await setMaintenance(site.name, { enabled, page, allowIps: list });
      setAllow(s.maintenance.allowIps.join("\n"));
      await onChange();
      setDone(
        !enabled
          ? t("sites.maintenance.off")
          : s.maintenance.allowIps.length > 0
            ? t("sites.maintenance.onListed")
            : t("sites.maintenance.on"),
      );
    } catch (err) {
      setError(pageMessage(err));
    } finally {
      setBusy(false);
    }
  }

  async function addMine() {
    setError("");
    try {
      const ip = await clientIp();
      if (!ip) {
        setError(t("sites.maintenance.noIp"));
        return;
      }
      const list = parseAllowIps(allow);
      if (!list.includes(ip)) setAllow([...list, ip].join("\n"));
    } catch (err) {
      setError(pageMessage(err));
    }
  }

  const rt = runtimeWord(site);
  const keeps = rt === "PHP" ? t("sites.maintenance.keepsPhp") : rt === "app" ? t("sites.maintenance.keepsApp") : "";

  const pageName = choices.find((c) => c.id === m.page)?.name ?? m.page;
  const allowed = m.allowIps.length === 0 ? t("sites.maintenance.none") : m.allowIps.length <= 2 ? m.allowIps.join(", ") : t("sites.maintenance.addresses", { count: m.allowIps.length });

  return (
    <div className="ui-stack">
      <Switch checked={m.enabled} disabled={busy} onChange={(on) => void save(on)} hint={(m.enabled ? t("sites.maintenance.hintOn") : t("sites.maintenance.hintOff")) + keeps}>
        {t("sites.maintenance.switch")}
      </Switch>
      {site.disabled && <Notice tone="warn" title={t("sites.maintenance.disabledWins")} />}
      {!editing ? (
        <p className="ui-hint ui-flush">
          {t("sites.maintenance.summary", { page: pageName, allowed })}{" "}
          <button type="button" className="ui-link-btn" onClick={() => setEditing(true)}>
            {t("sites.maintenance.change")}
          </button>
        </p>
      ) : (
        <form className="ui-stack" onSubmit={(e) => void save(m.enabled, e)} noValidate>
          <fieldset className="ui-fieldset ui-stack">
            <legend className="ui-sr-only">{t("sites.maintenance.legend", { site: site.name })}</legend>
            <PageSelect label={t("sites.maintenance.page")} value={page} choices={choices} onChange={setPage} />
            <Field label={t("sites.maintenance.allowed")} hint={t("sites.maintenance.allowedHint")}>
              <textarea className="ui-input ui-textarea" value={allow} onChange={(e) => setAllow(e.target.value)} rows={3} autoComplete="off" spellCheck={false} />
            </Field>
            <div className="ui-row">
              <Button disabled={busy} onClick={() => void addMine()}>
                {t("sites.maintenance.addMine")}
              </Button>
              <Button variant="primary" type="submit" disabled={busy}>
                {t("sites.maintenance.save")}
              </Button>
              <Button disabled={busy} onClick={() => setEditing(false)}>
                {t("sites.maintenance.close")}
              </Button>
            </div>
          </fieldset>
        </form>
      )}
      {error && <Notice tone="err" title={error} />}
      {done && <Notice tone="ok" title={done} />}
    </div>
  );
}
