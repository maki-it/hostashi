<script lang="ts">
  import { onMount } from "svelte";
  import { openTerminal } from "../../shared/terminal";

  let el: HTMLDivElement;

  onMount(() => {
    let close: (() => void) | undefined;
    let cancelled = false;
    openTerminal(el).then((c) => (cancelled ? c() : (close = c)));
    return () => {
      cancelled = true;
      close?.();
    };
  });
</script>

<div class="card"><div class="term" bind:this={el} data-testid="terminal"></div></div>
