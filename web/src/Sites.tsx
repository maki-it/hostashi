// SPDX-License-Identifier: AGPL-3.0-or-later

import { useCallback, useEffect, useState, type FormEvent } from "react";
import { CronSettings } from "./Cron";
import { LimitSettings } from "./Limits";
import { SnippetSettings } from "./Snippets";
import { allowIpsProblem, clientIp, listPages, pageMessage, parseAllowIps, setMaintenance, type StatusPage } from "./pages";
import {
  createSite,
  deleteSite,
  domainsProblem,
  httpsLabel,
  listSites,
  parseDomains,
  phpVersions,
  setDisabled,
  setDomains,
  setHSTS,
  setPHP,
  setTLS,
  siteMessage,
  siteNameProblem,
  type PHPVersions,
  type Site,
} from "./sites";

// Sites is the site page: list the sites with their state, create one with
// its domains, PHP version and HTTPS, and delete one.
export function Sites() {
  const [sites, setSites] = useState<Site[] | null>(null);
  const [php, setPhp] = useState<PHPVersions | null>(null);
  const [error, setError] = useState("");

  const load = useCallback(async () => {
    try {
      setSites(await listSites());
      setError("");
    } catch (err) {
      setError(siteMessage(err));
    }
  }, []);

  // A certificate comes in the background: look again while one is pending.
  const pending = sites?.some((s) => s.state === "active" && s.tls.enabled && !s.tls.active && !s.tls.error) ?? false;
  useEffect(() => {
    if (!pending) return;
    const t = setInterval(() => void load(), 10000);
    return () => clearInterval(t);
  }, [pending, load]);

  useEffect(() => {
    void load();
    // The PHP versions come from the agent; without them a site can still
    // be created without PHP.
    phpVersions()
      .then(setPhp)
      .catch(() => setPhp({ installed: [], supported: [], legacy: [] }));
  }, [load]);

  if (!sites) {
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
      <CreateCard php={php} onChange={load} />
      <section className="card" aria-labelledby="site-list">
        <h2 id="site-list">Sites</h2>
        {error && <p role="alert">{error}</p>}
        {sites.length === 0 ? (
          <p>No sites yet.</p>
        ) : (
          <ul className="rows">
            {sites.map((s) => (
              <SiteRow key={s.name} site={s} php={php} onChange={load} />
            ))}
          </ul>
        )}
      </section>
    </div>
  );
}

// PHPOptions lists the installed versions, newest first, legacy ones marked.
// A current version that is no longer installed stays selectable.
function PHPOptions({ php, current = "" }: { php: PHPVersions | null; current?: string }) {
  const installed = [...(php?.installed ?? [])].reverse();
  if (current && !installed.includes(current)) installed.unshift(current);
  return (
    <>
      <option value="">No PHP</option>
      {installed.map((v) => (
        <option key={v} value={v}>
          PHP {v}
          {php?.legacy.includes(v) ? " (legacy)" : ""}
          {php && !php.installed.includes(v) ? " (not installed)" : ""}
        </option>
      ))}
    </>
  );
}

function CreateCard({ php, onChange }: { php: PHPVersions | null; onChange: () => Promise<void> }) {
  const [name, setName] = useState("");
  const [domains, setDomainsText] = useState("");
  const [version, setVersion] = useState<string | null>(null);
  const [tls, setTls] = useState(true);
  const [hsts, setHsts] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [done, setDone] = useState("");

  // Default to the newest installed version that is not legacy.
  const newest = [...(php?.installed ?? [])].reverse().find((v) => !php?.legacy.includes(v)) ?? "";
  const phpValue = version ?? newest;

  async function submit(e: FormEvent) {
    e.preventDefault();
    const list = parseDomains(domains);
    const problem = siteNameProblem(name) || domainsProblem(list);
    if (problem) {
      setError(problem);
      return;
    }
    setBusy(true);
    setError("");
    setDone("");
    try {
      const s = await createSite({ name, php: phpValue, domains: list, tls, hsts: tls && hsts });
      setDone(
        s.tls.enabled && !s.tls.active
          ? `Site ${s.name} created. It is served over HTTP until its certificate is there.`
          : `Site ${s.name} created.`,
      );
      setName("");
      setDomainsText("");
      setVersion(null);
      await onChange();
    } catch (err) {
      setError(siteMessage(err));
    } finally {
      setBusy(false);
    }
  }

  return (
    <section className="card" aria-labelledby="site-create">
      <h2 id="site-create">New site</h2>
      <form onSubmit={(e) => void submit(e)} noValidate>
        <label>
          Name
          <small>Becomes the Linux user of the site. 2 to 32 characters: lowercase letters, digits, _ and -.</small>
          <input value={name} onChange={(e) => setName(e.target.value.trim())} required autoComplete="off" spellCheck={false} />
        </label>
        <label>
          Domains
          <small>One per line. The first one is the primary domain.</small>
          <textarea
            value={domains}
            onChange={(e) => setDomainsText(e.target.value)}
            rows={3}
            required
            autoComplete="off"
            spellCheck={false}
          />
        </label>
        <label>
          PHP
          <select value={phpValue} onChange={(e) => setVersion(e.target.value)}>
            <PHPOptions php={php} />
          </select>
        </label>
        {php?.legacy.includes(phpValue) && (
          <p className="muted">PHP {phpValue} gets no or only security fixes upstream. Use it only for software that needs it.</p>
        )}
        <label className="check">
          <input type="checkbox" checked={tls} onChange={(e) => setTls(e.target.checked)} />
          HTTPS with a Let's Encrypt certificate
        </label>
        {tls && (
          <label className="check">
            <input type="checkbox" checked={hsts} onChange={(e) => setHsts(e.target.checked)} />
            HSTS: browsers use only HTTPS for this site
          </label>
        )}
        {tls && <p className="muted">The domains must point to this server, or the certificate cannot be issued.</p>}
        {error && <p role="alert">{error}</p>}
        {done && <p role="status">{done}</p>}
        <button type="submit" disabled={busy}>
          {busy ? "Creating…" : "Create site"}
        </button>
      </form>
    </section>
  );
}

function SiteRow({ site, php, onChange }: { site: Site; php: PHPVersions | null; onChange: () => Promise<void> }) {
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [confirming, setConfirming] = useState(false);
  const [editing, setEditing] = useState(false);
  const [typed, setTyped] = useState("");

  async function drop() {
    setBusy(true);
    setError("");
    try {
      await deleteSite(site.name);
      await onChange();
    } catch (err) {
      setError(siteMessage(err));
      setBusy(false);
    }
  }

  const [primary, ...aliases] = site.domains;

  return (
    <li>
      <span>
        <strong>{site.name}</strong> <span className="muted">{primary}</span>
        {site.disabled && <span className="badge">Disabled</span>}
        {site.maintenance.enabled && !site.disabled && <span className="badge">Maintenance</span>}
      </span>
      {!confirming && (
        <button type="button" className="secondary danger" disabled={busy} onClick={() => setConfirming(true)}>
          Delete
        </button>
      )}
      <dl className="facts" aria-label={"Details of " + site.name}>
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
        <dt>PHP</dt>
        <dd>{site.php ? "PHP " + site.php + (site.disabled ? " (stopped)" : "") : "No PHP"}</dd>
        <dt>HTTPS</dt>
        <dd>
          {httpsLabel(site.tls)}
          {site.tls.active && site.tls.hsts ? ", HSTS" : ""}
          {site.tls.active && site.tls.notAfter ? ", valid until " + site.tls.notAfter.slice(0, 10) : ""}
        </dd>
        {aliases.length > 0 && (
          <>
            <dt>Other domains</dt>
            <dd>{aliases.join(", ")}</dd>
          </>
        )}
      </dl>
      {site.tls.enabled && !site.tls.active && site.tls.error && (
        <p className="muted">Last attempt failed: {site.tls.error}. Hostashi tries again within an hour.</p>
      )}
      {site.state === "active" &&
        !confirming &&
        (editing ? (
          <SiteSettings site={site} php={php} onChange={onChange} onClose={() => setEditing(false)} />
        ) : (
          <button type="button" className="secondary" onClick={() => setEditing(true)}>
            Change settings
          </button>
        ))}
      {confirming && (
        <form
          onSubmit={(e) => {
            e.preventDefault();
            void drop();
          }}
        >
          <label>
            <span>
              Type <code>{site.name}</code> to delete the site with its files and its databases. This cannot be undone.
            </span>
            <input value={typed} onChange={(e) => setTyped(e.target.value)} autoComplete="off" spellCheck={false} />
          </label>
          <div className="actions">
            <button type="submit" className="danger" disabled={busy || typed !== site.name}>
              Delete {site.name}
            </button>
            <button
              type="button"
              className="secondary"
              disabled={busy}
              onClick={() => {
                setConfirming(false);
                setTyped("");
              }}
            >
              Cancel
            </button>
          </div>
        </form>
      )}
      {error && <p role="alert">{error}</p>}
    </li>
  );
}

// SiteSettings changes domains, PHP version, HTTPS, HSTS, maintenance and
// cron jobs of an active site. Each change goes live on its own.
function SiteSettings({
  site,
  php,
  onChange,
  onClose,
}: {
  site: Site;
  php: PHPVersions | null;
  onChange: () => Promise<void>;
  onClose: () => void;
}) {
  const [domains, setDomainsText] = useState(site.domains.join("\n"));
  const [version, setVersion] = useState(site.php);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [done, setDone] = useState("");
  const [confirmDisable, setConfirmDisable] = useState(false);

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

  function saveDomains(e: FormEvent) {
    e.preventDefault();
    const list = parseDomains(domains);
    const problem = domainsProblem(list);
    if (problem) {
      setError(problem);
      setDone("");
      return;
    }
    void act(async () => {
      await setDomains(site.name, list);
      return site.tls.enabled
        ? "Domains saved. The site is served over HTTP until a certificate for the new domains is there."
        : "Domains saved.";
    });
  }

  function savePHP(e: FormEvent) {
    e.preventDefault();
    void act(async () => {
      await setPHP(site.name, version);
      return version ? `The site runs on PHP ${version} now.` : "PHP is off for this site now.";
    });
  }

  const toggleTLS = (on: boolean) =>
    act(async () => {
      const s = await setTLS(site.name, on);
      if (!on) return "HTTPS is off. The site is served over HTTP.";
      return s.tls.active ? "HTTPS is on." : "HTTPS is on. The site switches when its certificate is there.";
    });

  const toggleDisabled = (on: boolean) =>
    act(async () => {
      await setDisabled(site.name, on);
      setConfirmDisable(false);
      return on ? `${site.name} is disabled.` : `${site.name} is enabled again.`;
    });

  const toggleHSTS = (on: boolean) =>
    act(async () => {
      await setHSTS(site.name, on);
      return on ? "HSTS is on. Browsers keep using HTTPS for this site, even if HTTPS is turned off later." : "HSTS is off.";
    });

  return (
    <div className="stack" aria-label={"Settings of " + site.name} role="group">
      <form onSubmit={saveDomains} noValidate>
        <label>
          Domains of {site.name}
          <small>One per line. The first one is the primary domain.</small>
          <textarea value={domains} onChange={(e) => setDomainsText(e.target.value)} rows={3} autoComplete="off" spellCheck={false} />
        </label>
        <div className="actions">
          <button type="submit" disabled={busy}>
            Save domains
          </button>
        </div>
      </form>
      <form onSubmit={savePHP} noValidate>
        <label>
          PHP of {site.name}
          <select value={version} onChange={(e) => setVersion(e.target.value)}>
            <PHPOptions php={php} current={site.php} />
          </select>
        </label>
        {php?.legacy.includes(version) && version !== site.php && (
          <p className="muted">PHP {version} gets no or only security fixes upstream. Use it only for software that needs it.</p>
        )}
        <div className="actions">
          <button type="submit" disabled={busy || version === site.php}>
            Save PHP version
          </button>
        </div>
      </form>
      <fieldset>
        <legend>HTTPS of {site.name}</legend>
        <label className="check">
          <input type="checkbox" checked={site.tls.enabled} disabled={busy} onChange={(e) => void toggleTLS(e.target.checked)} />
          HTTPS with a Let's Encrypt certificate
        </label>
        <label className="check">
          <input type="checkbox" checked={site.tls.hsts} disabled={busy} onChange={(e) => void toggleHSTS(e.target.checked)} />
          HSTS: browsers use only HTTPS for this site
        </label>
        {site.tls.hsts && !site.tls.active && <p className="muted">HSTS is sent only while the site is served over HTTPS.</p>}
      </fieldset>
      <MaintenanceSettings site={site} onChange={onChange} />
      <LimitSettings site={site} />
      <CronSettings site={site} />
      <SnippetSettings site={site} />
      <fieldset>
        <legend>Availability of {site.name}</legend>
        {site.disabled ? (
          <>
            <p className="muted">The site is disabled. Enabling it serves its files and starts PHP again.</p>
            <div className="actions">
              <button type="button" disabled={busy} onClick={() => void toggleDisabled(false)}>
                Enable {site.name}
              </button>
            </div>
          </>
        ) : confirmDisable ? (
          <>
            <p>
              Visitors get “temporarily unavailable” (HTTP 503) and PHP stops. Files, databases, backups and the certificate stay. You can
              enable the site again at any time.
            </p>
            <div className="actions">
              <button type="button" className="danger" disabled={busy} onClick={() => void toggleDisabled(true)}>
                Disable {site.name}
              </button>
              <button type="button" className="secondary" disabled={busy} onClick={() => setConfirmDisable(false)}>
                Cancel
              </button>
            </div>
          </>
        ) : (
          <div className="actions">
            <button type="button" className="secondary danger" disabled={busy} onClick={() => setConfirmDisable(true)}>
              Disable site
            </button>
          </div>
        )}
      </fieldset>
      {error && <p role="alert">{error}</p>}
      {done && <p role="status">{done}</p>}
      <div className="actions">
        <button type="button" className="secondary" disabled={busy} onClick={onClose}>
          Close settings
        </button>
      </div>
    </div>
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
    <form onSubmit={(e) => void save(m.enabled, e)} noValidate>
      <fieldset>
        <legend>Maintenance of {site.name}</legend>
        <p className="muted">
          {m.enabled
            ? "Maintenance is on: visitors get the page with “503 Service Unavailable”. PHP keeps running."
            : "While maintenance is on, visitors get the page with “503 Service Unavailable”. The allowed addresses see the site as usual, so you can work on it. PHP keeps running."}
        </p>
        {site.disabled && <p className="muted">The site is disabled. Disabled wins: visitors get “temporarily unavailable” until it is enabled.</p>}
        <label>
          Maintenance page
          <select value={page} onChange={(e) => setPage(e.target.value)}>
            {choices.map((p) => (
              <option key={p.id} value={p.id}>
                {p.name}
              </option>
            ))}
          </select>
        </label>
        <label>
          Allowed addresses
          <small>IPv4 or IPv6 addresses or ranges like 10.0.0.0/8, one per line, at most 20.</small>
          <textarea value={allow} onChange={(e) => setAllow(e.target.value)} rows={3} autoComplete="off" spellCheck={false} />
        </label>
        <div className="actions">
          <button type="button" className="secondary" disabled={busy} onClick={() => void addMine()}>
            Add my current IP
          </button>
          {m.enabled && (
            <button type="submit" disabled={busy}>
              Save maintenance
            </button>
          )}
          <button type="button" className={m.enabled ? "secondary" : undefined} disabled={busy} onClick={() => void save(!m.enabled)}>
            {m.enabled ? "End maintenance" : "Start maintenance"}
          </button>
        </div>
        {error && <p role="alert">{error}</p>}
        {done && <p role="status">{done}</p>}
      </fieldset>
    </form>
  );
}
