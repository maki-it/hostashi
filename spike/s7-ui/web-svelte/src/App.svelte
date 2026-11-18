<script lang="ts">
  import SiteList from "./SiteList.svelte";
  import Metrics from "./Metrics.svelte";
  import Terminal from "./Terminal.svelte";

  const tabs = ["Sites", "Metrics", "Terminal"] as const;
  let tab = $state<(typeof tabs)[number]>("Sites");
</script>

<header>
  <h1>Hostashi (Svelte)</h1>
  <div role="tablist" class="tabs">
    {#each tabs as t (t)}
      <button role="tab" aria-selected={tab === t} onclick={() => (tab = t)}>{t}</button>
    {/each}
  </div>
</header>
<main>
  {#if tab === "Sites"}<SiteList />{/if}
  {#if tab === "Metrics"}<Metrics />{/if}
  {#if tab === "Terminal"}<Terminal />{/if}
</main>
