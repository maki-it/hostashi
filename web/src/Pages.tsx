// SPDX-License-Identifier: AGPL-3.0-or-later

import { ArrowLeft, Eye, GearSix, Plus, Trash } from "@phosphor-icons/react";
import { useCallback, useEffect, useState, type FormEvent, type ReactNode } from "react";
import { useTranslation } from "react-i18next";
import i18n from "./i18n";
import {
  createPage,
  deletePage,
  fileProblem,
  formatSize,
  kindLabel,
  listPages,
  pageMessage,
  pageNameProblem,
  openPreview,
  updatePage,
  type PageKind,
  type StatusPage,
} from "./pages";
import { formatHash } from "./routes";
import { Badge, Button, Card, Field, IconButton, IconLink, Notice, PageHead, RelTime, RowActions, Table, type Column, type Sort } from "./ui";

type QueryProps = {
  /** The address query: q, kind, sort, dir; new=1 or page=<id> for one view. */
  query?: URLSearchParams;
  /** Replaces the query in the address without a new history entry. */
  onQuery?: (q: URLSearchParams) => void;
};

const pagesHash = (q?: Record<string, string>) => formatHash({ page: "pages", ...(q ? { query: new URLSearchParams(q) } : {}) });
const usersOf = (p: StatusPage) => [...p.sites, ...p.parked];
const sizeOf = (p: StatusPage) => p.files.reduce((n, f) => n + f.size, 0);
const nobody = (p: StatusPage) => (p.kind === "parking" ? i18n.t("pages.noDomain") : i18n.t("pages.noSite"));

// Pages lists the maintenance and parking pages and uploads new ones. Sites
// choose their maintenance page in their settings.
export function Pages({ query = new URLSearchParams(), onQuery = () => undefined }: QueryProps) {
  const { t } = useTranslation();
  const [pages, setPages] = useState<StatusPage[] | null>(null);
  const [loadError, setLoadError] = useState("");
  const [done, setDone] = useState("");
  const [deleting, setDeleting] = useState<StatusPage | null>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");

  const load = useCallback(async () => {
    try {
      setPages(await listPages());
      setLoadError("");
    } catch (err) {
      setLoadError(pageMessage(err));
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
    set({ new: "", page: "" });
    await load();
  };

  const remove = async (p: StatusPage) => {
    setBusy(true);
    setError("");
    setDone("");
    try {
      await deletePage(p.id);
      setDeleting(null);
      await changed(t("pages.delete.done", { name: p.name }));
    } catch (err) {
      setError(pageMessage(err));
    } finally {
      setBusy(false);
    }
  };

  const listLink = (
    <a className="ui-btn" href={pagesHash()}>
      <ArrowLeft size={16} aria-hidden="true" />
      {t("pages.allLink")}
    </a>
  );

  if (!pages) {
    return (
      <>
        <PageHead title={t("pages.title")} />
        {loadError ? (
          <Notice tone="err" title={loadError}>
            <Button onClick={() => void load()}>{t("common.actions.tryAgain")}</Button>
          </Notice>
        ) : (
          <p aria-busy="true">{t("common.status.loading")}</p>
        )}
      </>
    );
  }

  const deleteDialog =
    deleting &&
    (() => {
      const users = usersOf(deleting);
      return (
        <section className="ui-card ui-card-body ui-stack" role="alertdialog" aria-labelledby="page-delete-title">
          <h2 id="page-delete-title" className="ui-flush">
            {t("pages.delete.title", { name: deleting.name })}
          </h2>
          <p className="ui-flush">{users.length > 0 ? t("pages.delete.inUse", { users: users.join(", ") }) : t("pages.delete.filesRemoved")}</p>
          {error && <Notice tone="err" title={error} />}
          <div className="ui-row">
            <Button variant="danger" disabled={busy || users.length > 0} onClick={() => void remove(deleting)}>
              {t("pages.delete.confirm", { name: deleting.name })}
            </Button>
            <Button disabled={busy} onClick={() => setDeleting(null)}>
              {t("common.actions.cancel")}
            </Button>
          </div>
        </section>
      );
    })();

  if (query.get("new") === "1") {
    return (
      <>
        <PageHead title={t("pages.upload.title")} description={t("pages.upload.description")} actions={listLink} />
        <Card>
          <UploadForm onCancel={() => set({ new: "" })} onUploaded={changed} />
        </Card>
      </>
    );
  }

  const id = query.get("page");
  if (id !== null) {
    const p = pages.find((x) => x.id === id);
    if (!p) {
      return (
        <>
          <PageHead title={t("pages.title")} actions={listLink} />
          <Notice tone="warn" title={t("pages.gone")} />
        </>
      );
    }
    return (
      <PageView
        key={p.id + (p.updatedAt ?? "")}
        page={p}
        back={listLink}
        deleteDialog={deleteDialog}
        onDelete={() => {
          setError("");
          setDeleting(p);
        }}
        onChanged={async (msg) => {
          setDone(msg);
          await load();
        }}
        done={done}
      />
    );
  }

  const columns: Column<StatusPage>[] = [
    {
      key: "name",
      header: t("pages.columns.name"),
      sortValue: (p) => p.name,
      cell: (p) => (
        <span className="ui-row ui-nowrap">
          <span className="ui-cell-main">
            <a href={pagesHash({ page: p.id })}>{p.name}</a>
          </span>
          {p.builtin && <Badge tone="off">{t("pages.builtin")}</Badge>}
        </span>
      ),
    },
    { key: "kind", header: t("pages.columns.kind"), sortValue: (p) => p.kind, cell: (p) => kindLabel(p.kind) },
    {
      key: "users",
      header: t("pages.columns.users"),
      sortValue: (p) => usersOf(p).length,
      cell: (p) => {
        const users = usersOf(p);
        return users.length > 0 ? users.join(", ") : <span className="ui-hint">{nobody(p)}</span>;
      },
    },
    {
      key: "files",
      header: t("pages.columns.files"),
      sortValue: sizeOf,
      cell: (p) => (
        <span className="ui-nowrap">
          {p.files.length} · {formatSize(sizeOf(p))}
        </span>
      ),
    },
    {
      key: "updated",
      header: t("pages.columns.updated"),
      sortValue: (p) => p.updatedAt ?? "",
      cell: (p) => (p.updatedAt ? <RelTime iso={p.updatedAt} /> : <span className="ui-hint">{t("pages.builtinChanged")}</span>),
    },
    {
      key: "actions",
      header: t("pages.columns.actions"),
      srHeader: true,
      cell: (p) => (
        <RowActions>
          <IconButton label={t("pages.actions.previewLabel", { name: p.name })} text={t("pages.actions.preview")} icon={<Eye size={16} />} onClick={() => void openPreview(p.id).then(setError)} />
          <IconLink label={t("pages.actions.manageLabel", { name: p.name })} text={t("pages.actions.manage")} icon={<GearSix size={16} />} href={pagesHash({ page: p.id })} />
          {!p.builtin && (
            <IconButton
              label={t("pages.actions.deleteLabel", { name: p.name })}
              text={t("pages.actions.delete")} icon={<Trash size={16} />}
              danger
              disabled={busy}
              onClick={() => {
                setError("");
                setDeleting(p);
              }}
            />
          )}
        </RowActions>
      ),
    },
  ];

  const q = (query.get("q") ?? "").trim().toLowerCase();
  const kind = query.get("kind") ?? "";
  const shown = pages.filter((p) => (!q || p.name.toLowerCase().includes(q)) && (!kind || p.kind === kind));
  const sortKey = query.get("sort");
  const sort: Sort | undefined = sortKey ? { key: sortKey, dir: query.get("dir") === "desc" ? "desc" : "asc" } : undefined;

  return (
    <>
      <PageHead
        title={t("pages.title")}
        description={t("pages.description")}
        actions={
          <a className="ui-btn ui-btn-primary" href={pagesHash({ new: "1" })}>
            <Plus size={16} aria-hidden="true" />
            {t("pages.upload.button")}
          </a>
        }
      />
      {loadError && <Notice tone="err" title={loadError} />}
      {done && <Notice tone="ok" title={done} />}
      {deleteDialog}
      <Card padded={false}>
        <div className="ui-filters">
          <input
            className="ui-input"
            type="search"
            aria-label={t("pages.filter.label")}
            placeholder={t("pages.filter.placeholder")}
            value={query.get("q") ?? ""}
            onChange={(e) => set({ q: e.target.value })}
          />
          <select className="ui-select" aria-label={t("pages.filter.kind")} value={kind} onChange={(e) => set({ kind: e.target.value })}>
            <option value="">{t("pages.filter.allKinds")}</option>
            <option value="maintenance">{t("pages.kinds.maintenance")}</option>
            <option value="parking">{t("pages.kinds.parking")}</option>
          </select>
        </div>
        <Table
          label={t("pages.table.label")}
          actionsBelow
          columns={columns}
          rows={shown}
          rowKey={(p) => p.id}
          {...(sort ? { sort } : {})}
          onSort={(s) => set({ sort: s.key, dir: s.dir })}
          empty={t("pages.table.noMatch")}
        />
      </Card>
    </>
  );
}

function UploadForm({ onUploaded, onCancel }: { onUploaded: (msg: string) => Promise<void>; onCancel: () => void }) {
  const { t } = useTranslation();
  const [name, setName] = useState("");
  const [kind, setKind] = useState<PageKind>("maintenance");
  const [file, setFile] = useState<File | null>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");

  async function submit(e: FormEvent) {
    e.preventDefault();
    const problem = pageNameProblem(name) || fileProblem(file);
    if (problem || !file) {
      setError(problem);
      return;
    }
    setBusy(true);
    setError("");
    try {
      const p = await createPage(name.trim(), kind, file);
      await onUploaded(t("pages.upload.done", { name: p.name, count: p.files.length }));
    } catch (err) {
      setError(pageMessage(err));
      setBusy(false);
    }
  }

  return (
    <form className="ui-stack" onSubmit={(e) => void submit(e)} noValidate aria-label={t("pages.upload.title")}>
      <div className="ui-narrow">
        <Field label={t("pages.upload.name")}>
          <input className="ui-input" value={name} onChange={(e) => setName(e.target.value)} maxLength={64} autoComplete="off" />
        </Field>
      </div>
      <Field label={t("pages.upload.kind")}>
        <select className="ui-select" value={kind} onChange={(e) => setKind(e.target.value as PageKind)}>
          <option value="maintenance">{t("pages.upload.kindMaintenance")}</option>
          <option value="parking">{t("pages.upload.kindParking")}</option>
        </select>
      </Field>
      <Field label={t("pages.upload.file")} hint={t("pages.upload.fileHint")}>
        <input className="ui-file" type="file" accept=".html,.htm,.zip" onChange={(e) => setFile(e.target.files?.[0] ?? null)} />
      </Field>
      <Notice tone="warn" title={t("pages.upload.scripts")} />
      {error && <Notice tone="err" title={error} />}
      <div className="ui-row">
        <Button type="submit" variant="primary" disabled={busy}>
          {busy ? t("pages.upload.uploading") : t("pages.upload.submit")}
        </Button>
        <Button onClick={onCancel}>{t("common.actions.cancel")}</Button>
      </div>
    </form>
  );
}

type ViewProps = {
  page: StatusPage;
  back: ReactNode;
  deleteDialog: ReactNode;
  onDelete: () => void;
  onChanged: (msg: string) => Promise<void>;
  done: string;
};

// PageView shows one page: where it is used and its files, and for an
// uploaded page lets the admin replace its files, rename or delete it.
function PageView({ page, back, deleteDialog, onDelete, onChanged, done }: ViewProps) {
  const { t } = useTranslation();
  const [name, setName] = useState(page.name);
  const [file, setFile] = useState<File | null>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const users = usersOf(page);

  async function act(f: () => Promise<string>) {
    setBusy(true);
    setError("");
    try {
      await onChanged(await f());
    } catch (err) {
      setError(pageMessage(err));
    } finally {
      setBusy(false);
    }
  }

  function replace(e: FormEvent) {
    e.preventDefault();
    const problem = fileProblem(file);
    if (problem || !file) {
      setError(problem);
      return;
    }
    void act(async () => {
      await updatePage(page.id, { file });
      return users.length > 0 ? t("pages.view.replacedUsed") : t("pages.view.replaced");
    });
  }

  function rename(e: FormEvent) {
    e.preventDefault();
    const problem = pageNameProblem(name);
    if (problem) {
      setError(problem);
      return;
    }
    void act(async () => {
      await updatePage(page.id, { name: name.trim() });
      return t("pages.view.renamed");
    });
  }

  return (
    <>
      <PageHead
        title={page.name}
        description={
          <span className="ui-row">
            {kindLabel(page.kind)}
            {page.builtin && <Badge tone="off">{t("pages.builtin")}</Badge>}
          </span>
        }
        actions={
          <span className="ui-row">
            <Button icon={<Eye size={16} aria-hidden="true" />} onClick={() => void openPreview(page.id).then(setError)}>
              {t("pages.actions.preview")}
            </Button>
            {back}
          </span>
        }
      />
      {done && <Notice tone="ok" title={done} />}
      {error && <Notice tone="err" title={error} />}
      <Card title={t("pages.view.details")}>
        <dl className="ui-facts">
          <dt>{t("pages.columns.users")}</dt>
          <dd>{users.length > 0 ? users.join(", ") : nobody(page)}</dd>
          <dt>{t("pages.columns.files")}</dt>
          <dd>
            <ul className="ui-plain-list">
              {page.files.map((f) => (
                <li key={f.path}>
                  <span className="ui-mono">{f.path}</span> <span className="ui-hint">{formatSize(f.size)}</span>
                </li>
              ))}
            </ul>
          </dd>
          {!page.builtin && (
            <>
              <dt>{t("pages.view.sha256")}</dt>
              <dd className="ui-mono ui-break">{page.sha256}</dd>
              {page.updatedAt && (
                <>
                  <dt>{t("pages.columns.updated")}</dt>
                  <dd>
                    <RelTime iso={page.updatedAt} />
                  </dd>
                </>
              )}
            </>
          )}
        </dl>
      </Card>
      {page.builtin ? (
        <Notice tone="info" title={t("pages.view.builtinNotice")} />
      ) : (
        <>
          <Card title={t("pages.view.replace")}>
            <form className="ui-stack" onSubmit={replace} noValidate>
              <Field label={t("pages.view.newFile", { name: page.name })} hint={t("pages.view.newFileHint")}>
                <input className="ui-file" type="file" accept=".html,.htm,.zip" onChange={(e) => setFile(e.target.files?.[0] ?? null)} />
              </Field>
              <div className="ui-row">
                <Button type="submit" variant="primary" disabled={busy || !file}>
                  {t("pages.view.replace")}
                </Button>
              </div>
            </form>
          </Card>
          <Card title={t("pages.view.rename")}>
            <form className="ui-stack" onSubmit={rename} noValidate>
              <div className="ui-narrow">
                <Field label={t("pages.view.newName", { name: page.name })}>
                  <input className="ui-input" value={name} onChange={(e) => setName(e.target.value)} maxLength={64} autoComplete="off" />
                </Field>
              </div>
              <div className="ui-row">
                <Button type="submit" variant="primary" disabled={busy || name.trim() === page.name}>
                  {t("pages.view.saveName")}
                </Button>
              </div>
            </form>
          </Card>
          {deleteDialog || (
            <Card title={t("pages.delete.card")}>
              <div className="ui-stack">
                <p className="ui-flush">{users.length > 0 ? t("pages.delete.inUse", { users: users.join(", ") }) : t("pages.delete.unused")}</p>
                <div className="ui-row">
                  <Button variant="danger" disabled={users.length > 0} onClick={onDelete}>
                    {t("pages.delete.confirm", { name: page.name })}
                  </Button>
                </div>
              </div>
            </Card>
          )}
        </>
      )}
    </>
  );
}
