// SPDX-License-Identifier: AGPL-3.0-or-later

import {
  ArrowSquareOut,
  Bug,
  CaretDown,
  ClockCounterClockwise,
  CopySimple,
  DotsThree,
  FileText,
  FolderSimple,
  GitBranch,
  Globe,
  HardDrives,
  Key,
  LockSimple,
  PencilSimple,
  Terminal,
  Timer,
} from "@phosphor-icons/react";
import { useEffect, useState, type ReactNode } from "react";
import { getBackups, type BackupInfo } from "./backups";
import { canClone } from "./Clone";
import { cronFailing, lastRun, listCron, type CronList } from "./cron";
import { getGit, listDeploys, type Deploy, type GitSetting } from "./git";
import { formatBytes, getSiteLimits, type SiteLimits } from "./limits";
import { formatHash, type SiteTab } from "./routes";
import { getSiteScan, type SiteScan } from "./scan";
import type { SiteGroup } from "./SiteNav";
import { hasAccount, type Site } from "./sites";
import { Card, Menu, type MenuItem } from "./ui";
import { relativeTime } from "./ui/Time";

type Tone = "warn" | "err" | undefined;

/** siteUrl is the address a browser opens for the site. */
export const siteUrl = (site: Site) => (site.tls.active ? "https://" : "http://") + (site.domains[0] ?? "");

const hasTab = (groups: SiteGroup[], id: SiteTab) => groups.some((g) => g.tabs.some((t) => t.id === id));
const tabHref = (site: string, tab: SiteTab) => formatHash({ page: "sites", site: { name: site, tab } });
const ago = (iso: string) => relativeTime(new Date(iso)) || "—";

function Tile({ icon, head, value, sub, tone, bar, href }: { icon: ReactNode; head: string; value: ReactNode; sub?: ReactNode; tone?: Tone; bar?: number | undefined; href: string }) {
  return (
    <li>
      <a className="ui-tile" href={href} data-tone={tone}>
        <span className="ui-tile-head">
          {icon}
          {head}
        </span>
        <span className="ui-tile-value">{value}</span>
        {bar !== undefined && (
          <span className="ui-tile-bar" aria-hidden="true">
            <span style={{ width: `${Math.min(100, Math.max(0, bar))}%` }} />
          </span>
        )}
        {sub && <span className="ui-tile-sub">{sub}</span>}
      </a>
    </li>
  );
}

// The data of the tiles. Each part stays null until it loaded; a part the
// account may not read, or that failed, leaves its tile out.
type Data = { limits: SiteLimits | null; backups: BackupInfo | null; scan: SiteScan | null; cron: CronList | null; git: GitSetting | null; deploy: Deploy | null };

function useStatus(site: Site, groups: SiteGroup[], admin: boolean): Data {
  const [d, setD] = useState<Data>({ limits: null, backups: null, scan: null, cron: null, git: null, deploy: null });
  const account = hasAccount(site);
  const malware = hasTab(groups, "malware");
  const cron = hasTab(groups, "cron");
  const git = hasTab(groups, "git");

  useEffect(() => {
    let live = true;
    const put = (p: Partial<Data>) => live && setD((o) => ({ ...o, ...p }));
    if (account) getSiteLimits(site.name).then((limits) => put({ limits }), () => undefined);
    if (admin && account) getBackups().then((backups) => put({ backups }), () => undefined);
    if (malware) getSiteScan(site.name).then((scan) => put({ scan }), () => undefined);
    if (cron) listCron(site.name).then((c) => put({ cron: c }), () => undefined);
    if (git) {
      getGit(site.name).then(
        (g) => {
          put({ git: g });
          if (g.configured) listDeploys(site.name).then((ds) => put({ deploy: ds[0] ?? null }), () => undefined);
        },
        () => undefined,
      );
    }
    return () => {
      live = false;
    };
  }, [site.name, account, admin, malware, cron, git]);
  return d;
}

/**
 * StatusTiles shows the state of a site at a glance: HTTPS, disk, backup,
 * virus scan, cron and Git deploy. A tile that needs a look is marked, and
 * each opens its tab.
 */
export function StatusTiles({ site, groups, admin }: { site: Site; groups: SiteGroup[]; admin: boolean }) {
  const d = useStatus(site, groups, admin);
  const tiles: ReactNode[] = [];
  const n = site.name;

  const tls = site.tls;
  tiles.push(
    <Tile
      key="https"
      href={tabHref(n, "https")}
      icon={<LockSimple size={14} aria-hidden="true" />}
      head="HTTPS"
      tone={tls.enabled && !tls.active ? (tls.error ? "err" : "warn") : undefined}
      value={tls.active ? (tls.notAfter ? `Valid until ${tls.notAfter.slice(0, 10)}` : "On") : tls.enabled ? (tls.error ? "Certificate failed" : "Being issued") : "Off"}
      sub={tls.active ? (tls.hsts ? "With HSTS" : "Without HSTS") : tls.error || (tls.enabled ? "" : "Visitors use plain HTTP")}
    />,
  );

  const u = d.limits?.usage;
  if (d.limits && u) {
    const max = d.limits.limits.diskBytes;
    tiles.push(
      <Tile
        key="disk"
        href={tabHref(n, "limits")}
        icon={<HardDrives size={14} aria-hidden="true" />}
        head="Disk"
        tone={u.level === "critical" ? "err" : u.level === "warning" ? "warn" : undefined}
        value={max > 0 ? `${formatBytes(u.bytes)} of ${formatBytes(max)}` : `${formatBytes(u.bytes)} used`}
        bar={max > 0 ? u.bytesPercent : undefined}
        sub={`${u.inodes.toLocaleString("en-GB")} files and folders`}
      />,
    );
  }

  if (d.backups) {
    const st = d.backups.status;
    const item = st.items.find((i) => i.site === n && i.kind === "files");
    const failed = !!item?.error || (!st.ok && !st.running && !!st.finished);
    tiles.push(
      <Tile
        key="backup"
        href={formatHash({ page: "backups" })}
        icon={<ClockCounterClockwise size={14} aria-hidden="true" />}
        head="Backup"
        tone={!d.backups.ready ? "warn" : failed ? "err" : d.backups.overdue ? "warn" : undefined}
        value={!d.backups.ready ? "Not set up" : st.running ? "Running now" : st.finished ? ago(st.finished) : "No backup yet"}
        sub={!d.backups.ready ? "Set up a backup target" : failed ? item?.error || st.error || "The last run failed" : d.backups.overdue ? "Overdue" : item ? "The last run included this site" : ""}
      />,
    );
  }

  if (d.scan) {
    const open = d.scan.findings.filter((f) => f.state === "open");
    const last = d.scan.runs.find((r) => r.state === "succeeded" || r.state === "failed");
    tiles.push(
      <Tile
        key="malware"
        href={tabHref(n, "malware")}
        icon={<Bug size={14} aria-hidden="true" />}
        head="Virus scan"
        tone={open.length > 0 ? "err" : undefined}
        value={open.length > 0 ? (open.length === 1 ? "1 file flagged" : `${open.length} files flagged`) : d.scan.engine !== "on" ? "Scanner off" : "No finds"}
        sub={open.length > 0 ? open[0]!.path : last ? `Last scan ${ago(last.finishedAt || last.createdAt)}` : "No scan yet"}
      />,
    );
  }

  if (d.cron) {
    const jobs = d.cron.jobs.filter((j) => !j.paused);
    const bad = jobs.find((j) => cronFailing(j.status));
    const failing = jobs.filter((j) => cronFailing(j.status)).length;
    tiles.push(
      <Tile
        key="cron"
        href={tabHref(n, "cron")}
        icon={<Timer size={14} aria-hidden="true" />}
        head="Cron"
        tone={bad ? "warn" : undefined}
        value={bad ? (failing === 1 ? "1 job failed" : `${failing} jobs failed`) : d.cron.jobs.length === 0 ? "No jobs" : jobs.length === 1 ? "1 job" : `${jobs.length} jobs`}
        sub={bad ? `${bad.command}: ${lastRun(bad.status)}` : d.cron.jobs.length > jobs.length ? `${d.cron.jobs.length - jobs.length} paused` : ""}
      />,
    );
  }

  if (d.git?.configured) {
    const dep = d.deploy;
    tiles.push(
      <Tile
        key="git"
        href={tabHref(n, "git")}
        icon={<GitBranch size={14} aria-hidden="true" />}
        head="Git deploy"
        tone={dep?.state === "failed" ? "err" : undefined}
        value={dep?.commit ? `${d.git.branch} @ ${dep.commit.slice(0, 7)}` : d.git.branch}
        sub={!dep ? "Not deployed yet" : dep.state === "failed" ? "The last deploy failed" : dep.state === "succeeded" ? `Deployed ${ago(dep.finishedAt || dep.createdAt)}` : "Deploying now"}
      />,
    );
  }

  return (
    <Card title="Status" actions={<span className="ui-hint">Each tile opens its tab</span>}>
      <ul className="ui-tiles" aria-label={"Status of " + n}>
        {tiles}
      </ul>
    </Card>
  );
}

/** QuickActions opens the tabs people use most. */
export function QuickActions({ site, groups }: { site: Site; groups: SiteGroup[] }) {
  const n = site.name;
  const link = (tab: SiteTab, icon: ReactNode, label: string) =>
    hasTab(groups, tab) && (
      <a className="ui-btn ui-btn-secondary" href={tabHref(n, tab)}>
        {icon}
        {label}
      </a>
    );
  return (
    <Card title="Quick actions">
      <div className="ui-row">
        {site.domains[0] && (
          <a className="ui-btn ui-btn-secondary" href={siteUrl(site)} target="_blank" rel="noopener noreferrer">
            <ArrowSquareOut size={16} aria-hidden="true" />
            Open site
          </a>
        )}
        {link("files", <FolderSimple size={16} aria-hidden="true" />, "File manager")}
        {link("logs", <FileText size={16} aria-hidden="true" />, "Logs")}
        {link("ssh", <Terminal size={16} aria-hidden="true" />, "SSH & SFTP")}
      </div>
    </Card>
  );
}

/** The ids of the overview cards the Actions menu scrolls to. */
export const cardId = (site: string, card: "rename" | "clone") => `site-${card}-${site}`;

/**
 * SiteActions is the Actions menu of the site header: rarer steps that
 * live on a tab or further down the overview.
 */
export function SiteActions({ site, groups, staff, onTab }: { site: Site; groups: SiteGroup[]; staff: boolean; onTab: (t: SiteTab) => void }) {
  // scrollTo opens the overview and brings a card of it into view.
  const scrollTo = (id: string) => () => {
    onTab("overview");
    requestAnimationFrame(() => {
      const el = document.getElementById(id);
      el?.scrollIntoView({ behavior: "smooth", block: "start" });
      el?.querySelector<HTMLElement>("input, button")?.focus({ preventScroll: true });
    });
  };

  const items: MenuItem[] = [];
  if (site.state !== "deleting") items.push({ label: "Rename…", icon: <PencilSimple size={16} aria-hidden="true" />, onSelect: scrollTo(cardId(site.name, "rename")) });
  if (staff && canClone(site)) items.push({ label: "Copy site…", icon: <CopySimple size={16} aria-hidden="true" />, onSelect: scrollTo(cardId(site.name, "clone")) });
  if (hasTab(groups, "domains")) items.push({ label: "Change domains…", icon: <Globe size={16} aria-hidden="true" />, onSelect: () => onTab("domains") });
  if (hasTab(groups, "files") && hasAccount(site)) items.push({ label: "Reset permissions…", icon: <Key size={16} aria-hidden="true" />, onSelect: () => onTab("files") });

  return (
    <Menu
      label={"Actions for " + site.name}
      items={items}
      button={
        <>
          <DotsThree size={16} aria-hidden="true" />
          Actions
          <CaretDown size={14} aria-hidden="true" />
        </>
      }
    />
  );
}
