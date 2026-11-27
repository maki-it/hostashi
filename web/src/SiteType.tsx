// SPDX-License-Identifier: AGPL-3.0-or-later

// Changing the type of a site (ADR-0054 addendum 3), and removing the
// account a redirect or reverse proxy site kept after such a change.

import { useState, type FormEvent } from "react";
import { Trans, useTranslation } from "react-i18next";
import { newestLine } from "./node";
import { newProgramApp, ProgramFields, programFieldsProblem, programRequest, programValue, type ProgramValue } from "./SiteApp";
import { newNodeApp, NodeFields, nodeProblem, useNodeStatus, type NodeValue } from "./SiteNode";
import { newPythonApp, PythonFields, pythonFieldsProblem, pythonFieldsValue, pythonRequest, usePythonStatus, type PythonFieldsValue } from "./SitePython";
import { newestPythonLine } from "./python";
import { newProxy, ProxyFields } from "./SiteProxy";
import { newRedirect, RedirectFields } from "./SiteRedirect";
import { PHPOptions, SiteTypeChoice, type ChosenType } from "./Sites";
import {
  hasAccount,
  proxyProblem,
  redirectProblem,
  removeSiteAccount,
  setSiteType,
  siteMessage,
  siteType,
  siteTypeLabel,
  typeChange,
  type PHPVersions,
  type Site,
  type SiteProxy,
  type SiteRedirect,
  type SiteTypeRequest,
} from "./sites";
import { Button, Card, Field, Notice } from "./ui";

type Props = { site: Site; php: PHPVersions | null; onChange: () => Promise<void>; admin?: boolean };

// ChangeTypeCard switches the site to another type: the fields of the new
// type, a summary of what starts, stops and stays, then one request.
export function ChangeTypeCard({ site, php, onChange, admin = false }: Props) {
  const { t } = useTranslation();
  const current = siteType(site);
  const [open, setOpen] = useState(false);
  const [kind, setKind] = useState<ChosenType>(current === "php" ? "static" : "php");
  const newest = [...(php?.installed ?? [])].reverse().find((v) => !php?.legacy.includes(v)) ?? "";
  const [version, setVersion] = useState<string | null>(null);
  const phpValue = version ?? (site.php || newest);
  const nodeStatus = useNodeStatus();
  const [node, setNode] = useState<NodeValue | null>(null);
  const nodeValue = node ?? (site.node ? { version: site.node.version, workdir: site.node.workdir, startKind: site.node.startKind, start: site.node.start } : newNodeApp(newestLine(nodeStatus)));
  const [program, setProgram] = useState<ProgramValue>(site.app ? programValue(site.app) : newProgramApp());
  const pythonStatus = usePythonStatus();
  const [python, setPython] = useState<PythonFieldsValue | null>(null);
  const pythonValue = python ?? (site.python ? pythonFieldsValue(site.python) : newPythonApp(newestPythonLine(pythonStatus)));
  const [proxy, setProxyValue] = useState<SiteProxy>(site.proxy ?? newProxy());
  const [redirect, setRedirectValue] = useState<SiteRedirect>(site.redirect ?? newRedirect());
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [done, setDone] = useState("");

  const req: SiteTypeRequest = { type: kind };
  if (kind === "php") req.php = phpValue;
  if (kind === "node") req.node = nodeValue;
  if (kind === "app") req.app = programRequest(program);
  if (kind === "python") req.python = pythonRequest(pythonValue);
  if (kind === "proxy") req.proxy = { ...proxy, insecure: proxy.target.startsWith("https://") && proxy.insecure };
  if (kind === "redirect") req.redirect = redirect;
  const change = typeChange(site, req);

  function problem(): string {
    switch (kind) {
      case "php":
        return phpValue ? "" : t("sites.typeCard.noPhp");
      case "node":
        return nodeProblem(nodeValue);
      case "app":
        return programFieldsProblem(program);
      case "python":
        return pythonFieldsProblem(pythonValue);
      case "proxy":
        return proxyProblem(proxy.target, site.domains);
      case "redirect":
        return redirectProblem(redirect.target, site.domains);
      default:
        return "";
    }
  }

  async function save(e: FormEvent) {
    e.preventDefault();
    const p = problem();
    if (p) {
      setError(p);
      return;
    }
    setBusy(true);
    setError("");
    try {
      const s = await setSiteType(site.name, req);
      await onChange();
      setOpen(false);
      setDone(t("sites.typeCard.isNow", { site: site.name, type: t(`sites.typeCard.sites.${kind}`) }) + (hasAccount(s) && !hasAccount(site) ? t("sites.typeCard.accountReady") : ""));
    } catch (err) {
      setError(siteMessage(err));
    } finally {
      setBusy(false);
    }
  }

  if (!open) {
    return (
      <Card title={t("sites.typeCard.title")}>
        <div className="ui-stack">
          <p className="ui-flush">
            <Trans i18nKey="sites.typeCard.current" values={{ type: siteTypeLabel(site) }} components={{ b: <strong /> }} />{" "}
            {hasAccount(site) ? t("sites.typeCard.keepsAccount") : t("sites.typeCard.createsAccount")}
          </p>
          <div className="ui-row">
            <Button
              onClick={() => {
                setOpen(true);
                setDone("");
              }}
            >
              {t("sites.typeCard.change")}
            </Button>
          </div>
          {done && <Notice tone="ok" title={done} />}
        </div>
      </Card>
    );
  }

  return (
    <Card title={t("sites.typeCard.changeTitle")}>
      <form className="ui-stack" onSubmit={(e) => void save(e)} noValidate>
        <SiteTypeChoice value={kind} onChange={setKind} name={`${site.name}-new-type`} disabled={busy} withRedirect except={current} />
        {kind === "php" && (
          <Field label={t("sites.typeCard.phpVersion")}>
            <select className="ui-select" value={phpValue} onChange={(e) => setVersion(e.target.value)}>
              <PHPOptions php={php} withoutNone />
            </select>
          </Field>
        )}
        {kind === "node" && <NodeFields value={nodeValue} onChange={setNode} status={nodeStatus} disabled={busy} admin={admin} />}
        {kind === "app" && <ProgramFields value={program} onChange={setProgram} disabled={busy} />}
        {kind === "python" && <PythonFields value={pythonValue} onChange={setPython} status={pythonStatus} disabled={busy} admin={admin} />}
        {kind === "proxy" && <ProxyFields value={proxy} onChange={setProxyValue} disabled={busy} />}
        {kind === "redirect" && <RedirectFields value={redirect} onChange={setRedirectValue} disabled={busy} />}
        <section className="ui-stack-tight" aria-label={t("sites.typeCard.whatChanges")}>
          <h3 className="ui-flush">{t("sites.typeCard.whatChanges")}</h3>
          <ChangeList title={t("sites.typeCard.starts")} items={change.starts} />
          <ChangeList title={t("sites.typeCard.stops")} items={change.stops} />
          <ChangeList title={t("sites.typeCard.stays")} items={change.stays} />
        </section>
        <div className="ui-row">
          <Button type="submit" variant="primary" disabled={busy}>
            {t("sites.typeCard.submit", { type: t(`sites.typeCard.names.${kind}`) })}
          </Button>
          <Button
            disabled={busy}
            onClick={() => {
              setOpen(false);
              setError("");
            }}
          >
            {t("common.actions.cancel")}
          </Button>
        </div>
        {error && <Notice tone="err" title={error} />}
      </form>
    </Card>
  );
}

function ChangeList({ title, items }: { title: string; items: string[] }) {
  if (items.length === 0) return null;
  return (
    <div>
      <strong>{title}</strong>
      <ul className="ui-flush">
        {items.map((i) => (
          <li key={i}>{i}</li>
        ))}
      </ul>
    </div>
  );
}

// RemoveAccountCard shows that a redirect or reverse proxy site still has
// its Linux account, and removes it with a typed confirmation.
export function RemoveAccountCard({ site, onChange }: { site: Site; onChange: () => Promise<void> }) {
  const { t } = useTranslation();
  const [open, setOpen] = useState(false);
  const [typed, setTyped] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");

  async function remove(e: FormEvent) {
    e.preventDefault();
    setBusy(true);
    setError("");
    try {
      await removeSiteAccount(site.name);
      await onChange();
    } catch (err) {
      setError(siteMessage(err));
      setBusy(false);
    }
  }

  const proxy = Boolean(site.proxy);
  return (
    <Card title={t("sites.account.title")}>
      <div className="ui-stack">
        <Notice tone="warn" title={t("sites.account.left")}>
          {t(proxy ? "sites.account.leftProxy" : "sites.account.leftRedirect")}
        </Notice>
        {!open ? (
          <div className="ui-row">
            <Button variant="danger" onClick={() => setOpen(true)}>
              {t("sites.account.remove")}
            </Button>
          </div>
        ) : (
          <form className="ui-stack" onSubmit={(e) => void remove(e)}>
            <div>
              <strong>{t("sites.account.removes")}</strong>
              <ul className="ui-flush">
                <li>{t("sites.account.user", { site: site.name })}</li>
                <li>{t("sites.account.databases")}</li>
                <li>{t("sites.account.rest")}</li>
              </ul>
              <p className="ui-hint ui-flush">{t(proxy ? "sites.account.staysProxy" : "sites.account.staysRedirect")}</p>
            </div>
            <div className="ui-narrow">
              <Field
                label={<Trans i18nKey="sites.confirm.type" values={{ name: site.name }} components={{ code: <code className="ui-confirm-phrase" /> }} />}
                hint={t("sites.confirm.undone")}
              >
                <input className="ui-input" value={typed} onChange={(e) => setTyped(e.target.value)} autoComplete="off" spellCheck={false} />
              </Field>
            </div>
            <div className="ui-row">
              <Button type="submit" variant="danger" disabled={busy || typed !== site.name}>
                {t("sites.account.remove")}
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
    </Card>
  );
}
