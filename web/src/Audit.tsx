// SPDX-License-Identifier: AGPL-3.0-or-later

import { DownloadSimple } from "@phosphor-icons/react";
import { useEffect, useState } from "react";
import { actionGroups, auditActions, auditMessage, detailPairs, diffRows, exportUrl, listAudit, type AuditEntry, type AuditFilter } from "./audit";
import { Button, Card, Notice, PageHead, RelTime, Table, type Column } from "./ui";

type QueryProps = {
  /** The address query: action, actor, target, from, to. */
  query?: URLSearchParams;
  /** Replaces the query in the address without a new history entry. */
  onQuery?: (q: URLSearchParams) => void;
};

const pageSize = 50;
// Typing in a filter waits this long before the log is read again.
const typingDelay = 300;

const columns: Column<AuditEntry>[] = [
  { key: "at", header: "Time", cell: (e) => <RelTime iso={e.at} /> },
  {
    key: "actor",
    header: "Account",
    cell: (e) =>
      e.actor ? (
        <span>
          {e.actor}
          {e.userId === null && <span className="ui-hint"> (deleted)</span>}
        </span>
      ) : (
        <span className="ui-hint">none</span>
      ),
  },
  { key: "ip", header: "IP address", cell: (e) => (e.ip ? <span className="ui-mono ui-nowrap">{e.ip}</span> : <span className="ui-hint">—</span>) },
  { key: "action", header: "Action", cell: (e) => <span className="ui-mono">{e.action}</span> },
  { key: "target", header: "Target", cell: (e) => <span className="ui-break">{e.target || "—"}</span> },
  {
    key: "detail",
    header: "Details",
    cell: (e) => {
      // The address has its own column; older entries also kept it here.
      const pairs = detailPairs(e.detail).filter(([k]) => k !== "ip");
      const changes = diffRows(e.diff);
      if (pairs.length === 0 && changes.length === 0) return <span className="ui-hint">—</span>;
      return (
        <div className="ui-stack-tight">
          {changes.length > 0 && (
            <dl className="ui-audit-detail ui-audit-diff" aria-label="Changes">
              {changes.map((c) => (
                <div key={c.field}>
                  <dt>{c.field}</dt>
                  <dd className="ui-mono ui-break">
                    <del>{c.old || "(none)"}</del>
                    <span aria-hidden="true"> → </span>
                    <span className="ui-sr-only"> changed to </span>
                    <ins>{c.new || "(none)"}</ins>
                  </dd>
                </div>
              ))}
            </dl>
          )}
          {pairs.length > 0 && (
            <dl className="ui-audit-detail">
              {pairs.map(([k, v]) => (
                <div key={k}>
                  <dt>{k}</dt>
                  <dd className="ui-mono ui-break">{v}</dd>
                </div>
              ))}
            </dl>
          )}
        </div>
      );
    },
  },
];

// Audit shows the append-only audit log, newest first, with filters in the
// address and a download of the filtered log. Nothing here changes the
// server; the download itself is written to the log.
export function Audit({ query = new URLSearchParams(), onQuery = () => undefined }: QueryProps) {
  const filter: AuditFilter = {
    action: query.get("action") ?? "",
    actor: query.get("actor") ?? "",
    target: query.get("target") ?? "",
    ip: query.get("ip") ?? "",
    from: query.get("from") ?? "",
    to: query.get("to") ?? "",
  };
  const key = JSON.stringify(filter);

  const [entries, setEntries] = useState<AuditEntry[] | null>(null);
  const [next, setNext] = useState<string | null>(null);
  const [actions, setActions] = useState<string[]>([]);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");

  useEffect(() => {
    auditActions().then(setActions, () => setActions([]));
  }, []);

  useEffect(() => {
    let live = true;
    const f = JSON.parse(key) as AuditFilter;
    setBusy(true);
    const t = window.setTimeout(() => {
      listAudit(f, undefined, pageSize).then(
        (p) => {
          if (!live) return;
          setEntries(p.entries);
          setNext(p.next);
          setError("");
          setBusy(false);
        },
        (err: unknown) => {
          if (!live) return;
          setError(auditMessage(err));
          setBusy(false);
        },
      );
    }, typingDelay);
    return () => {
      live = false;
      window.clearTimeout(t);
    };
  }, [key]);

  const more = async () => {
    if (!next) return;
    setBusy(true);
    try {
      const p = await listAudit(filter, next, pageSize);
      setEntries((old) => [...(old ?? []), ...p.entries]);
      setNext(p.next);
      setError("");
    } catch (err) {
      setError(auditMessage(err));
    } finally {
      setBusy(false);
    }
  };

  const set = (changes: Partial<Record<keyof AuditFilter, string>>) => {
    const q = new URLSearchParams(query);
    for (const [k, v] of Object.entries(changes)) {
      if (v) q.set(k, v);
      else q.delete(k);
    }
    onQuery(q);
  };

  const filtered = Object.values(filter).some((v) => v);
  const badRange = filter.from && filter.to && filter.from > filter.to;

  return (
    <>
      <PageHead
        title="Audit log"
        description="Who did what and when, in the panel and through the API. Entries cannot be changed or deleted."
        actions={
          <>
            <a className="ui-btn" href={exportUrl("csv", filter)} download>
              <DownloadSimple size={16} aria-hidden="true" />
              CSV
            </a>
            <a className="ui-btn" href={exportUrl("json", filter)} download>
              <DownloadSimple size={16} aria-hidden="true" />
              JSON
            </a>
          </>
        }
      />
      {error && <Notice tone="err" title={error} />}
      {badRange && <Notice tone="warn" title="The start date is after the end date." />}
      <Card padded={false}>
        <div className="ui-filters">
          <select className="ui-select" aria-label="Action" value={filter.action} onChange={(e) => set({ action: e.target.value })}>
            <option value="">All actions</option>
            {actionGroups(actions).map((g) => (
              <optgroup key={g.prefix} label={g.prefix}>
                {g.actions.length > 1 && <option value={g.prefix}>{g.prefix}.* (all)</option>}
                {g.actions.map((a) => (
                  <option key={a} value={a}>
                    {a}
                  </option>
                ))}
              </optgroup>
            ))}
          </select>
          <input
            className="ui-input"
            type="search"
            aria-label="Account"
            placeholder="Account (exact name)"
            maxLength={64}
            value={filter.actor}
            onChange={(e) => set({ actor: e.target.value })}
          />
          <input
            className="ui-input"
            type="search"
            aria-label="Target"
            placeholder="Target contains, such as a site"
            maxLength={200}
            value={filter.target}
            onChange={(e) => set({ target: e.target.value })}
          />
          <input
            className="ui-input ui-audit-ip"
            type="search"
            aria-label="IP address"
            placeholder="IP address"
            maxLength={45}
            value={filter.ip}
            onChange={(e) => set({ ip: e.target.value })}
          />
          <label className="ui-audit-date">
            From
            <input className="ui-input" type="date" value={filter.from} onChange={(e) => set({ from: e.target.value })} />
          </label>
          <label className="ui-audit-date">
            To
            <input className="ui-input" type="date" value={filter.to} onChange={(e) => set({ to: e.target.value })} />
          </label>
        </div>
        {entries === null ? (
          !error && <p className="ui-empty" aria-busy="true">Loading the audit log…</p>
        ) : (
          <Table
            label="Audit log"
            columns={columns}
            rows={entries}
            rowKey={(e) => String(e.id)}
            empty={filtered ? "No entry matches the filters." : "The audit log is empty."}
          />
        )}
      </Card>
      {next && (
        <div className="ui-row">
          <Button disabled={busy} onClick={() => void more()}>
            {busy ? "Loading…" : "Show older entries"}
          </Button>
        </div>
      )}
    </>
  );
}
