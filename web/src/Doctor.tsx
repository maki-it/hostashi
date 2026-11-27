// SPDX-License-Identifier: AGPL-3.0-or-later

import { ArrowClockwise } from "@phosphor-icons/react";
import { useCallback, useEffect, useState } from "react";
import { checkTitle, doctorMessage, runDoctor, type DoctorCheck, type DoctorReport, type DoctorStatus } from "./doctor";
import { formatHash, type Page } from "./routes";
import { Badge, Button, Card, Notice, PageHead, RelTime, Table, type Column, type NoticeTone, type Sort, type Tone } from "./ui";

const labels: Record<DoctorStatus, string> = { ok: "OK", warn: "Warning", fail: "Failed" };
const tones: Record<DoctorStatus, Tone> = { ok: "ok", warn: "warn", fail: "err" };
const noticeTones: Record<DoctorStatus, NoticeTone> = { ok: "ok", warn: "warn", fail: "err" };
const rank: Record<DoctorStatus, number> = { fail: 0, warn: 1, ok: 2 };
// Server checks whose setting lives on another page link there.
const settingsPage: Partial<Record<string, { page: Page; label: string; query?: Record<string, string> }>> = {
  "panel.tls": { page: "system", label: "System" },
  "service.fail2ban.jails": { page: "firewall", label: "Firewall", query: { tab: "blocked" } },
};

type QueryProps = {
  /** The address query: q, show (all or problems), sort, dir. */
  query?: URLSearchParams;
  /** Replaces the query in the address without a new history entry. */
  onQuery?: (q: URLSearchParams) => void;
};

function overall(r: DoctorReport): string {
  if (r.status === "ok") return "Everything is fine.";
  const parts = [];
  if (r.counts.fail) parts.push(r.counts.fail === 1 ? "1 check failed" : r.counts.fail + " checks failed");
  if (r.counts.warn) parts.push(r.counts.warn === 1 ? "1 warning" : r.counts.warn + " warnings");
  return parts.join(", ") + ".";
}

function ServerWhere({ id }: { id: string }) {
  const to = settingsPage[id];
  if (!to) return "Server";
  return (
    <a className="ui-nowrap" href={formatHash({ page: to.page, query: new URLSearchParams(to.query) })}>
      {to.label}
    </a>
  );
}

// Doctor shows the checks of hostashi doctor in one table, problems first.
// Nothing here changes the server.
export function Doctor({ query = new URLSearchParams(), onQuery = () => undefined }: QueryProps) {
  const [report, setReport] = useState<DoctorReport | null>(null);
  const [busy, setBusy] = useState(true);
  const [error, setError] = useState("");

  const load = useCallback(async () => {
    setBusy(true);
    try {
      setReport(await runDoctor());
      setError("");
    } catch (err) {
      setError(doctorMessage(err));
    } finally {
      setBusy(false);
    }
  }, []);

  useEffect(() => {
    void load();
  }, [load]);

  const set = (changes: Record<string, string>) => {
    const next = new URLSearchParams(query);
    for (const [k, v] of Object.entries(changes)) {
      if (v) next.set(k, v);
      else next.delete(k);
    }
    onQuery(next);
  };

  const again = (
    <Button icon={<ArrowClockwise size={16} aria-hidden="true" />} disabled={busy} onClick={() => void load()}>
      {busy ? "Checking…" : "Check again"}
    </Button>
  );
  const head = (
    <PageHead
      title="Health"
      description="The checks of hostashi doctor, for the server and each site. They only read the server and change nothing."
      actions={report ? again : undefined}
    />
  );

  if (!report) {
    return (
      <>
        {head}
        {error ? (
          <Notice tone="err" title={error}>
            <Button onClick={() => void load()}>Try again</Button>
          </Notice>
        ) : (
          <p aria-busy="true">Running the checks…</p>
        )}
      </>
    );
  }

  const columns: Column<DoctorCheck>[] = [
    {
      key: "check",
      header: "Check",
      sortValue: (c) => checkTitle(c.id),
      cell: (c) => (
        <div className="ui-stack-tight">
          <strong>{checkTitle(c.id)}</strong>
          <span>{c.summary}</span>
          {c.hint && <p className="ui-hint ui-flush">{c.hint}</p>}
        </div>
      ),
    },
    {
      key: "where",
      header: "Where",
      sortValue: (c) => (c.site ? "1" + c.site : "0"),
      cell: (c) =>
        c.site ? (
          <a className="ui-nowrap" href={formatHash({ page: "sites", site: { name: c.site, tab: "overview" } })}>
            Site {c.site}
          </a>
        ) : (
          <ServerWhere id={c.id} />
        ),
    },
    {
      key: "status",
      header: "Status",
      sortValue: (c) => rank[c.status],
      cell: (c) => <Badge tone={tones[c.status]}>{labels[c.status]}</Badge>,
    },
  ];

  const problemsOnly = query.get("show") === "problems";
  const q = (query.get("q") ?? "").trim().toLowerCase();
  const shown = report.checks.filter(
    (c) =>
      (!problemsOnly || c.status !== "ok") &&
      (!q || [checkTitle(c.id), c.summary, c.hint ?? "", c.site ?? "server"].some((s) => s.toLowerCase().includes(q))),
  );
  const sortKey = query.get("sort");
  const sort: Sort = sortKey ? { key: sortKey, dir: query.get("dir") === "desc" ? "desc" : "asc" } : { key: "status", dir: "asc" };

  return (
    <>
      {head}
      <Notice tone={noticeTones[report.status]} title={overall(report)}>
        Checked <RelTime iso={report.checkedAt} />.
      </Notice>
      {error && <Notice tone="err" title={error} />}
      <Card padded={false}>
        <div className="ui-filters">
          <input
            className="ui-input"
            type="search"
            aria-label="Filter checks"
            placeholder="Filter by check or site"
            value={query.get("q") ?? ""}
            onChange={(e) => set({ q: e.target.value })}
          />
          <select className="ui-select" aria-label="Show" value={problemsOnly ? "problems" : ""} onChange={(e) => set({ show: e.target.value })}>
            <option value="">All checks</option>
            <option value="problems">Only problems</option>
          </select>
        </div>
        <Table
          label="Checks"
          columns={columns}
          rows={shown}
          rowKey={(c) => [c.id, c.site ?? "", c.summary].join("/")}
          sort={sort}
          onSort={(s) => set({ sort: s.key, dir: s.dir })}
          empty={problemsOnly && !q ? "No problems." : "No check matches the filter."}
        />
      </Card>
    </>
  );
}
