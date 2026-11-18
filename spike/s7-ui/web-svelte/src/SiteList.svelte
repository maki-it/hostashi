<script lang="ts">
  import { onMount } from "svelte";
  import { listSites, type Site } from "../../shared/api";
  import SiteForm from "./SiteForm.svelte";

  type SortKey = "domain" | "kind" | "diskMb";

  let sites = $state<Site[]>([]);
  let error = $state("");
  let filter = $state("");
  let sort = $state<SortKey>("domain");

  const rows = $derived.by(() => {
    const f = filter.trim().toLowerCase();
    return sites
      .filter((s) => !f || s.domain.includes(f) || s.user.includes(f))
      .sort((a, b) => (sort === "diskMb" ? b.diskMb - a.diskMb : a[sort].localeCompare(b[sort])));
  });

  onMount(() => {
    const ac = new AbortController();
    listSites(ac.signal).then(
      (s) => (sites = s),
      (e) => !ac.signal.aborted && (error = String(e)),
    );
    return () => ac.abort();
  });
</script>

<SiteForm oncreated={(s) => (sites = [...sites, s])} />
<div class="card">
  <input placeholder="Filter" aria-label="Filter" bind:value={filter} />
  {#if error}<p class="err">{error}</p>{/if}
  <table>
    <thead>
      <tr>
        <th><button onclick={() => (sort = "domain")}>Domain</button></th>
        <th>User</th>
        <th><button onclick={() => (sort = "kind")}>Type</button></th>
        <th><button onclick={() => (sort = "diskMb")}>Disk</button></th>
      </tr>
    </thead>
    <tbody>
      {#each rows as s (s.domain)}
        {@const pct = Math.min(100, Math.round((s.diskMb / s.quotaMb) * 100))}
        <tr>
          <td>{s.domain}</td>
          <td>{s.user}</td>
          <td>{s.kind}{s.php ? ` ${s.php}` : ""}</td>
          <td>
            <div class={pct > 80 ? "bar warn" : "bar"} title="{s.diskMb} / {s.quotaMb} MB">
              <i style="width: {pct}%"></i>
            </div>
          </td>
        </tr>
      {/each}
    </tbody>
  </table>
</div>
