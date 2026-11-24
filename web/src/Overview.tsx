// SPDX-License-Identifier: AGPL-3.0-or-later

import { ArrowRight } from "@phosphor-icons/react";
import { useEffect, useState, type ReactNode } from "react";
import type { User } from "./auth";
import { daysLeft } from "./certs";
import { ServerDashboard } from "./Dashboard";
import { getBackups, type BackupInfo } from "./backups";
import { checkTitle, runDoctor, type DoctorReport } from "./doctor";
import { canSee } from "./nav";
import { listParked, type ParkedDomain } from "./parked";
import { formatHash } from "./routes";
import { bytes } from "./services";
import { listPools, listSites, poolUsed, type Pool, type Site } from "./sites";
import { Badge, Card, Meter, Notice, PageHead, RelTime, type Tone } from "./ui";

// Item is one thing that needs someone to act, with a link to where they can.
type Item = { key: string; tone: Tone; word: string; title: string; detail?: string; href: string; link: string };

// What the overview could load; a part the role may not see, or that
// failed to load, stays undefined and is left out.
type Data = { sites?: Site[]; doctor?: DoctorReport; backups?: BackupInfo; parked?: ParkedDomain[]; pools?: Pool[] };

const ignore = () => undefined;

// attention collects the problems that need action. With Health the
// doctor's checks cover the sites; without it, failed site certificates
// stand in for them.
export function attention(d: Data): Item[] {
  const items: Item[] = [];
  if (d.backups) {
    const st = d.backups.status;
    if (!d.backups.ready) {
      items.push({ key: "backups-setup", tone: "warn", word: "Warning", title: "Backups are not set up", href: formatHash({ page: "backups" }), link: "Set up backups" });
    } else if (st.started && !st.running && !st.ok) {
      items.push({
        key: "backups",
        tone: "err",
        word: "Failed",
        title: "The last backup failed",
        ...(st.error ? { detail: st.error } : {}),
        href: formatHash({ page: "backups" }),
        link: "Open backups",
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
      word: left < 7 ? "Failed" : "Warning",
      title: left < 0 ? `The own certificate of site ${s.name} has expired` : `The own certificate of site ${s.name} expires in ${left} days`,
      detail: "It is not renewed automatically. Upload a new one, or switch the site to Let's Encrypt.",
      href: formatHash({ page: "sites", site: { name: s.name, tab: "domains" } }),
      link: "Open domains",
    });
  }
  if (d.doctor) {
    for (const c of d.doctor.checks) {
      if (c.status === "ok") continue;
      if (c.id === "site.tls" && c.site && own.has(c.site)) continue;
      const where = c.site ? "site " + c.site : "the server";
      items.push({
        key: "doctor-" + c.id + (c.site ?? ""),
        tone: c.status === "fail" ? "err" : "warn",
        word: c.status === "fail" ? "Failed" : "Warning",
        title: `${checkTitle(c.id)} on ${where}: ${c.summary}`,
        ...(c.hint ? { detail: c.hint } : {}),
        href: c.site ? formatHash({ page: "sites", site: { name: c.site, tab: "overview" } }) : formatHash({ page: "health" }),
        link: c.site ? "Open " + c.site : "Open health",
      });
    }
  } else if (d.sites) {
    for (const s of d.sites) {
      if (!s.tls.enabled || s.tls.active || !s.tls.error) continue;
      items.push({
        key: "tls-" + s.name,
        tone: "err",
        word: "Failed",
        title: `No certificate for site ${s.name}`,
        detail: s.tls.error,
        href: formatHash({ page: "sites", site: { name: s.name, tab: "domains" } }),
        link: "Open domains",
      });
    }
  }
  for (const p of d.parked ?? []) {
    const left = p.tls.certSource === "custom" ? daysLeft(p.tls.notAfter) : null;
    if (left === null || left >= 30) continue;
    items.push({
      key: "owncert-park-" + p.domain,
      tone: left < 7 ? "err" : "warn",
      word: left < 7 ? "Failed" : "Warning",
      title: left < 0 ? `The own certificate of ${p.domain} has expired` : `The own certificate of ${p.domain} expires in ${left} days`,
      detail: "It is not renewed automatically. Upload a new one, or switch the domain to Let's Encrypt.",
      href: formatHash({ page: "parked", query: new URLSearchParams({ edit: p.domain }) }),
      link: "Open " + p.domain,
    });
  }
  for (const p of d.parked ?? []) {
    if (!p.tls.enabled || p.tls.active || !p.tls.error) continue;
    items.push({
      key: "parked-" + p.domain,
      tone: "err",
      word: "Failed",
      title: `No certificate for the parked domain ${p.domain}`,
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
  const [data, setData] = useState<Data | null>(null);
  const may = (p: Parameters<typeof canSee>[1]) => canSee(user.role, p);

  useEffect(() => {
    let live = true;
    const opt = <T,>(ok: boolean, f: () => Promise<T>) => (ok ? f().catch(ignore) : Promise.resolve(undefined));
    void Promise.all([
      opt(true, listSites),
      opt(canSee(user.role, "health"), runDoctor),
      opt(canSee(user.role, "backups"), getBackups),
      opt(canSee(user.role, "parked"), listParked),
      opt(canSee(user.role, "pools"), listPools),
    ]).then(([sites, doctor, backups, parked, pools]) => {
      if (!live) return;
      setData({
        ...(sites ? { sites } : {}),
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

  const head = <PageHead title="Overview" description={`Welcome, ${user.username}.`} />;
  if (!data) {
    return (
      <>
        {head}
        <p aria-busy="true">Loading…</p>
      </>
    );
  }

  const items = attention(data);
  const sites = data.sites ?? [];
  const disabled = sites.filter((s) => s.disabled).length;
  const maintenance = sites.filter((s) => s.maintenance.enabled).length;
  const siteNote = [disabled && `${disabled} disabled`, maintenance && `${maintenance} in maintenance`].filter(Boolean).join(", ");

  return (
    <>
      {head}
      <Card title="Needs attention" padded={items.length === 0}>
        {items.length === 0 ? (
          <p className="ui-flush">Nothing needs attention.</p>
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
          <Stat title="Sites" href={formatHash({ page: "sites" })} link="All sites">
            <span className="ui-stat-value">{sites.length}</span>
            {siteNote && <p className="ui-hint ui-flush">{siteNote}</p>}
          </Stat>
        )}
        {may("health") && data.doctor && (
          <Stat title="Health" href={formatHash({ page: "health" })} link="Open health">
            <Badge tone={data.doctor.status === "ok" ? "ok" : data.doctor.status === "warn" ? "warn" : "err"}>
              {data.doctor.status === "ok" ? "OK" : data.doctor.status === "warn" ? "Warnings" : "Problems"}
            </Badge>
            <p className="ui-hint ui-flush">
              Checked <RelTime iso={data.doctor.checkedAt} />
            </p>
          </Stat>
        )}
        {may("backups") && data.backups && (
          <Stat title="Backups" href={formatHash({ page: "backups" })} link="Open backups">
            {data.backups.status.finished ? (
              <>
                <Badge tone={data.backups.status.ok ? "ok" : "err"}>{data.backups.status.ok ? "OK" : "Failed"}</Badge>
                <p className="ui-hint ui-flush">
                  Last backup <RelTime iso={data.backups.status.finished} />
                </p>
              </>
            ) : (
              <Badge tone="off">{data.backups.ready ? "No backup yet" : "Not set up"}</Badge>
            )}
          </Stat>
        )}
      </div>
      {may("pools") && data.pools && data.pools.length > 0 && <PoolUsage pools={data.pools} />}
      {may("services") && <ServerDashboard />}
      {!data.sites && <Notice tone="err" title="The sites could not be loaded. Reload the page." />}
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
  return (
    <Card
      title="Storage pools"
      actions={
        <a className="ui-stat-link" href={formatHash({ page: "pools" })}>
          Open storage pools
          <ArrowRight size={14} aria-hidden="true" />
        </a>
      }
    >
      <div className="ui-stack">
        {pools.map((p) => {
          const used = poolUsed(p);
          const label = `${p.name}${p.default ? " (default)" : ""}`;
          const sites = p.sites === 1 ? "1 site" : `${p.sites} sites`;
          if (used === null || !p.totalBytes) {
            return (
              <div className="ui-meter" key={p.name}>
                <div className="ui-meter-head">
                  <strong>{label}</strong>
                  <span>
                    <Badge tone="err">Not available</Badge> {sites}
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
              detail={`${bytes(used)} of ${bytes(p.totalBytes)} used, ${bytes(p.freeBytes ?? 0)} free · ${sites}`}
            />
          );
        })}
      </div>
    </Card>
  );
}
