// SPDX-License-Identifier: AGPL-3.0-or-later

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

const when = (t?: string) => (t ? new Date(t).toLocaleString() : "never");

// Backups is the backup page: status, target, host key, schedule and the
// snapshots of each site with restore.
export function Backups() {
  const [info, setInfo] = useState<BackupInfo | null>(null);
  const [error, setError] = useState("");

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

  if (!info) {
    return error ? (
      <p role="alert" className="card">
        {error}
      </p>
    ) : (
      <p aria-busy="true">Loading…</p>
    );
  }

  return (
    <div className="stack">
      <StatusCard info={info} onChange={load} />
      <TargetCard info={info} onSaved={setInfo} />
      {info.settings.target && <HostKeyCard info={info} onConfirmed={setInfo} />}
      {info.ready && <SnapshotsCard />}
    </div>
  );
}

function StatusCard({ info, onChange }: { info: BackupInfo; onChange: () => Promise<void> }) {
  const [busy, setBusy] = useState(false);
  const [note, setNote] = useState("");
  const [error, setError] = useState("");
  const st = info.status;
  const failed = st.items.filter((i) => i.error);

  async function run() {
    setBusy(true);
    setError("");
    setNote("");
    try {
      await runBackup();
      setNote("Backup started. It runs in the background; reload this page to see the result.");
      await onChange();
    } catch (err) {
      setError(backupMessage(err));
    } finally {
      setBusy(false);
    }
  }

  let summary: string;
  if (!info.ready) summary = "Not set up yet: set a target and confirm its host key.";
  else if (st.running) summary = "A backup is running.";
  else if (!st.started) summary = "No backup has run yet.";
  else if (st.ok) summary = "The last backup succeeded.";
  else summary = "The last backup failed" + (st.error ? ": " + st.error : ".");

  return (
    <section className="card" aria-labelledby="backup-status">
      <h2 id="backup-status">Backups</h2>
      <p>{summary}</p>
      {st.started && (
        <dl className="facts">
          <dt>Last run</dt>
          <dd>{when(st.started)}</dd>
          <dt>Finished</dt>
          <dd>{when(st.finished)}</dd>
          <dt>Last check</dt>
          <dd>{when(st.lastCheck)}</dd>
          <dt>Last data check</dt>
          <dd>{when(st.lastReadCheck)}</dd>
        </dl>
      )}
      {failed.length > 0 && (
        <ul className="error">
          {failed.map((i) => (
            <li key={i.kind + (i.site ?? "") + (i.database ?? "")}>
              {i.kind} {i.site ?? ""} {i.database ?? ""}: {i.error}
            </li>
          ))}
        </ul>
      )}
      {!st.maintenanceOk && st.maintenance && <p className="error">Maintenance: {st.maintenance}</p>}
      {note && <p role="status">{note}</p>}
      {error && <p role="alert">{error}</p>}
      {info.ready && (
        <button type="button" disabled={busy || st.running} onClick={() => void run()}>
          Back up now
        </button>
      )}
    </section>
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
    <section className="card" aria-labelledby="backup-target">
      <h2 id="backup-target">Target and schedule</h2>
      <form onSubmit={(e) => void submit(e)}>
        <fieldset>
          <legend>Storage Box (SFTP)</legend>
          <label>
            User
            <input value={user} onChange={(e) => setUser(e.target.value)} required placeholder="u123456" autoComplete="off" />
          </label>
          <label>
            Host
            <input value={host} onChange={(e) => setHost(e.target.value)} required placeholder="u123456.your-storagebox.de" autoComplete="off" />
          </label>
          <label>
            Port
            <input value={port} onChange={(e) => setPort(e.target.value)} inputMode="numeric" required />
          </label>
          <label>
            Directory
            <small>Below the login directory; created on the first run.</small>
            <input value={path} onChange={(e) => setPath(e.target.value)} required />
          </label>
        </fieldset>
        <fieldset>
          <legend>Keep</legend>
          <label>
            Daily snapshots
            <input value={daily} onChange={(e) => setDaily(e.target.value)} inputMode="numeric" required />
          </label>
          <label>
            Weekly snapshots
            <input value={weekly} onChange={(e) => setWeekly(e.target.value)} inputMode="numeric" required />
          </label>
          <label>
            Monthly snapshots
            <input value={monthly} onChange={(e) => setMonthly(e.target.value)} inputMode="numeric" required />
          </label>
        </fieldset>
        <fieldset>
          <legend>Schedule</legend>
          <label>
            Start between (hour)
            <small>The daily backup starts at a random time in this window, server time.</small>
            <input value={start} onChange={(e) => setStart(e.target.value)} inputMode="numeric" required />
          </label>
          <label>
            And (hour)
            <input value={end} onChange={(e) => setEnd(e.target.value)} inputMode="numeric" required />
          </label>
          <label>
            Monthly data check reads 1 of
            <small>parts of all data, a different part each month. 0 turns it off.</small>
            <input value={parts} onChange={(e) => setParts(e.target.value)} inputMode="numeric" required />
          </label>
        </fieldset>
        {error && <p role="alert">{error}</p>}
        {saved && <p role="status">Saved.</p>}
        <button type="submit" disabled={busy}>
          Save
        </button>
      </form>
      {info.publicKey && (
        <div className="keybox">
          <p>
            Add this key to the Storage Box: in Hetzner&apos;s console under SSH keys, or to <code>.ssh/authorized_keys</code> of the account.
          </p>
          <pre aria-label="Public key">{info.publicKey}</pre>
        </div>
      )}
    </section>
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
    <section className="card" aria-labelledby="backup-hostkey">
      <h2 id="backup-hostkey">Host key</h2>
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
          <button type="button" disabled={busy} onClick={() => void confirm()}>
            Trust this key
          </button>
        </>
      )}
      {error && <p role="alert">{error}</p>}
      <button type="button" className="secondary" disabled={busy} onClick={() => void scan()}>
        {info.hostKey ? "Check the host key again" : "Fetch the host key"}
      </button>
    </section>
  );
}

function SnapshotsCard() {
  const [sites, setSites] = useState<Site[] | null>(null);
  const [site, setSite] = useState("");
  const [snaps, setSnaps] = useState<Snapshot[] | null>(null);
  const [error, setError] = useState("");

  useEffect(() => {
    listSites()
      .then((s) => {
        setSites(s);
        if (s[0]) setSite(s[0].name);
      })
      .catch((err: unknown) => setError(backupMessage(err)));
  }, []);

  useEffect(() => {
    if (!site) return;
    setSnaps(null);
    siteSnapshots(site)
      .then(setSnaps)
      .catch((err: unknown) => setError(backupMessage(err)));
  }, [site]);

  return (
    <section className="card" aria-labelledby="backup-snapshots">
      <h2 id="backup-snapshots">Snapshots</h2>
      <p>
        A restore writes into a new directory <code>restore-&lt;time&gt;</code> in the site&apos;s home and never changes the live site.
      </p>
      {error && <p role="alert">{error}</p>}
      {sites && sites.length === 0 && <p>No sites yet.</p>}
      {sites && sites.length > 0 && (
        <label>
          Site
          <select value={site} onChange={(e) => setSite(e.target.value)}>
            {sites.map((s) => (
              <option key={s.name} value={s.name}>
                {s.name}
              </option>
            ))}
          </select>
        </label>
      )}
      {snaps && snaps.length === 0 && <p>No snapshots of this site yet.</p>}
      {snaps && snaps.length > 0 && (
        <ul className="snapshots">
          {snaps.map((s) => (
            <SnapshotRow key={s.id} site={site} snap={s} />
          ))}
        </ul>
      )}
    </section>
  );
}

function SnapshotRow({ site, snap }: { site: string; snap: Snapshot }) {
  const [restore, setRestore] = useState<Restore | null>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");

  async function doRestore() {
    setBusy(true);
    setError("");
    try {
      const r = await startRestore(site, snap.id);
      setRestore(r);
      setRestore(await poll(() => getRestore(r.id), (x) => x.state === "done" || x.state === "failed"));
    } catch (err) {
      setError(backupMessage(err));
    } finally {
      setBusy(false);
    }
  }

  const label = snap.kind === "db" ? "Database " + (snap.database ?? "") : "Files";

  return (
    <li>
      <span>
        {label}, {when(snap.time)}
      </span>
      {!restore && (
        <button type="button" className="secondary" disabled={busy} onClick={() => void doRestore()}>
          Restore
        </button>
      )}
      {restore && restore.state !== "done" && restore.state !== "failed" && <p aria-busy="true">Restoring…</p>}
      {restore?.state === "failed" && <p role="alert">Restore failed: {restore.error}</p>}
      {restore?.state === "done" && (
        <p role="status">
          Restored {restore.files} files into <code>{restore.dir}</code>.
        </p>
      )}
      {restore?.state === "done" && restore.kind === "db" && <ImportForm site={site} restore={restore} />}
      {error && <p role="alert">{error}</p>}
    </li>
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
      <p role="status">
        The dump is in the new database <code>{name}</code>. Point the site to it when you have checked it.
      </p>
    );
  }

  return (
    <form onSubmit={(e) => void submit(e)}>
      <label>
        Import into a new database
        <input value={name} onChange={(e) => setName(e.target.value)} required />
      </label>
      {error && <p role="alert">{error}</p>}
      <button type="submit" disabled={state === "busy"}>
        Create and import
      </button>
    </form>
  );
}
