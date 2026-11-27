// SPDX-License-Identifier: AGPL-3.0-or-later

import { ArrowLeft, Copy, DownloadSimple, PencilSimple, Trash, UploadSimple } from "@phosphor-icons/react";
import { useCallback, useEffect, useState, type ChangeEvent, type FormEvent } from "react";
import { useTranslation } from "react-i18next";
import i18n from "./i18n";
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

// The page cache presets (ADR-0046); the application names stay as they are.
const pageCacheLabels = (): Record<string, string> => ({
  "": i18n.t("profiles.pageCache.none"),
  wordpress: "WordPress",
  woocommerce: "WooCommerce",
  php: i18n.t("profiles.pageCache.php"),
});

// The folder of a site's files, a path in every language.
const docRoot = "htdocs";

const link = (q: Record<string, string>) => formatHash({ page: "profiles", query: new URLSearchParams(q) });

function rulesText(p: Profile): string {
  const h = p.hardening;
  const parts: string[] = [];
  if (h.configFiles) parts.push(i18n.t("profiles.rules.dumps"));
  const n = (h.noPhpDirs?.length ?? 0) + (h.denyDirs?.length ?? 0) + (h.denyFiles?.length ?? 0) + (h.limitFiles?.length ?? 0);
  if (n > 0) parts.push(i18n.t("profiles.rules.paths", { count: n }));
  return parts.length > 0 ? parts.join(", ") : i18n.t("profiles.rules.none");
}

// Profiles is the page of the application profiles (ADR-0069): the
// built-in ones, which can be copied and exported, and the own ones,
// which can also be changed and deleted. Only admins see it.
export function Profiles({ query = new URLSearchParams(), onQuery = () => undefined }: QueryProps) {
  const { t } = useTranslation();
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
      {t("profiles.allLink")}
    </a>
  );

  if (!choices) {
    return (
      <>
        <PageHead title={t("profiles.title")} />
        {loadError ? <Notice tone="err" title={loadError} /> : <p aria-busy="true">{t("profiles.loading")}</p>}
      </>
    );
  }

  const profiles = choices.profiles;
  const names = profiles.map((p) => p.name);

  if (query.get("import") === "1") {
    return (
      <>
        <PageHead title={t("profiles.import.title")} description={t("profiles.import.description")} actions={listLink} />
        <Card>
          <ImportForm onCancel={() => set({ import: "" })} onDone={(p) => changed(t("profiles.imported", { name: p.name }))} />
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
          <PageHead title={t("profiles.title")} actions={listLink} />
          <Notice
            tone="warn"
            title={source ? t("profiles.builtinNoChange", { title: source.title }) : t("profiles.noSuch", { name: editName ?? copyOf ?? "" })}
          />
        </>
      );
    }
    const editing = editName !== null;
    const initial = editing ? source : { ...source, name: copyName(source.name, names), title: t("profiles.copyTitle", { title: source.title }) };
    const title = editing ? t("profiles.change", { name: source.name }) : t("profiles.copy", { title: source.title });
    return (
      <>
        <PageHead
          title={title}
          description={
            editing ? t("profiles.changeDescription") : t("profiles.copyDescription")
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
                await changed(t("profiles.saved", { name: p.name }));
              } else {
                await createProfile(p);
                await changed(t("profiles.added", { name: p.name }));
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
      await changed(t("profiles.deleted", { name }));
    } catch (err) {
      setError(ownProfileMessage(err));
    } finally {
      setBusy(false);
    }
  }

  const columns: Column<Profile>[] = [
    {
      key: "name",
      header: t("profiles.columns.name"),
      sortValue: (p) => p.title.toLowerCase(),
      cell: (p) => (
        <div className="ui-cell-main">
          <span className="ui-row ui-nowrap">
            <strong>{p.title}</strong>
            {p.builtin ? <Badge tone="off">{t("profiles.builtin")}</Badge> : <Badge tone="ok">{t("profiles.own")}</Badge>}
          </span>
          <span className="ui-hint">{p.name}</span>
        </div>
      ),
    },
    { key: "type", header: t("profiles.columns.type"), sortValue: (p) => p.type, cell: (p) => t(`profiles.types.${p.type}`) },
    {
      key: "php",
      header: t("profiles.columns.php"),
      sortValue: (p) => p.minPhp ?? "",
      cell: (p) => (p.type === "php" ? (p.minPhp ?? t("profiles.anyPhp")) : "–"),
    },
    { key: "webDir", header: t("profiles.columns.webDir"), sortValue: (p) => p.webDir ?? "", cell: (p) => <code>{p.webDir ? `${docRoot}/${p.webDir}` : docRoot}</code> },
    { key: "rules", header: t("profiles.columns.rules"), cell: rulesText },
    {
      key: "actions",
      header: t("profiles.columns.actions"),
      srHeader: true,
      cell: (p) => (
        <RowActions>
          <IconLink label={t("profiles.actions.copyLabel", { title: p.title })} text={t("profiles.actions.copy")} icon={<Copy size={16} />} href={link({ copy: p.name })} />
          {!p.builtin && <IconLink label={t("profiles.actions.changeLabel", { title: p.title })} icon={<PencilSimple size={16} />} href={link({ edit: p.name })} />}
          <IconLink label={t("profiles.actions.exportLabel", { title: p.title })} icon={<DownloadSimple size={16} />} href={exportUrl(p.name)} />
          {!p.builtin && (
            <IconButton
              label={t("profiles.actions.deleteLabel", { title: p.title })}
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
        title={t("profiles.title")}
        description={t("profiles.description")}
        actions={
          <a className="ui-btn" href={link({ import: "1" })}>
            <UploadSimple size={16} aria-hidden="true" />
            {t("profiles.import.button")}
          </a>
        }
      />
      {loadError && <Notice tone="err" title={loadError} />}
      {done && <Notice tone="ok" title={done} />}
      {deleting && (
        <section className="ui-card ui-card-body ui-stack" role="alertdialog" aria-labelledby="profile-delete-title">
          <h2 id="profile-delete-title" className="ui-flush">
            {t("profiles.delete.title", { name: deleting })}
          </h2>
          <p className="ui-flush">{t("profiles.delete.text")}</p>
          {error && <Notice tone="err" title={error} />}
          <div className="ui-row">
            <Button variant="danger" disabled={busy} onClick={() => void remove(deleting)}>
              {t("profiles.delete.confirm")}
            </Button>
            <Button disabled={busy} onClick={() => setDeleting(null)}>
              {t("common.actions.cancel")}
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
            aria-label={t("profiles.filter.label")}
            placeholder={t("profiles.filter.placeholder")}
            value={query.get("q") ?? ""}
            onChange={(e) => set({ q: e.target.value })}
          />
        </div>
        <Table
          label={t("profiles.table.label")}
          columns={columns}
          rows={shown}
          rowKey={(p) => p.name}
          sort={sort}
          onSort={(x) => set({ sort: x.key, dir: x.dir })}
          empty={t("profiles.table.noMatch")}
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
  const { t } = useTranslation();
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

  const pathHint = t("profiles.editor.pathHint", { max: maxPaths });

  return (
    <form className="ui-stack" onSubmit={submit} noValidate aria-label={label}>
      <div className="ui-form-grid">
        <Field label={t("profiles.editor.name")} hint={editing ? t("profiles.editor.nameHintEdit") : t("profiles.editor.nameHint")}>
          <input className="ui-input" value={f.name} onChange={field("name")} disabled={editing} autoComplete="off" spellCheck={false} />
        </Field>
        <Field label={t("profiles.editor.title")} hint={t("profiles.editor.titleHint")}>
          <input className="ui-input" value={f.title} onChange={field("title")} autoComplete="off" />
        </Field>
        <Field label={t("profiles.editor.type")} hint={editing ? t("profiles.editor.typeHintEdit") : t("profiles.editor.typeHint")}>
          <select className="ui-select" value={f.type} onChange={field("type")}>
            <option value="php">{t("profiles.types.php")}</option>
            <option value="static">{t("profiles.types.static")}</option>
          </select>
        </Field>
        {php && (
          <Field label={t("profiles.editor.minPhp")} hint={t("profiles.editor.minPhpHint")}>
            <input className="ui-input" value={f.minPhp} onChange={field("minPhp")} inputMode="decimal" autoComplete="off" />
          </Field>
        )}
        <Field label={t("profiles.editor.webDir")} hint={t("profiles.editor.webDirHint")}>
          <input className="ui-input" value={f.webDir} onChange={field("webDir")} autoComplete="off" spellCheck={false} />
        </Field>
        {php && (
          <Field label={t("profiles.editor.pageCache")} hint={t("profiles.editor.pageCacheHint")}>
            <select className="ui-select" value={f.pageCache} onChange={field("pageCache")}>
              {Object.entries(pageCacheLabels()).map(([v, l]) => (
                <option key={v} value={v}>
                  {l}
                </option>
              ))}
            </select>
          </Field>
        )}
      </div>
      <fieldset className="ui-fieldset ui-stack">
        <legend>{t("profiles.editor.hardening")}</legend>
        <label className="ui-check">
          <input type="checkbox" checked={f.configFiles} onChange={(e) => setF({ ...f, configFiles: e.target.checked })} />
          {t("profiles.editor.configFiles")}
        </label>
        <div className="ui-form-grid">
          <Field label={t("profiles.editor.denyDirs")} hint={pathHint}>
            <textarea className="ui-input" rows={3} value={f.denyDirs} onChange={field("denyDirs")} spellCheck={false} />
          </Field>
          <Field label={t("profiles.editor.denyFiles")} hint={pathHint}>
            <textarea className="ui-input" rows={3} value={f.denyFiles} onChange={field("denyFiles")} spellCheck={false} />
          </Field>
          {php && (
            <Field label={t("profiles.editor.noPhpDirs")} hint={pathHint}>
              <textarea className="ui-input" rows={3} value={f.noPhpDirs} onChange={field("noPhpDirs")} spellCheck={false} />
            </Field>
          )}
          {php && (
            <Field label={t("profiles.editor.limitFiles")} hint={pathHint}>
              <textarea className="ui-input" rows={3} value={f.limitFiles} onChange={field("limitFiles")} spellCheck={false} />
            </Field>
          )}
        </div>
      </fieldset>
      <div className="ui-form-grid">
        <Field label={t("profiles.editor.cron")} hint={t("profiles.editor.cronHint")}>
          <textarea className="ui-input" rows={3} value={f.cron} onChange={field("cron")} spellCheck={false} />
        </Field>
        <Field label={t("profiles.editor.checklist")} hint={t("profiles.editor.checklistHint")}>
          <textarea className="ui-input" rows={3} value={f.checklist} onChange={field("checklist")} />
        </Field>
      </div>
      <div className="ui-row">
        <Button variant="primary" type="submit" disabled={busy}>
          {editing ? t("profiles.editor.save") : t("profiles.editor.add")}
        </Button>
        <Button disabled={busy} onClick={onCancel}>
          {t("common.actions.cancel")}
        </Button>
      </div>
      {error && <Notice tone="err" title={error} />}
    </form>
  );
}

function ImportForm({ onDone, onCancel }: { onDone: (p: Profile) => Promise<void>; onCancel: () => void }) {
  const { t } = useTranslation();
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
      setError(t("profiles.import.readFailed"));
    }
  }

  async function submit(e: FormEvent) {
    e.preventDefault();
    if (text.trim() === "") {
      setError(t("profiles.import.empty"));
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
    <form className="ui-stack" onSubmit={submit} noValidate aria-label={t("profiles.import.title")}>
      <Field label={t("profiles.import.file")} hint={t("profiles.import.fileHint")}>
        <input type="file" accept=".yaml,.yml,application/yaml,text/yaml" onChange={(e) => void pick(e)} />
      </Field>
      <Field label={t("profiles.import.yaml")} hint={t("profiles.import.yamlHint")}>
        <textarea className="ui-input ui-mono" rows={14} value={text} onChange={(e) => setText(e.target.value)} spellCheck={false} />
      </Field>
      <div className="ui-row">
        <Button variant="primary" type="submit" disabled={busy}>
          {t("profiles.import.submit")}
        </Button>
        <Button disabled={busy} onClick={onCancel}>
          {t("common.actions.cancel")}
        </Button>
      </div>
      {error && <Notice tone="err" title={error} />}
    </form>
  );
}
