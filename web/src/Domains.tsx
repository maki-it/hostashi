// SPDX-License-Identifier: AGPL-3.0-or-later

// Domains of a site (ADR-0068): the served and redirect domains, a domain
// change that waits for its certificate, and the "Change primary domain"
// form.

import { useEffect, useState, type FormEvent } from "react";
import { Trans, useTranslation } from "react-i18next";
import { hadWWW, newNames, planPrimaryChange, uncovered, waitsForCertificate, wpSearchReplace, type DomainPlan } from "./domains";
import i18n from "./i18n";
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
  if (p) return i18n.t("siteDomains.saved.waits", { domain: p.domains[0] });
  if (s.tls.enabled && uncovered(s, [...s.domains, ...(s.redirectDomains ?? [])]).length > 0) return i18n.t("siteDomains.saved.http");
  return i18n.t("siteDomains.saved.done");
}

/** The served and redirect domains of a site. */
export function DomainsCard({ site, onChange }: Props) {
  const { t } = useTranslation();
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
      a.setError(problem || (both ? t("siteDomains.card.both", { domain: both }) : t("siteDomains.card.tooMany", { max: maxDomainsPerSite })));
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
        title={t("siteDomains.card.title")}
        actions={
          !changing && (
            <Button disabled={a.busy} onClick={() => setChanging(true)}>
              {t("siteDomains.change.title")}
            </Button>
          )
        }
      >
        <form className="ui-stack" onSubmit={save} noValidate>
          <Field label={t("siteDomains.card.domainsOf", { site: site.name })} hint={t("siteDomains.card.domainsHint")}>
            <textarea className="ui-input ui-textarea" value={domains} onChange={(e) => setDomainsText(e.target.value)} rows={4} autoComplete="off" spellCheck={false} />
          </Field>
          {!redirectSite && (
            <Field
              label={t("siteDomains.card.redirect")}
              hint={t("siteDomains.card.redirectHint", { primary: site.domains[0] ?? t("siteDomains.card.thePrimary") })}
            >
              <textarea className="ui-input ui-textarea" value={redirect} onChange={(e) => setRedirectText(e.target.value)} rows={2} autoComplete="off" spellCheck={false} />
            </Field>
          )}
          {a.error && <Notice tone="err" title={a.error} />}
          {a.done && <Notice tone="ok" title={a.done} />}
          <div className="ui-row">
            <Button type="submit" variant="primary" disabled={a.busy}>
              {t("siteDomains.card.save")}
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
  const { t } = useTranslation();
  const p = site.pendingDomains;
  const a = useAct(onChange);
  if (!p) return null;

  const retry = () =>
    a.act(async () => {
      await changeDomains(site.name, p.domains, p.redirectDomains);
      return t("siteDomains.pending.retried");
    });
  const now = () =>
    a.act(async () => {
      await changeDomains(site.name, p.domains, p.redirectDomains, false);
      return t("siteDomains.pending.changedNow");
    });
  const cancel = () =>
    a.act(async () => {
      await cancelDomainChange(site.name);
      return t("siteDomains.pending.canceled");
    });

  return (
    <Card title={t("siteDomains.pending.title")}>
      <div className="ui-stack">
        <p className="ui-flush">
          {t("siteDomains.pending.intro")} {t("siteDomains.pending.requested")} <RelTime iso={p.requestedAt} />.
        </p>
        <dl className="ui-facts" aria-label={t("siteDomains.pending.label")}>
          <dt>{t("siteDomains.card.title")}</dt>
          <dd className="ui-mono">{p.domains.join(", ")}</dd>
          {p.redirectDomains.length > 0 && (
            <>
              <dt>{t("siteDomains.card.redirect")}</dt>
              <dd className="ui-mono">{p.redirectDomains.join(", ")}</dd>
            </>
          )}
        </dl>
        {p.error ? (
          <Notice tone="warn" title={t("siteDomains.pending.failed")}>
            <span className="ui-mono">{p.error}</span>
            <br />
            {t("siteDomains.pending.checkDNS")}
          </Notice>
        ) : (
          <Notice tone="info" title={t("siteDomains.pending.getting")} />
        )}
        {site.tls.hsts && <p className="ui-hint ui-flush">{t("siteDomains.pending.hsts")}</p>}
        {a.error && <Notice tone="err" title={a.error} />}
        {a.done && <Notice tone="ok" title={a.done} />}
        <div className="ui-row">
          <Button disabled={a.busy} onClick={() => void retry()}>
            {t("siteDomains.pending.retry")}
          </Button>
          <Button disabled={a.busy} onClick={() => void now()}>
            {t("siteDomains.pending.now")}
          </Button>
          <Button variant="danger" disabled={a.busy} onClick={() => void cancel()}>
            {t("siteDomains.pending.cancel")}
          </Button>
        </div>
      </div>
    </Card>
  );
}

function PlanList({ label, names, fresh }: { label: string; names: string[]; fresh: string[] }) {
  const { t } = useTranslation();
  if (names.length === 0) return null;
  return (
    <>
      <dt>{label}</dt>
      <dd>
        <ul className="ui-flush ui-plain-list">
          {names.map((d) => (
            <li key={d}>
              <code>{d}</code>
              {fresh.includes(d) && <span className="ui-hint"> · {t("siteDomains.change.newOnCert")}</span>}
            </li>
          ))}
        </ul>
      </dd>
    </>
  );
}

/** "Change primary domain": the common case of a domain change in one step. */
export function ChangePrimaryCard({ site, onClose, onSaved }: { site: Site; onClose: () => void; onSaved: (s: Site) => Promise<void> }) {
  const { t } = useTranslation();
  const [primary, setPrimary] = useState("");
  const [withWWW, setWithWWW] = useState(hadWWW(site));
  const [redirectOld, setRedirectOld] = useState(siteType(site) !== "redirect");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [copied, setCopied] = useState("");

  const name = primary.trim().toLowerCase();
  const problem = name === "" ? "" : domainProblem(name) || (name === site.domains[0] ? t("siteDomains.change.alreadyPrimary", { domain: name }) : "");
  const plan: DomainPlan | null = name !== "" && !problem ? planPrimaryChange(site, { primary: name, withWWW, redirectOld }) : null;
  const fresh = plan ? newNames(site, plan) : [];
  const waits = plan ? waitsForCertificate(site, plan) : false;
  const old = site.domains[0] ?? "";
  const example = "new.example.com";
  const wp = wpSearchReplace(site, name || example);

  async function submit(e: FormEvent) {
    e.preventDefault();
    if (!plan) {
      setError(problem || t("siteDomains.change.enter"));
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
      setCopied(t("siteDomains.change.copied"));
    } catch {
      setCopied(t("siteDomains.change.copyFailed"));
    }
  }

  return (
    <Card title={t("siteDomains.change.title")}>
      <form className="ui-stack" onSubmit={(e) => void submit(e)} noValidate>
        <p className="ui-flush">
          <Trans i18nKey="siteDomains.change.intro" values={{ site: site.name }} components={{ code: <code /> }} />
        </p>
        <Field label={t("siteDomains.change.new")} hint={t("siteDomains.change.today", { domain: old })} {...(problem ? { error: problem } : {})}>
          <input
            className="ui-input ui-mono"
            value={primary}
            onChange={(e) => setPrimary(e.target.value)}
            placeholder={example}
            autoComplete="off"
            spellCheck={false}
            autoFocus
          />
        </Field>
        <label className="ui-check">
          <input type="checkbox" checked={withWWW} onChange={(e) => setWithWWW(e.target.checked)} />
          <span>
            <Trans i18nKey="siteDomains.change.alsoWWW" values={{ name: "www." + (name || example) }} components={{ code: <code /> }} />
          </span>
        </label>
        {siteType(site) !== "redirect" && (
          <label className="ui-check">
            <input type="checkbox" checked={redirectOld} onChange={(e) => setRedirectOld(e.target.checked)} />
            <span>
              {hadWWW(site) ? (
                <Trans
                  i18nKey="siteDomains.change.redirectOldBoth"
                  values={{ old, www: "www." + old.replace(/^www\./, "") }}
                  components={{ code1: <code />, code2: <code /> }}
                />
              ) : (
                <Trans i18nKey="siteDomains.change.redirectOld" values={{ old }} components={{ code: <code /> }} />
              )}
            </span>
          </label>
        )}

        {plan && (
          <section className="ui-card ui-card-body ui-stack" aria-label={t("siteDomains.change.after")}>
            <strong>{t("siteDomains.change.after")}</strong>
            <dl className="ui-facts ui-flush">
              <PlanList label={t("siteDomains.card.title")} names={plan.domains} fresh={fresh} />
              <PlanList label={t("siteDomains.card.redirect")} names={plan.redirectDomains} fresh={fresh} />
            </dl>
            {waits ? (
              <p className="ui-hint ui-flush">{t("siteDomains.change.waits")}</p>
            ) : (
              site.tls.enabled && fresh.length > 0 && <p className="ui-hint ui-flush">{t("siteDomains.change.http")}</p>
            )}
            {redirectOld && plan.redirectDomains.length > 0 && <p className="ui-hint ui-flush">{t("siteDomains.change.redirectDNS")}</p>}
          </section>
        )}

        <section className="ui-stack-tight" aria-label={t("siteDomains.change.notChanged")}>
          <strong>{t("siteDomains.change.notChanged")}</strong>
          <ul className="ui-flush">
            <li>{t("siteDomains.change.dns")}</li>
            <li>{t("siteDomains.change.appAddress")}</li>
          </ul>
          <pre className="ui-code">{wp}</pre>
          <div className="ui-row">
            <Button onClick={() => void copy()}>{t("siteDomains.change.copy")}</Button>
            {copied && <span className="ui-hint">{copied}</span>}
          </div>
          <ul className="ui-flush">
            <li>{t("siteDomains.change.appConfig")}</li>
          </ul>
        </section>

        {error && <Notice tone="err" title={error} />}
        <div className="ui-row">
          <Button type="submit" variant="primary" disabled={busy || !plan}>
            {name ? t("siteDomains.change.submit", { domain: name }) : t("siteDomains.change.submitEmpty")}
          </Button>
          <Button disabled={busy} onClick={onClose}>
            {t("siteDomains.change.close")}
          </Button>
        </div>
      </form>
    </Card>
  );
}
