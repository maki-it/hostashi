// SPDX-License-Identifier: AGPL-3.0-or-later

import { useCallback, useEffect, useRef, useState, type FormEvent } from "react";
import { cloneMessage, cloneSite, cloneSuggestion, configuredProfiles, getClone, refreshClone, type Clone, type CloneStart } from "./clones";
import { getSiteProfile } from "./profiles";
import { formatHash } from "./routes";
import { domainProblem, hasAccount, listPools, siteMessage, siteNameProblem, siteType, type Pool, type Site } from "./sites";
import { Badge, Button, Card, Field, Notice } from "./ui";

const bytes = (n: number) => {
  const units = ["B", "KiB", "MiB", "GiB", "TiB"];
  let i = 0;
  let v = n;
  while (v >= 1024 && i < units.length - 1) {
    v /= 1024;
    i++;
  }
  return (i === 0 ? v : v.toFixed(1)) + " " + units[i];
};

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
  if (!clone) return null;
  if (clone.state === "copying") return <Badge tone="warn">{clone.refresh ? "Refreshing" : "Copying"}</Badge>;
  return <Badge tone="warn">{clone.staging ? "Staging" : "Copy"}</Badge>;
}

// CloneCard creates a new site as a copy of this one: files, local
// databases and settings. A staging copy is protected and not indexed.
export function CloneCard({ site }: { site: Site }) {
  const suggestion = cloneSuggestion(site);
  const [open, setOpen] = useState(false);
  const [name, setName] = useState(suggestion.name);
  const [domain, setDomain] = useState(suggestion.domain);
  const [pool, setPool] = useState("");
  const [pools, setPools] = useState<Pool[]>([]);
  const [staging, setStaging] = useState(true);
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
  const domainErr = domain.trim() === "" ? "Enter a domain." : domainProblem(domain.trim().toLowerCase());

  async function submit(e: FormEvent) {
    e.preventDefault();
    setTried(true);
    if (nameErr || domainErr) return;
    setBusy(true);
    setError("");
    try {
      const out = await cloneSite(site.name, { name: name.trim(), domain: domain.trim().toLowerCase(), staging, ...(pool && { pool }) });
      setStarted(out);
      setOpen(false);
    } catch (err) {
      setError(cloneMessage(err) ?? siteMessage(err));
    } finally {
      setBusy(false);
    }
  }

  return (
    <Card title="Copy this site">
      <div className="ui-stack">
        <p className="ui-hint ui-flush">
          Creates a new site with a copy of the files, the databases on this server and the settings. A staging copy is for trying changes: its cron jobs
          are paused, it asks for a password, search engines are asked not to index it, and it can be refreshed from this site later.
        </p>
        {started && <CloneStarted started={started} onClose={() => setStarted(null)} />}
        {!open ? (
          !started && (
            <div className="ui-row">
              <Button onClick={() => setOpen(true)}>Copy site</Button>
            </div>
          )
        ) : (
          <form className="ui-stack" onSubmit={(e) => void submit(e)} noValidate>
            <div className="ui-narrow ui-stack">
              <Field label="System name of the copy" hint="Linux user and home folder of the new site." {...(tried && nameErr && { error: nameErr })}>
                <input className="ui-input" value={name} onChange={(e) => setName(e.target.value)} autoComplete="off" spellCheck={false} />
              </Field>
              <Field label="Domain of the copy" hint="Point it to this server first, so the copy gets its certificate." {...(tried && domainErr && { error: domainErr })}>
                <input className="ui-input" value={domain} onChange={(e) => setDomain(e.target.value)} autoComplete="off" spellCheck={false} />
              </Field>
              {pools.length > 1 && (
                <Field label="Storage pool">
                  <select className="ui-select" value={pool} onChange={(e) => setPool(e.target.value)}>
                    <option value="">Same as this site ({site.pool})</option>
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
              Staging copy of {site.name}
            </label>
            <Notice tone="warn" title="The copy takes over the application's own mail settings and can send real mail.">
              Environment variables of the app are copied too and may hold live API keys. Databases on external database servers are not copied: the copy
              keeps using them.
            </Notice>
            {error && <Notice tone="err" title={error} />}
            <div className="ui-row">
              <Button type="submit" variant="primary" disabled={busy}>
                {busy ? "Starting…" : staging ? "Create staging copy" : "Create copy"}
              </Button>
              <Button disabled={busy} onClick={() => setOpen(false)}>
                Cancel
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
  const [copied, setCopied] = useState("");
  const heading = useRef<HTMLHeadingElement>(null);
  const link = formatHash({ page: "sites", site: { name: started.site.name } });

  useEffect(() => {
    heading.current?.focus();
  }, []);

  async function copy(password: string) {
    try {
      await navigator.clipboard.writeText(password);
      setCopied("Copied.");
    } catch {
      setCopied("Copying is not possible here. Select the password and copy it by hand.");
    }
  }

  if (!started.access) {
    return (
      <Notice tone="ok" title={`The copy ${started.site.name} is being made.`}>
        <a href={link}>Open {started.site.name}</a> to follow the copy.
      </Notice>
    );
  }

  return (
    <section className="ui-card ui-card-body ui-stack ui-attention" aria-labelledby="clone-secret">
      <h2 id="clone-secret" ref={heading} tabIndex={-1}>
        Save the password of the copy now
      </h2>
      <p className="ui-flush">
        The copy <a href={link}>{started.site.name}</a> is being made and asks every visitor for this login. Hostashi does not keep the password and cannot
        show it again; you can set a new one under Security, Access.
      </p>
      <dl className="ui-facts">
        <dt>Address</dt>
        <dd>
          <code>{started.site.domains[0]}</code>
        </dd>
        <dt>User</dt>
        <dd>
          <code>{started.access.user}</code>
        </dd>
      </dl>
      <Field label="Password">
        <input readOnly value={started.access.password} onFocus={(e) => e.currentTarget.select()} autoComplete="off" spellCheck={false} className="ui-input ui-mono" />
      </Field>
      <div className="ui-row">
        <Button onClick={() => void copy(started.access?.password ?? "")}>Copy password</Button>
        <Button variant="primary" onClick={onClose}>
          I have saved it
        </Button>
      </div>
      {copied && <Notice tone="ok" title={copied} />}
    </section>
  );
}

const steps = (c: Clone) => {
  const dbs = c.databases.filter((d) => d.done).length;
  return [
    { label: "Files", done: c.filesDone },
    ...(c.databases.length > 0 ? [{ label: `Databases (${dbs} of ${c.databases.length})`, done: dbs === c.databases.length }] : []),
    { label: "Configuration", done: c.state === "done" },
  ];
};

// CloneStatusCard shows where a copy came from and how far it is, and
// offers "refresh from source" for a staging copy.
export function CloneStatusCard({ site, clone, staff, onRefresh }: { site: Site; clone: Clone; staff: boolean; onRefresh: () => void }) {
  const [profile, setProfile] = useState<string | null>(null);
  useEffect(() => {
    getSiteProfile(site.name)
      .then((p) => setProfile(p.profile))
      .catch(() => setProfile(null));
  }, [site.name]);

  const source = clone.source ? (
    <a href={formatHash({ page: "sites", site: { name: clone.source } })}>{clone.source}</a>
  ) : (
    "a site that was deleted since"
  );

  return (
    <Card title={clone.staging ? "Staging copy" : "Copy"}>
      <div className="ui-stack">
        <p className="ui-flush">
          {clone.staging ? "Staging copy" : "Copy"} of {source}
          {clone.finishedAt ? `, last copied ${clone.finishedAt.slice(0, 16).replace("T", " ")}` : ""}.
        </p>
        {clone.state === "copying" && (
          <div className="ui-stack" aria-live="polite">
            <ul className="ui-flush" aria-label="Copy progress">
              {steps(clone).map((s) => (
                <li key={s.label}>
                  {s.done ? "✓" : "…"} {s.label}
                </li>
              ))}
            </ul>
            {clone.files && !clone.filesDone && (
              <p className="ui-hint ui-flush">
                {clone.files.files.toLocaleString("en")} files, {bytes(clone.files.bytes)} copied so far.
              </p>
            )}
          </div>
        )}
        {clone.state === "failed" && (
          <Notice tone="err" title="The last copy failed">
            {clone.error}
          </Notice>
        )}
        {clone.files && clone.files.skipped > 0 && (
          <>
            <Notice tone="warn" title={`${clone.files.skipped.toLocaleString("en")} entries were not copied:`} />
            <ul className="ui-flush" aria-label="Skipped entries">
              {clone.files.skippedPaths.map((s) => (
                <li key={s.path}>
                  <span className="ui-mono">{s.path}</span>: {s.reason}
                </li>
              ))}
            </ul>
          </>
        )}
        {clone.state !== "copying" && profile !== null && !configuredProfiles.includes(profile) && <Checklist site={site} clone={clone} />}
        <Notice tone="warn" title="Before you test">
          The application's own mail settings were copied, so the copy can send real mail. Environment variables of the app may hold live API keys.
        </Notice>
        {clone.staging && staff && clone.source && <RefreshForm site={site} clone={clone} onDone={onRefresh} />}
      </div>
    </Card>
  );
}

// Checklist lists what to change by hand in an application Hostashi does
// not configure itself.
function Checklist({ site, clone }: { site: Site; clone: Clone }) {
  return (
    <div className="ui-stack">
      <p className="ui-flush">Hostashi does not know this application&apos;s configuration. Change it by hand:</p>
      <ul className="ui-flush" aria-label="What to change">
        {clone.databases.length > 0 && (
          <li>
            The database connection: use{" "}
            {clone.databases.map((d, i) => (
              <span key={d.target}>
                {i > 0 && ", "}
                <code>{d.target}</code>
              </span>
            ))}{" "}
            instead of the source&apos;s databases. The new passwords are set under Databases.
          </li>
        )}
        <li>
          The site&apos;s own address: <code>{site.domains[0]}</code>
          {clone.source ? " instead of the source's domain, in the configuration and in the database" : ""}.
        </li>
        <li>Mail, payment and other live services, so tests do not reach real customers.</li>
      </ul>
    </div>
  );
}

// RefreshForm copies the source's files and databases into the staging
// copy again, after the copy's name is typed.
function RefreshForm({ site, clone, onDone }: { site: Site; clone: Clone; onDone: () => void }) {
  const [open, setOpen] = useState(false);
  const [typed, setTyped] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");

  async function run(e: FormEvent) {
    e.preventDefault();
    setBusy(true);
    setError("");
    try {
      await refreshClone(site.name);
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
          <Button onClick={() => setOpen(true)}>Refresh from {clone.source}</Button>
        </div>
      ) : (
        <form className="ui-stack" onSubmit={(e) => void run(e)} data-unsaved="ignore">
          <div className="ui-narrow">
            <Field
              label={
                <>
                  Type <code className="ui-confirm-phrase">{site.name}</code> to confirm
                </>
              }
              hint={`The files and databases of ${site.name} are replaced with those of ${clone.source}. Changes made on the copy are lost. Domain, password and users stay.`}
            >
              <input className="ui-input" value={typed} onChange={(e) => setTyped(e.target.value)} autoComplete="off" spellCheck={false} />
            </Field>
          </div>
          <div className="ui-row">
            <Button type="submit" variant="danger" disabled={busy || typed !== site.name}>
              {busy ? "Starting…" : `Replace ${site.name} with ${clone.source}`}
            </Button>
            <Button
              disabled={busy}
              onClick={() => {
                setOpen(false);
                setTyped("");
                setError("");
              }}
            >
              Cancel
            </Button>
          </div>
        </form>
      )}
      {error && <Notice tone="err" title={error} />}
    </div>
  );
}
