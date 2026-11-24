// SPDX-License-Identifier: AGPL-3.0-or-later

import { useEffect, useState, type FormEvent, type ReactNode } from "react";
import { CertificateCard } from "./Certificate";
import { siteCertBase } from "./certs";
import { CronSettings } from "./Cron";
import { FileManager } from "./Files";
import { canDeploy } from "./git";
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
  domainsProblem,
  hasAccount,
  httpsLabel,
  httpsTone,
  parseDomains,
  setDisabled,
  setDomains,
  setHSTS,
  setPHP,
  setPHPPrograms,
  setTLS,
  siteMessage,
  siteStatus,
  siteType,
  siteTypeLabel,
  type PHPVersions,
  type Site,
} from "./sites";
import { PageCacheSettings } from "./PageCache";
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
import { SSHSettings } from "./SSH";
import { PermissionsResetCard } from "./PermissionsReset";
import { Badge, Button, Card, Field, Notice, tabPanelProps, Tabs } from "./ui";
import { SiteUsageCard } from "./Usage";
import { WWWCard } from "./WWW";

// The tabs that exist today (ADR-0037). Databases and backups per site
// come with their features; their addresses show the overview.
const tabs: { id: SiteTab; label: string }[] = [
  { id: "overview", label: "Overview" },
  { id: "domains", label: "Domains & HTTPS" },
  { id: "access", label: "Access" },
  { id: "web", label: "Web" },
  { id: "files", label: "Files" },
  { id: "git", label: "Git" },
  { id: "malware", label: "Virus scan" },
  { id: "php", label: "PHP" },
  { id: "cron", label: "Cron" },
  { id: "ssh", label: "SSH & SFTP" },
  { id: "limits", label: "Limits" },
  { id: "usage", label: "Usage" },
  { id: "logs", label: "Logs" },
  { id: "nginx", label: "nginx" },
];

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

// SitePage shows one site with its settings in tabs (#/sites/<name>/<tab>).
export function SitePage({ name, tab, onTab, role = "user", path = "", onPath = () => undefined }: Props) {
  const { sites, php, error, load } = useSites();
  const site = sites?.find((s) => s.name === name);

  if (!sites) return error ? <Notice tone="err" title={error} /> : <p aria-busy="true">Loading…</p>;
  if (!site) {
    return (
      <Notice tone="err" title={`There is no site named ${name}.`}>
        <a href={formatHash({ page: "sites" })}>Back to the sites</a>
      </Notice>
    );
  }

  const status = siteStatus(site);
  const ready = site.state === "active";
  // The PHP tab only for sites with PHP; the type is changed on the Web tab.
  // A redirect or reverse proxy that kept its account after a type change
  // keeps these tabs.
  // A site without an account (redirect, reverse proxy) has no files,
  // cron, SSH or limits, and redirect and reverse proxy no snippets
  // (ADR-0054). A redirect answers before nginx checks access, so it has no
  // access tab (ADR-0066).
  const noAccount = ["files", "malware", "php", "cron", "ssh", "limits"];
  const shown = tabs.filter(
    (t) =>
      (t.id !== "php" || site.php !== "") &&
      (t.id !== "git" || canDeploy(site)) &&
      (hasAccount(site) || !noAccount.includes(t.id)) &&
      (!["redirect", "proxy"].includes(siteType(site)) || t.id !== "nginx") &&
      (siteType(site) !== "redirect" || t.id !== "access"),
  );
  const active = ready && shown.some((t) => t.id === tab) ? tab : "overview";
  const prefix = `site-${site.name}`;

  return (
    <>
      <div className="ui-page-head">
        <div>
          <h1>{site.name}</h1>
          <p className="ui-row">
            <span className="ui-mono">{site.domains[0]}</span>
            <Badge tone={status.tone}>{status.word}</Badge>
            <Badge tone={httpsTone(site.tls)}>{httpsLabel(site.tls)}</Badge>
            <Badge tone="off">{siteTypeLabel(site)}</Badge>
          </p>
        </div>
      </div>
      {error && <Notice tone="err" title={error} />}
      {site.tls.enabled && !site.tls.active && site.tls.error && (
        <Notice tone="err" title="The certificate could not be issued">
          Last attempt failed: {site.tls.error}. Hostashi tries again within an hour.
        </Notice>
      )}
      {ready && <Tabs label={site.name} tabs={shown} active={active} onChange={(t) => onTab(t as SiteTab)} idPrefix={prefix} />}
      <div {...(ready ? tabPanelProps(prefix, active) : {})} className="ui-stack">
        {active === "overview" && <Overview site={site} onChange={load} staff={role === "admin" || role === "manager"} />}
        {active === "overview" && role === "admin" && hasAccount(site) && <Services site={site.name} />}
        {active === "domains" && <DomainsTab site={site} onChange={load} admin={role === "admin"} />}
        {active === "access" && <SiteAccess site={site} />}
        {active === "web" && <SiteWebSettings site={site} php={php} onChange={load} admin={role === "admin"} staff={role === "admin" || role === "manager"} />}
        {active === "files" && <FileManager site={site} path={path} onPath={onPath} />}
        {active === "files" && site.state === "active" && <PermissionsResetCard site={site} />}
        {active === "logs" && <SiteLogs site={site} admin={role === "admin"} />}
        {active === "php" && (
          <>
            <PHPTab site={site} php={php} onChange={load} />
            {site.php && <PHPSettingsCard site={site} />}
            {site.php && <ObjectCacheSettings site={site} canSize={role === "admin" || role === "manager"} />}
            {site.php && <PageCacheSettings site={site} canSize={role === "admin" || role === "manager"} />}
          </>
        )}
        {active === "git" && <GitSettings site={site} canEdit={role === "admin" || role === "manager"} />}
        {active === "malware" && <SiteMalware site={site} canManage={role === "admin" || role === "manager"} admin={role === "admin"} />}
        {active === "cron" && <CronSettings site={site} />}
        {active === "ssh" && <SSHSettings site={site} />}
        {active === "limits" && <LimitSettings site={site} />}
        {active === "usage" && <SiteUsageCard site={site.name} />}
        {active === "nginx" && <SnippetSettings site={site} />}
      </div>
    </>
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

function Overview({ site, onChange, staff }: TabProps & { staff: boolean }) {
  const [primary, ...aliases] = site.domains;
  return (
    <>
      <Card title="Details">
        <dl className="ui-facts" aria-label={"Details of " + site.name}>
          <dt>State</dt>
          <dd>
            {site.state === "deleting"
              ? "Being deleted"
              : site.state !== "active"
                ? "Being created"
                : site.disabled
                  ? "Disabled: everybody gets the chosen maintenance page, PHP is stopped"
                  : site.maintenance.enabled
                    ? "Maintenance: visitors get the maintenance page" +
                      (site.maintenance.allowIps.length > 0 ? ", except " + site.maintenance.allowIps.join(", ") : "")
                    : "Active"}
          </dd>
          <dt>Primary domain</dt>
          <dd className="ui-mono">{primary}</dd>
          {aliases.length > 0 && (
            <>
              <dt>Other domains</dt>
              <dd className="ui-mono">{aliases.join(", ")}</dd>
            </>
          )}
          {site.node && (
            <>
              <dt>Node.js app</dt>
              <dd>
                {site.node.startKind ? <span className="ui-mono">{startCommand(site.node)}</span> : "No start command yet"}
                {" on Node.js "}
                {site.node.version}
                {site.node.stopped ? ", stopped" : ""}
              </dd>
            </>
          )}
          {site.python && (
            <>
              <dt>Python app</dt>
              <dd>
                {site.python.startKind ? <span className="ui-mono">{pythonCommand(site.python)}</span> : "No start command yet"}
                {" on Python "}
                {site.python.version}
                {site.python.stopped ? ", stopped" : ""}
              </dd>
            </>
          )}
          {site.app && (
            <>
              <dt>App</dt>
              <dd>
                {site.app.program ? <span className="ui-mono">{programCommand(site.app)}</span> : "No program yet"}
                {site.app.stopped ? ", stopped" : ""}
              </dd>
            </>
          )}
          {site.proxy ? (
            <>
              <dt>Reverse proxy</dt>
              <dd>
                <span className="ui-mono">{site.proxy.target}</span>
                {site.proxy.insecure ? " (certificate not verified)" : ""}
              </dd>
            </>
          ) : site.redirect ? (
            <>
              <dt>Redirect</dt>
              <dd>
                <span className="ui-mono">{site.redirect.target}</span> ({site.redirect.status}
                {site.redirect.keepPath ? ", keeps the path" : ""})
              </dd>
            </>
          ) : (
            <>
              <dt>PHP</dt>
              <dd>{site.php ? "PHP " + site.php + (site.disabled ? " (stopped)" : "") : "No PHP"}</dd>
            </>
          )}
          <dt>HTTPS</dt>
          <dd>
            {httpsLabel(site.tls)}
            {site.tls.active && site.tls.hsts ? ", HSTS" : ""}
            {site.tls.active && site.tls.notAfter ? ", valid until " + site.tls.notAfter.slice(0, 10) : ""}
          </dd>
          {hasAccount(site) && (
            <>
              <dt>Storage pool</dt>
              <dd className="ui-mono">{site.pool}</dd>
            </>
          )}
        </dl>
      </Card>
      {site.state === "active" && (
        <>
          <Card title="Maintenance">
            <MaintenanceSettings site={site} onChange={onChange} />
          </Card>
          <Availability site={site} onChange={onChange} />
          {staff && hasAccount(site) && ["redirect", "proxy"].includes(siteType(site)) && <RemoveAccountCard site={site} onChange={onChange} />}
          <DeleteSite site={site} canPark={staff} />
        </>
      )}
    </>
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
    choices.unshift({ id: current, name: current === "builtin-maintenance" ? "Built-in maintenance page" : current });
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
  const { busy, act, result } = useAction(onChange);
  const [confirm, setConfirm] = useState(false);
  const [page, setPage] = useState(site.disabledPage);
  const choices = useMaintenancePages(page);

  const toggle = (on: boolean) =>
    act(async () => {
      await setDisabled(site.name, on, page);
      setConfirm(false);
      return on ? `${site.name} is disabled.` : `${site.name} is enabled again.`;
    });

  const changePage = () =>
    act(async () => {
      await setDisabled(site.name, true, page);
      return "The page is changed.";
    });

  return (
    <Card title="Availability">
      <div className="ui-stack">
        {site.disabled ? (
          <>
            <p className="ui-hint">
              The site is disabled: visitors get the page below with “503 Service Unavailable”. Enabling it serves its files and starts PHP again.
            </p>
            <PageSelect label="Page while disabled" value={page} choices={choices} onChange={setPage} />
            <div className="ui-row">
              <Button disabled={busy || page === site.disabledPage} onClick={() => void changePage()}>
                Change page
              </Button>
              <Button disabled={busy} onClick={() => void toggle(false)}>
                Enable {site.name}
              </Button>
            </div>
          </>
        ) : confirm ? (
          <>
            <p>
              Visitors get the chosen maintenance page with “503 Service Unavailable” and PHP stops. There are no allowed addresses. Files,
              databases, backups and the certificate stay. You can enable the site again at any time.
            </p>
            <PageSelect label="Page while disabled" value={page} choices={choices} onChange={setPage} />
            <div className="ui-row">
              <Button variant="danger" disabled={busy} onClick={() => void toggle(true)}>
                Disable {site.name}
              </Button>
              <Button disabled={busy} onClick={() => setConfirm(false)}>
                Cancel
              </Button>
            </div>
          </>
        ) : (
          <>
            <p className="ui-hint">A disabled site shows a maintenance page to everybody and its PHP is stopped.</p>
            <div className="ui-row">
              <Button variant="danger" disabled={busy} onClick={() => setConfirm(true)}>
                Disable site
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
    <Card title="Delete site">
      {mode === "" ? (
        <div className="ui-stack">
          <p className="ui-hint">
            {hasAccount(site)
              ? "Deletes the site with its files and its databases. Backups stay until they expire."
              : `Deletes the site and its ${site.proxy ? "reverse proxy" : "redirect"}. It has no files or databases.`}
          </p>
          {canPark && <p className="ui-hint ui-flush">To keep the domains answering, delete the site and park its domains in one step.</p>}
          <div className="ui-row">
            <Button variant="danger" onClick={() => setMode("delete")}>
              Delete
            </Button>
            {canPark && (
              <Button variant="danger" onClick={() => setMode("park")}>
                Delete and park domains
              </Button>
            )}
          </div>
        </div>
      ) : (
        <form className="ui-stack" onSubmit={(e) => void drop(e)}>
          {mode === "park" && (
            <>
              <fieldset className="ui-fieldset ui-stack-tight">
                <legend>Domains to park</legend>
                {site.domains.map((d) => (
                  <label key={d} className="ui-check">
                    <input type="checkbox" checked={chosen.includes(d)} onChange={(e) => toggle(d, e.target.checked)} />
                    {d}
                  </label>
                ))}
                <p className="ui-hint ui-flush">Domains you leave out are free afterwards.</p>
              </fieldset>
              <div className="ui-narrow">
                <Field label="Parking page">
                  <select className="ui-select" value={page} onChange={(e) => setPage(e.target.value)}>
                    <option value="">Built-in parking page</option>
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
                HTTPS with a Let's Encrypt certificate
              </label>
            </>
          )}
          <div className="ui-narrow">
            <Field
              label={
                <>
                  Type <code className="ui-confirm-phrase">{site.name}</code> to confirm
                </>
              }
              hint="The site is deleted with its files and its databases. This cannot be undone."
            >
              <input className="ui-input" value={typed} onChange={(e) => setTyped(e.target.value)} autoComplete="off" spellCheck={false} />
            </Field>
          </div>
          <div className="ui-row">
            <Button type="submit" variant="danger" disabled={busy || typed !== site.name || (mode === "park" && chosen.length === 0)}>
              {mode === "park" ? `Delete ${site.name} and park` : `Delete ${site.name}`}
            </Button>
            <Button disabled={busy} onClick={cancel}>
              Cancel
            </Button>
          </div>
          {error && <Notice tone="err" title={error} />}
        </form>
      )}
    </Card>
  );
}

function DomainsTab({ site, onChange, admin }: TabProps & { admin: boolean }) {
  const [domains, setDomainsText] = useState(site.domains.join("\n"));
  const d = useAction(onChange);
  const h = useAction(onChange);

  function saveDomains(e: FormEvent) {
    e.preventDefault();
    const list = parseDomains(domains);
    const problem = domainsProblem(list);
    if (problem) {
      d.setError(problem);
      d.setDone("");
      return;
    }
    void d.act(async () => {
      await setDomains(site.name, list);
      return site.tls.enabled
        ? "Domains saved. The site is served over HTTP until a certificate for the new domains is there."
        : "Domains saved.";
    });
  }

  const toggleTLS = (on: boolean) =>
    h.act(async () => {
      const s = await setTLS(site.name, on);
      if (!on) return "HTTPS is off. The site is served over HTTP.";
      return s.tls.active ? "HTTPS is on." : "HTTPS is on. The site switches when its certificate is there.";
    });

  const toggleHSTS = (on: boolean) =>
    h.act(async () => {
      await setHSTS(site.name, on);
      return on ? "HSTS is on. Browsers keep using HTTPS for this site, even if HTTPS is turned off later." : "HSTS is off.";
    });

  return (
    <>
      <Card title="Domains">
        <form className="ui-stack" onSubmit={saveDomains} noValidate>
          <Field label={`Domains of ${site.name}`} hint="One per line. The first one is the primary domain.">
            <textarea className="ui-input ui-textarea" value={domains} onChange={(e) => setDomainsText(e.target.value)} rows={4} autoComplete="off" spellCheck={false} />
          </Field>
          {d.result}
          <div className="ui-row">
            <Button type="submit" variant="primary" disabled={d.busy}>
              Save domains
            </Button>
          </div>
        </form>
      </Card>
      {siteType(site) !== "redirect" && <WWWCard site={site} onChange={onChange} onDomains={(l) => setDomainsText(l.join("\n"))} />}
      <Card title="HTTPS">
        <div className="ui-stack">
          <label className="ui-check">
            <input type="checkbox" checked={site.tls.enabled} disabled={h.busy} onChange={(e) => void toggleTLS(e.target.checked)} />
            {site.tls.source === "custom" ? "HTTPS with the own certificate" : "HTTPS with a Let's Encrypt certificate"}
          </label>
          <label className="ui-check">
            <input type="checkbox" checked={site.tls.hsts} disabled={h.busy} onChange={(e) => void toggleHSTS(e.target.checked)} />
            HSTS: browsers use only HTTPS for this site
          </label>
          {site.tls.hsts && !site.tls.active && <p className="ui-hint">HSTS is sent only while the site is served over HTTPS.</p>}
          {h.result}
        </div>
      </Card>
      <SecurityHeadersCard site={site} />
      <RealIPCard site={site} admin={admin} />
      <CertificateCard base={siteCertBase(site.name)} name={site.name} tls={site.tls} onChange={onChange} />
    </>
  );
}

function PHPTab({ site, php, onChange }: TabProps & { php: PHPVersions | null }) {
  const [version, setVersion] = useState(site.php);
  const v = useAction(onChange);
  const p = useAction(onChange);

  function savePHP(e: FormEvent) {
    e.preventDefault();
    void v.act(async () => {
      await setPHP(site.name, version);
      return version ? `The site runs on PHP ${version} now.` : "PHP is off for this site now.";
    });
  }

  const togglePrograms = (on: boolean) =>
    p.act(async () => {
      await setPHPPrograms(site.name, on);
      return on ? "PHP of this site may start programs now." : "PHP of this site can no longer start programs.";
    });

  return (
    <>
      <Card title="PHP version">
        <form className="ui-stack" onSubmit={savePHP} noValidate>
          <Field label={`PHP of ${site.name}`}>
            <select className="ui-select" value={version} onChange={(e) => setVersion(e.target.value)}>
              <PHPOptions php={php} current={site.php} withoutNone />
            </select>
          </Field>
          {php?.legacy.includes(version) && version !== site.php && (
            <p className="ui-hint">PHP {version} gets no or only security fixes upstream. Use it only for software that needs it.</p>
          )}
          <p className="ui-hint">
            Only installed versions are listed. <a href={formatHash({ page: "php" })}>Install more PHP versions</a>. To turn PHP off, make
            it a static site on the <a href={formatHash({ page: "sites", site: { name: site.name, tab: "web" } })}>Web</a> tab.
          </p>
          {v.result}
          <div className="ui-row">
            <Button type="submit" variant="primary" disabled={v.busy || version === site.php}>
              Save PHP version
            </Button>
          </div>
        </form>
      </Card>
      {site.php && (
        <Card title="Programs from PHP">
          <div className="ui-stack">
            <label className="ui-check">
              <input type="checkbox" checked={site.phpPrograms} disabled={p.busy} onChange={(e) => void togglePrograms(e.target.checked)} />
              Allow PHP to start programs (exec, system, shell_exec, proc_open, popen)
            </label>
            <p className="ui-hint">
              Off by default: a hacked plugin then cannot run commands on the server. Turn it on only for software that needs it, for
              example image optimizers or PDF tools. WP-CLI, Composer and cron jobs are not affected.
            </p>
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
  const m = site.maintenance;
  const [page, setPage] = useState(m.page);
  const choices = useMaintenancePages(page);
  const [allow, setAllow] = useState(m.allowIps.join("\n"));
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [done, setDone] = useState("");

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
          ? "Maintenance is off. Everybody sees the site again."
          : s.maintenance.allowIps.length > 0
            ? "Maintenance is on. The listed addresses still see the site."
            : "Maintenance is on. Every visitor gets the maintenance page.",
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
        setError("Your address is not known to the panel.");
        return;
      }
      const list = parseAllowIps(allow);
      if (!list.includes(ip)) setAllow([...list, ip].join("\n"));
    } catch (err) {
      setError(pageMessage(err));
    }
  }

  return (
    <form className="ui-stack" onSubmit={(e) => void save(m.enabled, e)} noValidate>
      <fieldset className="ui-fieldset ui-stack">
        <legend className="ui-sr-only">Maintenance of {site.name}</legend>
        <p className="ui-hint ui-flush">
          {m.enabled
            ? "Maintenance is on: visitors get the page with “503 Service Unavailable”. PHP keeps running."
            : "While maintenance is on, visitors get the page with “503 Service Unavailable”. The allowed addresses see the site as usual, so you can work on it. PHP keeps running."}
        </p>
        {site.disabled && <Notice tone="warn" title="The site is disabled. Disabled wins: everybody gets the page chosen under Availability until it is enabled." />}
        <PageSelect label="Maintenance page" value={page} choices={choices} onChange={setPage} />
        <Field label="Allowed addresses" hint="IPv4 or IPv6 addresses or ranges like 10.0.0.0/8, one per line, at most 20.">
          <textarea className="ui-input ui-textarea" value={allow} onChange={(e) => setAllow(e.target.value)} rows={3} autoComplete="off" spellCheck={false} />
        </Field>
        <div className="ui-row">
          <Button disabled={busy} onClick={() => void addMine()}>
            Add my current IP
          </Button>
          {m.enabled && (
            <Button variant="primary" type="submit" disabled={busy}>
              Save maintenance
            </Button>
          )}
          <Button variant={m.enabled ? "secondary" : "primary"} disabled={busy} onClick={() => void save(!m.enabled)}>
            {m.enabled ? "End maintenance" : "Start maintenance"}
          </Button>
        </div>
        {error && <Notice tone="err" title={error} />}
        {done && <Notice tone="ok" title={done} />}
      </fieldset>
    </form>
  );
}
