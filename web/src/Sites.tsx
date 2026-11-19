// SPDX-License-Identifier: AGPL-3.0-or-later

import { useCallback, useEffect, useState, type FormEvent } from "react";
import {
  createSite,
  deleteSite,
  domainsProblem,
  httpsLabel,
  listSites,
  parseDomains,
  phpVersions,
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
              <SiteRow key={s.name} site={s} onChange={load} />
            ))}
          </ul>
        )}
      </section>
    </div>
  );
}

// PHPOptions lists the installed versions, newest first, legacy ones marked.
export function PHPOptions({ php }: { php: PHPVersions | null }) {
  const installed = [...(php?.installed ?? [])].reverse();
  return (
    <>
      <option value="">No PHP</option>
      {installed.map((v) => (
        <option key={v} value={v}>
          PHP {v}
          {php?.legacy.includes(v) ? " (legacy)" : ""}
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

function SiteRow({ site, onChange }: { site: Site; onChange: () => Promise<void> }) {
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [confirming, setConfirming] = useState(false);
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
      </span>
      {!confirming && (
        <button type="button" className="secondary danger" disabled={busy} onClick={() => setConfirming(true)}>
          Delete
        </button>
      )}
      <dl className="facts" aria-label={"Details of " + site.name}>
        <dt>State</dt>
        <dd>{site.state === "active" ? "Active" : site.state === "deleting" ? "Being deleted" : "Being created"}</dd>
        <dt>PHP</dt>
        <dd>{site.php ? "PHP " + site.php : "No PHP"}</dd>
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
