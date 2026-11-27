// SPDX-License-Identifier: AGPL-3.0-or-later

import {
  ArrowSquareOut,
  Bug,
  CaretDown,
  ClockCounterClockwise,
  CopySimple,
  Database,
  DotsThree,
  FileText,
  FolderSimple,
  GitBranch,
  Gear,
  Globe,
  HardDrives,
  Key,
  LockSimple,
  PencilSimple,
  Terminal,
  Timer,
  X,
} from "@phosphor-icons/react";
import { useEffect, useState, type ReactNode } from "react";
import { useTranslation } from "react-i18next";
import { formatCount, formatDate } from "./format";
import { getBackups, type BackupInfo } from "./backups";
import { canClone } from "./Clone";
import { cronFailing, lastRun, listCron, type CronList } from "./cron";
import { getGit, listDeploys, type Deploy, type GitSetting } from "./git";
import { formatBytes, getSiteLimits, type SiteLimits } from "./limits";
import { formatHash, type SiteTab } from "./routes";
import { getSiteScan, type SiteScan } from "./scan";
import type { SiteGroup } from "./SiteNav";
import { hasAccount, type Site } from "./sites";
import { Card, IconButton, Menu, type MenuItem } from "./ui";
import { relativeTime } from "./ui/Time";

type Tone = "warn" | "err" | undefined;

/** siteUrl is the address a browser opens for the site. */
export const siteUrl = (site: Site) => (site.tls.active ? "https://" : "http://") + (site.domains[0] ?? "");

const hasTab = (groups: SiteGroup[], id: SiteTab) => groups.some((g) => g.tabs.some((t) => t.id === id));
const tabHref = (site: string, tab: SiteTab) => formatHash({ page: "sites", site: { name: site, tab } });
// dbHref lists the databases of a site; each opens in the database tool.
const dbHref = (site: string) => formatHash({ page: "databases", query: new URLSearchParams({ site }) });
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
  const { t } = useTranslation();
  const d = useStatus(site, groups, admin);
  const tiles: ReactNode[] = [];
  const n = site.name;

  const tls = site.tls;
  tiles.push(
    <Tile
      key="https"
      href={tabHref(n, "https")}
      icon={<LockSimple size={14} aria-hidden="true" />}
      head={t("sites.tiles.https")}
      tone={tls.enabled && !tls.active ? (tls.error ? "err" : "warn") : undefined}
      value={
        tls.active
          ? tls.notAfter
            ? t("sites.tiles.validUntil", { date: formatDate(new Date(tls.notAfter)) })
            : t("sites.tiles.on")
          : tls.enabled
            ? tls.error
              ? t("sites.tiles.certFailed")
              : t("sites.tiles.issuing")
            : t("sites.tiles.off")
      }
      sub={tls.active ? (tls.hsts ? t("sites.tiles.withHsts") : t("sites.tiles.withoutHsts")) : tls.error || (tls.enabled ? "" : t("sites.tiles.plainHttp"))}
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
        head={t("sites.tiles.disk")}
        tone={u.level === "critical" ? "err" : u.level === "warning" ? "warn" : undefined}
        value={max > 0 ? t("sites.tiles.diskOf", { used: formatBytes(u.bytes), limit: formatBytes(max) }) : t("sites.tiles.diskUsed", { used: formatBytes(u.bytes) })}
        bar={max > 0 ? u.bytesPercent : undefined}
        sub={t("sites.tiles.inodes", { count: u.inodes, n: formatCount(u.inodes) })}
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
        head={t("sites.tiles.backup")}
        tone={!d.backups.ready ? "warn" : failed ? "err" : d.backups.overdue ? "warn" : undefined}
        value={!d.backups.ready ? t("sites.tiles.notSetUp") : st.running ? t("sites.tiles.running") : st.finished ? ago(st.finished) : t("sites.tiles.noBackup")}
        sub={
          !d.backups.ready
            ? t("sites.tiles.setUp")
            : failed
              ? item?.error || st.error || t("sites.tiles.lastFailed")
              : d.backups.overdue
                ? t("sites.tiles.overdue")
                : item
                  ? t("sites.tiles.included")
                  : ""
        }
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
        head={t("sites.tiles.malware")}
        tone={open.length > 0 ? "err" : undefined}
        value={open.length > 0 ? t("sites.tiles.flagged", { count: open.length }) : d.scan.engine !== "on" ? t("sites.tiles.scannerOff") : t("sites.tiles.noFinds")}
        sub={open.length > 0 ? open[0]!.path : last ? t("sites.tiles.lastScan", { when: ago(last.finishedAt || last.createdAt) }) : t("sites.tiles.noScan")}
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
        head={t("sites.tiles.cron")}
        tone={bad ? "warn" : undefined}
        value={bad ? t("sites.tiles.jobsFailed", { count: failing }) : d.cron.jobs.length === 0 ? t("sites.tiles.noJobs") : t("sites.tiles.jobs", { count: jobs.length })}
        sub={bad ? `${bad.command}: ${lastRun(bad.status)}` : d.cron.jobs.length > jobs.length ? t("sites.tiles.paused", { count: d.cron.jobs.length - jobs.length }) : ""}
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
        head={t("sites.tiles.git")}
        tone={dep?.state === "failed" ? "err" : undefined}
        value={dep?.commit ? `${d.git.branch} @ ${dep.commit.slice(0, 7)}` : d.git.branch}
        sub={
          !dep
            ? t("sites.tiles.notDeployed")
            : dep.state === "failed"
              ? t("sites.tiles.deployFailed")
              : dep.state === "succeeded"
                ? t("sites.tiles.deployed", { when: ago(dep.finishedAt || dep.createdAt) })
                : t("sites.tiles.deploying")
        }
      />,
    );
  }

  return (
    <Card title={t("sites.tiles.title")} actions={<span className="ui-hint">{t("sites.tiles.hint")}</span>}>
      <ul className="ui-tiles" aria-label={t("sites.tiles.label", { site: n })}>
        {tiles}
      </ul>
    </Card>
  );
}

/** QuickActions opens the tabs people use most. */
export function QuickActions({ site, groups }: { site: Site; groups: SiteGroup[] }) {
  const { t } = useTranslation();
  const n = site.name;
  const link = (tab: SiteTab, icon: ReactNode, label: string) =>
    hasTab(groups, tab) && (
      <a className="ui-btn ui-btn-secondary" href={tabHref(n, tab)}>
        {icon}
        {label}
      </a>
    );
  return (
    <Card title={t("sites.quick.title")}>
      <div className="ui-row">
        {site.domains[0] && (
          <a className="ui-btn ui-btn-secondary" href={siteUrl(site)} target="_blank" rel="noopener noreferrer">
            <ArrowSquareOut size={16} aria-hidden="true" />
            {t("sites.quick.open")}
          </a>
        )}
        {link("files", <FolderSimple size={16} aria-hidden="true" />, t("sites.quick.files"))}
        {link("logs", <FileText size={16} aria-hidden="true" />, t("sites.quick.logs"))}
        {hasAccount(site) && (
          <a className="ui-btn ui-btn-secondary" href={dbHref(n)}>
            <Database size={16} aria-hidden="true" />
            {t("sites.quick.databases")}
          </a>
        )}
        {link("ssh", <Terminal size={16} aria-hidden="true" />, t("sites.quick.ssh"))}
      </div>
    </Card>
  );
}

/** CloseButton hides an overview card the Actions menu opened. */
export function CloseButton({ onClose }: { onClose: () => void }) {
  const { t } = useTranslation();
  return <IconButton data-close="" label={t("sites.actions.close")} icon={<X size={16} aria-hidden="true" />} onClick={onClose} />;
}

/** The overview cards that only show when the Actions menu opens them. */
export type OverviewCard = "rename" | "clone" | "services";

/** The ids of the overview cards the Actions menu scrolls to. */
export const cardId = (site: string, card: OverviewCard) => `site-${card}-${site}`;

/**
 * SiteActions is the Actions menu of the site header: rarer steps. Rename,
 * copy and the services open their card at the top of the overview; the
 * others go to their tab.
 */
export function SiteActions({ site, groups, staff, admin, onTab, onCard }: { site: Site; groups: SiteGroup[]; staff: boolean; admin: boolean; onTab: (t: SiteTab) => void; onCard: (c: OverviewCard) => void }) {
  const { t } = useTranslation();
  // open shows a card on the overview and brings it into view.
  const open = (card: OverviewCard) => () => {
    onCard(card);
    onTab("overview");
    requestAnimationFrame(() =>
      requestAnimationFrame(() => {
        const el = document.getElementById(cardId(site.name, card));
        el?.scrollIntoView?.({ behavior: "smooth", block: "start" });
        el?.querySelector<HTMLElement>("input, button:not([data-close])")?.focus({ preventScroll: true });
      }),
    );
  };

  const items: MenuItem[] = [];
  if (site.state !== "deleting") items.push({ label: t("sites.actions.rename"), icon: <PencilSimple size={16} aria-hidden="true" />, onSelect: open("rename") });
  if (staff && canClone(site)) items.push({ label: t("sites.actions.copy"), icon: <CopySimple size={16} aria-hidden="true" />, onSelect: open("clone") });
  if (staff && hasTab(groups, "domains")) items.push({ label: t("sites.actions.domains"), icon: <Globe size={16} aria-hidden="true" />, onSelect: () => onTab("domains") });
  if (hasAccount(site)) items.push({ label: t("sites.actions.databases"), icon: <Database size={16} aria-hidden="true" />, href: dbHref(site.name) });
  if (admin && hasAccount(site)) items.push({ label: site.php ? t("sites.actions.restartPhp") : t("sites.actions.services"), icon: <Gear size={16} aria-hidden="true" />, onSelect: open("services") });
  if (hasTab(groups, "files") && hasAccount(site)) items.push({ label: t("sites.actions.permissions"), icon: <Key size={16} aria-hidden="true" />, onSelect: () => onTab("files") });
  return (
    <Menu
      label={t("sites.actions.label", { site: site.name })}
      items={items}
      button={
        <>
          <DotsThree size={16} aria-hidden="true" />
          {t("sites.actions.button")}
          <CaretDown size={14} aria-hidden="true" />
        </>
      }
    />
  );
}
