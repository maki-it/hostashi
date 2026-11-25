// SPDX-License-Identifier: AGPL-3.0-or-later

// The "Files" tab of a site (ADR-0040): one folder at a time, its path in
// the address (?path=), a toolbar, a table with checkboxes and row actions,
// and a bar for the selection. Dialogs are cards above the table with
// labelled buttons; icons are for the toolbar and the rows (ADR-0037).

import {
  ArrowClockwise,
  ArrowSquareOut,
  Copy,
  DownloadSimple,
  File as FileIcon,
  FileArchive,
  FilePlus,
  Folder,
  FolderPlus,
  FolderSimpleDashed,
  House,
  LinkSimple,
  LockKey,
  NotePencil,
  PencilSimpleLine,
  Trash,
  UploadSimple,
} from "@phosphor-icons/react";
import { lazy, Suspense, useCallback, useEffect, useRef, useState, type DragEvent, type FormEvent, type ReactNode } from "react";
import {
  chmod,
  cleanPath,
  copy,
  downloadUrl,
  extract,
  filesMessage,
  formatSize,
  joinPath,
  listFiles,
  maxEditBytes,
  maxZipUrl,
  mkdir,
  modeProblem,
  move,
  nameProblem,
  remove,
  rename,
  touch,
  uploadFile,
  zipUrl,
  type FileEntry,
  type Listing,
} from "./files";
import type { Site } from "./sites";
import { Button, Card, Field, IconButton, Notice, RelTime, RowActions, Table, type Column, type Sort } from "./ui";

type Props = {
  site: Site;
  /** The folder shown, "" for the home. */
  path: string;
  /** Opens another folder: a new entry in the browser history. */
  onPath: (path: string) => void;
};

type Dialog =
  | { kind: "mkdir" | "touch" }
  | { kind: "rename"; entry: FileEntry }
  | { kind: "extract"; entry: FileEntry }
  | { kind: "move" | "copy" | "chmod" | "delete"; names: string[] };

type UploadRow = { name: string; size: number; sent: number; error?: string };

// CodeMirror is loaded only when the editor opens (ADR-0040).
const EditorDialog = lazy(() => import("./Editor"));

const isZip = (e: FileEntry) => e.type === "file" && e.name.toLowerCase().endsWith(".zip");

export function FileManager({ site, path, onPath }: Props) {
  const [listing, setListing] = useState<Listing | null>(null);
  const [loadError, setLoadError] = useState("");
  const [selected, setSelected] = useState<Set<string>>(new Set());
  const [sort, setSort] = useState<Sort>({ key: "name", dir: "asc" });
  const [filter, setFilter] = useState("");
  const [dialog, setDialog] = useState<Dialog | null>(null);
  const [done, setDone] = useState("");
  const [uploads, setUploads] = useState<UploadRow[]>([]);
  const [dragging, setDragging] = useState(false);
  const [editing, setEditing] = useState<FileEntry | null>(null);
  const fileInput = useRef<HTMLInputElement>(null);
  const folderInput = useRef<HTMLInputElement>(null);

  const load = useCallback(async () => {
    setLoadError("");
    try {
      const l = await listFiles(site.name, path);
      setListing(l);
      setSelected((s) => new Set([...s].filter((n) => l.entries.some((e) => e.name === n))));
    } catch (err) {
      setLoadError(filesMessage(err));
    }
  }, [site.name, path]);

  useEffect(() => {
    setListing(null);
    setSelected(new Set());
    setDialog(null);
    setEditing(null);
    setDone("");
    void load();
  }, [load]);

  const entries = listing?.entries ?? [];
  const shown = filter === "" ? entries : entries.filter((e) => e.name.toLowerCase().includes(filter.toLowerCase()));
  const allSelected = shown.length > 0 && shown.every((e) => selected.has(e.name));

  const toggle = (name: string) =>
    setSelected((s) => {
      const n = new Set(s);
      if (n.has(name)) n.delete(name);
      else n.add(name);
      return n;
    });

  const finished = async (msg: string) => {
    setDialog(null);
    setDone(msg);
    await load();
  };

  async function sendFiles(items: { file: File; rel: string }[]) {
    if (items.length === 0) return;
    setDone("");
    const rows: UploadRow[] = items.map((i) => ({ name: i.rel, size: i.file.size, sent: 0 }));
    setUploads(rows);
    let failed = 0;
    for (const [i, it] of items.entries()) {
      try {
        await uploadFile(site.name, joinPath(path, it.rel), it.file, {
          parents: it.rel.includes("/"),
          onProgress: (sent) => setUploads((u) => u.map((r, j) => (j === i ? { ...r, sent } : r))),
        });
      } catch (err) {
        failed++;
        const msg = filesMessage(err);
        setUploads((u) => u.map((r, j) => (j === i ? { ...r, error: msg } : r)));
      }
    }
    await load();
    if (failed === 0) {
      setUploads([]);
      setDone(items.length === 1 ? `${items[0]!.rel} uploaded.` : `${String(items.length)} files uploaded.`);
    }
  }

  function onPick(list: FileList | null) {
    if (!list) return;
    const items = [...list].map((file) => ({ file, rel: file.webkitRelativePath || file.name }));
    void sendFiles(items);
  }

  async function onDrop(e: DragEvent) {
    e.preventDefault();
    setDragging(false);
    void sendFiles(await droppedFiles(e.dataTransfer));
  }

  const columns: Column<FileEntry>[] = [
    {
      key: "select",
      header: "Select",
      srHeader: true,
      cell: (e) => (
        <input type="checkbox" className="ui-check" aria-label={"Select " + e.name} checked={selected.has(e.name)} onChange={() => toggle(e.name)} />
      ),
    },
    {
      key: "name",
      header: "Name",
      sortValue: (e) => (e.type === "dir" ? "0" : "1") + e.name,
      cell: (e) => <EntryName entry={e} onOpen={() => onPath(joinPath(path, e.name))} />,
    },
    { key: "size", header: "Size", sortValue: (e) => (e.type === "dir" ? -1 : e.size), cell: (e) => (e.type === "file" ? formatSize(e.size) : "") },
    { key: "modified", header: "Modified", sortValue: (e) => e.modified, cell: (e) => <RelTime iso={e.modified} /> },
    { key: "mode", header: "Mode", sortValue: (e) => e.mode, cell: (e) => <span className="ui-mono">{e.mode.slice(1)}</span> },
    {
      key: "actions",
      header: "Actions",
      srHeader: true,
      cell: (e) => (
        <RowActions>
          {e.type === "file" && (
            <a className="ui-icon-btn" href={downloadUrl(site.name, joinPath(path, e.name))} download aria-label={"Download " + e.name} data-tip="Download">
              <DownloadSimple size={18} aria-hidden="true" />
            </a>
          )}
          {e.type === "dir" && (
            <a className="ui-icon-btn" href={zipUrl(site.name, [joinPath(path, e.name)])} download aria-label={"Download " + e.name + " as ZIP"} data-tip="Download as ZIP">
              <DownloadSimple size={18} aria-hidden="true" />
            </a>
          )}
          {e.type === "file" && e.size <= maxEditBytes && (
            <IconButton label={"Edit " + e.name} icon={<NotePencil size={18} aria-hidden="true" />} onClick={() => setEditing(e)} />
          )}
          {isZip(e) && <IconButton label={"Extract " + e.name} icon={<FileArchive size={18} aria-hidden="true" />} onClick={() => setDialog({ kind: "extract", entry: e })} />}
          <IconButton label={"Rename " + e.name} icon={<PencilSimpleLine size={18} aria-hidden="true" />} onClick={() => setDialog({ kind: "rename", entry: e })} />
          <IconButton label={"Delete " + e.name} danger icon={<Trash size={18} aria-hidden="true" />} onClick={() => setDialog({ kind: "delete", names: [e.name] })} />
        </RowActions>
      ),
    },
  ];

  const sel = [...selected];
  const selPaths = sel.map((n) => joinPath(path, n));
  const zipHref = zipUrl(site.name, selPaths);

  return (
    <Card padded={false}>
      <div className="ui-files-head">
        <Breadcrumb site={site.name} path={path} onPath={onPath} />
        <div className="ui-row ui-nowrap" role="toolbar" aria-label="Files">
          <IconButton label="Reload" icon={<ArrowClockwise size={18} aria-hidden="true" />} onClick={() => void load()} />
          <IconButton label="Upload files" icon={<UploadSimple size={18} aria-hidden="true" />} onClick={() => fileInput.current?.click()} />
          <IconButton label="Upload a folder" icon={<FolderSimpleDashed size={18} aria-hidden="true" />} onClick={() => folderInput.current?.click()} />
          <IconButton label="New folder" icon={<FolderPlus size={18} aria-hidden="true" />} onClick={() => setDialog({ kind: "mkdir" })} />
          <IconButton label="New file" icon={<FilePlus size={18} aria-hidden="true" />} onClick={() => setDialog({ kind: "touch" })} />
        </div>
        <input ref={fileInput} type="file" multiple hidden aria-label="Files to upload" onChange={(e) => { onPick(e.target.files); e.target.value = ""; }} />
        <input
          ref={folderInput}
          type="file"
          hidden
          aria-label="Folder to upload"
          {...{ webkitdirectory: "" }}
          onChange={(e) => { onPick(e.target.files); e.target.value = ""; }}
        />
      </div>
      {(loadError || done || uploads.length > 0 || listing?.truncated || (listing?.unnamed ?? 0) > 0 || dialog || sel.length > 0) && (
      <div className="ui-card-body ui-stack">
        {loadError && <Notice tone="err" title={loadError} />}
        {done && <Notice tone="ok" title={done} />}
        {uploads.length > 0 && <Uploads rows={uploads} onClose={() => setUploads([])} />}
        {listing?.truncated && <Notice tone="warn" title="This folder has more entries than the panel shows. Use SFTP to see all of them." />}
        {listing !== null && listing.unnamed > 0 && (
          <Notice tone="warn" title={`${String(listing.unnamed)} entries have names that are not valid UTF-8 and are not shown. Rename them over SFTP.`} />
        )}
        {dialog && (
          <DialogCard
            site={site.name}
            path={path}
            dialog={dialog}
            onCancel={() => setDialog(null)}
            onDone={(msg) => {
              setSelected(new Set());
              void finished(msg);
            }}
          />
        )}
        {sel.length > 0 && (
          <div className="ui-files-selection" role="region" aria-label="Selection">
            <strong>{sel.length === 1 ? "1 selected" : `${String(sel.length)} selected`}</strong>
            {zipHref.length <= maxZipUrl ? (
              <a className="ui-btn" href={zipHref} download>
                <DownloadSimple size={16} aria-hidden="true" />
                Download as ZIP
              </a>
            ) : (
              <span className="ui-hint">Too many entries for one ZIP; download the folder instead.</span>
            )}
            <Button icon={<ArrowSquareOut size={16} aria-hidden="true" />} onClick={() => setDialog({ kind: "move", names: sel })}>
              Move
            </Button>
            <Button icon={<Copy size={16} aria-hidden="true" />} onClick={() => setDialog({ kind: "copy", names: sel })}>
              Copy
            </Button>
            <Button icon={<LockKey size={16} aria-hidden="true" />} onClick={() => setDialog({ kind: "chmod", names: sel })}>
              Change mode
            </Button>
            <Button variant="danger" icon={<Trash size={16} aria-hidden="true" />} onClick={() => setDialog({ kind: "delete", names: sel })}>
              Delete
            </Button>
            <Button onClick={() => setSelected(new Set())}>Clear selection</Button>
          </div>
        )}
      </div>
      )}
      <div className="ui-filters">
        <input className="ui-input" type="search" aria-label="Filter files" placeholder="Filter by name" value={filter} onChange={(e) => setFilter(e.target.value)} />
        <label className="ui-row">
          <input
            type="checkbox"
            className="ui-check"
            checked={allSelected}
            disabled={shown.length === 0}
            onChange={() => setSelected(allSelected ? new Set() : new Set(shown.map((e) => e.name)))}
          />
          Select all
        </label>
      </div>
      <div
        className="ui-files-drop"
        data-dragging={dragging}
        onDragOver={(e) => {
          if (e.dataTransfer.types.includes("Files")) {
            e.preventDefault();
            setDragging(true);
          }
        }}
        onDragLeave={() => setDragging(false)}
        onDrop={(e) => void onDrop(e)}
      >
        {listing === null && !loadError ? (
          <p className="ui-card-body" aria-busy="true">
            Loading…
          </p>
        ) : (
          <Table
            label={"Files in " + (path === "" ? "the home folder" : path)}
            columns={columns}
            rows={shown}
            rowKey={(e) => e.name}
            sort={sort}
            onSort={setSort}
            empty={filter === "" ? "This folder is empty. Drop files here to upload them." : "No entry matches the filter."}
          />
        )}
      </div>
      {editing && (
        <Suspense fallback={<p aria-busy="true">Loading the editor…</p>}>
          <EditorDialog
            site={site.name}
            path={joinPath(path, editing.name)}
            entry={editing}
            onClose={() => {
              setEditing(null);
              void load();
            }}
            onSaved={() => setDone(`${editing.name} saved.`)}
          />
        </Suspense>
      )}
    </Card>
  );
}

function EntryName({ entry, onOpen }: { entry: FileEntry; onOpen: () => void }) {
  if (entry.type === "dir") {
    return (
      <button type="button" className="ui-files-name ui-link-btn" onClick={onOpen}>
        <Folder size={18} aria-hidden="true" />
        <span>{entry.name}</span>
      </button>
    );
  }
  if (entry.type === "symlink") {
    return (
      <span className="ui-files-name" title="Symlinks are shown, not followed">
        <LinkSimple size={18} aria-hidden="true" />
        <span>
          {entry.name}
          <span className="ui-files-target ui-hint ui-mono">→ {entry.target}</span>
        </span>
      </span>
    );
  }
  return (
    <span className="ui-files-name">
      <FileIcon size={18} aria-hidden="true" />
      <span>{entry.name}</span>
    </span>
  );
}

function Breadcrumb({ site, path, onPath }: { site: string; path: string; onPath: (p: string) => void }) {
  const parts = path === "" ? [] : path.split("/");
  return (
    <nav aria-label="Folder" className="ui-files-crumbs">
      <button type="button" className="ui-link-btn" onClick={() => onPath("")} aria-current={parts.length === 0 ? "page" : undefined}>
        <House size={16} aria-hidden="true" />
        {site}
      </button>
      {parts.map((p, i) => {
        const to = parts.slice(0, i + 1).join("/");
        const last = i === parts.length - 1;
        return (
          <span key={to} className="ui-row ui-nowrap">
            <span aria-hidden="true">/</span>
            <button type="button" className="ui-link-btn" onClick={() => onPath(to)} aria-current={last ? "page" : undefined}>
              {p}
            </button>
          </span>
        );
      })}
    </nav>
  );
}

function Uploads({ rows, onClose }: { rows: UploadRow[]; onClose: () => void }) {
  const running = rows.some((r) => r.sent < r.size && !r.error);
  return (
    <section className="ui-stack-tight" aria-label="Uploads">
      {rows.map((r) => (
        <div key={r.name} className="ui-row">
          <span className="ui-mono">{r.name}</span>
          {r.error ? (
            <span className="ui-error">{r.error}</span>
          ) : (
            <progress max={Math.max(r.size, 1)} value={r.size === 0 ? 1 : r.sent} aria-label={"Upload of " + r.name} />
          )}
        </div>
      ))}
      {!running && (
        <div>
          <Button onClick={onClose}>Close</Button>
        </div>
      )}
    </section>
  );
}

type DialogProps = { site: string; path: string; dialog: Dialog; onCancel: () => void; onDone: (msg: string) => void };

function DialogCard({ site, path, dialog, onCancel, onDone }: DialogProps) {
  const [value, setValue] = useState(initialValue(dialog, path));
  const [overwrite, setOverwrite] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [progress, setProgress] = useState("");

  const problem = check(dialog, value);
  const n = "names" in dialog ? dialog.names.length : 1;
  const what = "names" in dialog ? (n === 1 ? dialog.names[0]! : `${String(n)} entries`) : "";

  async function submit(e: FormEvent) {
    e.preventDefault();
    if (problem) {
      setError(problem);
      return;
    }
    setBusy(true);
    setError("");
    try {
      onDone(await run(site, path, dialog, value, overwrite, (d, t) => setProgress(`${String(d)} of ${String(t)} entries`)));
    } catch (err) {
      setError(filesMessage(err));
      setBusy(false);
    }
  }

  const title: Record<Dialog["kind"], string> = {
    mkdir: "New folder",
    touch: "New file",
    rename: "Rename " + ("entry" in dialog ? dialog.entry.name : ""),
    extract: "Extract " + ("entry" in dialog ? dialog.entry.name : ""),
    move: "Move " + what,
    copy: "Copy " + what,
    chmod: "Change the mode of " + what,
    delete: "Delete " + what + "?",
  };

  let body: ReactNode;
  let action: string;
  switch (dialog.kind) {
    case "mkdir":
    case "touch":
    case "rename":
      action = dialog.kind === "rename" ? "Rename" : "Create";
      body = (
        <Field label="Name" {...(error ? { error } : {})}>
          <input className="ui-input" value={value} onChange={(e) => setValue(e.target.value)} autoFocus />
        </Field>
      );
      break;
    case "move":
    case "copy":
    case "extract":
      action = dialog.kind === "move" ? "Move" : dialog.kind === "copy" ? "Copy" : "Extract";
      body = (
        <>
          <Field label="Into the folder" hint="A path in the site's home, for example htdocs/old. Empty is the home itself." {...(error ? { error } : {})}>
            <input className="ui-input ui-mono" value={value} onChange={(e) => setValue(e.target.value)} autoFocus />
          </Field>
          {dialog.kind === "extract" && (
            <label className="ui-row">
              <input type="checkbox" className="ui-check" checked={overwrite} onChange={(e) => setOverwrite(e.target.checked)} />
              Replace files that exist already
            </label>
          )}
          {progress && <p className="ui-hint" role="status">{progress}</p>}
        </>
      );
      break;
    case "chmod":
      action = "Change mode";
      body = (
        <Field label="Mode" hint="Octal, for example 644 for files and 755 for folders. Applies to the entries themselves, not to their content." {...(error ? { error } : {})}>
          <input className="ui-input ui-mono ui-narrow" value={value} onChange={(e) => setValue(e.target.value)} inputMode="numeric" autoFocus />
        </Field>
      );
      break;
    case "delete":
      action = n === 1 ? "Delete" : `Delete ${String(n)} entries`;
      body = (
        <>
          <p className="ui-flush">Folders are deleted with their content. There is no recycle bin; backups are the way back.</p>
          {error && <Notice tone="err" title={error} />}
        </>
      );
      break;
  }

  return (
    <form className="ui-card ui-card-body ui-stack" role={dialog.kind === "delete" ? "alertdialog" : "dialog"} aria-labelledby="files-dialog-title" onSubmit={(e) => void submit(e)}>
      <h2 id="files-dialog-title" className="ui-flush">
        {title[dialog.kind]}
      </h2>
      {body}
      <div className="ui-row">
        <Button type="submit" variant={dialog.kind === "delete" ? "danger" : "primary"} disabled={busy}>
          {action}
        </Button>
        <Button disabled={busy} onClick={onCancel}>
          Cancel
        </Button>
      </div>
    </form>
  );
}

function initialValue(d: Dialog, path: string): string {
  switch (d.kind) {
    case "rename":
      return d.entry.name;
    case "move":
    case "copy":
    case "extract":
      return path;
    case "chmod":
      return "644";
    default:
      return "";
  }
}

function check(d: Dialog, value: string): string {
  switch (d.kind) {
    case "mkdir":
    case "touch":
    case "rename":
      return nameProblem(value);
    case "move":
    case "copy":
    case "extract":
      return cleanPath(value) === null ? "Enter a path without “..”, “.” or empty parts." : "";
    case "chmod":
      return modeProblem(value);
    case "delete":
      return "";
  }
}

async function run(
  site: string,
  path: string,
  d: Dialog,
  value: string,
  overwrite: boolean,
  onProgress: (done: number, total: number) => void,
): Promise<string> {
  const paths = "names" in d ? d.names.map((n) => joinPath(path, n)) : [];
  const where = (p: string) => (p === "" ? "the home folder" : p);
  switch (d.kind) {
    case "mkdir":
      await mkdir(site, joinPath(path, value));
      return `Folder ${value} created.`;
    case "touch":
      await touch(site, joinPath(path, value));
      return `File ${value} created.`;
    case "rename":
      await rename(site, joinPath(path, d.entry.name), joinPath(path, value));
      return `Renamed to ${value}.`;
    case "move": {
      const to = cleanPath(value) ?? "";
      await move(site, paths, to);
      return `Moved to ${where(to)}.`;
    }
    case "copy": {
      const to = cleanPath(value) ?? "";
      const r = await copy(site, paths, to);
      return `Copied to ${where(to)}.` + (r.skipped > 0 ? ` ${String(r.skipped)} special files were left out.` : "");
    }
    case "chmod":
      await chmod(site, paths, value.length === 3 ? "0" + value : value);
      return `Mode set to ${value}.`;
    case "delete":
      await remove(site, paths);
      return paths.length === 1 ? `${d.names[0]!} deleted.` : `${String(paths.length)} entries deleted.`;
    case "extract": {
      const to = cleanPath(value) ?? "";
      const r = await extract(site, joinPath(path, d.entry.name), to, overwrite, onProgress);
      const parts = [`${String(r.files)} files extracted into ${where(to)}`];
      if (r.kept > 0) parts.push(`${String(r.kept)} existing files kept`);
      if (r.skipped > 0) parts.push(`${String(r.skipped)} symlinks or special entries skipped`);
      return parts.join(", ") + ".";
    }
  }
}

// droppedFiles reads the files of a drop, folders included (the browser's
// file system entries API), with their paths relative to the drop.
async function droppedFiles(dt: DataTransfer): Promise<{ file: File; rel: string }[]> {
  const out: { file: File; rel: string }[] = [];
  const items = [...dt.items].map((i) => i.webkitGetAsEntry?.() ?? null);
  if (items.every((e) => e === null)) {
    return [...dt.files].map((file) => ({ file, rel: file.name }));
  }
  async function walk(entry: FileSystemEntry, prefix: string) {
    if (entry.isFile) {
      const file = await new Promise<File>((res, rej) => (entry as FileSystemFileEntry).file(res, rej));
      out.push({ file, rel: prefix + entry.name });
    } else if (entry.isDirectory) {
      const reader = (entry as FileSystemDirectoryEntry).createReader();
      for (;;) {
        const batch = await new Promise<FileSystemEntry[]>((res, rej) => reader.readEntries(res, rej));
        if (batch.length === 0) break;
        for (const child of batch) await walk(child, prefix + entry.name + "/");
      }
    }
  }
  for (const e of items) if (e) await walk(e, "");
  return out;
}
