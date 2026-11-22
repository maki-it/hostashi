// SPDX-License-Identifier: AGPL-3.0-or-later

import { Gear, Plus } from "@phosphor-icons/react";
import { useCallback, useEffect, useState, type FormEvent } from "react";
import { formatHash } from "./routes";
import {
  createSite,
  domainsProblem,
  httpsLabel,
  httpsTone,
  listPools,
  listSites,
  parseDomains,
  phpChoices,
  phpVersions,
  poolLabel,
  poolNote,
  proxyProblem,
  redirectProblem,
  siteMessage,
  siteNameProblem,
  siteStatus,
  siteTypeLabel,
  type PHPVersions,
  type Pool,
  type Site,
  type SiteProxy,
  type SiteRedirect,
} from "./sites";
import { newNodeApp, NodeFields, nodeProblem, useNodeStatus, type NodeValue } from "./SiteNode";
import { newProgramApp, ProgramFields, programFieldsProblem, programRequest, type ProgramValue } from "./SiteApp";
import { newProxy, ProxyFields } from "./SiteProxy";
import { newestLine } from "./node";
import { newRedirect, RedirectFields } from "./SiteRedirect";
import { Badge, Button, Card, Field, IconLink, Notice, PageHead, RowActions, Table, type Column, type Sort } from "./ui";

type QueryProps = {
  /** The address query: q (text filter), state, sort, dir, new. */
  query: URLSearchParams;
  /** Replaces the query in the address, without a new history entry for typing. */
  onQuery: (q: URLSearchParams) => void;
};

// useSites loads the sites and the PHP versions, and looks again while a
// certificate is pending.
export function useSites() {
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

  return { sites, php, error, load };
}

const stateFilters = [
  { value: "", label: "All states" },
  { value: "active", label: "Active" },
  { value: "maintenance", label: "Maintenance" },
  { value: "disabled", label: "Disabled" },
  { value: "problem", label: "Certificate problems" },
] as const;

function matches(s: Site, q: string, state: string): boolean {
  if (q && !s.name.includes(q) && !s.domains.some((d) => d.includes(q))) return false;
  if (state === "problem") return s.tls.enabled && !s.tls.active && Boolean(s.tls.error);
  if (state) return siteStatus(s).key === state;
  return true;
}

const columns: Column<Site>[] = [
  {
    key: "name",
    header: "Site",
    sortValue: (s) => s.name,
    cell: (s) => (
      <div className="ui-cell-main">
        <a href={formatHash({ page: "sites", site: { name: s.name } })}>{s.name}</a>
        <span>{s.domains[0]}</span>
      </div>
    ),
  },
  { key: "type", header: "Type", sortValue: (s) => siteTypeLabel(s), cell: (s) => siteTypeLabel(s) },
  {
    key: "https",
    header: "HTTPS",
    sortValue: (s) => httpsLabel(s.tls),
    cell: (s) => <Badge tone={httpsTone(s.tls)}>{httpsLabel(s.tls)}</Badge>,
  },
  {
    key: "state",
    header: "State",
    sortValue: (s) => siteStatus(s).word,
    cell: (s) => {
      const st = siteStatus(s);
      return <Badge tone={st.tone}>{st.word}</Badge>;
    },
  },
  {
    key: "actions",
    header: "Actions",
    srHeader: true,
    cell: (s) => (
      <RowActions>
        <IconLink label={`Settings of ${s.name}`} icon={<Gear size={16} />} href={formatHash({ page: "sites", site: { name: s.name } })} />
      </RowActions>
    ),
  },
];

// Sites is the list of sites (ADR-0037): a table that can be sorted and
// filtered, with the choice kept in the address. "New site" opens the form
// as its own view (?new=1). Each site's settings are on its own page.
export function Sites({ query, onQuery }: QueryProps) {
  const { sites, php, error, load } = useSites();
  const set = (key: string, value: string) => {
    const next = new URLSearchParams(query);
    if (value) next.set(key, value);
    else next.delete(key);
    onQuery(next);
  };

  if (query.get("new") === "1") {
    return (
      <>
        <PageHead title="New site" description="A site answers for its domains: with PHP, static files, a Node.js app or a program of its own from its own Linux user and home, or as a redirect or reverse proxy." />
        <CreateForm php={php} onChange={load} parked={query.get("domain") ?? ""} parkedTls={query.get("tls") !== "0"} />
      </>
    );
  }

  const q = (query.get("q") ?? "").trim().toLowerCase();
  const state = query.get("state") ?? "";
  const sortKey = query.get("sort");
  const sort: Sort | undefined = sortKey ? { key: sortKey, dir: query.get("dir") === "desc" ? "desc" : "asc" } : undefined;
  const shown = (sites ?? []).filter((s) => matches(s, q, state));

  return (
    <>
      <PageHead
        title="Sites"
        description="Every site has its domains and certificate. PHP, static, Node.js and app sites also have their own Linux user; redirect and reverse proxy sites have none."
        actions={
          <a className="ui-btn ui-btn-primary" href={formatHash({ page: "sites", query: new URLSearchParams({ new: "1" }) })}>
            <Plus size={16} aria-hidden="true" />
            New site
          </a>
        }
      />
      {error && <Notice tone="err" title={error} />}
      {!sites ? (
        !error && <p aria-busy="true">Loading…</p>
      ) : (
        <Card padded={false}>
          <div className="ui-filters">
            <input
              className="ui-input"
              type="search"
              aria-label="Filter sites"
              placeholder="Filter by name or domain"
              value={query.get("q") ?? ""}
              onChange={(e) => set("q", e.target.value)}
            />
            <select className="ui-select" aria-label="State" value={state} onChange={(e) => set("state", e.target.value)}>
              {stateFilters.map((f) => (
                <option key={f.value} value={f.value}>
                  {f.label}
                </option>
              ))}
            </select>
          </div>
          <Table
            label="Sites"
            columns={columns}
            rows={shown}
            rowKey={(s) => s.name}
            sort={sort}
            onSort={(s) => {
              const next = new URLSearchParams(query);
              next.set("sort", s.key);
              next.set("dir", s.dir);
              onQuery(next);
            }}
            empty={sites.length === 0 ? "No sites yet." : "No site matches the filter."}
          />
        </Card>
      )}
    </>
  );
}

// SiteTypeChoice picks the type of a site (ADR-0054). Only the types that
// exist today are offered; the others come with their features.
export type ChosenType = "static" | "php" | "node" | "app" | "proxy" | "redirect";

/** webOnly: types without a Linux account (ADR-0054). */
const webOnly = (t: ChosenType) => t === "redirect" || t === "proxy";

export function SiteTypeChoice<T extends ChosenType>({
  value,
  onChange,
  name,
  disabled = false,
  withRedirect = false,
  except,
}: {
  value: T;
  onChange: (v: T) => void;
  name: string;
  disabled?: boolean;
  /** Offers Node.js, app, redirect and reverse proxy as well. */
  withRedirect?: boolean;
  /** Leaves out this type, for example the site's current one. */
  except?: ChosenType;
}) {
  const all: { value: ChosenType; label: string; hint: string }[] = [
    { value: "php", label: "PHP", hint: "WordPress, Laravel, TYPO3 and other PHP software." },
    { value: "static", label: "Static", hint: "HTML, CSS and JavaScript, for example the build of Hugo, Astro or a single-page app. No PHP." },
    { value: "node", label: "Node.js", hint: "An app such as Next.js, Nuxt or Express that runs all the time as the site's user. Requests go to its port." },
    { value: "app", label: "App", hint: "A program of your own, for example a Go or Rust service, that runs all the time as the site's user. Requests go to its port." },
    { value: "proxy", label: "Reverse proxy", hint: "Passes every request to another web server, for example an app in your network. No files, no Linux account." },
    { value: "redirect", label: "Redirect", hint: "Sends every address to another one, for example an old domain to the new one. No files, no Linux account." },
  ];
  const choices = all.filter((c) => c.value !== except && (withRedirect || !(webOnly(c.value) || c.value === "node" || c.value === "app"))) as { value: T; label: string; hint: string }[];
  return (
    <fieldset className="ui-fieldset ui-stack-tight">
      <legend>Type</legend>
      {choices.map((c) => (
        <label key={c.value} className="ui-check ui-check-hint">
          <input type="radio" name={name} value={c.value} checked={value === c.value} disabled={disabled} onChange={() => onChange(c.value)} />
          <span>
            {c.label}
            <span className="ui-hint ui-flush">{c.hint}</span>
          </span>
        </label>
      ))}
    </fieldset>
  );
}

// PHPOptions lists the installed versions, newest first, legacy ones marked.
// A current version that is no longer installed stays selectable.
export function PHPOptions({ php, current = "", withoutNone = false }: { php: PHPVersions | null; current?: string; withoutNone?: boolean }) {
  return (
    <>
      {!withoutNone && <option value="">No PHP</option>}
      {phpChoices(php, current).map((v) => (
        <option key={v} value={v}>
          PHP {v}
          {php?.legacy.includes(v) ? " (legacy)" : ""}
          {php && !php.installed.includes(v) ? " (not installed)" : ""}
        </option>
      ))}
    </>
  );
}

type CreateProps = {
  php: PHPVersions | null;
  onChange: () => Promise<void>;
  /** A parked domain the site is made from, with its HTTPS setting (ADR-0048). */
  parked?: string;
  parkedTls?: boolean;
};

function CreateForm({ php, onChange, parked = "", parkedTls = true }: CreateProps) {
  const [name, setName] = useState("");
  const [domains, setDomainsText] = useState(parked);
  const [version, setVersion] = useState<string | null>(null);
  const [kind, setKind] = useState<ChosenType>("php");
  const [redirect, setRedirectValue] = useState<SiteRedirect>(newRedirect());
  const [proxy, setProxyValue] = useState<SiteProxy>(newProxy());
  const nodeStatus = useNodeStatus();
  const [node, setNodeValue] = useState<NodeValue | null>(null);
  const nodeValue = node ?? newNodeApp(newestLine(nodeStatus));
  const [program, setProgramValue] = useState<ProgramValue>(newProgramApp());
  const [tls, setTls] = useState(parked ? parkedTls : true);
  const [hsts, setHsts] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [done, setDone] = useState("");
  const [created, setCreated] = useState("");
  const [pools, setPools] = useState<Pool[]>([]);
  const [pool, setPool] = useState("default");

  // The pool choice shows only with a second pool (ADR-0016). Without the
  // list, the site goes to the default pool as before.
  useEffect(() => {
    listPools()
      .then(setPools)
      .catch(() => setPools([]));
  }, []);
  const choosePool = pools.length > 1;
  const chosen = pools.find((p) => p.name === pool);

  // Default to the newest installed version that is not legacy.
  const newest = [...(php?.installed ?? [])].reverse().find((v) => !php?.legacy.includes(v)) ?? "";
  const phpValue = version ?? newest;

  async function submit(e: FormEvent) {
    e.preventDefault();
    const list = parseDomains(domains);
    const problem =
      siteNameProblem(name) ||
      domainsProblem(list) ||
      (kind === "redirect" ? redirectProblem(redirect.target, list) : "") ||
      (kind === "proxy" ? proxyProblem(proxy.target, list) : "") ||
      (kind === "node" ? nodeProblem(nodeValue) : "") ||
      (kind === "app" ? programFieldsProblem(program) : "");
    if (problem) {
      setError(problem);
      return;
    }
    setBusy(true);
    setError("");
    setDone("");
    try {
      if (kind === "php" && !phpValue) {
        setError("No PHP version is installed. Install one under PHP, or create a static site.");
        setBusy(false);
        return;
      }
      const s = await createSite({
        name,
        ...(choosePool && !webOnly(kind) && { pool }),
        php: kind === "php" ? phpValue : "",
        domains: list,
        tls,
        hsts: tls && hsts,
        ...(kind === "redirect" && { type: "redirect" as const, redirect }),
        ...(kind === "proxy" && { type: "proxy" as const, proxy: { ...proxy, insecure: proxy.target.startsWith("https://") && proxy.insecure } }),
        ...(kind === "node" && { type: "node" as const, node: nodeValue }),
        ...(kind === "app" && { type: "app" as const, app: programRequest(program) }),
      });
      setDone(
        s.tls.enabled && !s.tls.active
          ? `Site ${s.name} created. It is served over HTTP until its certificate is there.`
          : `Site ${s.name} created.`,
      );
      setCreated(s.name);
      setName("");
      setDomainsText("");
      setVersion(null);
      setRedirectValue(newRedirect());
      setProxyValue(newProxy());
      setNodeValue(null);
      setProgramValue(newProgramApp());
      await onChange();
    } catch (err) {
      setError(siteMessage(err));
    } finally {
      setBusy(false);
    }
  }

  return (
    <Card>
      <form className="ui-stack" onSubmit={(e) => void submit(e)} noValidate>
        {parked && !created && <Notice tone="info" title={`${parked} is parked. Its parking page ends when the site is live.`} />}
        <Field
          label="Name"
          hint={
            webOnly(kind)
              ? "The name of the site in the panel. 2 to 32 characters: lowercase letters, digits, _ and -."
              : "Becomes the Linux user of the site. 2 to 32 characters: lowercase letters, digits, _ and -."
          }
        >
          <input className="ui-input" value={name} onChange={(e) => setName(e.target.value.trim())} required autoComplete="off" spellCheck={false} />
        </Field>
        <Field label="Domains" hint="One per line. The first one is the primary domain.">
          <textarea
            className="ui-input ui-textarea"
            value={domains}
            onChange={(e) => setDomainsText(e.target.value)}
            rows={3}
            required
            autoComplete="off"
            spellCheck={false}
          />
        </Field>
        {choosePool && !webOnly(kind) && (
          <Field label="Storage pool" hint="Where the site's home lives. A site stays on its pool.">
            <select className="ui-select" value={pool} onChange={(e) => setPool(e.target.value)}>
              {pools.map((p) => (
                <option key={p.name} value={p.name} disabled={!p.available}>
                  {poolLabel(p)}
                </option>
              ))}
            </select>
          </Field>
        )}
        {choosePool && !webOnly(kind) && poolNote(chosen) && <Notice tone="warn" title={poolNote(chosen)} />}
        <SiteTypeChoice value={kind} onChange={setKind} name="new-site-type" withRedirect />
        {kind === "php" && (
          <>
            <Field label="PHP version">
              <select className="ui-select" value={phpValue} onChange={(e) => setVersion(e.target.value)}>
                <PHPOptions php={php} withoutNone />
              </select>
            </Field>
            {php?.legacy.includes(phpValue) && (
              <p className="ui-hint ui-flush">PHP {phpValue} gets no or only security fixes upstream. Use it only for software that needs it.</p>
            )}
            <p className="ui-hint ui-flush">
              Only installed versions are listed. <a href="#/php">Install more PHP versions</a>
            </p>
          </>
        )}
        {kind === "static" && (
          <p className="ui-hint ui-flush">The web root folder, a fallback for single-page apps and asset caching can be set on the site's Web tab.</p>
        )}
        {kind === "node" && <NodeFields value={nodeValue} onChange={setNodeValue} status={nodeStatus} disabled={busy} admin />}
        {kind === "app" && <ProgramFields value={program} onChange={setProgramValue} disabled={busy} />}
        {kind === "proxy" && <ProxyFields value={proxy} onChange={setProxyValue} disabled={busy} />}
        {kind === "redirect" && <RedirectFields value={redirect} onChange={setRedirectValue} disabled={busy} />}
        <label className="ui-check">
          <input type="checkbox" checked={tls} onChange={(e) => setTls(e.target.checked)} />
          HTTPS with a Let's Encrypt certificate
        </label>
        {tls && (
          <label className="ui-check">
            <input type="checkbox" checked={hsts} onChange={(e) => setHsts(e.target.checked)} />
            HSTS: browsers use only HTTPS for this site
          </label>
        )}
        {tls && <p className="ui-hint ui-flush">The domains must point to this server, or the certificate cannot be issued.</p>}
        {error && <Notice tone="err" title={error} />}
        {done && (
          <Notice tone="ok" title={done}>
            {created && <a href={formatHash({ page: "sites", site: { name: created } })}>Open {created}</a>}
          </Notice>
        )}
        <div className="ui-row">
          <Button type="submit" variant="primary" disabled={busy}>
            {busy ? "Creating…" : "Create site"}
          </Button>
          <a href={formatHash({ page: "sites" })}>Back to the sites</a>
        </div>
      </form>
    </Card>
  );
}

