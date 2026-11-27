// SPDX-License-Identifier: AGPL-3.0-or-later

import { ClockCounterClockwise, CloudArrowUp } from "@phosphor-icons/react";
import { useCallback, useEffect, useState, type FormEvent, type ReactNode } from "react";
import { Trans, useTranslation } from "react-i18next";
import {
  backupMessage,
  confirmHostKey,
  createDatabase,
  deletedSnapshots,
  getBackups,
  getImport,
  getRestore,
  everyText,
  importDump,
  intervals,
  overdueHours,
  keepBackups,
  listDeleted,
  markRemoval,
  listSites,
  poll,
  runBackup,
  saveSettings,
  scanHostKey,
  siteSnapshots,
  startDeletedRestore,
  startRestore,
  type BackupInfo,
  type DeletedSite,
  type Restore,
  type Settings,
  type Site,
  type Snapshot,
} from "./backups";
import { isPostgres, listServers, type DBServer } from "./databases";
import i18n from "./i18n";
import { Badge, Button, Card, CopyButton, exactTime, Field, IconButton, Notice, PageHead, RelTime, RowActions, Table, type Column, type Sort, type Tone } from "./ui";

type QueryProps = {
  /** The address query: site (snapshots of that site) or deleted (of a deleted site), sort, dir. */
  query: URLSearchParams;
  /** Replaces the query in the address without a new history entry. */
  onQuery: (q: URLSearchParams) => void;
};

// Backups is the backup page (ADR-0037): status with "Back up now", the
// snapshots of a site in a table with restore, then target, schedule and
// host key.
export function Backups({ query, onQuery }: QueryProps) {
  const { t } = useTranslation();
  const [info, setInfo] = useState<BackupInfo | null>(null);
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const [note, setNote] = useState("");

  const load = useCallback(async () => {
    try {
      setInfo(await getBackups());
      setError("");
    } catch (err) {
      setError(backupMessage(err));
    }
  }, []);

  useEffect(() => {
    void load();
  }, [load]);

  async function run() {
    setBusy(true);
    setError("");
    setNote("");
    try {
      await runBackup();
      setNote(t("backups.started"));
      await load();
    } catch (err) {
      setError(backupMessage(err));
    } finally {
      setBusy(false);
    }
  }

  return (
    <>
      <PageHead
        title={t("backups.title")}
        description={t("backups.description")}
        actions={
          info?.ready && (
            <Button variant="primary" icon={<CloudArrowUp size={16} aria-hidden="true" />} disabled={busy || info.status.running} onClick={() => void run()}>
              {t("backups.backUpNow")}
            </Button>
          )
        }
      />
      {error && <Notice tone="err" title={error} />}
      {note && <Notice tone="info" title={note} />}
      {!info ? (
        !error && <p aria-busy="true">{t("common.status.loading")}</p>
      ) : (
        <>
          <StatusCard info={info} />
          {info.ready && <SnapshotsCard query={query} onQuery={onQuery} />}
          <TargetCard info={info} onSaved={setInfo} />
          {info.settings.target && <HostKeyCard info={info} onConfirmed={setInfo} />}
        </>
      )}
    </>
  );
}

function statusOf(info: BackupInfo): { tone: Tone; word: string; summary: ReactNode } {
  const t = i18n.t;
  const st = info.status;
  if (!info.ready) return { tone: "off", word: t("backups.status.notSetUp"), summary: t("backups.status.notSetUpText") };
  if (st.running) return { tone: "warn", word: t("backups.status.running"), summary: t("backups.status.runningText") };
  if (!st.started) return { tone: "off", word: t("backups.status.none"), summary: t("backups.status.noneText") };
  if (info.overdue) {
    const h = info.settings.intervalHours;
    return {
      tone: "warn",
      word: t("backups.status.overdue"),
      summary: <Trans i18nKey="backups.status.overdueText" values={{ hours: overdueHours(h), every: everyText(h) }} components={{ code: <code /> }} />,
    };
  }
  if (st.ok) return { tone: "ok", word: t("backups.status.ok"), summary: t("backups.status.okText") };
  return { tone: "err", word: t("backups.status.failed"), summary: st.error ? t("backups.status.failedWith", { error: st.error }) : t("backups.status.failedText") };
}

const timeOrNever = (t?: string) => (t ? <RelTime iso={t} /> : i18n.t("backups.status.never"));

// kindLabel names what a failed item of a run backed up.
const kindLabel = (kind: string) => (kind === "files" || kind === "db" || kind === "panel" ? i18n.t(`backups.status.kinds.${kind}`) : kind);

function StatusCard({ info }: { info: BackupInfo }) {
  const { t } = useTranslation();
  const st = info.status;
  const failed = st.items.filter((i) => i.error);
  const s = statusOf(info);

  return (
    <Card title={t("backups.status.title")} actions={<Badge tone={s.tone}>{s.word}</Badge>}>
      <div className="ui-stack">
        <p className="ui-flush">{s.summary}</p>
        {st.started && (
          <dl className="ui-facts">
            <dt>{t("backups.status.lastRun")}</dt>
            <dd>{timeOrNever(st.started)}</dd>
            <dt>{t("backups.status.finished")}</dt>
            <dd>{timeOrNever(st.finished)}</dd>
            <dt>{t("backups.status.lastCheck")}</dt>
            <dd>{timeOrNever(st.lastCheck)}</dd>
            <dt>{t("backups.status.lastDataCheck")}</dt>
            <dd>{timeOrNever(st.lastReadCheck)}</dd>
          </dl>
        )}
        {failed.length > 0 && (
          <ul className="ui-error ui-flush">
            {failed.map((i) => (
              <li key={i.kind + (i.site ?? "") + (i.database ?? "")}>
                {kindLabel(i.kind)} {i.site ?? ""} {i.database ?? ""}: {i.error}
              </li>
            ))}
          </ul>
        )}
        {!st.maintenanceOk && st.maintenance && <p className="ui-error ui-flush">{t("backups.status.maintenance", { error: st.maintenance })}</p>}
        {st.removed && st.removed.length > 0 && (
          <ul className="ui-flush">
            {st.removed.map((x) => (
              <li key={x.site}>
                <Trans
                  i18nKey={x.expired ? "backups.status.removedExpired" : "backups.status.removed"}
                  count={x.snapshots}
                  values={{ count: x.snapshots, site: x.site }}
                  components={{ code: <code /> }}
                />
              </li>
            ))}
          </ul>
        )}
        {st.removeError && <p className="ui-error ui-flush">{t("backups.status.removeFailed", { error: st.removeError })}</p>}
      </div>
    </Card>
  );
}

// A Hetzner Storage Box serves SFTP on port 23 and has its own key setup;
// the form only adds hints for it, the target itself stays plain SFTP.
const isStorageBox = (host: string) => /\.your-storagebox\.de$/.test(host.trim().toLowerCase());

function num(v: string, fallback: number) {
  const n = Number.parseInt(v, 10);
  return Number.isNaN(n) ? fallback : n;
}

function TargetCard({ info, onSaved }: { info: BackupInfo; onSaved: (i: BackupInfo) => void }) {
  const { t } = useTranslation();
  const s = info.settings;
  const [user, setUser] = useState(s.target?.user ?? "");
  const [host, setHost] = useState(s.target?.host ?? "");
  const [port, setPort] = useState(String(s.target?.port ?? 22));
  const [path, setPath] = useState(s.target?.path ?? "hostashi");
  const [hourly, setHourly] = useState(String(s.retention.hourly ?? 0));
  const [daily, setDaily] = useState(String(s.retention.daily));
  const [weekly, setWeekly] = useState(String(s.retention.weekly));
  const [monthly, setMonthly] = useState(String(s.retention.monthly));
  const [yearly, setYearly] = useState(String(s.retention.yearly ?? 0));
  const [interval, setIntervalHours] = useState(String(s.intervalHours ?? 24));
  const [start, setStart] = useState(String(s.windowStart));
  const [end, setEnd] = useState(String(s.windowEnd));
  const [parts, setParts] = useState(String(s.readParts));
  const [goneDays, setGoneDays] = useState(String(s.deletedSiteDays ?? 0));
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [saved, setSaved] = useState(false);
  const storageBox = isStorageBox(host);
  const savedStorageBox = isStorageBox(s.target?.host ?? "");

  async function submit(e: FormEvent) {
    e.preventDefault();
    setBusy(true);
    setError("");
    setSaved(false);
    const next: Settings = {
      version: s.version,
      target: { user: user.trim(), host: host.trim().toLowerCase(), port: num(port, 22), path: path.trim() },
      retention: { hourly: num(hourly, 0), daily: num(daily, 7), weekly: num(weekly, 4), monthly: num(monthly, 6), yearly: num(yearly, 0) },
      intervalHours: num(interval, 24),
      windowStart: num(start, 1),
      windowEnd: num(end, 5),
      readParts: num(parts, 12),
      deletedSiteDays: num(goneDays, 0),
    };
    try {
      onSaved(await saveSettings(next));
      setSaved(true);
    } catch (err) {
      setError(backupMessage(err));
    } finally {
      setBusy(false);
    }
  }

  return (
    <Card title={t("backups.target.title")}>
      <form className="ui-stack" onSubmit={(e) => void submit(e)}>
        <fieldset className="ui-fieldset ui-group ui-stack">
          <legend>{t("backups.target.server")}</legend>
          <div className="ui-fields ui-fields-server">
            <Field label={t("backups.target.user")}>
              <input className="ui-input" value={user} onChange={(e) => setUser(e.target.value)} required placeholder={t("backups.target.userExample")} autoComplete="off" />
            </Field>
            <Field label={t("backups.target.host")} hint={t("backups.target.hostHint")}>
              <input className="ui-input" value={host} onChange={(e) => setHost(e.target.value)} required placeholder="backup.example.com" autoComplete="off" />
            </Field>
            <Field
              label={t("backups.target.port")}
              {...(storageBox && (port.trim() === "23" ? { hint: t("backups.target.portStorageBox") } : { error: t("backups.target.portStorageBoxWrong") }))}
            >
              <input className="ui-input" value={port} onChange={(e) => setPort(e.target.value)} inputMode="numeric" required />
            </Field>
          </div>
          <div className="ui-wide">
            <Field label={t("backups.target.directory")} hint={t("backups.target.directoryHint")}>
              <input className="ui-input" value={path} onChange={(e) => setPath(e.target.value)} required />
            </Field>
          </div>
        </fieldset>
        <fieldset className="ui-fieldset ui-group ui-stack">
          <legend>{t("backups.target.keep")}</legend>
          <div className="ui-fields ui-fields-num">
            <Field label={t("backups.target.hourly")}>
              <input className="ui-input" value={hourly} onChange={(e) => setHourly(e.target.value)} inputMode="numeric" required />
            </Field>
            <Field label={t("backups.target.daily")}>
              <input className="ui-input" value={daily} onChange={(e) => setDaily(e.target.value)} inputMode="numeric" required />
            </Field>
            <Field label={t("backups.target.weekly")}>
              <input className="ui-input" value={weekly} onChange={(e) => setWeekly(e.target.value)} inputMode="numeric" required />
            </Field>
            <Field label={t("backups.target.monthly")}>
              <input className="ui-input" value={monthly} onChange={(e) => setMonthly(e.target.value)} inputMode="numeric" required />
            </Field>
            <Field label={t("backups.target.yearly")}>
              <input className="ui-input" value={yearly} onChange={(e) => setYearly(e.target.value)} inputMode="numeric" required />
            </Field>
          </div>
          <p className="ui-hint ui-flush">{t("backups.target.keepHint")}</p>
          <div className="ui-fields ui-fields-num">
            <Field label={t("backups.target.deletedDays")} hint={t("backups.target.deletedDaysHint")}>
              <input className="ui-input" value={goneDays} onChange={(e) => setGoneDays(e.target.value)} inputMode="numeric" required />
            </Field>
          </div>
        </fieldset>
        <fieldset className="ui-fieldset ui-group ui-stack">
          <legend>{t("backups.target.schedule")}</legend>
          <div className="ui-fields ui-fields-num">
            <Field label={t("backups.target.run")}>
              <select className="ui-select" value={interval} onChange={(e) => setIntervalHours(e.target.value)}>
                {intervals.map((h) => (
                  <option key={h} value={String(h)}>
                    {h === 24 ? t("backups.target.onceADay") : t("backups.target.every", { count: h })}
                  </option>
                ))}
              </select>
            </Field>
            {interval === "24" && (
              <>
                <Field label={t("backups.target.startFrom")}>
                  <input className="ui-input" value={start} onChange={(e) => setStart(e.target.value)} inputMode="numeric" required />
                </Field>
                <Field label={t("backups.target.startUntil")}>
                  <input className="ui-input" value={end} onChange={(e) => setEnd(e.target.value)} inputMode="numeric" required />
                </Field>
              </>
            )}
          </div>
          <p className="ui-hint ui-flush">
            {interval === "24" ? t("backups.target.windowHint") : t("backups.target.intervalHint")}
          </p>
          <div className="ui-fields ui-fields-num">
            <Field label={t("backups.target.readParts")} hint={t("backups.target.readPartsHint")}>
              <input className="ui-input" value={parts} onChange={(e) => setParts(e.target.value)} inputMode="numeric" required />
            </Field>
          </div>
        </fieldset>
        {error && <Notice tone="err" title={error} />}
        {saved && <Notice tone="ok" title={t("backups.target.saved")} />}
        <div className="ui-row">
          <Button variant="primary" type="submit" disabled={busy}>
            {t("common.actions.save")}
          </Button>
        </div>
        {info.publicKey && (
          <div className="ui-stack-tight">
            <p className="ui-flush">
              <Trans i18nKey="backups.target.addKey" components={{ code: <code /> }} />
              {savedStorageBox && <> {t("backups.target.addKeyStorageBox")}</>}
            </p>
            <pre className="ui-code" aria-label={t("backups.target.publicKey")}>
              {info.publicKey}
            </pre>
            <div className="ui-row">
              <CopyButton text={info.publicKey} label={t("backups.target.copyKey")} />
            </div>
          </div>
        )}
      </form>
    </Card>
  );
}

function HostKeyCard({ info, onConfirmed }: { info: BackupInfo; onConfirmed: (i: BackupInfo) => void }) {
  const { t } = useTranslation();
  const [scanned, setScanned] = useState<{ key: string; fingerprint: string } | null>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");

  async function scan() {
    setBusy(true);
    setError("");
    try {
      setScanned(await scanHostKey());
    } catch (err) {
      setError(backupMessage(err));
    } finally {
      setBusy(false);
    }
  }

  async function confirm() {
    if (!scanned) return;
    setBusy(true);
    setError("");
    try {
      onConfirmed(await confirmHostKey(scanned.key));
      setScanned(null);
    } catch (err) {
      setError(backupMessage(err));
    } finally {
      setBusy(false);
    }
  }

  return (
    <Card title={t("backups.hostKey.title")}>
      {info.hostKey ? (
        <p>
          <Trans i18nKey="backups.hostKey.trusted" values={{ key: info.hostKey }} components={{ code: <code /> }} />
        </p>
      ) : (
        <p>{t("backups.hostKey.unconfirmed")}</p>
      )}
      {scanned && (
        <>
          <p>
            <Trans
              i18nKey={isStorageBox(info.settings.target?.host ?? "") ? "backups.hostKey.compareStorageBox" : "backups.hostKey.compare"}
              values={{ fingerprint: scanned.fingerprint }}
              components={{ code: <code /> }}
            />
          </p>
          <Button variant="primary" disabled={busy} onClick={() => void confirm()}>
            {t("backups.hostKey.trust")}
          </Button>
        </>
      )}
      {error && <Notice tone="err" title={error} />}
      <Button disabled={busy} onClick={() => void scan()}>
        {info.hostKey ? t("backups.hostKey.checkAgain") : t("backups.hostKey.fetch")}
      </Button>
    </Card>
  );
}

const snapLabel = (s: Snapshot) => (s.kind === "db" ? i18n.t("backups.snapshots.database", { name: s.database ?? "" }) : i18n.t("backups.snapshots.files"));
// restoreWhat is the snapshot's content inside a sentence ("Restore files of …").
const restoreWhat = (s: Snapshot) =>
  s.kind === "db" ? i18n.t("backups.snapshots.restoreDatabase", { name: s.database ?? "" }) : i18n.t("backups.snapshots.restoreFiles");

function SnapshotsCard({ query, onQuery }: QueryProps) {
  const { t } = useTranslation();
  const [sites, setSites] = useState<Site[] | null>(null);
  // Deleted sites, and earlier sites of a reused name, with snapshots
  // (ADR-0074); only administrators get the list.
  const [deleted, setDeleted] = useState<DeletedSite[]>([]);
  const [snaps, setSnaps] = useState<Snapshot[] | null>(null);
  const [error, setError] = useState("");
  const [active, setActive] = useState<{ snap: Snapshot; restore: Restore | null } | null>(null);
  const [restoreError, setRestoreError] = useState("");
  const [into, setInto] = useState("");

  const gone = query.get("deleted") || (sites && sites.length === 0 ? (deleted[0]?.name ?? "") : "");
  const goneInfo = deleted.find((d) => d.name === gone);
  const site = gone ? "" : query.get("site") || sites?.[0]?.name || "";
  const set = (changes: Record<string, string>) => {
    const next = new URLSearchParams(query);
    for (const [k, v] of Object.entries(changes)) {
      if (v) next.set(k, v);
      else next.delete(k);
    }
    onQuery(next);
  };

  const loadDeleted = () =>
    listDeleted()
      .then(setDeleted)
      .catch(() => setDeleted([]));

  useEffect(() => {
    listSites()
      .then(setSites)
      .catch((err: unknown) => setError(backupMessage(err)));
    void loadDeleted();
  }, []);

  useEffect(() => {
    if (!site && !gone) return;
    setSnaps(null);
    setActive(null);
    (gone ? deletedSnapshots(gone) : siteSnapshots(site))
      .then(setSnaps)
      .catch((err: unknown) => setError(backupMessage(err)));
  }, [site, gone]);

  // An earlier site's snapshots go back into the site of that name; a
  // deleted site's into the first site until the administrator picks one.
  useEffect(() => {
    setInto(goneInfo?.earlier ? goneInfo.name : (sites?.[0]?.name ?? ""));
  }, [goneInfo, sites]);

  async function doRestore(snap: Snapshot) {
    setActive({ snap, restore: null });
    setRestoreError("");
    try {
      const r = gone ? await startDeletedRestore(gone, snap.id, into) : await startRestore(site, snap.id);
      setActive({ snap, restore: r });
      const end = await poll(() => getRestore(r.id), (x) => x.state === "done" || x.state === "failed");
      setActive({ snap, restore: end });
    } catch (err) {
      setActive(null);
      setRestoreError(backupMessage(err));
    }
  }

  const restoring = active !== null && active.restore?.state !== "done" && active.restore?.state !== "failed";
  const columns: Column<Snapshot>[] = [
    {
      key: "time",
      header: t("backups.snapshots.time"),
      main: true,
      sortValue: (s) => s.time,
      cell: (s) => (
        <>
          <RelTime iso={s.time} />
          <span className="ui-phone-only ui-compact-meta">{snapLabel(s)}</span>
        </>
      ),
    },
    { key: "kind", header: t("backups.snapshots.content"), sortValue: snapLabel, cell: snapLabel },
    {
      key: "actions",
      header: t("backups.snapshots.actions"),
      srHeader: true,
      cell: (s) => (
        <RowActions>
          <IconButton
            label={t("backups.snapshots.restoreLabel", { what: restoreWhat(s), time: exactTime(new Date(s.time)) })}
            text={t("backups.snapshots.restore")}
            icon={<ClockCounterClockwise size={16} />}
            disabled={restoring || (gone !== "" && !into)}
            onClick={() => void doRestore(s)}
          />
        </RowActions>
      ),
    },
  ];
  const sortKey = query.get("sort");
  const sort: Sort = sortKey ? { key: sortKey, dir: query.get("dir") === "asc" ? "asc" : "desc" } : { key: "time", dir: "desc" };
  const r = active?.restore;

  return (
    <Card title={t("backups.snapshots.title")} padded={false}>
      <div className="ui-filters">
        {sites && (sites.length > 0 || deleted.length > 0) && (
          <select
            className="ui-select"
            aria-label={t("backups.snapshots.site")}
            value={gone ? "d:" + gone : "s:" + site}
            onChange={(e) => {
              const v = e.target.value;
              set(v.startsWith("d:") ? { deleted: v.slice(2), site: "" } : { site: v.slice(2), deleted: "" });
            }}
          >
            {deleted.length === 0 ? (
              sites.map((s) => (
                <option key={s.name} value={"s:" + s.name}>
                  {s.name}
                </option>
              ))
            ) : (
              <>
                {sites.length > 0 && (
                  <optgroup label={t("backups.snapshots.sites")}>
                    {sites.map((s) => (
                      <option key={s.name} value={"s:" + s.name}>
                        {s.name}
                      </option>
                    ))}
                  </optgroup>
                )}
                <optgroup label={t("backups.snapshots.deletedSites")}>
                  {deleted.map((d) => (
                    <option key={d.name} value={"d:" + d.name}>
                      {d.earlier ? t("backups.snapshots.earlierSite", { name: d.name }) : t("backups.snapshots.deletedSite", { name: d.name })}
                    </option>
                  ))}
                </optgroup>
              </>
            )}
          </select>
        )}
        {gone && sites && sites.length > 0 && (
          <label className="ui-filter-inline">
            {t("backups.snapshots.restoreInto")}
            <select className="ui-select" value={into} disabled={goneInfo?.earlier} onChange={(e) => setInto(e.target.value)}>
              {sites.map((s) => (
                <option key={s.name} value={s.name}>
                  {s.name}
                </option>
              ))}
            </select>
          </label>
        )}
        {gone ? (
          <p className="ui-hint ui-filters-note">
            {goneInfo?.earlier ? (
              <Trans i18nKey="backups.snapshots.earlierNote" values={{ site: gone }} components={{ code: <code /> }} />
            ) : (
              <Trans i18nKey="backups.snapshots.deletedNote" values={{ site: gone }} components={{ code: <code /> }} />
            )}
          </p>
        ) : (
          <p className="ui-hint ui-filters-note">
            <Trans i18nKey="backups.snapshots.note" components={{ code: <code /> }} />
          </p>
        )}
      </div>
      {(error || restoreError || active || (gone && sites?.length === 0)) && (
        <div className="ui-card-body ui-stack">
          {error && <Notice tone="err" title={error} />}
          {restoreError && <Notice tone="err" title={restoreError} />}
          {active && restoring && <p aria-busy="true">{t("backups.snapshots.restoring", { what: restoreWhat(active.snap) })}</p>}
          {r?.state === "failed" && <Notice tone="err" title={t("backups.snapshots.restoreFailed", { error: r.error ?? t("backups.snapshots.unknownError") })} />}
          {r?.state === "done" && (
            <Notice tone="ok" title={t("backups.snapshots.restored")}>
              <p>
                <Trans
                  i18nKey={r.source ? "backups.snapshots.restoredFilesOf" : "backups.snapshots.restoredFiles"}
                  count={r.files}
                  values={{ count: r.files, dir: r.dir, site: r.site }}
                  components={{ code: <code /> }}
                />
              </p>
            </Notice>
          )}
          {r?.state === "done" && r.kind === "db" && <ImportForm key={r.id} site={r.site} restore={r} />}
          {gone && sites && sites.length === 0 && <Notice tone="warn" title={t("backups.snapshots.createSiteFirst")} />}
        </div>
      )}
      {sites && sites.length === 0 && deleted.length === 0 ? (
        <p className="ui-empty">{t("backups.snapshots.noSites")}</p>
      ) : !snaps ? (
        !error && <p className="ui-empty" aria-busy="true">{t("common.status.loading")}</p>
      ) : (
        <Table
          label={t("backups.snapshots.tableLabel", { site: gone || site })}
          columns={columns}
          rows={snaps}
          rowKey={(s) => s.id}
          sort={sort}
          onSort={(x) => set({ sort: x.key, dir: x.dir })}
          empty={t("backups.snapshots.empty")}
          compact
        />
      )}
      {goneInfo && <DeleteBackups key={goneInfo.name} site={goneInfo} onChange={() => void loadDeleted()} />}
    </Card>
  );
}

// DeleteBackups marks the snapshots of a deleted site, or of an earlier
// site of a reused name, for removal by the next backup run (ADR-0074, 4).
// Until that run the mark can be taken back.
function DeleteBackups({ site, onChange }: { site: DeletedSite; onChange: () => void }) {
  const [typed, setTyped] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const { t } = useTranslation();

  async function act(f: () => Promise<unknown>) {
    setBusy(true);
    setError("");
    try {
      await f();
      setTyped("");
      onChange();
    } catch (err) {
      setError(backupMessage(err));
    } finally {
      setBusy(false);
    }
  }

  if (site.markedAt) {
    return (
      <div className="ui-card-body ui-stack">
        <Notice tone="warn" title={t("backups.delete.marked")}>
          <p>
            <Trans i18nKey={site.earlier ? "backups.delete.markedTextEarlier" : "backups.delete.markedText"} components={{ b: <strong /> }} />
          </p>
        </Notice>
        {error && <Notice tone="err" title={error} />}
        <div className="ui-row">
          <Button disabled={busy} onClick={() => void act(() => keepBackups(site.name))}>
            {t("backups.delete.keep")}
          </Button>
        </div>
      </div>
    );
  }

  return (
    <form
      className="ui-card-body ui-stack"
      onSubmit={(e) => {
        e.preventDefault();
        void act(() => markRemoval(site.name, typed));
      }}
    >
      <h3 className="ui-flush">{t("backups.delete.title")}</h3>
      <div className="ui-narrow">
        <Field
          label={<Trans i18nKey="backups.delete.confirm" values={{ name: site.name }} components={{ phrase: <code className="ui-confirm-phrase" /> }} />}
          hint={t(site.earlier ? "backups.delete.hintEarlier" : "backups.delete.hint", { n: site.snapshots })}
        >
          <input className="ui-input" value={typed} onChange={(e) => setTyped(e.target.value)} autoComplete="off" spellCheck={false} />
        </Field>
      </div>
      {error && <Notice tone="err" title={error} />}
      <div className="ui-row">
        <Button variant="danger" type="submit" disabled={busy || typed !== site.name}>
          {t("backups.delete.submit", { name: site.name })}
        </Button>
      </div>
    </form>
  );
}

// ImportForm loads a restored dump into a new database of the site. A
// PostgreSQL dump (.dump, ADR-0051) needs a database on a PostgreSQL
// server, a MariaDB dump one on this machine or a MariaDB/MySQL server.
function ImportForm({ site, restore }: { site: string; restore: Restore }) {
  const { t } = useTranslation();
  const postgres = restore.dumpFile?.endsWith(".dump") ?? false;
  const [name, setName] = useState((restore.database ?? site) + "_restored");
  const [servers, setServers] = useState<DBServer[] | null>(null);
  const [server, setServer] = useState("");
  const [state, setState] = useState<"idle" | "busy" | "done">("idle");
  const [error, setError] = useState("");

  useEffect(() => {
    listServers()
      .then((all) => {
        const fit = all.filter((s) => isPostgres(s) === postgres);
        setServers(fit);
        setServer((fit.find((s) => s.default) ?? fit[0])?.name ?? "");
      })
      // Without the list the default server takes the dump, as before.
      .catch(() => setServers([]));
  }, [postgres]);

  if (postgres && servers && servers.length === 0) {
    return <Notice tone="warn" title={t("backups.import.needPostgres")} />;
  }

  async function submit(e: FormEvent) {
    e.preventDefault();
    setState("busy");
    setError("");
    try {
      await createDatabase(site, name.trim(), server);
      const job = await importDump(restore.id, name.trim());
      const end = await poll(() => getImport(job.id), (j) => j.state !== "running");
      if (end.state === "failed") throw new Error(end.error ?? t("backups.snapshots.unknownError"));
      setState("done");
    } catch (err) {
      setError(err instanceof Error && !(err instanceof TypeError) && !("code" in err) ? t("backups.import.failed", { error: err.message }) : backupMessage(err));
      setState("idle");
    }
  }

  if (state === "done") {
    return (
      <Notice tone="ok" title={t("backups.import.done", { name })} />
    );
  }

  return (
    <form className="ui-stack" onSubmit={(e) => void submit(e)}>
      <Field label={t("backups.import.into")}>
        <input className="ui-input" value={name} onChange={(e) => setName(e.target.value)} required />
      </Field>
      {servers && servers.length > 1 && (
        <Field label={t("backups.import.server")}>
          <select className="ui-select" value={server} onChange={(e) => setServer(e.target.value)}>
            {servers.map((s) => (
              <option key={s.name} value={s.name}>
                {s.local ? t("backups.import.thisMachine", { name: s.name }) : s.name}
              </option>
            ))}
          </select>
        </Field>
      )}
      {error && <Notice tone="err" title={error} />}
      <Button variant="primary" type="submit" disabled={state === "busy" || servers === null}>
        {t("backups.import.submit")}
      </Button>
    </form>
  );
}
