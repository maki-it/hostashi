// SPDX-License-Identifier: AGPL-3.0-or-later

import { useEffect, useState, type FormEvent, type ReactNode } from "react";
import { CertificateCard } from "./Certificate";
import { canClone, CloneBadge, CloneCard, CloneStatusCard, useClone } from "./Clone";
import type { Clone } from "./clones";
import { siteCertBase } from "./certs";
import { CronSettings } from "./Cron";
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
  siteType,
  siteTypeLabel,
  maxTitleLength,
  setTitle,
  siteLabel,
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
import { HardeningCard } from "./Hardening";
import { SSHSettings } from "./SSH";
import { PermissionsResetCard } from "./PermissionsReset";
import { Badge, Button, Card, Field, Notice } from "./ui";
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

// SitePage shows one site with its settings in tabs (#/sites/<name>/<tab>).
export function SitePage({ name, tab, onTab, role = "user", path = "", onPath = () => undefined }: Props) {
  const { sites, php, error, load } = useSites();
  const site = sites?.find((s) => s.name === name);
  const { clone, reload: reloadClone } = useClone(name);

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
  const staff = role === "admin" || role === "manager";
  const groups = groupsForSite(site);
  const active: SiteTab = ready && groups.some((g) => g.tabs.some((t) => t.id === tab)) ? tab : "overview";

  const panel = (
    <>
      {active === "overview" && <Overview site={site} onChange={load} staff={staff} admin={role === "admin"} clone={clone ?? null} onCloneChange={reloadClone} />}
      {active === "domains" && <DomainsTab site={site} onChange={load} />}
      {active === "https" && <HTTPSTab site={site} onChange={load} />}
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
          <PHPTab site={site} php={php} onChange={load} />
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
      {active === "limits" && <LimitSettings site={site} />}
      {active === "usage" && <SiteUsageCard site={site.name} />}
      {active === "nginx" && <SnippetSettings site={site} />}
    </>
  );

  return (
    <>
      <div className="ui-page-head">
        <div>
          <h1>{siteLabel(site)}</h1>
          <p className="ui-row">
            {site.title && (
              <span title="Linux user, home folder and SSH login">
                System name <code>{site.name}</code>
              </span>
            )}
            <span className="ui-mono">{site.domains[0]}</span>
            <Badge tone={status.tone}>{status.word}</Badge>
            <Badge tone={httpsTone(site.tls)}>{httpsLabel(site.tls)}</Badge>
            <Badge tone="off">{siteTypeLabel(site)}</Badge>
            <CloneBadge clone={clone} />
          </p>
        </div>
      </div>
      {error && <Notice tone="err" title={error} />}
      {site.tls.enabled && !site.tls.active && site.tls.error && (
        <Notice tone="err" title="The certificate could not be issued">
          Last attempt failed: {site.tls.error}. Hostashi tries again within an hour.
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

function Overview({ site, onChange, staff, admin, clone, onCloneChange }: TabProps & { staff: boolean; admin: boolean; clone: Clone | null; onCloneChange: () => void }) {
  const [primary, ...aliases] = site.domains;
  return (
    <>
      <Card title="Details">
        <dl className="ui-facts" aria-label={"Details of " + site.name}>
          <dt>System name</dt>
          <dd>
            <code>{site.name}</code>
            {hasAccount(site) ? " (Linux user, home folder and SSH login)" : ""}
          </dd>
          {site.title && (
            <>
              <dt>Display name</dt>
              <dd>{site.title}</dd>
            </>
          )}
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
      {clone && <CloneStatusCard site={site} clone={clone} staff={staff} onRefresh={onCloneChange} />}
      {site.state !== "deleting" && <RenameCard site={site} onChange={onChange} />}
      {staff && canClone(site) && clone?.state !== "copying" && <CloneCard site={site} />}
      {admin && hasAccount(site) && <Services site={site.name} />}
      {site.state === "active" && (
        <>
          <Card title="Maintenance">
            <MaintenanceSettings site={site} onChange={onChange} />
          </Card>
          <section className="ui-danger-zone" aria-labelledby={`danger-${site.name}`}>
            <h2 id={`danger-${site.name}`}>Danger zone</h2>
            <Availability site={site} onChange={onChange} />
            {staff && hasAccount(site) && ["redirect", "proxy"].includes(siteType(site)) && <RemoveAccountCard site={site} onChange={onChange} />}
            <DeleteSite site={site} canPark={staff} />
          </section>
        </>
      )}
    </>
  );
}

// RenameCard sets the display name (ADR-0072). The system name stays, and
// the card says so with the name in code style.
function RenameCard({ site, onChange }: TabProps) {
  const [title, setTitleText] = useState(site.title ?? "");
  const { busy, act, result } = useAction(onChange);
  const current = site.title ?? "";
  const next = title.trim();

  useEffect(() => setTitleText(site.title ?? ""), [site.title]);

  function save(e: FormEvent, value: string) {
    e.preventDefault();
    void act(async () => {
      await setTitle(site.name, value);
      return value ? `The site is now shown as ${value}.` : "The display name is removed.";
    });
  }

  return (
    <Card title="Rename">
      <form className="ui-stack" onSubmit={(e) => save(e, next)} noValidate>
        <Field
          label="Display name"
          hint={
            <>
              Shown in the panel instead of the system name, up to {maxTitleLength} characters. The system name <code>{site.name}</code>
              {hasAccount(site) ? " stays: Linux user, home folder, SSH login and API keep it." : " stays in the API."}
            </>
          }
        >
          <input className="ui-input" value={title} onChange={(e) => setTitleText(e.target.value)} maxLength={maxTitleLength} placeholder={site.name} autoComplete="off" />
        </Field>
        {result}
        <div className="ui-row">
          <Button type="submit" variant="primary" disabled={busy || next === current}>
            Save display name
          </Button>
          {current && (
            <Button type="button" variant="secondary" disabled={busy} onClick={(e) => save(e, "")}>
              Remove display name
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

function DomainsTab({ site, onChange }: TabProps) {
  return (
    <>
      <PendingDomainsNotice site={site} onChange={onChange} />
      <DomainsCard site={site} onChange={onChange} />
      {siteType(site) !== "redirect" && <WWWCard site={site} onChange={onChange} onDomains={() => undefined} />}
    </>
  );
}

function HTTPSTab({ site, onChange }: TabProps) {
  const h = useAction(onChange);
  // Turning HTTPS off and HSTS on are asked again: the first serves the
  // site over plain HTTP, the second sticks in browsers for a long time.
  const [ask, setAsk] = useState<"tls-off" | "hsts-on" | null>(null);

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
      <Card title="HTTPS">
        <div className="ui-stack">
          <label className="ui-check">
            <input
              type="checkbox"
              checked={site.tls.enabled}
              disabled={h.busy || ask !== null}
              onChange={(e) => (e.target.checked ? void toggleTLS(true) : setAsk("tls-off"))}
            />
            {site.tls.source === "custom" ? "HTTPS with the own certificate" : "HTTPS with a Let's Encrypt certificate"}
          </label>
          <label className="ui-check">
            <input
              type="checkbox"
              checked={site.tls.hsts}
              disabled={h.busy || ask !== null}
              onChange={(e) => (e.target.checked ? setAsk("hsts-on") : void toggleHSTS(false))}
            />
            HSTS: browsers use only HTTPS for this site
          </label>
          {ask === "tls-off" && (
            <Notice tone="warn" title="Turn HTTPS off?">
              <p className="ui-flush">
                The site is then served over plain HTTP: passwords and cookies travel unencrypted. Browsers that know HSTS for it refuse to open
                it.
              </p>
              <div className="ui-row">
                <Button variant="danger" disabled={h.busy} onClick={() => void toggleTLS(false).finally(() => setAsk(null))}>
                  Turn HTTPS off
                </Button>
                <Button disabled={h.busy} onClick={() => setAsk(null)}>
                  Cancel
                </Button>
              </div>
            </Notice>
          )}
          {ask === "hsts-on" && (
            <Notice tone="warn" title="Turn HSTS on?">
              <p className="ui-flush">
                Browsers that visit the site remember for a year to use only HTTPS, even if HTTPS is turned off here later. Turn it on once the
                certificate works for every domain of the site.
              </p>
              <div className="ui-row">
                <Button variant="primary" disabled={h.busy} onClick={() => void toggleHSTS(true).finally(() => setAsk(null))}>
                  Turn HSTS on
                </Button>
                <Button disabled={h.busy} onClick={() => setAsk(null)}>
                  Cancel
                </Button>
              </div>
            </Notice>
          )}
          {site.tls.hsts && !site.tls.active && <p className="ui-hint">HSTS is sent only while the site is served over HTTPS.</p>}
          {h.result}
        </div>
      </Card>
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
