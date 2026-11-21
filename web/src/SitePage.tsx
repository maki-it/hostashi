// SPDX-License-Identifier: AGPL-3.0-or-later

import { useEffect, useState, type FormEvent, type ReactNode } from "react";
import { CertificateCard } from "./Certificate";
import { CronSettings } from "./Cron";
import { LimitSettings } from "./Limits";
import { allowIpsProblem, clientIp, listPages, pageMessage, parseAllowIps, setMaintenance, type StatusPage } from "./pages";
import { formatHash, type SiteTab } from "./routes";
import { PHPOptions, useSites } from "./Sites";
import {
  deleteSite,
  domainsProblem,
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
  type PHPVersions,
  type Site,
} from "./sites";
import { SnippetSettings } from "./Snippets";
import { SSHSettings } from "./SSH";
import { Badge, Button, Card, Field, Notice, tabPanelProps, Tabs } from "./ui";

// The tabs that exist today (ADR-0037). Files, databases and backups per
// site come with their features; their addresses show the overview.
const tabs: { id: SiteTab; label: string }[] = [
  { id: "overview", label: "Overview" },
  { id: "domains", label: "Domains & HTTPS" },
  { id: "php", label: "PHP" },
  { id: "cron", label: "Cron" },
  { id: "ssh", label: "SSH & SFTP" },
  { id: "limits", label: "Limits" },
  { id: "nginx", label: "nginx" },
];

type Props = {
  name: string;
  tab: SiteTab;
  /** Opens another tab: a new entry in the browser history. */
  onTab: (tab: SiteTab) => void;
};

// SitePage shows one site with its settings in tabs (#/sites/<name>/<tab>).
export function SitePage({ name, tab, onTab }: Props) {
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
  const active = ready && tabs.some((t) => t.id === tab) ? tab : "overview";
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
          </p>
        </div>
      </div>
      {error && <Notice tone="err" title={error} />}
      {site.tls.enabled && !site.tls.active && site.tls.error && (
        <Notice tone="err" title="The certificate could not be issued">
          Last attempt failed: {site.tls.error}. Hostashi tries again within an hour.
        </Notice>
      )}
      {ready && <Tabs label={site.name} tabs={tabs} active={active} onChange={(t) => onTab(t as SiteTab)} idPrefix={prefix} />}
      <div {...(ready ? tabPanelProps(prefix, active) : {})} className="ui-stack">
        {active === "overview" && <Overview site={site} onChange={load} />}
        {active === "domains" && <DomainsTab site={site} onChange={load} />}
        {active === "php" && <PHPTab site={site} php={php} onChange={load} />}
        {active === "cron" && <CronSettings site={site} />}
        {active === "ssh" && <SSHSettings site={site} />}
        {active === "limits" && <LimitSettings site={site} />}
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

function Overview({ site, onChange }: TabProps) {
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
                  ? "Disabled: visitors get “temporarily unavailable”, PHP is stopped"
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
          <dt>PHP</dt>
          <dd>{site.php ? "PHP " + site.php + (site.disabled ? " (stopped)" : "") : "No PHP"}</dd>
          <dt>HTTPS</dt>
          <dd>
            {httpsLabel(site.tls)}
            {site.tls.active && site.tls.hsts ? ", HSTS" : ""}
            {site.tls.active && site.tls.notAfter ? ", valid until " + site.tls.notAfter.slice(0, 10) : ""}
          </dd>
          <dt>Storage pool</dt>
          <dd className="ui-mono">{site.pool}</dd>
        </dl>
      </Card>
      {site.state === "active" && (
        <>
          <Card title="Maintenance">
            <MaintenanceSettings site={site} onChange={onChange} />
          </Card>
          <Availability site={site} onChange={onChange} />
          <DeleteSite site={site} />
        </>
      )}
    </>
  );
}

function Availability({ site, onChange }: TabProps) {
  const { busy, act, result } = useAction(onChange);
  const [confirm, setConfirm] = useState(false);

  const toggle = (on: boolean) =>
    act(async () => {
      await setDisabled(site.name, on);
      setConfirm(false);
      return on ? `${site.name} is disabled.` : `${site.name} is enabled again.`;
    });

  return (
    <Card title="Availability">
      <div className="ui-stack">
        {site.disabled ? (
          <>
            <p className="ui-hint">The site is disabled. Enabling it serves its files and starts PHP again.</p>
            <div className="ui-row">
              <Button disabled={busy} onClick={() => void toggle(false)}>
                Enable {site.name}
              </Button>
            </div>
          </>
        ) : confirm ? (
          <>
            <p>
              Visitors get “temporarily unavailable” (HTTP 503) and PHP stops. Files, databases, backups and the certificate stay. You can
              enable the site again at any time.
            </p>
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
            <p className="ui-hint">A disabled site answers “temporarily unavailable” and its PHP is stopped.</p>
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

function DeleteSite({ site }: { site: Site }) {
  const [confirming, setConfirming] = useState(false);
  const [typed, setTyped] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");

  async function drop(e: FormEvent) {
    e.preventDefault();
    setBusy(true);
    setError("");
    try {
      await deleteSite(site.name);
      window.location.hash = formatHash({ page: "sites" });
    } catch (err) {
      setError(siteMessage(err));
      setBusy(false);
    }
  }

  return (
    <Card title="Delete site">
      {!confirming ? (
        <div className="ui-stack">
          <p className="ui-hint">Deletes the site with its files and its databases. Backups stay until they expire.</p>
          <div className="ui-row">
            <Button variant="danger" onClick={() => setConfirming(true)}>
              Delete
            </Button>
          </div>
        </div>
      ) : (
        <form className="ui-stack" onSubmit={(e) => void drop(e)}>
          <div className="ui-narrow">
            <Field label={`Type ${site.name} to confirm`} hint="The site is deleted with its files and its databases. This cannot be undone.">
              <input className="ui-input" value={typed} onChange={(e) => setTyped(e.target.value)} autoComplete="off" spellCheck={false} />
            </Field>
          </div>
          <div className="ui-row">
            <Button type="submit" variant="danger" disabled={busy || typed !== site.name}>
              Delete {site.name}
            </Button>
            <Button
              disabled={busy}
              onClick={() => {
                setConfirming(false);
                setTyped("");
              }}
            >
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
      <CertificateCard site={site} onChange={onChange} />
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
              <PHPOptions php={php} current={site.php} />
            </select>
          </Field>
          {php?.legacy.includes(version) && version !== site.php && (
            <p className="ui-hint">PHP {version} gets no or only security fixes upstream. Use it only for software that needs it.</p>
          )}
          <p className="ui-hint">
            Only installed versions are listed. <a href={formatHash({ page: "php" })}>Install more PHP versions</a>
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
  const [pages, setPages] = useState<StatusPage[] | null>(null);
  const [page, setPage] = useState(m.page);
  const [allow, setAllow] = useState(m.allowIps.join("\n"));
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [done, setDone] = useState("");

  useEffect(() => {
    listPages()
      .then((all) => setPages(all.filter((p) => p.kind === "maintenance")))
      .catch(() => setPages([]));
  }, []);

  const choices: { id: string; name: string }[] = [...(pages ?? [])];
  if (!choices.some((p) => p.id === page)) {
    choices.unshift({ id: page, name: page === "builtin-maintenance" ? "Built-in maintenance page" : page });
  }

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
        {site.disabled && <Notice tone="warn" title="The site is disabled. Disabled wins: visitors get “temporarily unavailable” until it is enabled." />}
        <Field label="Maintenance page">
          <select className="ui-select" value={page} onChange={(e) => setPage(e.target.value)}>
            {choices.map((p) => (
              <option key={p.id} value={p.id}>
                {p.name}
              </option>
            ))}
          </select>
        </Field>
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
