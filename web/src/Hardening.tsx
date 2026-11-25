// SPDX-License-Identifier: AGPL-3.0-or-later

import { useCallback, useEffect, useState, type FormEvent } from "react";
import {
  fromHardeningForm,
  getSiteProfile,
  profileMessage,
  setSiteProfile,
  toHardeningForm,
  type HardeningForm,
  type SiteProfileView,
} from "./profiles";
import type { Site } from "./sites";
import { Badge, Button, Card, Field, Notice } from "./ui";

// HardeningCard chooses a site's application profile and its hardening
// rules (ADR-0069, ADR-0017 Part 4). Only what differs from the profile is
// stored, so later changes of the profile reach the rules the site kept.
export function HardeningCard({ site }: { site: Site }) {
  const [view, setView] = useState<SiteProfileView | null>(null);
  const [profile, setProfile] = useState("");
  const [form, setForm] = useState<HardeningForm | null>(null);
  const [loadError, setLoadError] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [done, setDone] = useState("");

  const show = useCallback((v: SiteProfileView) => {
    setView(v);
    setProfile(v.profile);
    setForm(toHardeningForm(v.hardening));
  }, []);

  const load = useCallback(async () => {
    try {
      show(await getSiteProfile(site.name));
      setLoadError("");
    } catch (err) {
      setLoadError(profileMessage(err));
    }
  }, [site.name, show]);

  useEffect(() => {
    void load();
  }, [load]);

  const title = "Application profile and hardening";
  if (loadError) {
    return (
      <Card title={title}>
        <Notice tone="err" title={loadError} />
      </Card>
    );
  }
  if (!view || !form) {
    return (
      <Card title={title}>
        <p aria-busy="true">Loading…</p>
      </Card>
    );
  }

  const php = site.php !== "";
  const choices = view.choices;
  const chosen = choices.profiles.find((p) => p.name === profile);
  const set = (next: Partial<HardeningForm>) => setForm({ ...form, ...next });

  // A new profile brings its own rules into the form; saving keeps them.
  function choose(name: string) {
    setProfile(name);
    const p = choices.profiles.find((c) => c.name === name);
    if (p) setForm(toHardeningForm(p.hardening));
    setDone("");
  }

  async function save(useProfile: boolean) {
    setError("");
    setDone("");
    setBusy(true);
    try {
      const v = await setSiteProfile(site.name, profile, useProfile ? undefined : fromHardeningForm(form!, php));
      show(v);
      setDone(useProfile ? "Saved. The site follows the profile's rules again." : "Saved. nginx uses the new rules now.");
    } catch (err) {
      setError(profileMessage(err));
    } finally {
      setBusy(false);
    }
  }

  function submit(e: FormEvent) {
    e.preventDefault();
    void save(false);
  }

  const pathHint = `One per line, relative to the document root, such as wp-content/uploads. At most ${choices.maxPaths}.`;

  return (
    <Card title={title}>
      <form className="ui-stack" onSubmit={submit} noValidate>
        <p className="ui-hint">
          nginx answers these requests with 404 before the site's files or PHP see them. Hidden files and folders such as <code>.git</code> or{" "}
          <code>.env</code> are always blocked, except <code>/.well-known</code>.
        </p>
        <Field
          label="Profile"
          hint={
            chosen?.minPhp
              ? `Needs PHP ${chosen.minPhp} or newer.${chosen.webDir ? ` New sites serve the ${chosen.webDir} folder.` : ""}`
              : "The rules the site starts from. Changing it never touches the site's files or document root."
          }
        >
          <select className="ui-select" value={profile} disabled={busy} onChange={(e) => choose(e.target.value)}>
            {choices.profiles.map((p) => (
              <option key={p.name} value={p.name}>
                {p.title}
              </option>
            ))}
          </select>
        </Field>
        {chosen?.checklist && chosen.checklist.length > 0 && (
          <ul className="ui-hint">
            {chosen.checklist.map((c) => (
              <li key={c}>{c}</li>
            ))}
          </ul>
        )}
        <fieldset className="ui-fieldset ui-stack" disabled={busy}>
          <legend>
            Rules {view.changed && profile === view.profile && <Badge tone="warn">Changed from the profile</Badge>}
          </legend>
          <label className="ui-check">
            <input type="checkbox" checked={form.configFiles} onChange={(e) => set({ configFiles: e.target.checked })} />
            Block database dumps, backups, logs and editor copies
          </label>
          <p className="ui-hint">
            Files ending in{" "}
            {choices.configFileSuffixes.map((s, i) => (
              <span key={s}>
                {i > 0 && " "}
                <code>{s}</code>
              </span>
            ))}
            , anywhere below the document root.
          </p>
          <div className="ui-form-grid">
            <Field label="Blocked folders" hint={pathHint + " Everything in them."}>
              <textarea className="ui-input ui-textarea ui-mono" rows={3} spellCheck={false} value={form.denyDirs} onChange={(e) => set({ denyDirs: e.target.value })} />
            </Field>
            <Field label="Blocked files" hint="One per line, such as xmlrpc.php.">
              <textarea className="ui-input ui-textarea ui-mono" rows={3} spellCheck={false} value={form.denyFiles} onChange={(e) => set({ denyFiles: e.target.value })} />
            </Field>
            {php && (
              <>
                <Field label="Folders without PHP" hint="A PHP file uploaded there never runs. One per line, such as wp-content/uploads.">
                  <textarea className="ui-input ui-textarea ui-mono" rows={3} spellCheck={false} value={form.noPhpDirs} onChange={(e) => set({ noPhpDirs: e.target.value })} />
                </Field>
                <Field
                  label="Rate limited PHP files"
                  hint={`At most ${choices.limitPerMinute} requests per minute per address, ${choices.limitBurst} more at once; then 429. One per line, such as wp-login.php.`}
                >
                  <textarea className="ui-input ui-textarea ui-mono" rows={3} spellCheck={false} value={form.limitFiles} onChange={(e) => set({ limitFiles: e.target.value })} />
                </Field>
              </>
            )}
          </div>
        </fieldset>
        {error && <Notice tone="err" title={error} />}
        {done && <Notice tone="ok" title={done} />}
        <div className="ui-row">
          <Button type="submit" variant="primary" disabled={busy}>
            Save profile and rules
          </Button>
          <Button disabled={busy} onClick={() => void save(true)}>
            Use the profile's rules
          </Button>
        </div>
      </form>
    </Card>
  );
}
