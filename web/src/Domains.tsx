// SPDX-License-Identifier: AGPL-3.0-or-later

// Domains of a site (ADR-0068): the served and redirect domains, a domain
// change that waits for its certificate, and the "Change primary domain"
// form.

import { useEffect, useState, type FormEvent } from "react";
import { hadWWW, newNames, planPrimaryChange, uncovered, waitsForCertificate, wpSearchReplace, type DomainPlan } from "./domains";
import { cancelDomainChange, changeDomains, domainProblem, domainsProblem, maxDomainsPerSite, parseDomains, siteMessage, siteType, type Site } from "./sites";
import { Button, Card, Field, Notice, RelTime } from "./ui";

type Props = { site: Site; onChange: () => Promise<void> };

function useAct(onChange: () => Promise<void>) {
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

  return { busy, error, done, act, setError, setDone };
}

// savedMessage says what a saved domain change does now.
export function savedMessage(s: Site): string {
  const p = s.pendingDomains;
  if (p) return `Saved. The change waits for a certificate for ${p.domains[0]}; until then the site stays as it is.`;
  if (s.tls.enabled && uncovered(s, [...s.domains, ...(s.redirectDomains ?? [])]).length > 0)
    return "Domains saved. The site is served over HTTP until a certificate for the new domains is there.";
  return "Domains saved.";
}

/** The served and redirect domains of a site. */
export function DomainsCard({ site, onChange }: Props) {
  const [domains, setDomainsText] = useState(site.domains.join("\n"));
  const [redirect, setRedirectText] = useState((site.redirectDomains ?? []).join("\n"));
  const [changing, setChanging] = useState(false);
  const a = useAct(onChange);
  const redirectSite = siteType(site) === "redirect";

  // Show the stored lists again after any change, also one made by the
  // www card or the change form.
  const stored = [...site.domains, "|", ...(site.redirectDomains ?? [])].join(" ");
  useEffect(() => {
    setDomainsText(site.domains.join("\n"));
    setRedirectText((site.redirectDomains ?? []).join("\n"));
  }, [stored]);

  function save(e: FormEvent) {
    e.preventDefault();
    const list = parseDomains(domains);
    const red = redirectSite ? [] : parseDomains(redirect);
    const problem = domainsProblem(list) || red.map(domainProblem).find((p) => p) || "";
    const both = red.find((d) => list.includes(d));
    if (problem || both || list.length + red.length > maxDomainsPerSite) {
      a.setError(
        problem ||
          (both ? `"${both}" is a served domain and a redirect domain.` : `A site can have at most ${maxDomainsPerSite} domains, redirect domains included.`),
      );
      a.setDone("");
      return;
    }
    void a.act(async () => {
      const s = await changeDomains(site.name, list, red);
      return savedMessage(s);
    });
  }

  return (
    <>
      <Card
        title="Domains"
        actions={
          !changing && (
            <Button disabled={a.busy} onClick={() => setChanging(true)}>
              Change primary domain
            </Button>
          )
        }
      >
        <form className="ui-stack" onSubmit={save} noValidate>
          <Field label={`Domains of ${site.name}`} hint="One per line. The first one is the primary domain.">
            <textarea className="ui-input ui-textarea" value={domains} onChange={(e) => setDomainsText(e.target.value)} rows={4} autoComplete="off" spellCheck={false} />
          </Field>
          {!redirectSite && (
            <Field
              label="Redirect domains"
              hint={`One per line. They answer with 301 to ${site.domains[0] ?? "the primary domain"} and stay on the certificate, so their DNS must keep pointing to this server.`}
            >
              <textarea className="ui-input ui-textarea" value={redirect} onChange={(e) => setRedirectText(e.target.value)} rows={2} autoComplete="off" spellCheck={false} />
            </Field>
          )}
          {a.error && <Notice tone="err" title={a.error} />}
          {a.done && <Notice tone="ok" title={a.done} />}
          <div className="ui-row">
            <Button type="submit" variant="primary" disabled={a.busy}>
              Save domains
            </Button>
          </div>
        </form>
      </Card>
      {changing && (
        <ChangePrimaryCard
          site={site}
          onClose={() => setChanging(false)}
          onSaved={async (s) => {
            setChanging(false);
            a.setDone(savedMessage(s));
            await onChange();
          }}
        />
      )}
    </>
  );
}

/** A domain change that waits for its certificate, with what to do about it. */
export function PendingDomainsNotice({ site, onChange }: { site: Site; onChange: () => Promise<void> }) {
  const p = site.pendingDomains;
  const a = useAct(onChange);
  if (!p) return null;

  const retry = () =>
    a.act(async () => {
      await changeDomains(site.name, p.domains, p.redirectDomains);
      return "Trying again now.";
    });
  const now = () =>
    a.act(async () => {
      await changeDomains(site.name, p.domains, p.redirectDomains, false);
      return "Domains changed. The site is served over HTTP until a certificate for the new domains is there.";
    });
  const cancel = () =>
    a.act(async () => {
      await cancelDomainChange(site.name);
      return "Domain change canceled. The site keeps its domains.";
    });

  return (
    <Card title="Domain change waiting for its certificate">
      <div className="ui-stack">
        <p className="ui-flush">
          The site stays on HTTPS with its current domains until a certificate for the new ones is there. Requested <RelTime iso={p.requestedAt} />.
        </p>
        <dl className="ui-facts" aria-label="Pending domains">
          <dt>Domains</dt>
          <dd className="ui-mono">{p.domains.join(", ")}</dd>
          {p.redirectDomains.length > 0 && (
            <>
              <dt>Redirect domains</dt>
              <dd className="ui-mono">{p.redirectDomains.join(", ")}</dd>
            </>
          )}
        </dl>
        {p.error ? (
          <Notice tone="warn" title="The certificate could not be obtained yet. The panel tries again within the hour.">
            <span className="ui-mono">{p.error}</span>
            <br />
            Check that the DNS of every new name points to this server.
          </Notice>
        ) : (
          <Notice tone="info" title="Getting the certificate. This usually takes less than a minute." />
        )}
        {site.tls.hsts && (
          <p className="ui-hint ui-flush">
            HSTS is on: browsers that know this site refuse plain HTTP, so changing without waiting makes it unreachable for them until the certificate is there.
          </p>
        )}
        {a.error && <Notice tone="err" title={a.error} />}
        {a.done && <Notice tone="ok" title={a.done} />}
        <div className="ui-row">
          <Button disabled={a.busy} onClick={() => void retry()}>
            Try again now
          </Button>
          <Button disabled={a.busy} onClick={() => void now()}>
            Change now without HTTPS
          </Button>
          <Button variant="danger" disabled={a.busy} onClick={() => void cancel()}>
            Cancel change
          </Button>
        </div>
      </div>
    </Card>
  );
}

function PlanList({ label, names, fresh }: { label: string; names: string[]; fresh: string[] }) {
  if (names.length === 0) return null;
  return (
    <>
      <dt>{label}</dt>
      <dd>
        <ul className="ui-flush ui-plain-list">
          {names.map((d) => (
            <li key={d}>
              <code>{d}</code>
              {fresh.includes(d) && <span className="ui-hint"> · new on the certificate</span>}
            </li>
          ))}
        </ul>
      </dd>
    </>
  );
}

/** "Change primary domain": the common case of a domain change in one step. */
export function ChangePrimaryCard({ site, onClose, onSaved }: { site: Site; onClose: () => void; onSaved: (s: Site) => Promise<void> }) {
  const [primary, setPrimary] = useState("");
  const [withWWW, setWithWWW] = useState(hadWWW(site));
  const [redirectOld, setRedirectOld] = useState(siteType(site) !== "redirect");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [copied, setCopied] = useState("");

  const name = primary.trim().toLowerCase();
  const problem = name === "" ? "" : domainProblem(name) || (name === site.domains[0] ? `${name} is already the primary domain.` : "");
  const plan: DomainPlan | null = name !== "" && !problem ? planPrimaryChange(site, { primary: name, withWWW, redirectOld }) : null;
  const fresh = plan ? newNames(site, plan) : [];
  const waits = plan ? waitsForCertificate(site, plan) : false;
  const old = site.domains[0] ?? "";
  const wp = wpSearchReplace(site, name || "new.example.com");

  async function submit(e: FormEvent) {
    e.preventDefault();
    if (!plan) {
      setError(problem || "Enter the new primary domain.");
      return;
    }
    setBusy(true);
    setError("");
    try {
      const s = await changeDomains(site.name, plan.domains, plan.redirectDomains);
      await onSaved(s);
    } catch (err) {
      setError(siteMessage(err));
    } finally {
      setBusy(false);
    }
  }

  async function copy() {
    try {
      await navigator.clipboard.writeText(wp);
      setCopied("Command copied.");
    } catch {
      setCopied("Copying is not possible here. Select the text and copy it by hand.");
    }
  }

  return (
    <Card title="Change primary domain">
      <form className="ui-stack" onSubmit={(e) => void submit(e)} noValidate>
        <p className="ui-flush">
          The site keeps its name <code>{site.name}</code>, its Linux user, files, databases, backups and settings. Only the names it answers to change.
        </p>
        <Field label="New primary domain" hint={`Today: ${old}`} {...(problem ? { error: problem } : {})}>
          <input
            className="ui-input ui-mono"
            value={primary}
            onChange={(e) => setPrimary(e.target.value)}
            placeholder="new.example.com"
            autoComplete="off"
            spellCheck={false}
            autoFocus
          />
        </Field>
        <label className="ui-check">
          <input type="checkbox" checked={withWWW} onChange={(e) => setWithWWW(e.target.checked)} />
          <span>
            Also <code>www.{name || "new.example.com"}</code>
          </span>
        </label>
        {siteType(site) !== "redirect" && (
          <label className="ui-check">
            <input type="checkbox" checked={redirectOld} onChange={(e) => setRedirectOld(e.target.checked)} />
            <span>
              Redirect <code>{old}</code>
              {hadWWW(site) && (
                <>
                  {" "}
                  and <code>www.{old.replace(/^www\./, "")}</code>
                </>
              )}{" "}
              to the new domain (301)
            </span>
          </label>
        )}

        {plan && (
          <section className="ui-card ui-card-body ui-stack" aria-label="After the change">
            <strong>After the change</strong>
            <dl className="ui-facts ui-flush">
              <PlanList label="Domains" names={plan.domains} fresh={fresh} />
              <PlanList label="Redirect domains" names={plan.redirectDomains} fresh={fresh} />
            </dl>
            {waits ? (
              <p className="ui-hint ui-flush">
                The site stays on HTTPS with its current domains until a certificate for the new names is there, usually within a minute. If the DNS
                of a new name is not ready, nothing changes and the error shows here.
              </p>
            ) : (
              site.tls.enabled &&
              fresh.length > 0 && <p className="ui-hint ui-flush">The site is served over HTTP until a certificate for the new names is there.</p>
            )}
            {redirectOld && plan.redirectDomains.length > 0 && (
              <p className="ui-hint ui-flush">The redirect domains stay on the certificate, so their DNS must keep pointing to this server.</p>
            )}
          </section>
        )}

        <section className="ui-stack-tight" aria-label="Not changed by the panel">
          <strong>Not changed by the panel</strong>
          <ul className="ui-flush">
            <li>DNS: point every new name to this server first.</li>
            <li>Addresses stored by the app, such as the WordPress site address. For WordPress, after a backup:</li>
          </ul>
          <pre className="ui-code">{wp}</pre>
          <div className="ui-row">
            <Button onClick={() => void copy()}>Copy command</Button>
            {copied && <span className="ui-hint">{copied}</span>}
          </div>
          <ul className="ui-flush">
            <li>Values with the old address in the app's environment or config files, such as APP_URL.</li>
          </ul>
        </section>

        {error && <Notice tone="err" title={error} />}
        <div className="ui-row">
          <Button type="submit" variant="primary" disabled={busy || !plan}>
            Change to {name || "the new domain"}
          </Button>
          <Button disabled={busy} onClick={onClose}>
            Close
          </Button>
        </div>
      </form>
    </Card>
  );
}
