<script lang="ts">
  import { onMount } from "svelte";
  import { streamMetrics } from "../../shared/api";
  import { createChart } from "../../shared/chart";

  let el: HTMLDivElement;
  let status = $state("connecting");
  let count = $state(0);

  onMount(() => {
    const chart = createChart(el);
    const ac = new AbortController();
    const ro = new ResizeObserver(() => chart.resize(el.clientWidth));
    ro.observe(el);
    streamMetrics((s) => {
      status = "live";
      count++;
      chart.push(s);
    }, ac.signal).catch((e) => !ac.signal.aborted && (status = `error: ${e}`));
    return () => {
      ac.abort();
      ro.disconnect();
      chart.destroy();
    };
  });
</script>

<div class="card">
  <p data-testid="metrics-status">{status} · {count} samples</p>
  <div bind:this={el}></div>
</div>
