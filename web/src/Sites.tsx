// SPDX-License-Identifier: AGPL-3.0-or-later

import { listProfiles, type Profile } from "./profiles";
import { LockSimple, LockSimpleOpen, Plus } from "@phosphor-icons/react";
import { useCallback, useEffect, useMemo, useState, type FormEvent } from "react";
import { getBackups, type BackupInfo } from "./backups";
import { formatBytes, getSiteLimits, type Usage } from "./limits";
import { relativeTime } from "./ui/Time";
import { cronFailing, listCron } from "./cron";
import { formatHash, type SiteTab } from "./routes";
import { groupsForSite } from "./SiteNav";
import {
  compareVersions,
  createSite,
  domainsProblem,
  httpsLabel,
  httpsTone,
  defaultPool,
  listPools,
  hasAccount,
  listSitesMarked,
  type SiteMarks,
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
  maxTitleLength,
  siteLabel,
} from "./sites";
import { newNodeApp, NodeFields, nodeProblem, useNodeStatus, type NodeValue } from "./SiteNode";
import { newPythonApp, PythonFields, pythonFieldsProblem, pythonRequest, usePythonStatus, type PythonFieldsValue } from "./SitePython";
import { newestPythonLine } from "./python";
import { newProgramApp, ProgramFields, programFieldsProblem, programRequest, type ProgramValue } from "./SiteApp";
import { newProxy, ProxyFields } from "./SiteProxy";
import { newestLine } from "./node";
import { newRedirect, RedirectFields } from "./SiteRedirect";
import { Badge, Button, Card, Field, Notice, PageHead, Table, type Column, type Sort } from "./ui";

type QueryProps = {
  /** The address query: q (text filter), state, sort, dir, new. */
  query: URLSearchParams;
  /** Replaces the query in the address, without a new history entry for typing. */
  onQuery: (q: URLSearchParams) => void;
  /** The role of the logged-in account; users cannot create sites (ADR-0035). */
  role?: string;
};

// useSites loads the sites and the PHP versions, and looks again while a
// certificate is pending.
export function useSites() {
  const [sites, setSites] = useState<Site[] | null>(null);
  const [marks, setMarks] = useState<Record<string, SiteMarks>>({});
  const [php, setPhp] = useState<PHPVersions | null>(null);
  const [error, setError] = useState("");

  const load = useCallback(async () => {
    try {
      const r = await listSitesMarked();
      setSites(r.sites);
      setMarks(r.marks);
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

  return { sites, marks, php, error, load };
}

// Disk is the disk use of a site and its limit (0: none).
type Disk = { usage: Usage; limit: number };

// useDisks loads the disk use of the sites with files, a few at a time, so
// a long list does not ask the agent for every site at once.
// usePerSite loads one value per site after the list, four requests at a
// time. A site whose request fails stays out of the result.
function usePerSite<T>(names: string[], load: (name: string) => Promise<T | undefined>): Record<string, T> {
  const [out, setOut] = useState<Record<string, T>>({});
  const key = names.join(" ");
  useEffect(() => {
    let live = true;
    const queue = key ? key.split(" ") : [];
    const next = async (): Promise<void> => {
      const name = queue.shift();
      if (!name || !live) return;
      try {
        const v = await load(name);
        if (live && v !== undefined) setOut((o) => ({ ...o, [name]: v }));
      } catch {
        // The cell stays empty for this site.
      }
      return next();
    };
    void Promise.all([next(), next(), next(), next()]);
    return () => {
      live = false;
    };
    // load is a module-level function; the names decide what to fetch.
  }, [key]);
  return out;
}

const loadDisk = async (name: string): Promise<Disk | undefined> => {
  const l = await getSiteLimits(name);
  return l.usage ? { usage: l.usage, limit: l.limits.diskBytes } : undefined;
};

// loadCronFailing counts the active cron jobs of a site whose last run failed.
const loadCronFailing = async (name: string): Promise<number> => (await listCron(name)).jobs.filter((j) => !j.paused && cronFailing(j.status)).length;

const activeWith = (sites: Site[] | null, tab: SiteTab) =>
  (sites ?? []).filter((s) => s.state === "active" && groupsForSite(s).some((g) => g.tabs.some((t) => t.id === tab))).map((s) => s.name);

// useBackupStatus loads the backup state for administrators; for other
// roles the request fails and the column stays hidden.
function useBackupStatus(): BackupInfo | null {
  const [info, setInfo] = useState<BackupInfo | null>(null);
  useEffect(() => {
    let live = true;
    getBackups().then(
      (b) => live && setInfo(b),
      () => undefined,
    );
    return () => {
      live = false;
    };
  }, []);
  return info;
}

// backupOf says how the last backup run went for a site: "" when the site
// was not part of it.
function backupOf(info: BackupInfo, s: Site): { word: string; tone?: "err" | "warn"; at: number } {
  const st = info.status;
  const item = st.items.find((i) => i.site === s.name && i.kind === "files");
  if (!info.ready || !item || !st.finished) return { word: "", at: 0 };
  if (item.error) return { word: "Failed", tone: "err", at: 0 };
  const at = new Date(st.finished).getTime();
  return { word: relativeTime(new Date(at)), at, ...(info.overdue ? { tone: "warn" as const } : {}) };
}

const stateFilters = [
  { value: "", label: "All states" },
  { value: "active", label: "Active" },
  { value: "maintenance", label: "Maintenance" },
  { value: "disabled", label: "Disabled" },
  { value: "problem", label: "Certificate problems" },
  { value: "attention", label: "Needs attention" },
  { value: "staging", label: "Staging sites" },
] as const;

function matches(s: Site, q: string, state: string, m: SiteMarks | undefined, disk: Disk | undefined, failing: number, backups: BackupInfo | null): boolean {
  if (q && !s.name.includes(q) && !(s.title ?? "").toLowerCase().includes(q) && !s.domains.some((d) => d.includes(q))) return false;
  if (state === "attention")
    return siteIssues(s).length > 0 || (m?.openFindings ?? 0) > 0 || failing > 0 || (disk !== undefined && disk.usage.level !== "ok") || (backups !== null && backupOf(backups, s).tone !== undefined);
  if (state === "staging") return m?.staging === true;
  if (state === "problem") return s.tls.enabled && !s.tls.active && Boolean(s.tls.error);
  if (state) return siteStatus(s).key === state;
  return true;
}

// siteIssues lists what needs a look on a site: maintenance and a
// certificate that is missing or failed. Errors sort first.
export function siteIssues(s: Site): { word: string; tone: "ok" | "warn" | "err" | "off" }[] {
  const out: { word: string; tone: "ok" | "warn" | "err" | "off" }[] = [];
  const st = siteStatus(s);
  if (st.key === "maintenance") out.push({ word: st.word, tone: st.tone });
  if (s.tls.enabled && !s.tls.active) out.push({ word: httpsLabel(s.tls), tone: httpsTone(s.tls) });
  return out;
}

const rank = { err: 0, warn: 1, off: 2, ok: 3 } as const;

// statusRank sorts problems first: errors, warnings, then HTTP only.
function statusRank(s: Site): number {
  const r = Math.min(3, ...siteIssues(s).map((i) => rank[i.tone]));
  return r === 3 && !s.tls.enabled ? 2 : r;
}

// stateBadge marks a site that is not simply on: switched off, being
// created or being deleted. An active site gets no mark (Kim, 2026-11-25).
function stateBadge(s: Site) {
  const st = siteStatus(s);
  if (st.key === "active" || st.key === "maintenance") return null;
  return <Badge tone={st.tone}>{st.key === "disabled" ? "Disabled" : st.word}</Badge>;
}

type ListData = { marks: Record<string, SiteMarks>; disks: Record<string, Disk>; crons: Record<string, number>; backups: BackupInfo | null };

// siteColumns are the columns of the list. Disk and backup come in after
// the list; the backup column is only there for administrators.
function siteColumns({ marks, disks, crons, backups }: ListData): Column<Site>[] {
  const cols: Column<Site>[] = [
    {
      key: "name",
      header: "Site",
      sortValue: (s) => siteLabel(s).toLowerCase(),
      cell: (s) => {
        const m = marks[s.name];
        return (
          <div className={`ui-cell-main${s.disabled ? " ui-cell-off" : ""}`}>
            <span className="ui-row ui-row-tight">
              <a className="ui-row-link" href={formatHash({ page: "sites", site: { name: s.name } })}>
                {siteLabel(s)}
              </a>
              {stateBadge(s)}
              {m?.cloneOf && <Badge tone={m.staging ? "warn" : "off"}>{`${m.staging ? "Staging" : "Copy"} of ${m.cloneOf}`}</Badge>}
            </span>
            <span>
              {s.title && (
                <>
                  <span className="ui-mono" title="System name">
                    {s.name}
                  </span>
                  {" · "}
                </>
              )}
              {s.domains[0]}
            </span>
          </div>
        );
      },
    },
    { key: "type", header: "Type", sortValue: (s) => siteTypeLabel(s), cell: (s) => siteTypeLabel(s) },
    {
      key: "disk",
      header: "Disk",
      sortValue: (s) => disks[s.name]?.usage.bytes ?? -1,
      cell: (s) => {
        const d = disks[s.name];
        if (!d) return <span className="ui-hint">—</span>;
        const tone = d.usage.level === "critical" ? "err" : d.usage.level === "warning" ? "warn" : undefined;
        return (
          <span className="ui-cellbar" data-tone={tone}>
            {d.limit > 0 && (
              <span className="ui-cellbar-track" aria-hidden="true">
                <span style={{ width: `${Math.min(100, d.usage.bytesPercent)}%` }} />
              </span>
            )}
            <span>{d.limit > 0 ? `${formatBytes(d.usage.bytes)} of ${formatBytes(d.limit)}` : formatBytes(d.usage.bytes)}</span>
          </span>
        );
      },
    },
  ];
  if (backups) {
    cols.push({
      key: "backup",
      header: "Last backup",
      sortValue: (s) => backupOf(backups, s).at,
      cell: (s) => {
        const b = backupOf(backups, s);
        if (!b.word) return <span className="ui-hint">—</span>;
        return b.tone ? <Badge tone={b.tone}>{b.word}</Badge> : b.word;
      },
    });
  }
  cols.push({
    key: "status",
    header: "Status",
    sortValue: (s) => statusRank(s) - ((marks[s.name]?.openFindings ?? 0) > 0 ? 10 : 0) - ((crons[s.name] ?? 0) > 0 ? 5 : 0),
    cell: (s) => {
      const finds = marks[s.name]?.openFindings ?? 0;
      const failing = crons[s.name] ?? 0;
      return (
        <span className="ui-row ui-row-tight">
          {s.tls.active ? (
            <span className="ui-https">
              <LockSimple size={16} aria-hidden />
              HTTPS
            </span>
          ) : (
            <span className="ui-https ui-https-off">
              <LockSimpleOpen size={16} aria-hidden />
              HTTP only
            </span>
          )}
          {finds > 0 && (
            <a href={formatHash({ page: "sites", site: { name: s.name, tab: "malware" } })}>
              <Badge tone="err">{finds === 1 ? "1 virus find" : `${finds} virus finds`}</Badge>
            </a>
          )}
          {failing > 0 && (
            <a href={formatHash({ page: "sites", site: { name: s.name, tab: "cron" } })}>
              <Badge tone="warn">{failing === 1 ? "1 cron job fails" : `${failing} cron jobs fail`}</Badge>
            </a>
          )}
          {siteIssues(s).map((i) => (
            <Badge key={i.word} tone={i.tone}>
              {i.word}
            </Badge>
          ))}
        </span>
      );
    },
  });
  return cols;
}

// Sites is the list of sites (ADR-0037): a table that can be sorted and
// filtered, with the choice kept in the address. "New site" opens the form
// as its own view (?new=1). Each site's settings are on its own page.
export function Sites({ query, onQuery, role = "user" }: QueryProps) {
  const { sites, marks, php, error, load } = useSites();
  const staff = role === "admin" || role === "manager";
  const disks = usePerSite(
    (sites ?? []).filter((s) => s.state === "active" && hasAccount(s)).map((s) => s.name),
    loadDisk,
  );
  const crons = usePerSite(activeWith(sites, "cron"), loadCronFailing);
  const backups = useBackupStatus();
  const columns = useMemo(() => siteColumns({ marks, disks, crons, backups }), [marks, disks, crons, backups]);
  const set = (key: string, value: string) => {
    const next = new URLSearchParams(query);
    if (value) next.set(key, value);
    else next.delete(key);
    onQuery(next);
  };

  if (staff && query.get("new") === "1") {
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
  const shown = (sites ?? []).filter((s) => matches(s, q, state, marks[s.name], disks[s.name], crons[s.name] ?? 0, backups));

  return (
    <>
      <PageHead
        title="Sites"
        description="Every site has its domains and certificate. PHP, static, Node.js and app sites also have their own Linux user; redirect and reverse proxy sites have none."
        actions={
          staff && (
            <a className="ui-btn ui-btn-primary" href={formatHash({ page: "sites", query: new URLSearchParams({ new: "1" }) })}>
              <Plus size={16} aria-hidden="true" />
              New site
            </a>
          )
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
export type ChosenType = "static" | "php" | "node" | "python" | "app" | "proxy" | "redirect";

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
    { value: "python", label: "Python", hint: "A Django, Flask or FastAPI app that runs all the time as the site's user, from its own environment .venv. Requests go to its port." },
    { value: "app", label: "App", hint: "A program of your own, for example a Go or Rust service, that runs all the time as the site's user. Requests go to its port." },
    { value: "proxy", label: "Reverse proxy", hint: "Passes every request to another web server, for example an app in your network. No files, no Linux account." },
    { value: "redirect", label: "Redirect", hint: "Sends every address to another one, for example an old domain to the new one. No files, no Linux account." },
  ];
  const choices = all.filter((c) => c.value !== except && (withRedirect || !(webOnly(c.value) || c.value === "node" || c.value === "python" || c.value === "app"))) as { value: T; label: string; hint: string }[];
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
  const [title, setTitleText] = useState("");
  const [domains, setDomainsText] = useState(parked);
  const [version, setVersion] = useState<string | null>(null);
  const [kind, setKind] = useState<ChosenType>("php");
  const [redirect, setRedirectValue] = useState<SiteRedirect>(newRedirect());
  const [proxy, setProxyValue] = useState<SiteProxy>(newProxy());
  const nodeStatus = useNodeStatus();
  const [node, setNodeValue] = useState<NodeValue | null>(null);
  const nodeValue = node ?? newNodeApp(newestLine(nodeStatus));
  const [program, setProgramValue] = useState<ProgramValue>(newProgramApp());
  const pythonStatus = usePythonStatus();
  const [python, setPythonValue] = useState<PythonFieldsValue | null>(null);
  const pythonValue = python ?? newPythonApp(newestPythonLine(pythonStatus));
  const [tls, setTls] = useState(parked ? parkedTls : true);
  const [hsts, setHsts] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [pools, setPools] = useState<Pool[]>([]);
  const [pool, setPool] = useState("default");
  // Application profiles of PHP and static sites (ADR-0069), built-in and
  // own ones; without the list a site gets the default of its type.
  const [profiles, setProfiles] = useState<Profile[]>([]);
  const [profile, setProfile] = useState("php");

  useEffect(() => {
    listProfiles()
      .then((c) => setProfiles(c.profiles))
      .catch(() => setProfiles([]));
  }, []);

  // The pool choice shows only with a second pool (ADR-0016). Without the
  // list, the site goes to the default pool as before.
  useEffect(() => {
    listPools()
      .then((list) => {
        setPools(list);
        setPool(defaultPool(list));
      })
      .catch(() => setPools([]));
  }, []);
  const choosePool = pools.length > 1;
  const chosen = pools.find((p) => p.name === pool);

  // Default to the newest installed version that is not legacy.
  const newest = [...(php?.installed ?? [])].reverse().find((v) => !php?.legacy.includes(v)) ?? "";
  const phpValue = version ?? newest;
  const kindProfiles = profiles.filter((p) => p.type === kind);
  const defaultProfile = kind === "static" ? "static" : "php";
  const profileValue = kindProfiles.some((p) => p.name === profile) ? profile : defaultProfile;
  const chosenProfile = kindProfiles.find((p) => p.name === profileValue);
  const tooOld = kind === "php" && !!chosenProfile?.minPhp && !!phpValue && compareVersions(phpValue, chosenProfile.minPhp) < 0;

  const profileChoice = (
    <Field
      label="Application"
      hint={
        chosenProfile?.webDir
          ? `Serves the ${chosenProfile.webDir} folder and brings the hardening rules for ${chosenProfile.title}; both can be changed later.`
          : "Brings fitting hardening rules; they can be changed later under Security."
      }
    >
      <select className="ui-select" value={profileValue} onChange={(e) => setProfile(e.target.value)}>
        {kindProfiles.map((p) => (
          <option key={p.name} value={p.name}>
            {p.title}
          </option>
        ))}
      </select>
    </Field>
  );

  async function submit(e: FormEvent) {
    e.preventDefault();
    const list = parseDomains(domains);
    const problem =
      siteNameProblem(name) ||
      domainsProblem(list) ||
      (kind === "redirect" ? redirectProblem(redirect.target, list) : "") ||
      (kind === "proxy" ? proxyProblem(proxy.target, list) : "") ||
      (kind === "node" ? nodeProblem(nodeValue) : "") ||
      (kind === "app" ? programFieldsProblem(program) : "") ||
      (kind === "python" ? pythonFieldsProblem(pythonValue) : "");
    if (problem) {
      setError(problem);
      return;
    }
    setBusy(true);
    setError("");
    try {
      if (tooOld) {
        setError(`${chosenProfile!.title} needs PHP ${chosenProfile!.minPhp} or newer.`);
        setBusy(false);
        return;
      }
      if (kind === "php" && !phpValue) {
        setError("No PHP version is installed. Install one under PHP, or create a static site.");
        setBusy(false);
        return;
      }
      const s = await createSite({
        name,
        ...(title.trim() && { title: title.trim() }),
        ...(choosePool && !webOnly(kind) && { pool }),
        php: kind === "php" ? phpValue : "",
        domains: list,
        tls,
        hsts: tls && hsts,
        ...((kind === "php" || kind === "static") && profileValue !== defaultProfile && { profile: profileValue }),
        ...(kind === "redirect" && { type: "redirect" as const, redirect }),
        ...(kind === "proxy" && { type: "proxy" as const, proxy: { ...proxy, insecure: proxy.target.startsWith("https://") && proxy.insecure } }),
        ...(kind === "node" && { type: "node" as const, node: nodeValue }),
        ...(kind === "app" && { type: "app" as const, app: programRequest(program) }),
        ...(kind === "python" && { type: "python" as const, python: pythonRequest(pythonValue) }),
      });
      await onChange();
      // Straight to the new site; its overview shows the certificate state.
      window.location.hash = formatHash({ page: "sites", site: { name: s.name } });
    } catch (err) {
      setError(siteMessage(err));
    } finally {
      setBusy(false);
    }
  }

  return (
    <Card>
      <form className="ui-stack" onSubmit={(e) => void submit(e)} noValidate>
        {parked && <Notice tone="info" title={`${parked} is parked. Its parking page ends when the site is live.`} />}
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
        <Field label="Display name (optional)" hint="Shown in the panel instead of the name. Free text, up to 64 characters; it can be changed at any time.">
          <input className="ui-input" value={title} onChange={(e) => setTitleText(e.target.value)} maxLength={maxTitleLength} autoComplete="off" />
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
            {kindProfiles.length > 1 && profileChoice}
            {tooOld && <Notice tone="warn" title={`${chosenProfile!.title} needs PHP ${chosenProfile!.minPhp} or newer.`} />}
          </>
        )}
        {kind === "static" && kindProfiles.length > 1 && profileChoice}
        {kind === "static" && (
          <p className="ui-hint ui-flush">The web root folder, a fallback for single-page apps and asset caching can be set on the site's Web tab.</p>
        )}
        {kind === "node" && <NodeFields value={nodeValue} onChange={setNodeValue} status={nodeStatus} disabled={busy} admin />}
        {kind === "app" && <ProgramFields value={program} onChange={setProgramValue} disabled={busy} />}
        {kind === "python" && <PythonFields value={pythonValue} onChange={setPythonValue} status={pythonStatus} disabled={busy} admin />}
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

