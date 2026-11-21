// SPDX-License-Identifier: AGPL-3.0-or-later

import { ClockCounterClockwise, CloudArrowUp } from "@phosphor-icons/react";
import { useCallback, useEffect, useState, type FormEvent } from "react";
import {
  backupMessage,
  confirmHostKey,
  createDatabase,
  getBackups,
  getImport,
  getRestore,
  importDump,
  listSites,
  poll,
  runBackup,
  saveSettings,
  scanHostKey,
  siteSnapshots,
  startRestore,
  type BackupInfo,
  type Restore,
  type Settings,
  type Site,
  type Snapshot,
} from "./backups";
import { Badge, Button, Card, exactTime, Field, IconButton, Notice, PageHead, RelTime, RowActions, Table, type Column, type Sort, type Tone } from "./ui";

type QueryProps = {
  /** The address query: site (snapshots of that site), sort, dir. */
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
        description="Daily restic backups of all sites and databases to a Storage Box over SFTP."
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

function statusOf(info: BackupInfo): { tone: Tone; word: string; summary: string } {
  const st = info.status;
  if (!info.ready) return { tone: "off", word: "Not set up", summary: "Not set up yet: set a target and confirm its host key." };
  if (st.running) return { tone: "warn", word: "Running", summary: "A backup is running." };
  if (!st.started) return { tone: "off", word: "No backup yet", summary: "No backup has run yet." };
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
      </div>
    </Card>
  );
}

function num(v: string, fallback: number) {
  const n = Number.parseInt(v, 10);
  return Number.isNaN(n) ? fallback : n;
}

function TargetCard({ info, onSaved }: { info: BackupInfo; onSaved: (i: BackupInfo) => void }) {
  const s = info.settings;
  const [user, setUser] = useState(s.target?.user ?? "");
  const [host, setHost] = useState(s.target?.host ?? "");
  const [port, setPort] = useState(String(s.target?.port ?? 23));
  const [path, setPath] = useState(s.target?.path ?? "hostashi");
  const [daily, setDaily] = useState(String(s.retention.daily));
  const [weekly, setWeekly] = useState(String(s.retention.weekly));
  const [monthly, setMonthly] = useState(String(s.retention.monthly));
  const [start, setStart] = useState(String(s.windowStart));
  const [end, setEnd] = useState(String(s.windowEnd));
  const [parts, setParts] = useState(String(s.readParts));
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [saved, setSaved] = useState(false);

  async function submit(e: FormEvent) {
    e.preventDefault();
    setBusy(true);
    setError("");
    setSaved(false);
    const next: Settings = {
      version: s.version,
      target: { user: user.trim(), host: host.trim().toLowerCase(), port: num(port, 23), path: path.trim() },
      retention: { daily: num(daily, 7), weekly: num(weekly, 4), monthly: num(monthly, 6) },
      windowStart: num(start, 1),
      windowEnd: num(end, 5),
      readParts: num(parts, 12),
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
          <legend>Storage Box (SFTP)</legend>
          <Field label="User">
            <input className="ui-input" value={user} onChange={(e) => setUser(e.target.value)} required placeholder="u123456" autoComplete="off" />
          </Field>
          <Field label="Host">
            <input className="ui-input" value={host} onChange={(e) => setHost(e.target.value)} required placeholder="u123456.your-storagebox.de" autoComplete="off" />
          </Field>
          <Field label="Port">
            <input className="ui-input" value={port} onChange={(e) => setPort(e.target.value)} inputMode="numeric" required />
          </Field>
          <Field label="Directory" hint="Below the login directory; created on the first run.">
            <input className="ui-input" value={path} onChange={(e) => setPath(e.target.value)} required />
          </Field>
        </fieldset>
        <fieldset className="ui-fieldset ui-stack">
          <legend>Keep</legend>
          <Field label="Daily snapshots">
            <input className="ui-input" value={daily} onChange={(e) => setDaily(e.target.value)} inputMode="numeric" required />
          </Field>
          <Field label="Weekly snapshots">
            <input className="ui-input" value={weekly} onChange={(e) => setWeekly(e.target.value)} inputMode="numeric" required />
          </Field>
          <Field label="Monthly snapshots">
            <input className="ui-input" value={monthly} onChange={(e) => setMonthly(e.target.value)} inputMode="numeric" required />
          </Field>
        </fieldset>
        <fieldset className="ui-fieldset ui-stack">
          <legend>Schedule</legend>
          <Field label="Start between (hour)" hint="The daily backup starts at a random time in this window, server time.">
            <input className="ui-input" value={start} onChange={(e) => setStart(e.target.value)} inputMode="numeric" required />
          </Field>
          <Field label="And (hour)">
            <input className="ui-input" value={end} onChange={(e) => setEnd(e.target.value)} inputMode="numeric" required />
          </Field>
          <Field label="Monthly data check reads 1 of" hint="parts of all data, a different part each month. 0 turns it off.">
            <input className="ui-input" value={parts} onChange={(e) => setParts(e.target.value)} inputMode="numeric" required />
          </Field>
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
              Add this key to the Storage Box: in Hetzner&apos;s console under SSH keys, or to <code>.ssh/authorized_keys</code> of the account.
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
            The target presents <code>{scanned.fingerprint}</code>. Compare it with the fingerprints Hetzner publishes for Storage Boxes. Trust it only if it matches.
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
  const [snaps, setSnaps] = useState<Snapshot[] | null>(null);
  const [error, setError] = useState("");
  const [active, setActive] = useState<{ snap: Snapshot; restore: Restore | null } | null>(null);
  const [restoreError, setRestoreError] = useState("");

  const site = query.get("site") || sites?.[0]?.name || "";
  const set = (changes: Record<string, string>) => {
    const next = new URLSearchParams(query);
    for (const [k, v] of Object.entries(changes)) {
      if (v) next.set(k, v);
      else next.delete(k);
    }
    onQuery(next);
  };

  useEffect(() => {
    listSites()
      .then(setSites)
      .catch((err: unknown) => setError(backupMessage(err)));
  }, []);

  useEffect(() => {
    if (!site) return;
    setSnaps(null);
    setActive(null);
    siteSnapshots(site)
      .then(setSnaps)
      .catch((err: unknown) => setError(backupMessage(err)));
  }, [site]);

  async function doRestore(snap: Snapshot) {
    setActive({ snap, restore: null });
    setRestoreError("");
    try {
      const r = await startRestore(site, snap.id);
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
            icon={<ClockCounterClockwise size={16} />}
            disabled={restoring}
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
        {sites && sites.length > 0 && (
          <select className="ui-select" aria-label="Site" value={site} onChange={(e) => set({ site: e.target.value })}>
            {sites.map((s) => (
              <option key={s.name} value={s.name}>
                {s.name}
              </option>
            ))}
          </select>
        )}
        <p className="ui-hint ui-filters-note">
          A restore writes into a new directory <code>restore-&lt;time&gt;</code> in the site&apos;s home and never changes the live site.
        </p>
      </div>
      {(error || restoreError || active) && (
        <div className="ui-card-body ui-stack">
          {error && <Notice tone="err" title={error} />}
          {restoreError && <Notice tone="err" title={restoreError} />}
          {active && restoring && <p aria-busy="true">Restoring {snapLabel(active.snap).toLowerCase()}…</p>}
          {r?.state === "failed" && <Notice tone="err" title={"Restore failed: " + (r.error ?? "unknown error")} />}
          {r?.state === "done" && (
            <Notice tone="ok" title="Restored">
              <p>
                Restored {r.files} files into <code>{r.dir}</code>.
              </p>
            </Notice>
          )}
          {r?.state === "done" && r.kind === "db" && <ImportForm key={r.id} site={site} restore={r} />}
        </div>
      )}
      {sites && sites.length === 0 ? (
        <p className="ui-empty">No sites yet.</p>
      ) : !snaps ? (
        !error && <p className="ui-empty" aria-busy="true">Loading…</p>
      ) : (
        <Table
          label={"Snapshots of " + site}
          columns={columns}
          rows={snaps}
          rowKey={(s) => s.id}
          sort={sort}
          onSort={(x) => set({ sort: x.key, dir: x.dir })}
          empty="No snapshots of this site yet."
        />
      )}
    </Card>
  );
}

function ImportForm({ site, restore }: { site: string; restore: Restore }) {
  const [name, setName] = useState((restore.database ?? site) + "_restored");
  const [state, setState] = useState<"idle" | "busy" | "done">("idle");
  const [error, setError] = useState("");

  async function submit(e: FormEvent) {
    e.preventDefault();
    setState("busy");
    setError("");
    try {
      await createDatabase(site, name.trim());
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
      {error && <Notice tone="err" title={error} />}
      <Button variant="primary" type="submit" disabled={state === "busy"}>
        Create and import
      </Button>
    </form>
  );
}
