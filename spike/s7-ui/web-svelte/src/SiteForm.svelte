<script lang="ts">
  import { createSite, validate, type FieldErrors, type NewSite, type Site } from "../../shared/api";

  let { oncreated }: { oncreated: (s: Site) => void } = $props();

  let v = $state<NewSite>({ domain: "", user: "", kind: "php" });
  let errors = $state<FieldErrors>({});
  let busy = $state(false);

  async function submit(e: SubmitEvent) {
    e.preventDefault();
    errors = validate(v);
    if (Object.keys(errors).length) return;
    busy = true;
    try {
      const r = await createSite(v);
      if (r.ok) {
        oncreated(r.site);
        v = { domain: "", user: "", kind: "php" };
      } else {
        errors = r.errors;
      }
    } finally {
      busy = false;
    }
  }
</script>

<form class="card" onsubmit={submit} novalidate>
  <label>
    Domain
    <input name="domain" bind:value={v.domain} />
    <span class="err">{errors.domain ?? ""}</span>
  </label>
  <label>
    User
    <input name="user" bind:value={v.user} />
    <span class="err">{errors.user ?? ""}</span>
  </label>
  <label>
    Type
    <select name="kind" bind:value={v.kind}>
      <option value="php">PHP</option><option value="static">Static</option>
      <option value="node">Node.js</option><option value="proxy">Proxy</option>
    </select>
  </label>
  <button class="primary" disabled={busy}>Create site</button>
</form>
