// SPDX-License-Identifier: AGPL-3.0-or-later

import { ArrowLeft, Copy, DownloadSimple, PencilSimple, Trash, UploadSimple } from "@phosphor-icons/react";
import { useCallback, useEffect, useState, type ChangeEvent, type FormEvent } from "react";
import {
  copyName,
  createProfile,
  deleteProfile,
  exportUrl,
  fromProfileForm,
  importProfile,
  listProfiles,
  ownProfileMessage,
  profileNameProblem,
  toProfileForm,
  updateProfile,
  type Profile,
  type ProfileChoices,
  type ProfileForm,
} from "./profiles";
import { formatHash } from "./routes";
import { Badge, Button, Card, Field, IconButton, IconLink, Notice, PageHead, RowActions, Table, type Column, type Sort } from "./ui";

type QueryProps = {
  /** The address query: q, sort, dir; copy=<name>, edit=<name> or import=1 for a form. */
  query?: URLSearchParams;
  /** Replaces the query in the address without a new history entry. */
  onQuery?: (q: URLSearchParams) => void;
};

const pageCacheLabels: Record<string, string> = {
  "": "None",
  wordpress: "WordPress",
  woocommerce: "WooCommerce",
  php: "Any PHP application (strict)",
};

const link = (q: Record<string, string>) => formatHash({ page: "profiles", query: new URLSearchParams(q) });

function rulesText(p: Profile): string {
  const h = p.hardening;
  const parts: string[] = [];
  if (h.configFiles) parts.push("dumps");
  const n = (h.noPhpDirs?.length ?? 0) + (h.denyDirs?.length ?? 0) + (h.denyFiles?.length ?? 0) + (h.limitFiles?.length ?? 0);
  if (n > 0) parts.push(`${String(n)} ${n === 1 ? "path" : "paths"}`);
  return parts.length > 0 ? parts.join(", ") : "none";
}

// Profiles is the page of the application profiles (ADR-0069): the
// built-in ones, which can be copied and exported, and the own ones,
// which can also be changed and deleted. Only admins see it.
export function Profiles({ query = new URLSearchParams(), onQuery = () => undefined }: QueryProps) {
  const [choices, setChoices] = useState<ProfileChoices | null>(null);
  const [loadError, setLoadError] = useState("");
  const [done, setDone] = useState("");
  const [deleting, setDeleting] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");

  const load = useCallback(async () => {
    try {
      setChoices(await listProfiles());
      setLoadError("");
    } catch (err) {
      setLoadError(ownProfileMessage(err));
    }
  }, []);

  useEffect(() => {
    void load();
  }, [load]);

  const set = (changes: Record<string, string>) => {
    const next = new URLSearchParams(query);
    for (const [k, v] of Object.entries(changes)) {
      if (v) next.set(k, v);
      else next.delete(k);
    }
    onQuery(next);
  };

  const changed = async (msg: string) => {
    setDone(msg);
    setDeleting(null);
    set({ copy: "", edit: "", import: "" });
    await load();
  };

  const listLink = (
    <a className="ui-btn" href={formatHash({ page: "profiles" })}>
      <ArrowLeft size={16} aria-hidden="true" />
      All profiles
    </a>
  );

  if (!choices) {
    return (
      <>
        <PageHead title="Application profiles" />
        {loadError ? <Notice tone="err" title={loadError} /> : <p aria-busy="true">Loading profiles…</p>}
      </>
    );
  }

  const profiles = choices.profiles;
  const names = profiles.map((p) => p.name);

  if (query.get("import") === "1") {
    return (
      <>
        <PageHead title="Import a profile" description="A profile exported from this or another Hostashi panel, in YAML with version: 1." actions={listLink} />
        <Card>
          <ImportForm onCancel={() => set({ import: "" })} onDone={(p) => changed(`Profile ${p.name} imported.`)} />
        </Card>
      </>
    );
  }

  const copyOf = query.get("copy");
  const editName = query.get("edit");
  if (copyOf !== null || editName !== null) {
    const source = profiles.find((p) => p.name === (editName ?? copyOf));
    if (!source || (editName !== null && source.builtin)) {
      return (
        <>
          <PageHead title="Application profiles" actions={listLink} />
          <Notice tone="warn" title={source ? `${source.title} is built in and cannot be changed. Copy it instead.` : `There is no profile named ${editName ?? copyOf ?? ""}.`} />
        </>
      );
    }
    const editing = editName !== null;
    const initial = editing ? source : { ...source, name: copyName(source.name, names), title: source.title + " (copy)" };
    const title = editing ? "Change profile " + source.name : "Copy " + source.title;
    return (
      <>
        <PageHead
          title={title}
          description={
            editing
              ? "Sites that use this profile get the new rules right away and keep their own changes. The web root folder only applies to new sites."
              : "A copy is your own profile: change its values, then pick it for new sites or on a site's Hardening tab."
          }
          actions={listLink}
        />
        <Card>
          <ProfileEditor
            key={(editName ?? "") + "|" + (copyOf ?? "")}
            label={title}
            initial={initial}
            editing={editing}
            taken={names}
            maxPaths={choices.maxPaths}
            onCancel={() => set({ copy: "", edit: "" })}
            onSubmit={async (p) => {
              if (editing) {
                await updateProfile(p);
                await changed(`Profile ${p.name} saved. Sites that use it got the new rules.`);
              } else {
                await createProfile(p);
                await changed(`Profile ${p.name} added.`);
              }
            }}
          />
        </Card>
      </>
    );
  }

  async function remove(name: string) {
    setBusy(true);
    setError("");
    setDone("");
    try {
      await deleteProfile(name);
      await changed(`Profile ${name} deleted.`);
    } catch (err) {
      setError(ownProfileMessage(err));
    } finally {
      setBusy(false);
    }
  }

  const columns: Column<Profile>[] = [
    {
      key: "name",
      header: "Profile",
      sortValue: (p) => p.title.toLowerCase(),
      cell: (p) => (
        <div className="ui-cell-main">
          <span className="ui-row ui-nowrap">
            <strong>{p.title}</strong>
            {p.builtin ? <Badge tone="off">Built-in</Badge> : <Badge tone="ok">Own</Badge>}
          </span>
          <span className="ui-hint">{p.name}</span>
        </div>
      ),
    },
    { key: "type", header: "Type", sortValue: (p) => p.type, cell: (p) => (p.type === "php" ? "PHP" : "Static") },
    { key: "php", header: "Needs PHP", sortValue: (p) => p.minPhp ?? "", cell: (p) => (p.type === "php" ? (p.minPhp ?? "any") : "–") },
    { key: "webDir", header: "Serves", sortValue: (p) => p.webDir ?? "", cell: (p) => <code>{p.webDir ? "htdocs/" + p.webDir : "htdocs"}</code> },
    { key: "rules", header: "Blocks", cell: rulesText },
    {
      key: "actions",
      header: "Actions",
      srHeader: true,
      cell: (p) => (
        <RowActions>
          <IconLink label={`Copy ${p.title}`} text="Copy" icon={<Copy size={16} />} href={link({ copy: p.name })} />
          {!p.builtin && <IconLink label={`Change ${p.title}`} icon={<PencilSimple size={16} />} href={link({ edit: p.name })} />}
          <IconLink label={`Export ${p.title} as YAML`} icon={<DownloadSimple size={16} />} href={exportUrl(p.name)} />
          {!p.builtin && (
            <IconButton
              label={`Delete ${p.title}`}
              icon={<Trash size={16} />}
              danger
              disabled={busy}
              onClick={() => {
                setError("");
                setDeleting(p.name);
              }}
            />
          )}
        </RowActions>
      ),
    },
  ];

  const q = (query.get("q") ?? "").trim().toLowerCase();
  const shown = profiles.filter((p) => !q || p.name.includes(q) || p.title.toLowerCase().includes(q));
  const sortKey = query.get("sort");
  const sort: Sort | undefined = sortKey ? { key: sortKey, dir: query.get("dir") === "desc" ? "desc" : "asc" } : undefined;

  return (
    <>
      <PageHead
        title="Application profiles"
        description="A profile brings a site's hardening rules and, for new sites, its web root folder. Copy a built-in profile to make your own."
        actions={
          <a className="ui-btn" href={link({ import: "1" })}>
            <UploadSimple size={16} aria-hidden="true" />
            Import YAML
          </a>
        }
      />
      {loadError && <Notice tone="err" title={loadError} />}
      {done && <Notice tone="ok" title={done} />}
      {deleting && (
        <section className="ui-card ui-card-body ui-stack" role="alertdialog" aria-labelledby="profile-delete-title">
          <h2 id="profile-delete-title" className="ui-flush">
            Delete the profile {deleting}?
          </h2>
          <p className="ui-flush">Only a profile that no site uses can be deleted.</p>
          {error && <Notice tone="err" title={error} />}
          <div className="ui-row">
            <Button variant="danger" disabled={busy} onClick={() => void remove(deleting)}>
              Delete profile
            </Button>
            <Button disabled={busy} onClick={() => setDeleting(null)}>
              Cancel
            </Button>
          </div>
        </section>
      )}
      {error && !deleting && <Notice tone="err" title={error} />}
      <Card padded={false}>
        <div className="ui-filters">
          <input
            className="ui-input"
            type="search"
            aria-label="Filter profiles"
            placeholder="Filter by name"
            value={query.get("q") ?? ""}
            onChange={(e) => set({ q: e.target.value })}
          />
        </div>
        <Table
          label="Application profiles"
          columns={columns}
          rows={shown}
          rowKey={(p) => p.name}
          sort={sort}
          onSort={(x) => set({ sort: x.key, dir: x.dir })}
          empty="No profile matches the filter."
        />
      </Card>
    </>
  );
}

function ProfileEditor({
  label,
  initial,
  editing,
  taken,
  maxPaths,
  onSubmit,
  onCancel,
}: {
  label: string;
  initial: Profile;
  editing: boolean;
  taken: string[];
  maxPaths: number;
  onSubmit: (p: Omit<Profile, "builtin">) => Promise<void>;
  onCancel: () => void;
}) {
  const [f, setF] = useState<ProfileForm>(toProfileForm(initial));
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const php = f.type === "php";
  const field = (k: keyof ProfileForm) => (e: ChangeEvent<HTMLInputElement | HTMLTextAreaElement | HTMLSelectElement>) =>
    setF({ ...f, [k]: e.target.value });

  async function submit(e: FormEvent) {
    e.preventDefault();
    const problem = editing ? "" : profileNameProblem(f.name.trim(), taken);
    const p = problem || fromProfileForm(f, initial.cron);
    if (typeof p === "string") {
      setError(p);
      return;
    }
    setBusy(true);
    setError("");
    try {
      await onSubmit(p);
    } catch (err) {
      setError(ownProfileMessage(err));
    } finally {
      setBusy(false);
    }
  }

  const pathHint = `One per line, relative to the document root. At most ${String(maxPaths)}.`;

  return (
    <form className="ui-stack" onSubmit={submit} noValidate aria-label={label}>
      <div className="ui-form-grid">
        <Field label="Name" hint={editing ? "The name cannot change; copy the profile for a new one." : "Lower-case letters, digits and dashes"}>
          <input className="ui-input" value={f.name} onChange={field("name")} disabled={editing} autoComplete="off" spellCheck={false} />
        </Field>
        <Field label="Title" hint="Shown in the profile lists">
          <input className="ui-input" value={f.title} onChange={field("title")} autoComplete="off" />
        </Field>
        <Field label="Site type" hint={editing ? "Cannot change while sites use the profile." : "Static profiles are offered for static sites."}>
          <select className="ui-select" value={f.type} onChange={field("type")}>
            <option value="php">PHP</option>
            <option value="static">Static</option>
          </select>
        </Field>
        {php && (
          <Field label="Lowest PHP version" hint="Such as 8.2; empty for any">
            <input className="ui-input" value={f.minPhp} onChange={field("minPhp")} inputMode="decimal" autoComplete="off" />
          </Field>
        )}
        <Field label="Web root folder" hint="Below htdocs, such as public; empty for htdocs. Only for new sites.">
          <input className="ui-input" value={f.webDir} onChange={field("webDir")} autoComplete="off" spellCheck={false} />
        </Field>
        {php && (
          <Field label="Page cache preset" hint="Offered when the page cache is turned on">
            <select className="ui-select" value={f.pageCache} onChange={field("pageCache")}>
              {Object.entries(pageCacheLabels).map(([v, l]) => (
                <option key={v} value={v}>
                  {l}
                </option>
              ))}
            </select>
          </Field>
        )}
      </div>
      <fieldset className="ui-fieldset ui-stack">
        <legend>Hardening rules</legend>
        <label className="ui-check">
          <input type="checkbox" checked={f.configFiles} onChange={(e) => setF({ ...f, configFiles: e.target.checked })} />
          Block database dumps, backups, logs and editor copies
        </label>
        <div className="ui-form-grid">
          <Field label="Blocked folders" hint={pathHint}>
            <textarea className="ui-input" rows={3} value={f.denyDirs} onChange={field("denyDirs")} spellCheck={false} />
          </Field>
          <Field label="Blocked files" hint={pathHint}>
            <textarea className="ui-input" rows={3} value={f.denyFiles} onChange={field("denyFiles")} spellCheck={false} />
          </Field>
          {php && (
            <Field label="Folders without PHP" hint={pathHint}>
              <textarea className="ui-input" rows={3} value={f.noPhpDirs} onChange={field("noPhpDirs")} spellCheck={false} />
            </Field>
          )}
          {php && (
            <Field label="Rate limited PHP files" hint={pathHint}>
              <textarea className="ui-input" rows={3} value={f.limitFiles} onChange={field("limitFiles")} spellCheck={false} />
            </Field>
          )}
        </div>
      </fieldset>
      <div className="ui-form-grid">
        <Field label="Suggested cron jobs" hint="One per line: schedule, then command, such as */5 * * * * php cron.php. Shown, never created.">
          <textarea className="ui-input" rows={3} value={f.cron} onChange={field("cron")} spellCheck={false} />
        </Field>
        <Field label="Checklist" hint="One note per line, shown after a site is created">
          <textarea className="ui-input" rows={3} value={f.checklist} onChange={field("checklist")} />
        </Field>
      </div>
      <div className="ui-row">
        <Button variant="primary" type="submit" disabled={busy}>
          {editing ? "Save profile" : "Add profile"}
        </Button>
        <Button disabled={busy} onClick={onCancel}>
          Cancel
        </Button>
      </div>
      {error && <Notice tone="err" title={error} />}
    </form>
  );
}

function ImportForm({ onDone, onCancel }: { onDone: (p: Profile) => Promise<void>; onCancel: () => void }) {
  const [text, setText] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");

  async function pick(e: ChangeEvent<HTMLInputElement>) {
    const file = e.target.files?.[0];
    if (!file) return;
    try {
      setText(await file.text());
      setError("");
    } catch {
      setError("The file could not be read.");
    }
  }

  async function submit(e: FormEvent) {
    e.preventDefault();
    if (text.trim() === "") {
      setError("Paste the YAML or choose a file.");
      return;
    }
    setBusy(true);
    setError("");
    try {
      await onDone(await importProfile(text));
    } catch (err) {
      setError(ownProfileMessage(err));
    } finally {
      setBusy(false);
    }
  }

  return (
    <form className="ui-stack" onSubmit={submit} noValidate aria-label="Import a profile">
      <Field label="YAML file" hint="A .yaml file from Export">
        <input type="file" accept=".yaml,.yml,application/yaml,text/yaml" onChange={(e) => void pick(e)} />
      </Field>
      <Field label="YAML" hint="Or paste it here. The name must not be taken; edit it here to import a profile twice.">
        <textarea className="ui-input ui-mono" rows={14} value={text} onChange={(e) => setText(e.target.value)} spellCheck={false} />
      </Field>
      <div className="ui-row">
        <Button variant="primary" type="submit" disabled={busy}>
          Import profile
        </Button>
        <Button disabled={busy} onClick={onCancel}>
          Cancel
        </Button>
      </div>
      {error && <Notice tone="err" title={error} />}
    </form>
  );
}
