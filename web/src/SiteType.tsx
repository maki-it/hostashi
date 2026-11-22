// SPDX-License-Identifier: AGPL-3.0-or-later

// Changing the type of a site (ADR-0054 addendum 3), and removing the
// account a redirect or reverse proxy site kept after such a change.

import { useState, type FormEvent } from "react";
import { newestLine } from "./node";
import { newProgramApp, ProgramFields, programFieldsProblem, programRequest, programValue, type ProgramValue } from "./SiteApp";
import { newNodeApp, NodeFields, nodeProblem, useNodeStatus, type NodeValue } from "./SiteNode";
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

const typeSites: Record<ChosenType, string> = {
  static: "a static site",
  php: "a PHP site",
  node: "a Node.js site",
  app: "an app site",
  proxy: "a reverse proxy",
  redirect: "a redirect",
};

const typeNames: Record<ChosenType, string> = {
  static: "static",
  php: "PHP",
  node: "Node.js",
  app: "app",
  proxy: "reverse proxy",
  redirect: "redirect",
};

type Props = { site: Site; php: PHPVersions | null; onChange: () => Promise<void>; admin?: boolean };

// ChangeTypeCard switches the site to another type: the fields of the new
// type, a summary of what starts, stops and stays, then one request.
export function ChangeTypeCard({ site, php, onChange, admin = false }: Props) {
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
  const [proxy, setProxyValue] = useState<SiteProxy>(site.proxy ?? newProxy());
  const [redirect, setRedirectValue] = useState<SiteRedirect>(site.redirect ?? newRedirect());
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [done, setDone] = useState("");

  const req: SiteTypeRequest = { type: kind };
  if (kind === "php") req.php = phpValue;
  if (kind === "node") req.node = nodeValue;
  if (kind === "app") req.app = programRequest(program);
  if (kind === "proxy") req.proxy = { ...proxy, insecure: proxy.target.startsWith("https://") && proxy.insecure };
  if (kind === "redirect") req.redirect = redirect;
  const change = typeChange(site, req);

  function problem(): string {
    switch (kind) {
      case "php":
        return phpValue ? "" : "No PHP version is installed. Install one under PHP first.";
      case "node":
        return nodeProblem(nodeValue);
      case "app":
        return programFieldsProblem(program);
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
      setDone(`${site.name} is ${typeSites[kind]} now.` + (hasAccount(s) && !hasAccount(site) ? " Its Linux account is ready." : ""));
    } catch (err) {
      setError(siteMessage(err));
    } finally {
      setBusy(false);
    }
  }

  if (!open) {
    return (
      <Card title="Site type">
        <div className="ui-stack">
          <p className="ui-flush">
            This is a <strong>{siteTypeLabel(site)}</strong> site.{" "}
            {hasAccount(site)
              ? "Another type keeps the Linux account, files and databases; only what serves the site changes."
              : "A type with files creates a Linux account for the site."}
          </p>
          <div className="ui-row">
            <Button
              onClick={() => {
                setOpen(true);
                setDone("");
              }}
            >
              Change type
            </Button>
          </div>
          {done && <Notice tone="ok" title={done} />}
        </div>
      </Card>
    );
  }

  return (
    <Card title="Change type">
      <form className="ui-stack" onSubmit={(e) => void save(e)} noValidate>
        <SiteTypeChoice value={kind} onChange={setKind} name={`${site.name}-new-type`} disabled={busy} withRedirect except={current} />
        {kind === "php" && (
          <Field label="PHP version">
            <select className="ui-select" value={phpValue} onChange={(e) => setVersion(e.target.value)}>
              <PHPOptions php={php} withoutNone />
            </select>
          </Field>
        )}
        {kind === "node" && <NodeFields value={nodeValue} onChange={setNode} status={nodeStatus} disabled={busy} admin={admin} />}
        {kind === "app" && <ProgramFields value={program} onChange={setProgram} disabled={busy} />}
        {kind === "proxy" && <ProxyFields value={proxy} onChange={setProxyValue} disabled={busy} />}
        {kind === "redirect" && <RedirectFields value={redirect} onChange={setRedirectValue} disabled={busy} />}
        <section className="ui-stack-tight" aria-label="What changes">
          <h3 className="ui-flush">What changes</h3>
          <ChangeList title="Starts" items={change.starts} />
          <ChangeList title="Stops" items={change.stops} />
          <ChangeList title="Stays" items={change.stays} />
        </section>
        <div className="ui-row">
          <Button type="submit" variant="primary" disabled={busy}>
            {`Change to ${typeNames[kind]}`}
          </Button>
          <Button
            disabled={busy}
            onClick={() => {
              setOpen(false);
              setError("");
            }}
          >
            Cancel
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

  const what = site.proxy ? "reverse proxy" : "redirect";
  return (
    <Card title="Linux account">
      <div className="ui-stack">
        <Notice tone="warn" title="This site still has a Linux account and files">
          They are left from its former type and keep working: SSH and SFTP, cron jobs and backups. nginx serves only the {what}.
        </Notice>
        {!open ? (
          <div className="ui-row">
            <Button variant="danger" onClick={() => setOpen(true)}>
              Remove account and files
            </Button>
          </div>
        ) : (
          <form className="ui-stack" onSubmit={(e) => void remove(e)}>
            <div>
              <strong>This removes</strong>
              <ul className="ui-flush">
                <li>the Linux user {site.name} and its home with all files</li>
                <li>the site's databases and their users</li>
                <li>its cron jobs, SSH keys, limits and object cache</li>
              </ul>
              <p className="ui-hint ui-flush">The {what}, the domains and HTTPS stay. Backups stay until they expire.</p>
            </div>
            <div className="ui-narrow">
              <Field
                label={
                  <>
                    Type <code className="ui-confirm-phrase">{site.name}</code> to confirm
                  </>
                }
                hint="This cannot be undone."
              >
                <input className="ui-input" value={typed} onChange={(e) => setTyped(e.target.value)} autoComplete="off" spellCheck={false} />
              </Field>
            </div>
            <div className="ui-row">
              <Button type="submit" variant="danger" disabled={busy || typed !== site.name}>
                Remove account and files
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
    </Card>
  );
}
