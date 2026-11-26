// SPDX-License-Identifier: AGPL-3.0-or-later

import { ClockCounterClockwise, CloudArrowUp } from "@phosphor-icons/react";
import { useCallback, useEffect, useState, type FormEvent, type ReactNode } from "react";
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
import { Badge, Button, Card, exactTime, Field, IconButton, Notice, PageHead, RelTime, RowActions, Table, type Column, type Sort, type Tone } from "./ui";

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
      setNote("Backup started. It runs in the background; reload this page to see the result.");
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
        title="Backups"
        description="Scheduled restic backups of all sites and databases to any SFTP server, for example a Hetzner Storage Box, a NAS or another Linux server."
        actions={
          info?.ready && (
            <Button variant="primary" icon={<CloudArrowUp size={16} aria-hidden="true" />} disabled={busy || info.status.running} onClick={() => void run()}>
              Back up now
            </Button>
          )
        }
      />
      {error && <Notice tone="err" title={error} />}
      {note && <Notice tone="info" title={note} />}
      {!info ? (
        !error && <p aria-busy="true">Loading…</p>
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
  const st = info.status;
  if (!info.ready) return { tone: "off", word: "Not set up", summary: "Not set up yet: set a target and confirm its host key." };
  if (st.running) return { tone: "warn", word: "Running", summary: "A backup is running." };
  if (!st.started) return { tone: "off", word: "No backup yet", summary: "No backup has run yet." };
  if (info.overdue) {
    const h = info.settings.intervalHours;
    return {
      tone: "warn",
      word: "Overdue",
      summary: (
        <>
          No backup has finished in the last {overdueHours(h)} hours, although backups run {everyText(h)}. Check the timer on the server with{" "}
          <code>systemctl status hostashi-backup.timer</code>.
        </>
      ),
    };
  }
  if (st.ok) return { tone: "ok", word: "OK", summary: "The last backup succeeded." };
  return { tone: "err", word: "Failed", summary: "The last backup failed" + (st.error ? ": " + st.error : ".") };
}

const timeOrNever = (t?: string) => (t ? <RelTime iso={t} /> : "never");

function StatusCard({ info }: { info: BackupInfo }) {
  const st = info.status;
  const failed = st.items.filter((i) => i.error);
  const s = statusOf(info);

  return (
    <Card title="Status" actions={<Badge tone={s.tone}>{s.word}</Badge>}>
      <div className="ui-stack">
        <p className="ui-flush">{s.summary}</p>
        {st.started && (
          <dl className="ui-facts">
            <dt>Last run</dt>
            <dd>{timeOrNever(st.started)}</dd>
            <dt>Finished</dt>
            <dd>{timeOrNever(st.finished)}</dd>
            <dt>Last check</dt>
            <dd>{timeOrNever(st.lastCheck)}</dd>
            <dt>Last data check</dt>
            <dd>{timeOrNever(st.lastReadCheck)}</dd>
          </dl>
        )}
        {failed.length > 0 && (
          <ul className="ui-error ui-flush">
            {failed.map((i) => (
              <li key={i.kind + (i.site ?? "") + (i.database ?? "")}>
                {i.kind} {i.site ?? ""} {i.database ?? ""}: {i.error}
              </li>
            ))}
          </ul>
        )}
        {!st.maintenanceOk && st.maintenance && <p className="ui-error ui-flush">Maintenance: {st.maintenance}</p>}
        {st.removed && st.removed.length > 0 && (
          <ul className="ui-flush">
            {st.removed.map((x) => (
              <li key={x.site}>
                Removed {x.snapshots} {x.snapshots === 1 ? "snapshot" : "snapshots"} of the deleted site <code>{x.site}</code>
                {x.expired ? " (expired)" : ""}.
              </li>
            ))}
          </ul>
        )}
        {st.removeError && <p className="ui-error ui-flush">Removing backups of deleted sites failed: {st.removeError}</p>}
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
    <Card title="Target and schedule">
      <form className="ui-stack" onSubmit={(e) => void submit(e)}>
        <fieldset className="ui-fieldset ui-stack">
          <legend>SFTP server</legend>
          <div className="ui-fields ui-fields-server">
            <Field label="User">
              <input className="ui-input" value={user} onChange={(e) => setUser(e.target.value)} required placeholder="backup" autoComplete="off" />
            </Field>
            <Field label="Host" hint="DNS name or IP address of the SSH server.">
              <input className="ui-input" value={host} onChange={(e) => setHost(e.target.value)} required placeholder="backup.example.com" autoComplete="off" />
            </Field>
            <Field
              label="Port"
              {...(storageBox && (port.trim() === "23" ? { hint: "Right for a Hetzner Storage Box." } : { error: "Hetzner Storage Boxes serve SFTP on port 23." }))}
            >
              <input className="ui-input" value={port} onChange={(e) => setPort(e.target.value)} inputMode="numeric" required />
            </Field>
          </div>
          <div className="ui-wide">
            <Field label="Directory" hint="Relative to the login directory, or absolute with a leading /. Created on the first run.">
              <input className="ui-input" value={path} onChange={(e) => setPath(e.target.value)} required />
            </Field>
          </div>
        </fieldset>
        <fieldset className="ui-fieldset ui-stack">
          <legend>Keep snapshots</legend>
          <div className="ui-fields ui-fields-num">
            <Field label="Hourly">
              <input className="ui-input" value={hourly} onChange={(e) => setHourly(e.target.value)} inputMode="numeric" required />
            </Field>
            <Field label="Daily">
              <input className="ui-input" value={daily} onChange={(e) => setDaily(e.target.value)} inputMode="numeric" required />
            </Field>
            <Field label="Weekly">
              <input className="ui-input" value={weekly} onChange={(e) => setWeekly(e.target.value)} inputMode="numeric" required />
            </Field>
            <Field label="Monthly">
              <input className="ui-input" value={monthly} onChange={(e) => setMonthly(e.target.value)} inputMode="numeric" required />
            </Field>
            <Field label="Yearly">
              <input className="ui-input" value={yearly} onChange={(e) => setYearly(e.target.value)} inputMode="numeric" required />
            </Field>
          </div>
          <p className="ui-hint ui-flush">
            The newest snapshot of each hour, day, week, month and year is kept, up to these counts. Hourly only matters when backups run more than once a day.
          </p>
          <div className="ui-fields ui-fields-num">
            <Field
              label="Deleted sites (days)"
              hint="The snapshots of a deleted site are removed this many days after its last snapshot. 0 keeps them until you delete them."
            >
              <input className="ui-input" value={goneDays} onChange={(e) => setGoneDays(e.target.value)} inputMode="numeric" required />
            </Field>
          </div>
        </fieldset>
        <fieldset className="ui-fieldset ui-stack">
          <legend>Schedule</legend>
          <div className="ui-fields ui-fields-num">
            <Field label="Run backups">
              <select className="ui-select" value={interval} onChange={(e) => setIntervalHours(e.target.value)}>
                {intervals.map((h) => (
                  <option key={h} value={String(h)}>
                    {h === 24 ? "Once a day" : h === 1 ? "Every hour" : `Every ${h} hours`}
                  </option>
                ))}
              </select>
            </Field>
            {interval === "24" && (
              <>
                <Field label="Start from (hour)">
                  <input className="ui-input" value={start} onChange={(e) => setStart(e.target.value)} inputMode="numeric" required />
                </Field>
                <Field label="Start until (hour)">
                  <input className="ui-input" value={end} onChange={(e) => setEnd(e.target.value)} inputMode="numeric" required />
                </Field>
              </>
            )}
          </div>
          <p className="ui-hint ui-flush">
            {interval === "24"
              ? "The daily backup starts at a random time in this window, server time."
              : "Backups start at a fixed random minute after each of these hours, counted from midnight, server time. A backup that is still running when the next one is due skips that one."}
          </p>
          <div className="ui-fields ui-fields-num">
            <Field label="Data check parts" hint="Each month 1 of these parts is read back and checked. 0 turns it off.">
              <input className="ui-input" value={parts} onChange={(e) => setParts(e.target.value)} inputMode="numeric" required />
            </Field>
          </div>
        </fieldset>
        {error && <Notice tone="err" title={error} />}
        {saved && <Notice tone="ok" title="Saved." />}
        <div className="ui-row">
          <Button variant="primary" type="submit" disabled={busy}>
            Save
          </Button>
        </div>
        {info.publicKey && (
          <div className="ui-stack-tight">
            <p className="ui-flush">
              Add this key to <code>~/.ssh/authorized_keys</code> of the SFTP account.
              {savedStorageBox && <> On a Hetzner Storage Box you can also add it in Hetzner&apos;s console under SSH keys.</>}
            </p>
            <pre className="ui-code" aria-label="Public key">
              {info.publicKey}
            </pre>
          </div>
        )}
      </form>
    </Card>
  );
}

function HostKeyCard({ info, onConfirmed }: { info: BackupInfo; onConfirmed: (i: BackupInfo) => void }) {
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
    <Card title="Host key">
      {info.hostKey ? (
        <p>
          Trusted: <code>{info.hostKey}</code>
        </p>
      ) : (
        <p>The server&apos;s SSH host key is not confirmed yet. Backups start once it is.</p>
      )}
      {scanned && (
        <>
          <p>
            The target presents <code>{scanned.fingerprint}</code>. Compare it with the fingerprint of the server: run{" "}
            <code>ssh-keygen -lf /etc/ssh/ssh_host_ed25519_key.pub</code> on that server, or ask your provider
            {isStorageBox(info.settings.target?.host ?? "") && <> (Hetzner publishes the fingerprints of its Storage Boxes)</>}. Trust it only if it matches.
          </p>
          <Button variant="primary" disabled={busy} onClick={() => void confirm()}>
            Trust this key
          </Button>
        </>
      )}
      {error && <Notice tone="err" title={error} />}
      <Button disabled={busy} onClick={() => void scan()}>
        {info.hostKey ? "Check the host key again" : "Fetch the host key"}
      </Button>
    </Card>
  );
}

const snapLabel = (s: Snapshot) => (s.kind === "db" ? "Database " + (s.database ?? "") : "Files");

function SnapshotsCard({ query, onQuery }: QueryProps) {
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
    { key: "time", header: "Time", sortValue: (s) => s.time, cell: (s) => <RelTime iso={s.time} /> },
    { key: "kind", header: "Content", sortValue: snapLabel, cell: snapLabel },
    {
      key: "actions",
      header: "Actions",
      srHeader: true,
      cell: (s) => (
        <RowActions>
          <IconButton
            label={`Restore ${snapLabel(s).toLowerCase()} of ${exactTime(new Date(s.time))}`}
            text="Restore"
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
    <Card title="Snapshots" padded={false}>
      <div className="ui-filters">
        {sites && (sites.length > 0 || deleted.length > 0) && (
          <select
            className="ui-select"
            aria-label="Site"
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
                  <optgroup label="Sites">
                    {sites.map((s) => (
                      <option key={s.name} value={"s:" + s.name}>
                        {s.name}
                      </option>
                    ))}
                  </optgroup>
                )}
                <optgroup label="Deleted sites">
                  {deleted.map((d) => (
                    <option key={d.name} value={"d:" + d.name}>
                      {d.name + (d.earlier ? " (earlier site)" : " (deleted)")}
                    </option>
                  ))}
                </optgroup>
              </>
            )}
          </select>
        )}
        {gone && sites && sites.length > 0 && (
          <label className="ui-filter-inline">
            Restore into
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
              <>
                These snapshots are from an earlier site named <code>{gone}</code>, from before the current site was created. Only administrators
                see them. A restore writes into a new directory <code>restore-&lt;time&gt;</code> in the home of the current site.
              </>
            ) : (
              <>
                The site <code>{gone}</code> was deleted; its snapshots stay until you delete them. A restore writes into a new directory{" "}
                <code>restore-&lt;time&gt;</code> in the home of the site you pick and never changes live files.
              </>
            )}
          </p>
        ) : (
          <p className="ui-hint ui-filters-note">
            A restore writes into a new directory <code>restore-&lt;time&gt;</code> in the site&apos;s home and never changes the live site.
          </p>
        )}
      </div>
      {(error || restoreError || active || (gone && sites?.length === 0)) && (
        <div className="ui-card-body ui-stack">
          {error && <Notice tone="err" title={error} />}
          {restoreError && <Notice tone="err" title={restoreError} />}
          {active && restoring && <p aria-busy="true">Restoring {snapLabel(active.snap).toLowerCase()}…</p>}
          {r?.state === "failed" && <Notice tone="err" title={"Restore failed: " + (r.error ?? "unknown error")} />}
          {r?.state === "done" && (
            <Notice tone="ok" title="Restored">
              <p>
                Restored {r.files} {r.files === 1 ? "file" : "files"} into <code>{r.dir}</code>
                {r.source ? (
                  <>
                    {" "}
                    of the site <code>{r.site}</code>
                  </>
                ) : null}
                .
              </p>
            </Notice>
          )}
          {r?.state === "done" && r.kind === "db" && <ImportForm key={r.id} site={r.site} restore={r} />}
          {gone && sites && sites.length === 0 && <Notice tone="warn" title="Create a site first to restore these snapshots into." />}
        </div>
      )}
      {sites && sites.length === 0 && deleted.length === 0 ? (
        <p className="ui-empty">No sites yet.</p>
      ) : !snaps ? (
        !error && <p className="ui-empty" aria-busy="true">Loading…</p>
      ) : (
        <Table
          label={"Snapshots of " + (gone || site)}
          columns={columns}
          rows={snaps}
          rowKey={(s) => s.id}
          sort={sort}
          onSort={(x) => set({ sort: x.key, dir: x.dir })}
          empty="No snapshots of this site yet."
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
  const what = site.earlier ? "the earlier site's snapshots" : "all snapshots of this site";

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
        <Notice tone="warn" title="Marked for deletion">
          <p>
            The next backup run removes {what}. <strong>Back up now</strong> removes them at once. Until then you can keep them.
          </p>
        </Notice>
        {error && <Notice tone="err" title={error} />}
        <div className="ui-row">
          <Button disabled={busy} onClick={() => void act(() => keepBackups(site.name))}>
            Keep backups
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
      <h3 className="ui-flush">Delete backups</h3>
      <div className="ui-narrow">
        <Field
          label={
            <>
              Type <code className="ui-confirm-phrase">{site.name}</code> to confirm
            </>
          }
          hint={`The next backup run removes ${what} (${site.snapshots}). Once removed, they cannot be restored.`}
        >
          <input className="ui-input" value={typed} onChange={(e) => setTyped(e.target.value)} autoComplete="off" spellCheck={false} />
        </Field>
      </div>
      {error && <Notice tone="err" title={error} />}
      <div className="ui-row">
        <Button variant="danger" type="submit" disabled={busy || typed !== site.name}>
          Delete backups of {site.name}
        </Button>
      </div>
    </form>
  );
}

// ImportForm loads a restored dump into a new database of the site. A
// PostgreSQL dump (.dump, ADR-0051) needs a database on a PostgreSQL
// server, a MariaDB dump one on this machine or a MariaDB/MySQL server.
function ImportForm({ site, restore }: { site: string; restore: Restore }) {
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
    return <Notice tone="warn" title="This is a PostgreSQL dump. Add a PostgreSQL server under Databases to import it." />;
  }

  async function submit(e: FormEvent) {
    e.preventDefault();
    setState("busy");
    setError("");
    try {
      await createDatabase(site, name.trim(), server);
      const job = await importDump(restore.id, name.trim());
      const end = await poll(() => getImport(job.id), (j) => j.state !== "running");
      if (end.state === "failed") throw new Error(end.error ?? "import failed");
      setState("done");
    } catch (err) {
      setError(err instanceof Error && !(err instanceof TypeError) && !("code" in err) ? "Import failed: " + err.message : backupMessage(err));
      setState("idle");
    }
  }

  if (state === "done") {
    return (
      <Notice tone="ok" title={`The dump is in the new database ${name}. Point the site to it when you have checked it.`} />
    );
  }

  return (
    <form className="ui-stack" onSubmit={(e) => void submit(e)}>
      <Field label="Import into a new database">
        <input className="ui-input" value={name} onChange={(e) => setName(e.target.value)} required />
      </Field>
      {servers && servers.length > 1 && (
        <Field label="On the server">
          <select className="ui-select" value={server} onChange={(e) => setServer(e.target.value)}>
            {servers.map((s) => (
              <option key={s.name} value={s.name}>
                {s.local ? "This machine (" + s.name + ")" : s.name}
              </option>
            ))}
          </select>
        </Field>
      )}
      {error && <Notice tone="err" title={error} />}
      <Button variant="primary" type="submit" disabled={state === "busy" || servers === null}>
        Create and import
      </Button>
    </form>
  );
}
