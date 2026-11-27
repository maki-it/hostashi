// SPDX-License-Identifier: AGPL-3.0-or-later

import { useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import { formatNumber } from "./format";
import { apiErrorText } from "./i18n";
import { bytes, getDashboard, rate, ranges, type Dashboard, type Range } from "./services";
import { Card, LineChart, Meter, Notice } from "./ui";

// pct writes a share with one decimal below 10: "4.5 %", in German "4,5 %".
const pct = (v: number) => {
  const digits = v >= 10 || v === 0 ? 0 : 1;
  return `${formatNumber(v, { minimumFractionDigits: digits, maximumFractionDigits: digits })} %`;
};

// ServerDashboard is the server's load over a chosen range (ADR-0052): CPU,
// memory and network as lines, and the disks as they are now.
export function ServerDashboard() {
  const { t } = useTranslation();
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
        (err: unknown) => live && setError(apiErrorText(err)),
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
    <div className="ui-range" role="group" aria-label={t("overview.dashboard.timeRange")}>
      {ranges.map((r) => (
        <button key={r} type="button" aria-pressed={r === range} onClick={() => setRange(r)}>
          {t(`overview.dashboard.range.${r}`)}
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
    <Card title={t(`overview.dashboard.title.${range}`)} actions={picker}>
      {error && <Notice tone="err" title={error} />}
      {!data && !error ? (
        <p aria-busy="true">{t("common.status.loading")}</p>
      ) : (
        <div className="ui-stack">
          <div className="ui-charts">
            <section className="ui-stack-tight" aria-label={t("overview.dashboard.cpu")}>
              <h3 className="ui-chart-title">{t("overview.dashboard.cpu")}</h3>
              <LineChart
                title={t("overview.dashboard.cpuChart")}
                times={times}
                days={days}
                max={100}
                format={pct}
                series={[
                  { label: t("overview.dashboard.busy"), values: points.map((p) => p.cpuBusy) },
                  { label: t("overview.dashboard.iowait"), values: points.map((p) => p.cpuIowait) },
                ]}
              />
            </section>
            <section className="ui-stack-tight" aria-label={t("overview.dashboard.memory")}>
              <h3 className="ui-chart-title">
                {t("overview.dashboard.memory")}
                {memTotal > 0 && <span className="ui-hint"> {t("overview.dashboard.memoryOf", { total: bytes(memTotal) })}</span>}
              </h3>
              <LineChart
                title={t("overview.dashboard.memoryChart")}
                times={times}
                days={days}
                {...(memTotal > 0 ? { max: memTotal } : {})}
                format={bytes}
                series={[{ label: t("overview.dashboard.inUse"), values: points.map((p) => p.memUsed) }]}
              />
            </section>
            <section className="ui-stack-tight" aria-label={t("overview.dashboard.network")}>
              <h3 className="ui-chart-title">{t("overview.dashboard.network")}</h3>
              <LineChart
                title={t("overview.dashboard.networkChart")}
                times={times}
                days={days}
                format={rate}
                series={[
                  { label: t("overview.dashboard.in"), values: rx },
                  { label: t("overview.dashboard.out"), values: tx },
                ]}
              />
            </section>
            <section className="ui-stack-tight" aria-label={t("overview.dashboard.disks")}>
              <h3 className="ui-chart-title">{t("overview.dashboard.disks")}</h3>
              {last && last.disks.length > 0 ? (
                last.disks.map((d) => (
                  <Meter key={d.name} label={d.name} value={d.used} max={d.total} detail={t("overview.dashboard.usedOf", { used: bytes(d.used), total: bytes(d.total) })} />
                ))
              ) : (
                <p className="ui-hint ui-flush">{t("overview.dashboard.noData")}</p>
              )}
              {last && last.swapTotal > 0 && (
                <Meter label={t("overview.dashboard.swap")} value={last.swapUsed} max={last.swapTotal} detail={t("overview.dashboard.usedOf", { used: bytes(last.swapUsed), total: bytes(last.swapTotal) })} />
              )}
            </section>
          </div>
          {last && (
            <p className="ui-hint ui-flush">
              {t("overview.dashboard.load", { value: formatNumber(last.load1, { minimumFractionDigits: 2, maximumFractionDigits: 2 }) })}
              {last.cpuSteal >= 1 && <> · {t("overview.dashboard.steal", { value: pct(last.cpuSteal) })}</>}
            </p>
          )}
        </div>
      )}
    </Card>
  );
}
