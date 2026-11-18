import { useEffect, useRef, useState } from "react";
import { streamMetrics } from "../../shared/api";
import { createChart } from "../../shared/chart";

export function Metrics() {
  const el = useRef<HTMLDivElement>(null);
  const [status, setStatus] = useState("connecting");
  const [count, setCount] = useState(0);

  useEffect(() => {
    const chart = createChart(el.current!);
    const ac = new AbortController();
    const ro = new ResizeObserver(() => chart.resize(el.current!.clientWidth));
    ro.observe(el.current!);
    streamMetrics((s) => {
      setStatus("live");
      setCount((c) => c + 1);
      chart.push(s);
    }, ac.signal).catch((e) => !ac.signal.aborted && setStatus(`error: ${e}`));
    return () => {
      ac.abort();
      ro.disconnect();
      chart.destroy();
    };
  }, []);

  return (
    <div className="card">
      <p data-testid="metrics-status">{status} · {count} samples</p>
      <div ref={el} />
    </div>
  );
}
