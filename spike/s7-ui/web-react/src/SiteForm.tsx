import { useState, type FormEvent } from "react";
import { createSite, validate, type FieldErrors, type NewSite, type Site, type SiteKind } from "../../shared/api";

export function SiteForm({ onCreated }: { onCreated: (s: Site) => void }) {
  const [v, setV] = useState<NewSite>({ domain: "", user: "", kind: "php" });
  const [errors, setErrors] = useState<FieldErrors>({});
  const [busy, setBusy] = useState(false);

  async function submit(e: FormEvent) {
    e.preventDefault();
    const local = validate(v);
    setErrors(local);
    if (Object.keys(local).length) return;
    setBusy(true);
    try {
      const r = await createSite(v);
      if (r.ok) {
        onCreated(r.site);
        setV({ domain: "", user: "", kind: "php" });
      } else {
        setErrors(r.errors);
      }
    } finally {
      setBusy(false);
    }
  }

  return (
    <form className="card" onSubmit={submit} noValidate>
      <label>
        Domain
        <input name="domain" value={v.domain} onChange={(e) => setV({ ...v, domain: e.target.value })} />
        <span className="err">{errors.domain}</span>
      </label>
      <label>
        User
        <input name="user" value={v.user} onChange={(e) => setV({ ...v, user: e.target.value })} />
        <span className="err">{errors.user}</span>
      </label>
      <label>
        Type
        <select name="kind" value={v.kind} onChange={(e) => setV({ ...v, kind: e.target.value as SiteKind })}>
          <option value="php">PHP</option><option value="static">Static</option>
          <option value="node">Node.js</option><option value="proxy">Proxy</option>
        </select>
      </label>
      <button className="primary" disabled={busy}>Create site</button>
    </form>
  );
}
