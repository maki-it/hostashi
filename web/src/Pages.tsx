// SPDX-License-Identifier: AGPL-3.0-or-later

import { ArrowLeft, Eye, GearSix, Plus, Trash } from "@phosphor-icons/react";
import { useCallback, useEffect, useState, type FormEvent, type ReactNode } from "react";
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
const nobody = (p: StatusPage) => (p.kind === "parking" ? "No domain" : "No site");

const fileHint =
  "One .html file, or a .zip file with index.html at the top and its CSS, JavaScript, images (png, jpg, webp, gif, svg, ico) and woff2 fonts. At most 2 MiB, 100 files and two folder levels. Files load only from the page itself.";

// Pages lists the maintenance and parking pages and uploads new ones. Sites
// choose their maintenance page in their settings.
export function Pages({ query = new URLSearchParams(), onQuery = () => undefined }: QueryProps) {
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
      await changed(`Page “${p.name}” deleted.`);
    } catch (err) {
      setError(pageMessage(err));
    } finally {
      setBusy(false);
    }
  };

  const listLink = (
    <a className="ui-btn" href={pagesHash()}>
      <ArrowLeft size={16} aria-hidden="true" />
      All pages
    </a>
  );

  if (!pages) {
    return (
      <>
        <PageHead title="Maintenance pages" />
        {loadError ? (
          <Notice tone="err" title={loadError}>
            <Button onClick={() => void load()}>Try again</Button>
          </Notice>
        ) : (
          <p aria-busy="true">Loading…</p>
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
            Delete the page “{deleting.name}”?
          </h2>
          <p className="ui-flush">
            {users.length > 0 ? `${users.join(", ")} still use this page. Choose another page there first.` : "Its files are removed."}
          </p>
          {error && <Notice tone="err" title={error} />}
          <div className="ui-row">
            <Button variant="danger" disabled={busy || users.length > 0} onClick={() => void remove(deleting)}>
              Delete {deleting.name}
            </Button>
            <Button disabled={busy} onClick={() => setDeleting(null)}>
              Cancel
            </Button>
          </div>
        </section>
      );
    })();

  if (query.get("new") === "1") {
    return (
      <>
        <PageHead title="Upload a page" description="A maintenance or parking page of your own." actions={listLink} />
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
          <PageHead title="Maintenance pages" actions={listLink} />
          <Notice tone="warn" title="This page does not exist any more." />
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
      header: "Page",
      sortValue: (p) => p.name,
      cell: (p) => (
        <span className="ui-row ui-nowrap">
          <span className="ui-cell-main">
            <a href={pagesHash({ page: p.id })}>{p.name}</a>
          </span>
          {p.builtin && <Badge tone="off">Built-in</Badge>}
        </span>
      ),
    },
    { key: "kind", header: "Kind", sortValue: (p) => p.kind, cell: (p) => kindLabel(p.kind) },
    {
      key: "users",
      header: "Used by",
      sortValue: (p) => usersOf(p).length,
      cell: (p) => {
        const users = usersOf(p);
        return users.length > 0 ? users.join(", ") : <span className="ui-hint">{nobody(p)}</span>;
      },
    },
    {
      key: "files",
      header: "Files",
      sortValue: sizeOf,
      cell: (p) => (
        <span className="ui-nowrap">
          {p.files.length} · {formatSize(sizeOf(p))}
        </span>
      ),
    },
    {
      key: "updated",
      header: "Changed",
      sortValue: (p) => p.updatedAt ?? "",
      cell: (p) => (p.updatedAt ? <RelTime iso={p.updatedAt} /> : <span className="ui-hint">Built in</span>),
    },
    {
      key: "actions",
      header: "Actions",
      srHeader: true,
      cell: (p) => (
        <RowActions>
          <IconButton label={`Preview ${p.name}`} text="Preview" icon={<Eye size={16} />} onClick={() => void openPreview(p.id).then(setError)} />
          <IconLink label={`Manage ${p.name}`} text="Manage" icon={<GearSix size={16} />} href={pagesHash({ page: p.id })} />
          {!p.builtin && (
            <IconButton
              label={`Delete ${p.name}`}
              text="Delete" icon={<Trash size={16} />}
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
        title="Maintenance pages"
        description="Pages a site shows during maintenance (with 503) and pages a parked domain shows. Sites choose theirs in their settings."
        actions={
          <a className="ui-btn ui-btn-primary" href={pagesHash({ new: "1" })}>
            <Plus size={16} aria-hidden="true" />
            Upload a page
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
            aria-label="Filter pages"
            placeholder="Filter by name"
            value={query.get("q") ?? ""}
            onChange={(e) => set({ q: e.target.value })}
          />
          <select className="ui-select" aria-label="Kind" value={kind} onChange={(e) => set({ kind: e.target.value })}>
            <option value="">All kinds</option>
            <option value="maintenance">Maintenance</option>
            <option value="parking">Parking</option>
          </select>
        </div>
        <Table
          label="Pages"
          columns={columns}
          rows={shown}
          rowKey={(p) => p.id}
          {...(sort ? { sort } : {})}
          onSort={(s) => set({ sort: s.key, dir: s.dir })}
          empty="No page matches the filter."
        />
      </Card>
    </>
  );
}

function UploadForm({ onUploaded, onCancel }: { onUploaded: (msg: string) => Promise<void>; onCancel: () => void }) {
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
      await onUploaded(`Page “${p.name}” uploaded with ${p.files.length} ${p.files.length === 1 ? "file" : "files"}.`);
    } catch (err) {
      setError(pageMessage(err));
      setBusy(false);
    }
  }

  return (
    <form className="ui-stack" onSubmit={(e) => void submit(e)} noValidate aria-label="Upload a page">
      <div className="ui-narrow">
        <Field label="Name">
          <input className="ui-input" value={name} onChange={(e) => setName(e.target.value)} maxLength={64} autoComplete="off" />
        </Field>
      </div>
      <Field label="Kind">
        <select className="ui-select" value={kind} onChange={(e) => setKind(e.target.value as PageKind)}>
          <option value="maintenance">Maintenance: shown with 503 while a site is in maintenance</option>
          <option value="parking">Parking: shown on domains without a site</option>
        </select>
      </Field>
      <Field label="File" hint={fileHint}>
        <input className="ui-file" type="file" accept=".html,.htm,.zip" onChange={(e) => setFile(e.target.files?.[0] ?? null)} />
      </Field>
      <Notice tone="warn" title="Scripts in the page run on the site's domain while the page is shown. Upload only pages you trust." />
      {error && <Notice tone="err" title={error} />}
      <div className="ui-row">
        <Button type="submit" variant="primary" disabled={busy}>
          {busy ? "Uploading…" : "Upload page"}
        </Button>
        <Button onClick={onCancel}>Cancel</Button>
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
      return users.length > 0 ? "Files replaced. Sites and domains that show it use the new page now." : "Files replaced.";
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
      return "Page renamed.";
    });
  }

  return (
    <>
      <PageHead
        title={page.name}
        description={
          <span className="ui-row">
            {kindLabel(page.kind)}
            {page.builtin && <Badge tone="off">Built-in</Badge>}
          </span>
        }
        actions={
          <span className="ui-row">
            <Button icon={<Eye size={16} aria-hidden="true" />} onClick={() => void openPreview(page.id).then(setError)}>
              Preview
            </Button>
            {back}
          </span>
        }
      />
      {done && <Notice tone="ok" title={done} />}
      {error && <Notice tone="err" title={error} />}
      <Card title="Details">
        <dl className="ui-facts">
          <dt>Used by</dt>
          <dd>{users.length > 0 ? users.join(", ") : nobody(page)}</dd>
          <dt>Files</dt>
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
              <dt>SHA-256</dt>
              <dd className="ui-mono ui-break">{page.sha256}</dd>
              {page.updatedAt && (
                <>
                  <dt>Changed</dt>
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
        <Notice tone="info" title="Built-in pages come with Hostashi and cannot be changed." />
      ) : (
        <>
          <Card title="Replace files">
            <form className="ui-stack" onSubmit={replace} noValidate>
              <Field label={"New file for " + page.name} hint="An .html or .zip file. It replaces all files of the page.">
                <input className="ui-file" type="file" accept=".html,.htm,.zip" onChange={(e) => setFile(e.target.files?.[0] ?? null)} />
              </Field>
              <div className="ui-row">
                <Button type="submit" variant="primary" disabled={busy || !file}>
                  Replace files
                </Button>
              </div>
            </form>
          </Card>
          <Card title="Rename">
            <form className="ui-stack" onSubmit={rename} noValidate>
              <div className="ui-narrow">
                <Field label={"New name for " + page.name}>
                  <input className="ui-input" value={name} onChange={(e) => setName(e.target.value)} maxLength={64} autoComplete="off" />
                </Field>
              </div>
              <div className="ui-row">
                <Button type="submit" variant="primary" disabled={busy || name.trim() === page.name}>
                  Save name
                </Button>
              </div>
            </form>
          </Card>
          {deleteDialog || (
            <Card title="Delete">
              <div className="ui-stack">
                <p className="ui-flush">
                  {users.length > 0 ? `${users.join(", ")} still use this page. Choose another page there first.` : "No site or domain uses this page."}
                </p>
                <div className="ui-row">
                  <Button variant="danger" disabled={users.length > 0} onClick={onDelete}>
                    Delete {page.name}
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
