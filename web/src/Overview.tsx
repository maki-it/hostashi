// SPDX-License-Identifier: AGPL-3.0-or-later

import { ArrowRight } from "@phosphor-icons/react";
import { useEffect, useState, type ReactNode } from "react";
import { useTranslation } from "react-i18next";
import type { User } from "./auth";
import { daysLeft } from "./certs";
import { ServerDashboard } from "./Dashboard";
import { SitesByUsage } from "./Usage";
import { getBackups, overdueHours, type BackupInfo } from "./backups";
import { checkTitle, runDoctor, type DoctorReport } from "./doctor";
import { canSee } from "./nav";
import { listParked, type ParkedDomain } from "./parked";
import i18n from "./i18n";
import { formatHash } from "./routes";
import { bytes } from "./services";
import { cronFailing, listCron } from "./cron";
import { groupsForSite } from "./SiteNav";
import { listPools, listSitesMarked, poolUsed, type Pool, type Site, type SiteMarks } from "./sites";
import { Badge, Card, Meter, Notice, PageHead, RelTime, type Tone } from "./ui";

// Item is one thing that needs someone to act, with a link to where they can.
type Item = { key: string; tone: Tone; word: string; title: string; detail?: string; href: string; link: string };

// What the overview could load; a part the role may not see, or that
// failed to load, stays undefined and is left out.
type Data = { sites?: Site[]; marks?: Record<string, SiteMarks>; cron?: Record<string, number>; doctor?: DoctorReport; backups?: BackupInfo; parked?: ParkedDomain[]; pools?: Pool[] };

const ignore = () => undefined;

// attention collects the problems that need action. With Health the
// doctor's checks cover the sites; without it, failed site certificates
// stand in for them.
export function attention(d: Data): Item[] {
  const t = i18n.t;
  const warning = t("overview.word.warning");
  const failed = t("overview.word.failed");
  const items: Item[] = [];
  if (d.backups) {
    const st = d.backups.status;
    if (!d.backups.ready) {
      items.push({ key: "backups-setup", tone: "warn", word: warning, title: t("overview.attention.backupsNotSetUp"), href: formatHash({ page: "backups" }), link: t("overview.attention.setUpBackups") });
    } else if (d.backups.overdue) {
      items.push({
        key: "backups-overdue",
        tone: "warn",
        word: warning,
        title: t("overview.attention.backupOverdue"),
        detail: t("overview.attention.backupOverdueDetail", { hours: overdueHours(d.backups.settings.intervalHours) }),
        href: formatHash({ page: "backups" }),
        link: t("overview.attention.openBackups"),
      });
    } else if (st.started && !st.running && !st.ok) {
      items.push({
        key: "backups",
        tone: "err",
        word: failed,
        title: t("overview.attention.backupFailed"),
        ...(st.error ? { detail: st.error } : {}),
        href: formatHash({ page: "backups" }),
        link: t("overview.attention.openBackups"),
      });
    }
  }
  // Own certificates are never renewed by the panel (ADR-0039): 30 and 7
  // days before expiry they need someone. They replace the doctor's check.
  const own = new Set<string>();
  for (const s of d.sites ?? []) {
    if (s.tls.certSource !== "custom") continue;
    own.add(s.name);
    const left = daysLeft(s.tls.notAfter);
    if (left === null || left >= 30) continue;
    items.push({
      key: "owncert-" + s.name,
      tone: left < 7 ? "err" : "warn",
      word: left < 7 ? failed : warning,
      title: left < 0 ? t("overview.attention.siteCertExpired", { site: s.name }) : t("overview.attention.siteCertExpires", { site: s.name, count: left }),
      detail: t("overview.attention.siteCertDetail"),
      href: formatHash({ page: "sites", site: { name: s.name, tab: "domains" } }),
      link: t("overview.attention.openDomains"),
    });
  }
  if (d.doctor) {
    for (const c of d.doctor.checks) {
      if (c.status === "ok") continue;
      if (c.id === "site.tls" && c.site && own.has(c.site)) continue;
      items.push({
        key: "doctor-" + c.id + (c.site ?? ""),
        tone: c.status === "fail" ? "err" : "warn",
        word: c.status === "fail" ? failed : warning,
        title: c.site
          ? t("overview.attention.checkOnSite", { check: checkTitle(c.id), site: c.site, summary: c.summary })
          : t("overview.attention.checkOnServer", { check: checkTitle(c.id), summary: c.summary }),
        ...(c.hint ? { detail: c.hint } : {}),
        href: c.site ? formatHash({ page: "sites", site: { name: c.site, tab: "overview" } }) : formatHash({ page: "health" }),
        link: c.site ? t("overview.attention.open", { name: c.site }) : t("overview.attention.openHealth"),
      });
    }
  } else if (d.sites) {
    for (const s of d.sites) {
      if (!s.tls.enabled || s.tls.active || !s.tls.error) continue;
      items.push({
        key: "tls-" + s.name,
        tone: "err",
        word: failed,
        title: t("overview.attention.siteNoCert", { site: s.name }),
        detail: s.tls.error,
        href: formatHash({ page: "sites", site: { name: s.name, tab: "domains" } }),
        link: t("overview.attention.openDomains"),
      });
    }
  }
  // Virus finds are no doctor check: they come from the sites list.
  for (const s of d.sites ?? []) {
    const n = d.marks?.[s.name]?.openFindings ?? 0;
    if (n === 0) continue;
    items.push({
      key: "malware-" + s.name,
      tone: "err",
      word: failed,
      title: t("overview.attention.malware", { count: n, site: s.name }),
      href: formatHash({ page: "sites", site: { name: s.name, tab: "malware" } }),
      link: t("overview.attention.openMalware"),
    });
  }
  // Without Health, failing cron jobs of the own sites stand in for it.
  for (const [site, n] of Object.entries(d.cron ?? {})) {
    if (n === 0) continue;
    items.push({
      key: "cron-" + site,
      tone: "warn",
      word: warning,
      title: t("overview.attention.cron", { count: n, site }),
      href: formatHash({ page: "sites", site: { name: site, tab: "cron" } }),
      link: t("overview.attention.openCron"),
    });
  }
  for (const p of d.parked ?? []) {
    const left = p.tls.certSource === "custom" ? daysLeft(p.tls.notAfter) : null;
    if (left === null || left >= 30) continue;
    items.push({
      key: "owncert-park-" + p.domain,
      tone: left < 7 ? "err" : "warn",
      word: left < 7 ? failed : warning,
      title: left < 0 ? t("overview.attention.domainCertExpired", { domain: p.domain }) : t("overview.attention.domainCertExpires", { domain: p.domain, count: left }),
      detail: t("overview.attention.domainCertDetail"),
      href: formatHash({ page: "parked", query: new URLSearchParams({ edit: p.domain }) }),
      link: "Open " + p.domain,
    });
  }
  for (const p of d.parked ?? []) {
    if (!p.tls.enabled || p.tls.active || !p.tls.error) continue;
    items.push({
      key: "parked-" + p.domain,
      tone: "err",
      word: failed,
      title: t("overview.attention.parkedNoCert", { domain: p.domain }),
      detail: p.tls.error,
      href: formatHash({ page: "parked", query: new URLSearchParams({ edit: p.domain }) }),
      link: "Open " + p.domain,
    });
  }
  const rank = (t: Tone) => (t === "err" ? 0 : 1);
  return items.sort((a, b) => rank(a.tone) - rank(b.tone));
}

// Overview is the first page after login: what needs attention, and the
// state of sites, health and backups at a glance.
export function Overview({ user }: { user: User }) {
  const { t } = useTranslation();
  const [data, setData] = useState<Data | null>(null);
  const may = (p: Parameters<typeof canSee>[1]) => canSee(user.role, p);

  useEffect(() => {
    let live = true;
    const opt = <T,>(ok: boolean, f: () => Promise<T>) => (ok ? f().catch(ignore) : Promise.resolve(undefined));
    void Promise.all([
      opt(true, listSitesMarked),
      opt(canSee(user.role, "health"), runDoctor),
      opt(canSee(user.role, "backups"), getBackups),
      opt(canSee(user.role, "parked"), listParked),
      opt(canSee(user.role, "pools"), listPools),
    ]).then(async ([list, doctor, backups, parked, pools]) => {
      const sites = list?.sites;
      // Without Health, look at the cron jobs of the (few) own sites.
      const cron: Record<string, number> = {};
      if (!canSee(user.role, "health") && sites) {
        await Promise.all(
          sites
            .filter((s) => s.state === "active" && groupsForSite(s).some((g) => g.tabs.some((t) => t.id === "cron")))
            .slice(0, 20)
            .map((s) =>
              listCron(s.name)
                .then((l) => {
                  cron[s.name] = l.jobs.filter((j) => !j.paused && cronFailing(j.status)).length;
                })
                .catch(ignore),
            ),
        );
      }
      if (!live) return;
      setData({
        ...(sites ? { sites, marks: list.marks, cron } : {}),
        ...(doctor ? { doctor } : {}),
        ...(backups ? { backups } : {}),
        ...(parked ? { parked } : {}),
        ...(pools ? { pools } : {}),
      });
    });
    return () => {
      live = false;
    };
  }, [user.role]);

  const head = <PageHead title={t("overview.title")} description={t("overview.welcome", { name: user.username })} />;
  if (!data) {
    return (
      <>
        {head}
        <p aria-busy="true">{t("common.status.loading")}</p>
      </>
    );
  }

  const items = attention(data);
  const sites = data.sites ?? [];
  const disabled = sites.filter((s) => s.disabled).length;
  const maintenance = sites.filter((s) => s.maintenance.enabled).length;
  const siteNote = [disabled && t("overview.stats.disabled", { count: disabled }), maintenance && t("overview.stats.maintenance", { count: maintenance })].filter(Boolean).join(", ");

  return (
    <>
      {head}
      <Card
        title={t("overview.attention.title")}
        padded={items.length === 0}
        actions={
          may("health") &&
          data.doctor && (
            <span className="ui-hint">
              {t("overview.attention.checksRun")} <RelTime iso={data.doctor.checkedAt} /> · <a href={formatHash({ page: "health" })}>{t("overview.attention.allChecks")}</a>
            </span>
          )
        }
      >
        {items.length === 0 ? (
          <p className="ui-flush">{t("overview.attention.none")}</p>
        ) : (
          <ul className="ui-attention-list">
            {items.map((i) => (
              <li key={i.key}>
                <Badge tone={i.tone}>{i.word}</Badge>
                <div className="ui-stack-tight">
                  <strong>{i.title}</strong>
                  {i.detail && <p className="ui-hint ui-flush">{i.detail}</p>}
                </div>
                <a className="ui-btn" href={i.href}>
                  {i.link}
                </a>
              </li>
            ))}
          </ul>
        )}
      </Card>

      <div className="ui-stats">
        {data.sites && (
          <Stat title={t("overview.stats.sites")} href={formatHash({ page: "sites" })} link={t("overview.stats.allSites")}>
            <span className="ui-stat-value">{sites.length}</span>
            {siteNote && <p className="ui-hint ui-flush">{siteNote}</p>}
          </Stat>
        )}
        {may("backups") && data.backups && (
          <Stat title={t("overview.stats.backups")} href={formatHash({ page: "backups" })} link={t("overview.attention.openBackups")}>
            {data.backups.status.finished ? (
              <>
                <Badge tone={data.backups.overdue ? "warn" : data.backups.status.ok ? "ok" : "err"}>
                  {data.backups.overdue ? t("overview.stats.overdue") : data.backups.status.ok ? t("overview.stats.ok") : t("overview.stats.failed")}
                </Badge>
                <p className="ui-hint ui-flush">
                  {t("overview.stats.lastBackup")} <RelTime iso={data.backups.status.finished} />
                </p>
              </>
            ) : (
              <Badge tone="off">{data.backups.ready ? t("overview.stats.noBackup") : t("overview.stats.notSetUp")}</Badge>
            )}
          </Stat>
        )}
      </div>
      {may("pools") && data.pools && data.pools.length > 0 && <PoolUsage pools={data.pools} />}
      {may("services") && <ServerDashboard />}
      {may("services") && <SitesByUsage />}
      {!data.sites && <Notice tone="err" title={t("overview.sitesFailed")} />}
    </>
  );
}

function Stat({ title, href, link, children }: { title: string; href: string; link: string; children: ReactNode }) {
  return (
    <Card title={title}>
      <div className="ui-stack-tight">
        {children}
        <a className="ui-stat-link" href={href}>
          {link}
          <ArrowRight size={14} aria-hidden="true" />
        </a>
      </div>
    </Card>
  );
}

// PoolUsage shows how full each storage pool is (ADR-0016 addendum 2).
function PoolUsage({ pools }: { pools: Pool[] }) {
  const { t } = useTranslation();
  return (
    <Card
      title={t("overview.pools.title")}
      actions={
        <a className="ui-stat-link" href={formatHash({ page: "pools" })}>
          {t("overview.pools.open")}
          <ArrowRight size={14} aria-hidden="true" />
        </a>
      }
    >
      <div className="ui-stack">
        {pools.map((p) => {
          const used = poolUsed(p);
          const label = p.default ? t("overview.pools.default", { name: p.name }) : p.name;
          const sites = t("overview.pools.sites", { count: p.sites });
          if (used === null || !p.totalBytes) {
            return (
              <div className="ui-meter" key={p.name}>
                <div className="ui-meter-head">
                  <strong>{label}</strong>
                  <span>
                    <Badge tone="err">{t("overview.pools.unavailable")}</Badge> {sites}
                  </span>
                </div>
              </div>
            );
          }
          return (
            <Meter
              key={p.name}
              label={label}
              value={used}
              max={p.totalBytes}
              detail={t("overview.pools.usage", { used: bytes(used), total: bytes(p.totalBytes), free: bytes(p.freeBytes ?? 0), sites })}
            />
          );
        })}
      </div>
    </Card>
  );
}
