// SPDX-License-Identifier: AGPL-3.0-or-later

import { useId, useRef, useState, type PointerEvent } from "react";
import { useTranslation } from "react-i18next";
import { formatDateTime } from "../format";

export type Series = {
  label: string;
  /** One value per time; null where there is no point (a gap). */
  values: readonly (number | null)[];
};

type LineChartProps = {
  /** Accessible name, also the card's title in most places. */
  title: string;
  /** ISO times, one per value. */
  times: readonly string[];
  /** At most two series; colours --series-1 and --series-2 in this order. */
  series: readonly Series[];
  /** Formats a value for the axis and the tooltip. */
  format: (v: number) => string;
  /** Upper end of the axis; the largest value (at least 1) by default. */
  max?: number;
  /** Shows dates instead of times of day on the axis and in the tooltip. */
  days?: boolean;
};

const W = 600;
const H = 160;

function timeLabel(iso: string, days: boolean): string {
  const d = new Date(iso);
  return days
    ? formatDateTime(d, { month: "short", day: "numeric", hour: "2-digit", minute: "2-digit" })
    : formatDateTime(d, { hour: "2-digit", minute: "2-digit" });
}

// path draws one series as line segments; a null value breaks the line.
function path(values: readonly (number | null)[], max: number): string {
  const n = values.length;
  let d = "";
  let pen = false;
  values.forEach((v, i) => {
    if (v === null) {
      pen = false;
      return;
    }
    const x = n === 1 ? W / 2 : (i / (n - 1)) * W;
    const y = H - (Math.min(v, max) / max) * H;
    d += `${pen ? "L" : "M"}${x.toFixed(1)},${y.toFixed(1)}`;
    pen = true;
  });
  return d;
}

/**
 * LineChart draws one or two series over time (ADR-0052): a thin line per
 * series on one axis that starts at zero, a legend for two series, and a
 * crosshair with a tooltip on hover or touch. A table with the same
 * numbers is available below it for screen readers and copying.
 */
export function LineChart({ title, times, series, format, max, days = false }: LineChartProps) {
  const { t } = useTranslation();
  const id = useId();
  const box = useRef<HTMLDivElement>(null);
  const [hover, setHover] = useState<number | null>(null);
  const top = max ?? Math.max(1, ...series.flatMap((s) => s.values.filter((v): v is number => v !== null)));
  const n = times.length;

  const move = (e: PointerEvent<HTMLDivElement>) => {
    const r = box.current?.getBoundingClientRect();
    if (!r || n === 0) return;
    const i = Math.round(((e.clientX - r.left) / r.width) * (n - 1));
    setHover(Math.min(n - 1, Math.max(0, i)));
  };

  if (n === 0) {
    return <p className="ui-hint ui-flush">{t("ui.chart.empty")}</p>;
  }

  const x = hover === null ? 0 : n === 1 ? 50 : (hover / (n - 1)) * 100;

  return (
    <figure className="ui-chart" aria-labelledby={id}>
      <figcaption id={id} className="ui-sr-only">
        {title}
      </figcaption>
      {series.length < 2 ? (
        // An empty row keeps the plot in line with charts that have a legend.
        <div className="ui-chart-legend" aria-hidden="true" />
      ) : (
        <ul className="ui-chart-legend">
          {series.map((s, i) => (
            <li key={s.label}>
              <span className={`ui-chart-key ui-series-${i + 1}`} aria-hidden="true" />
              {s.label}
            </li>
          ))}
        </ul>
      )}
      <div className="ui-chart-plot">
        <div className="ui-chart-axis" aria-hidden="true">
          <span>{format(top)}</span>
          <span>{format(top / 2)}</span>
          <span>{format(0)}</span>
        </div>
        <div ref={box} className="ui-chart-area" onPointerMove={move} onPointerDown={move} onPointerLeave={() => setHover(null)}>
          <svg viewBox={`0 0 ${W} ${H}`} preserveAspectRatio="none" aria-hidden="true" focusable="false">
            <line className="ui-chart-grid" x1="0" x2={W} y1={H / 2} y2={H / 2} />
            <line className="ui-chart-base" x1="0" x2={W} y1={H} y2={H} />
            {series.map((s, i) => (
              <path key={s.label} className={`ui-chart-line ui-series-${i + 1}`} d={path(s.values, top)} />
            ))}
          </svg>
          {hover !== null && (
            <>
              <div className="ui-chart-cross" style={{ left: `${x}%` }} />
              <div className={x > 60 ? "ui-chart-tip ui-chart-tip-left" : "ui-chart-tip"} style={{ left: `${x}%` }} role="status">
                <strong>{timeLabel(times[hover]!, days)}</strong>
                {series.map((s, i) => (
                  <span key={s.label}>
                    <span className={`ui-chart-key ui-series-${i + 1}`} aria-hidden="true" />
                    {s.label}: {s.values[hover] === null ? t("ui.chart.noData") : format(s.values[hover]!)}
                  </span>
                ))}
              </div>
            </>
          )}
        </div>
      </div>
      <div className="ui-chart-times" aria-hidden="true">
        <span>{timeLabel(times[0]!, days)}</span>
        <span>{timeLabel(times[n - 1]!, days)}</span>
      </div>
      <details className="ui-chart-table">
        <summary>{t("ui.chart.showTable")}</summary>
        <table>
          <thead>
            <tr>
              <th scope="col">{t("ui.chart.time")}</th>
              {series.map((s) => (
                <th key={s.label} scope="col">
                  {s.label}
                </th>
              ))}
            </tr>
          </thead>
          <tbody>
            {times.map((t, i) => (
              <tr key={t}>
                <td>{timeLabel(t, days)}</td>
                {series.map((s) => (
                  <td key={s.label}>{s.values[i] === null ? "–" : format(s.values[i]!)}</td>
                ))}
              </tr>
            ))}
          </tbody>
        </table>
      </details>
    </figure>
  );
}

type MeterProps = { label: string; value: number; max: number; detail: string };

/** Meter is one horizontal bar of a share, such as the disk use of one file system. */
export function Meter({ label, value, max, detail }: MeterProps) {
  const pct = max > 0 ? Math.min(100, Math.max(0, (value / max) * 100)) : 0;
  const tone = pct >= 90 ? " ui-meter-err" : pct >= 80 ? " ui-meter-warn" : "";
  return (
    <div className="ui-meter">
      <div className="ui-meter-head">
        <strong>{label}</strong>
        <span>{detail}</span>
      </div>
      <div className={"ui-meter-track" + tone} role="meter" aria-label={label} aria-valuemin={0} aria-valuemax={100} aria-valuenow={Math.round(pct)} aria-valuetext={detail}>
        <div className="ui-meter-fill" style={{ width: `${pct}%` }} />
      </div>
    </div>
  );
}
