// SPDX-License-Identifier: AGPL-3.0-or-later

import { useEffect, useState } from "react";
import { bytes, getDashboard, rate, ranges, serviceMessage, type Dashboard, type Range } from "./services";
import { Card, LineChart, Meter, Notice } from "./ui";

const rangeLabels: Record<Range, string> = { "1h": "1 hour", "24h": "24 hours", "7d": "7 days", "30d": "30 days" };

const pct = (v: number) => `${v >= 10 || v === 0 ? v.toFixed(0) : v.toFixed(1)} %`;

// ServerDashboard is the server's load over a chosen range (ADR-0052): CPU,
// memory and network as lines, and the disks as they are now.
export function ServerDashboard() {
  const [range, setRange] = useState<Range>("1h");
  const [data, setData] = useState<Dashboard | null>(null);
  const [error, setError] = useState("");

  useEffect(() => {
    let live = true;
    const load = () =>
      getDashboard(range).then(
        (d) => {
          if (!live) return;
          setData(d);
          setError("");
        },
        (err: unknown) => live && setError(serviceMessage(err)),
      );
    void load();
    // The newest point of 1h comes every 15 s; the stored tiers every 5 min.
    const t = window.setInterval(() => void load(), range === "1h" ? 15000 : 300000);
    return () => {
      live = false;
      window.clearInterval(t);
    };
  }, [range]);

  const picker = (
    <div className="ui-range" role="group" aria-label="Time range">
      {ranges.map((r) => (
        <button key={r} type="button" aria-pressed={r === range} onClick={() => setRange(r)}>
          {r}
        </button>
      ))}
    </div>
  );

  const points = data && data.range === range ? data.points : [];
  const times = points.map((p) => p.at);
  const days = range === "7d" || range === "30d";
  const last = points[points.length - 1];

  // Interfaces are summed: the server has one or two, and one line per
  // direction stays readable.
  const rx = points.map((p) => p.net.reduce((s, n) => s + n.rx, 0));
  const tx = points.map((p) => p.net.reduce((s, n) => s + n.tx, 0));
  const memTotal = last?.memTotal ?? 0;

  return (
    <Card title={`Server load, last ${rangeLabels[range]}`} actions={picker}>
      {error && <Notice tone="err" title={error} />}
      {!data && !error ? (
        <p aria-busy="true">Loading…</p>
      ) : (
        <div className="ui-stack">
          <div className="ui-charts">
            <section className="ui-stack-tight" aria-label="CPU">
              <h3 className="ui-chart-title">CPU</h3>
              <LineChart
                title="CPU use in percent"
                times={times}
                days={days}
                max={100}
                format={pct}
                series={[
                  { label: "Busy", values: points.map((p) => p.cpuBusy) },
                  { label: "Waiting for disk", values: points.map((p) => p.cpuIowait) },
                ]}
              />
            </section>
            <section className="ui-stack-tight" aria-label="Memory">
              <h3 className="ui-chart-title">Memory{memTotal > 0 && <span className="ui-hint"> of {bytes(memTotal)}</span>}</h3>
              <LineChart
                title="Memory in use"
                times={times}
                days={days}
                {...(memTotal > 0 ? { max: memTotal } : {})}
                format={bytes}
                series={[{ label: "In use", values: points.map((p) => p.memUsed) }]}
              />
            </section>
            <section className="ui-stack-tight" aria-label="Network">
              <h3 className="ui-chart-title">Network</h3>
              <LineChart
                title="Network traffic"
                times={times}
                days={days}
                format={rate}
                series={[
                  { label: "In", values: rx },
                  { label: "Out", values: tx },
                ]}
              />
            </section>
            <section className="ui-stack-tight" aria-label="Disks">
              <h3 className="ui-chart-title">Disks</h3>
              {last && last.disks.length > 0 ? (
                last.disks.map((d) => (
                  <Meter key={d.name} label={d.name} value={d.used} max={d.total} detail={`${bytes(d.used)} of ${bytes(d.total)}`} />
                ))
              ) : (
                <p className="ui-hint ui-flush">No data yet.</p>
              )}
              {last && last.swapTotal > 0 && (
                <Meter label="Swap" value={last.swapUsed} max={last.swapTotal} detail={`${bytes(last.swapUsed)} of ${bytes(last.swapTotal)}`} />
              )}
            </section>
          </div>
          {last && (
            <p className="ui-hint ui-flush">
              Load average (1 min): {last.load1.toFixed(2)}
              {last.cpuSteal >= 1 && <> · CPU taken by the host (steal): {pct(last.cpuSteal)}</>}
            </p>
          )}
        </div>
      )}
    </Card>
  );
}
