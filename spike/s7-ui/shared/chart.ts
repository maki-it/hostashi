import uPlot from "uplot";
import "uplot/dist/uPlot.min.css";
import type { Sample } from "./api";

const MAX = 120;

export interface LiveChart {
  push(s: Sample): void;
  resize(width: number): void;
  destroy(): void;
}

export function createChart(el: HTMLElement): LiveChart {
  const data: [number[], number[], number[]] = [[], [], []];
  const style = getComputedStyle(document.documentElement);
  const c1 = style.getPropertyValue("--series-1").trim() || "#2a6fdb";
  const c2 = style.getPropertyValue("--series-2").trim() || "#c4570a";
  const plot = new uPlot(
    {
      width: el.clientWidth || 600,
      height: 220,
      scales: { y: { range: [0, 100] } },
      series: [
        {},
        { label: "CPU %", stroke: c1, width: 2 },
        { label: "RAM %", stroke: c2, width: 2 },
      ],
      axes: [{}, { values: (_u, v) => v.map((x) => `${x}%`) }],
    },
    data,
    el,
  );
  return {
    push(s) {
      data[0].push(s.t);
      data[1].push(s.cpu);
      data[2].push(s.mem);
      if (data[0].length > MAX) data.forEach((a) => a.shift());
      plot.setData(data);
    },
    resize(width) {
      plot.setSize({ width, height: 220 });
    },
    destroy() {
      plot.destroy();
    },
  };
}
