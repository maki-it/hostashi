// SPDX-License-Identifier: AGPL-3.0-or-later

import { useEffect, useState, type ReactNode } from "react";
import { getSiteLimits } from "./limits";
import { formatHash } from "./routes";
import { bytes, rate, ranges, serviceMessage, type Range } from "./services";
import { Card, LineChart, Notice, Table, type Column, type Sort } from "./ui";
import { cores, count, getSiteUsage, ofCores, percent, rankSites, type SiteTotal, type SiteUsage } from "./usage";

const rangeLabels: Record<Range, string> = { "1h": "1 hour", "24h": "24 hours", "7d": "7 days", "30d": "30 days" };

// bytesPerSecond formats disk I/O: bytes, not bits as for the network.
const bytesPerSecond = (v: number) => `${bytes(v)}/s`;

function RangePicker({ range, onChange }: { range: Range; onChange: (r: Range) => void }) {
  return (
    <div className="ui-range" role="group" aria-label="Time range">
      {ranges.map((r) => (
        <button key={r} type="button" aria-pressed={r === range} onClick={() => onChange(r)}>
          {r}
        </button>
      ))}
    </div>
  );
}

// usePolled loads f for the range and again every 15 s (1h) or 5 min.
function usePolled<T>(range: Range, f: (r: Range) => Promise<T>, deps: readonly unknown[] = []) {
  const [data, setData] = useState<{ range: Range; value: T } | null>(null);
  const [error, setError] = useState("");

  useEffect(() => {
    let live = true;
    const load = () =>
      f(range).then(
        (value) => {
          if (!live) return;
          setData({ range, value });
          setError("");
        },
        (err: unknown) => live && setError(serviceMessage(err)),
      );
    void load();
    const t = window.setInterval(() => void load(), range === "1h" ? 15000 : 300000);
    return () => {
      live = false;
      window.clearInterval(t);
    };
    // f is recreated on every render; deps name what it depends on.
  }, [range, ...deps]);

  return { data: data && data.range === range ? data.value : null, loading: data === null && !error, error };
}

// SiteUsageCard shows one site's usage over a chosen range (ADR-0063):
// CPU, memory, web traffic and disk I/O, like the server dashboard.
export function SiteUsageCard({ site }: { site: string }) {
  const [range, setRange] = useState<Range>("1h");
  const { data, loading, error } = usePolled<SiteUsage>(range, (r) => getSiteUsage(site, r), [site]);
  const [memLimit, setMemLimit] = useState(0);

  useEffect(() => {
    getSiteLimits(site).then(
      (l) => setMemLimit(l.limits.memoryBytes),
      () => setMemLimit(0),
    );
  }, [site]);

  const cpus = data?.cpus ?? 1;
  const avail = data?.cores ?? cpus;

  const points = data?.points ?? [];
  const times = points.map((p) => p.at);
  const days = range === "7d" || range === "30d";
  const last = points[points.length - 1];

  return (
    <Card title={`Usage, last ${rangeLabels[range]}`} actions={<RangePicker range={range} onChange={setRange} />}>
      {error && <Notice tone="err" title={error} />}
      {loading ? (
        <p aria-busy="true">Loading…</p>
      ) : (
        <div className="ui-stack">
          <div className="ui-charts ui-charts-2">
            <section className="ui-stack-tight" aria-label="CPU">
              <h3 className="ui-chart-title">
                CPU
                <span className="ui-hint">
                  {" "}
                  · 100 % is {avail < cpus ? `the limit of ${cores(avail * 100)}` : `all ${cores(cpus * 100)} of the server`}
                </span>
              </h3>
              <LineChart
                title="CPU use in percent of the cores the site may use"
                times={times}
                days={days}
                max={100}
                format={percent}
                series={[{ label: "CPU", values: points.map((p) => ofCores(p.cpu, avail)) }]}
              />
            </section>
            <section className="ui-stack-tight" aria-label="Memory">
              <h3 className="ui-chart-title">Memory{memLimit > 0 && <span className="ui-hint"> of {bytes(memLimit)} limit</span>}</h3>
              <LineChart
                title="Memory in use"
                times={times}
                days={days}
                {...(memLimit > 0 ? { max: memLimit } : {})}
                format={bytes}
                series={[{ label: "In use", values: points.map((p) => p.mem) }]}
              />
            </section>
            <section className="ui-stack-tight" aria-label="Web traffic">
              <h3 className="ui-chart-title">Web traffic</h3>
              <LineChart
                title="Web traffic"
                times={times}
                days={days}
                format={rate}
                series={[
                  { label: "In", values: points.map((p) => p.httpIn) },
                  { label: "Out", values: points.map((p) => p.httpOut) },
                ]}
              />
            </section>
            <section className="ui-stack-tight" aria-label="Disk I/O">
              <h3 className="ui-chart-title">Disk I/O</h3>
              <LineChart
                title="Disk reads and writes"
                times={times}
                days={days}
                format={bytesPerSecond}
                series={[
                  { label: "Read", values: points.map((p) => p.ioRead) },
                  { label: "Write", values: points.map((p) => p.ioWrite) },
                ]}
              />
            </section>
          </div>
          {last && (
            <p className="ui-hint ui-flush">
              Now: {cores(last.cpu)} of {cores(avail * 100)} busy · {last.httpReq.toFixed(1)} requests/s · {last.tasks} processes · {bytes(last.cache)} file cache. Database queries run in the shared
              database server and are not counted here.
            </p>
          )}
        </div>
      )}
    </Card>
  );
}

const traffic = (t: SiteTotal) => t.httpInBytes + t.httpOutBytes;
const io = (t: SiteTotal) => t.ioReadBytes + t.ioWriteBytes;

function Bar({ value, max, children }: { value: number; max: number; children: ReactNode }) {
  const pct = max > 0 ? Math.min(100, (value / max) * 100) : 0;
  return (
    <div className="ui-usage-cell">
      <span>{children}</span>
      <span className="ui-usage-bar" aria-hidden="true">
        <span style={{ width: `${pct.toFixed(1)}%` }} />
      </span>
    </div>
  );
}

// SitesByUsage ranks every site over a chosen range (ADR-0063), for admins:
// which site takes the most CPU, memory, traffic or disk I/O.
export function SitesByUsage() {
  const [range, setRange] = useState<Range>("24h");
  const [sort, setSort] = useState<Sort>({ key: "cpu", dir: "desc" });
  const { data, loading, error } = usePolled(range, (r) => rankSites(r));
  const rows = data?.sites ?? [];
  const cpus = data?.cpus ?? 1;

  const top = {
    mem: Math.max(0, ...rows.map((r) => r.memAvg)),
    traffic: Math.max(0, ...rows.map(traffic)),
    io: Math.max(0, ...rows.map(io)),
  };

  const columns: Column<SiteTotal>[] = [
    {
      key: "site",
      header: "Site",
      cell: (r) => <a href={formatHash({ page: "sites", site: { name: r.site, tab: "usage" } })}>{r.site}</a>,
      sortValue: (r) => r.site,
    },
    {
      key: "cpu",
      header: "CPU (avg · peak)",
      // 100 % is the cores the site may use: its limit, else the server's.
      cell: (r) => (
        <Bar value={ofCores(r.cpuAvg, r.cores)} max={100}>
          {percent(ofCores(r.cpuAvg, r.cores))} <span className="ui-hint">· {percent(ofCores(r.cpuMax, r.cores))}</span>
          <span className="ui-hint">
            {" "}
            of {cores(r.cores * 100)}
            {r.cores < cpus ? " (limit)" : ""}
          </span>
        </Bar>
      ),
      sortValue: (r) => ofCores(r.cpuAvg, r.cores),
    },
    {
      key: "memory",
      header: "Memory (avg · peak)",
      cell: (r) => (
        <Bar value={r.memAvg} max={top.mem}>
          {bytes(r.memAvg)} <span className="ui-hint">· {bytes(r.memMax)}</span>
        </Bar>
      ),
      sortValue: (r) => r.memAvg,
    },
    {
      key: "traffic",
      header: "Web traffic",
      cell: (r) => (
        <Bar value={traffic(r)} max={top.traffic}>
          {bytes(traffic(r))} <span className="ui-hint">· {count(r.httpRequests)} req.</span>
        </Bar>
      ),
      sortValue: traffic,
    },
    {
      key: "io",
      header: "Disk I/O",
      cell: (r) => (
        <Bar value={io(r)} max={top.io}>
          {bytes(io(r))}
        </Bar>
      ),
      sortValue: io,
    },
  ];

  return (
    <Card title={`Sites by usage, last ${rangeLabels[range]}`} actions={<RangePicker range={range} onChange={setRange} />}>
      {error && <Notice tone="err" title={error} />}
      {loading ? (
        <p aria-busy="true">Loading…</p>
      ) : (
        <Table
          label="Sites by usage"
          columns={columns}
          rows={rows}
          rowKey={(r) => r.site}
          sort={sort}
          onSort={setSort}
          empty="No data for this range yet."
        />
      )}
    </Card>
  );
}
