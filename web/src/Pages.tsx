// SPDX-License-Identifier: AGPL-3.0-or-later

import { useCallback, useEffect, useRef, useState, type FormEvent } from "react";
import {
  createPage,
  deletePage,
  fileProblem,
  formatSize,
  kindLabel,
  listPages,
  pageMessage,
  pageNameProblem,
  updatePage,
  type PageKind,
  type StatusPage,
} from "./pages";

// Pages lists the maintenance and parking pages and uploads new ones. Sites
// choose their maintenance page in their settings.
export function Pages() {
  const [pages, setPages] = useState<StatusPage[] | null>(null);
  const [error, setError] = useState("");

  const load = useCallback(async () => {
    try {
      setPages(await listPages());
      setError("");
    } catch (err) {
      setError(pageMessage(err));
    }
  }, []);

  useEffect(() => {
    void load();
  }, [load]);

  if (!pages) {
    return error ? (
      <p role="alert" className="card">
        {error}
      </p>
    ) : (
      <p aria-busy="true">Loading…</p>
    );
  }

  return (
    <div className="stack">
      <UploadCard onChange={load} />
      <section className="card" aria-labelledby="page-list">
        <h2 id="page-list">Pages</h2>
        {error && <p role="alert">{error}</p>}
        <ul className="rows">
          {pages.map((p) => (
            <PageRow key={p.id} page={p} onChange={load} />
          ))}
        </ul>
      </section>
    </div>
  );
}

function UploadCard({ onChange }: { onChange: () => Promise<void> }) {
  const [name, setName] = useState("");
  const [kind, setKind] = useState<PageKind>("maintenance");
  const [file, setFile] = useState<File | null>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [done, setDone] = useState("");
  const input = useRef<HTMLInputElement>(null);

  async function submit(e: FormEvent) {
    e.preventDefault();
    const problem = pageNameProblem(name) || fileProblem(file);
    if (problem || !file) {
      setError(problem);
      setDone("");
      return;
    }
    setBusy(true);
    setError("");
    setDone("");
    try {
      const p = await createPage(name.trim(), kind, file);
      setDone(`Page “${p.name}” uploaded with ${p.files.length} ${p.files.length === 1 ? "file" : "files"}.`);
      setName("");
      setFile(null);
      if (input.current) input.current.value = "";
      await onChange();
    } catch (err) {
      setError(pageMessage(err));
    } finally {
      setBusy(false);
    }
  }

  return (
    <section className="card" aria-labelledby="page-upload">
      <h2 id="page-upload">Upload a page</h2>
      <form onSubmit={(e) => void submit(e)} noValidate>
        <label>
          Name
          <input value={name} onChange={(e) => setName(e.target.value)} maxLength={64} autoComplete="off" />
        </label>
        <label>
          Kind
          <select value={kind} onChange={(e) => setKind(e.target.value as PageKind)}>
            <option value="maintenance">Maintenance: shown with 503 while a site is in maintenance</option>
            <option value="parking">Parking: shown on domains without a site</option>
          </select>
        </label>
        <label>
          File
          <small>
            One .html file, or a .zip file with index.html at the top and its CSS, JavaScript, images (png, jpg, webp, gif, svg, ico) and
            woff2 fonts. At most 2 MiB, 100 files and two folder levels. Files load only from the page itself.
          </small>
          <input ref={input} type="file" accept=".html,.htm,.zip" onChange={(e) => setFile(e.target.files?.[0] ?? null)} />
        </label>
        <p className="muted">Scripts in the page run on the site's domain while the page is shown. Upload only pages you trust.</p>
        {error && <p role="alert">{error}</p>}
        {done && <p role="status">{done}</p>}
        <button type="submit" disabled={busy}>
          {busy ? "Uploading…" : "Upload page"}
        </button>
      </form>
    </section>
  );
}

function PageRow({ page, onChange }: { page: StatusPage; onChange: () => Promise<void> }) {
  const [mode, setMode] = useState<"" | "replace" | "rename" | "delete">("");
  const [name, setName] = useState(page.name);
  const [file, setFile] = useState<File | null>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [done, setDone] = useState("");

  async function act(f: () => Promise<string>) {
    setBusy(true);
    setError("");
    setDone("");
    try {
      const msg = await f();
      setMode("");
      setDone(msg);
      await onChange();
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

  const size = page.files.reduce((n, f) => n + f.size, 0);
  const users = [...page.sites, ...page.parked];

  return (
    <li>
      <span>
        <strong>{page.name}</strong> <span className="badge">{kindLabel(page.kind)}</span>
        {page.builtin && <span className="badge">Built-in</span>}
      </span>
      {!page.builtin && mode === "" && (
        <span className="actions">
          <button type="button" className="secondary" onClick={() => setMode("replace")}>
            Replace files
          </button>
          <button type="button" className="secondary" onClick={() => setMode("rename")}>
            Rename
          </button>
          <button type="button" className="secondary danger" onClick={() => setMode("delete")}>
            Delete
          </button>
        </span>
      )}
      <dl className="facts" aria-label={"Details of " + page.name}>
        <dt>Used by</dt>
        <dd>{users.length > 0 ? users.join(", ") : page.kind === "parking" ? "No domain" : "No site"}</dd>
        <dt>Files</dt>
        <dd>
          {page.files.length} ({formatSize(size)})
        </dd>
        {!page.builtin && (
          <>
            <dt>SHA-256</dt>
            <dd>
              <code>{page.sha256}</code>
            </dd>
          </>
        )}
      </dl>
      {page.files.length > 1 && (
        <details>
          <summary>Files of {page.name}</summary>
          <ul>
            {page.files.map((f) => (
              <li key={f.path}>
                <code>{f.path}</code> <span className="muted">{formatSize(f.size)}</span>
              </li>
            ))}
          </ul>
        </details>
      )}
      {mode === "replace" && (
        <form onSubmit={replace} noValidate>
          <label>
            New file for {page.name}
            <small>An .html or .zip file. It replaces all files of the page.</small>
            <input type="file" accept=".html,.htm,.zip" onChange={(e) => setFile(e.target.files?.[0] ?? null)} />
          </label>
          <div className="actions">
            <button type="submit" disabled={busy}>
              Replace files
            </button>
            <button type="button" className="secondary" disabled={busy} onClick={() => setMode("")}>
              Cancel
            </button>
          </div>
        </form>
      )}
      {mode === "rename" && (
        <form onSubmit={rename} noValidate>
          <label>
            New name for {page.name}
            <input value={name} onChange={(e) => setName(e.target.value)} maxLength={64} autoComplete="off" />
          </label>
          <div className="actions">
            <button type="submit" disabled={busy}>
              Save name
            </button>
            <button type="button" className="secondary" disabled={busy} onClick={() => setMode("")}>
              Cancel
            </button>
          </div>
        </form>
      )}
      {mode === "delete" && (
        <div>
          <p>{users.length > 0 ? `${users.join(", ")} still use this page. Choose another page there first.` : "Delete this page?"}</p>
          <div className="actions">
            <button
              type="button"
              className="danger"
              disabled={busy || users.length > 0}
              onClick={() =>
                void act(async () => {
                  await deletePage(page.id);
                  return "";
                })
              }
            >
              Delete {page.name}
            </button>
            <button type="button" className="secondary" disabled={busy} onClick={() => setMode("")}>
              Cancel
            </button>
          </div>
        </div>
      )}
      {error && <p role="alert">{error}</p>}
      {done && <p role="status">{done}</p>}
    </li>
  );
}
