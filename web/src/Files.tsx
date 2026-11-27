// SPDX-License-Identifier: AGPL-3.0-or-later

// The "Files" tab of a site (ADR-0040): one folder at a time, its path in
// the address (?path=), a bar with the folder, filter and toolbar, a table
// with checkboxes and row actions, and a bar for the selection. A click on
// a file's name opens the editor. Steps for one entry open in its row;
// steps for the selection or a new entry are cards above the table.

import {
  ArrowClockwise,
  ArrowSquareOut,
  CaretDown,
  Copy,
  DotsThree,
  DownloadSimple,
  File as FileIcon,
  FileArchive,
  FilePlus,
  Folder,
  FolderPlus,
  House,
  LinkSimple,
  LockKey,
  NotePencil,
  PencilSimpleLine,
  Plus,
  Trash,
  UploadSimple,
  ShieldCheck,
} from "@phosphor-icons/react";
import { lazy, Suspense, useCallback, useEffect, useLayoutEffect, useRef, useState, type RefObject, type DragEvent, type FormEvent, type ReactNode } from "react";
import { Trans, useTranslation } from "react-i18next";
import i18n from "./i18n";
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
import { formatHash } from "./routes";
import { getSiteScan, maxScanPaths, scanMessage, startScan } from "./scan";
import type { Site } from "./sites";
import { Button, Card, Field, IconButton, Menu, Notice, relativeTime, RelTime, RowActions, Table, type Column, type MenuItem, type Sort } from "./ui";

type Props = {
  site: Site;
  /** The folder shown, "" for the home. */
  path: string;
  /** Opens another folder: a new entry in the browser history. */
  onPath: (path: string) => void;
};

/** row is the entry whose row shows the step; without it, the step is a
 *  card above the table. */
type Dialog =
  | { kind: "mkdir" | "touch" }
  | { kind: "rename"; entry: FileEntry }
  | { kind: "extract"; entry: FileEntry; row: string }
  | { kind: "move" | "copy" | "chmod" | "delete"; names: string[]; row?: string };

type UploadRow = { name: string; size: number; sent: number; error?: string };

// CodeMirror is loaded only when the editor opens (ADR-0040).
const EditorDialog = lazy(() => import("./Editor"));

const isZip = (e: FileEntry) => e.type === "file" && e.name.toLowerCase().endsWith(".zip");
const editable = (e: FileEntry) => e.type === "file" && e.size <= maxEditBytes;

/** symbolicMode spells out octal permission bits, "0644" as "rw-r--r--". */
export function symbolicMode(mode: string): string {
  return [...mode.slice(-3)].map((d) => {
    const n = Number(d);
    return (n & 4 ? "r" : "-") + (n & 2 ? "w" : "-") + (n & 1 ? "x" : "-");
  }).join("");
}

/** where names a folder in a sentence: the home folder or its path. */
const where = (p: string) => (p === "" ? i18n.t("files.home") : p);

export function FileManager({ site, path, onPath }: Props) {
  const { t } = useTranslation();
  const [listing, setListing] = useState<Listing | null>(null);
  const [listed, setListed] = useState("");
  const [loadError, setLoadError] = useState("");
  const [selected, setSelected] = useState<Set<string>>(new Set());
  const [sort, setSort] = useState<Sort>({ key: "name", dir: "asc" });
  const [filter, setFilter] = useState("");
  const [dialog, setDialog] = useState<Dialog | null>(null);
  const [done, setDone] = useState("");
  const [uploads, setUploads] = useState<UploadRow[]>([]);
  const [dragging, setDragging] = useState(false);
  const [editing, setEditing] = useState<FileEntry | null>(null);
  const [scanOn, setScanOn] = useState(false);
  const [scanning, setScanning] = useState(false);
  const [scanNote, setScanNote] = useState<{ tone: "ok" | "err"; text: string } | null>(null);
  const fileInput = useRef<HTMLInputElement>(null);
  const folderInput = useRef<HTMLInputElement>(null);
  const rootRef = useRef<HTMLDivElement>(null);
  const sentinelRef = useRef<HTMLDivElement>(null);
  const barRef = useRef<HTMLDivElement>(null);
  const bodyRef = useRef<HTMLDivElement>(null);
  const { stuck, gap } = useStickyBar(rootRef, sentinelRef, barRef);

  const load = useCallback(async () => {
    setLoadError("");
    try {
      const l = await listFiles(site.name, path);
      setListing(l);
      setListed(site.name + "/" + path);
      setSelected((s) => new Set([...s].filter((n) => l.entries.some((e) => e.name === n))));
    } catch (err) {
      setLoadError(filesMessage(err));
    }
  }, [site.name, path]);

  // The old listing stays on screen (dimmed, not clickable) until the new
  // folder is read, so switching folders does not make the page jump.
  useEffect(() => {
    setSelected(new Set());
    setDialog(null);
    setEditing(null);
    setDone("");
    void load();
  }, [load]);

  // The selection bar stays in view while the listing scrolls; a dialog or
  // a result it starts shows above the table, so bring that into view.
  useEffect(() => {
    if (cardDialog || done || scanNote || loadError) bodyRef.current?.scrollIntoView?.({ block: "nearest" });
  }, [dialog, done, scanNote, loadError]);

  const stale = listing !== null && listed !== site.name + "/" + path;

  // The scan button shows only while ClamAV is on for this server and the
  // account may read the site's scan (ADR-0064).
  useEffect(() => {
    let live = true;
    getSiteScan(site.name).then(
      (v) => live && setScanOn(v.engine === "on"),
      () => live && setScanOn(false),
    );
    return () => {
      live = false;
    };
  }, [site.name]);

  useEffect(() => setScanNote(null), [path]);

  async function scanSelection(paths: string[]) {
    setScanning(true);
    setScanNote(null);
    try {
      await startScan(site.name, paths);
      setSelected(new Set());
      setScanNote({ tone: "ok", text: paths.length === 1 ? t("files.scan.one", { path: paths[0] }) : t("files.scan.many", { count: paths.length }) });
    } catch (err) {
      setScanNote({ tone: "err", text: scanMessage(err) });
    } finally {
      setScanning(false);
    }
  }

  const entries = listing?.entries ?? [];
  const shown = filter === "" ? entries : entries.filter((e) => e.name.toLowerCase().includes(filter.toLowerCase()));
  const allSelected = shown.length > 0 && shown.every((e) => selected.has(e.name));
  const selectAll = () => setSelected(allSelected ? new Set() : new Set(shown.map((e) => e.name)));

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
      setDone(items.length === 1 ? t("files.upload.one", { name: items[0]!.rel }) : t("files.upload.many", { count: items.length }));
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

  const download = (e: FileEntry): MenuItem | null => {
    const p = joinPath(path, e.name);
    if (e.type === "file") return { label: t("files.actions.download"), icon: <DownloadSimple size={16} aria-hidden="true" />, href: downloadUrl(site.name, p), download: true };
    if (e.type === "dir") return { label: t("files.actions.downloadZip"), icon: <DownloadSimple size={16} aria-hidden="true" />, href: zipUrl(site.name, [p]), download: true };
    return null;
  };

  // entryItems are the actions of a row's "More" menu. On a phone the menu
  // is the only control of a row, so it starts with the download.
  const entryItems = (e: FileEntry, phone: boolean): MenuItem[] => {
    const items: MenuItem[] = [];
    const dl = download(e);
    if (phone && dl) items.push(dl);
    if (editable(e)) items.push({ label: t("files.actions.edit"), icon: <NotePencil size={16} aria-hidden="true" />, onSelect: () => setEditing(e) });
    if (isZip(e)) items.push({ label: t("files.actions.extract"), icon: <FileArchive size={16} aria-hidden="true" />, onSelect: () => setDialog({ kind: "extract", entry: e, row: e.name }) });
    items.push({ label: t("files.actions.rename"), icon: <PencilSimpleLine size={16} aria-hidden="true" />, onSelect: () => setDialog({ kind: "rename", entry: e }) });
    items.push({ label: t("files.actions.move"), icon: <ArrowSquareOut size={16} aria-hidden="true" />, onSelect: () => setDialog({ kind: "move", names: [e.name], row: e.name }) });
    items.push({ label: t("files.actions.copy"), icon: <Copy size={16} aria-hidden="true" />, onSelect: () => setDialog({ kind: "copy", names: [e.name], row: e.name }) });
    items.push({ label: t("files.actions.chmod"), icon: <LockKey size={16} aria-hidden="true" />, onSelect: () => setDialog({ kind: "chmod", names: [e.name], row: e.name }) });
    if (scanOn) items.push({ label: t("files.actions.scan"), icon: <ShieldCheck size={16} aria-hidden="true" />, onSelect: () => void scanSelection([joinPath(path, e.name)]) });
    items.push({ label: t("files.actions.delete"), icon: <Trash size={16} aria-hidden="true" />, danger: true, onSelect: () => setDialog({ kind: "delete", names: [e.name], row: e.name }) });
    return items;
  };

  const renaming = dialog?.kind === "rename" ? dialog.entry.name : null;
  const rowDialog = dialog && "row" in dialog && dialog.row !== undefined ? dialog : null;
  const cardDialog = dialog && dialog.kind !== "rename" && rowDialog === null ? dialog : null;
  const dialogDone = (msg: string) => {
    setSelected(new Set());
    void finished(msg);
  };

  const columns: Column<FileEntry>[] = [
    {
      key: "select",
      header: t("files.columns.select"),
      srHeader: true,
      headerCell: (
        <label className="ui-check-cell">
          <input
            type="checkbox"
            className="ui-check"
            aria-label={t("files.selectAll")}
            checked={allSelected}
            disabled={shown.length === 0}
            onChange={selectAll}
          />
        </label>
      ),
      cell: (e) => (
        // The whole cell is the click target, not only the small box.
        <label className="ui-check-cell">
          <input type="checkbox" className="ui-check" aria-label={t("files.selectOne", { name: e.name })} checked={selected.has(e.name)} onChange={() => toggle(e.name)} />
        </label>
      ),
    },
    {
      key: "name",
      header: t("files.columns.name"),
      main: true,
      sortValue: (e) => (e.type === "dir" ? "0" : "1") + e.name,
      cell: (e) =>
        renaming === e.name && dialog?.kind === "rename" ? (
          <RenameForm site={site.name} path={path} entry={dialog.entry} onCancel={() => setDialog(null)} onDone={dialogDone} />
        ) : (
        <>
          <EntryName
            entry={e}
            onOpen={() => onPath(joinPath(path, e.name))}
            onEdit={() => setEditing(e)}
            href={e.type === "file" ? downloadUrl(site.name, joinPath(path, e.name)) : ""}
          />
          <span className="ui-phone-only ui-files-meta">{[e.type === "file" ? formatSize(e.size) : "", relativeTime(new Date(e.modified)), e.mode.slice(1)].filter(Boolean).join(" · ")}</span>
        </>
      ),
    },
    {
      key: "size",
      header: t("files.columns.size"),
      sortValue: (e) => (e.type === "dir" ? -1 : e.size),
      cell: (e) => (e.type === "file" ? formatSize(e.size) : <span className="ui-hint" aria-label={t("files.noSize")}>—</span>),
    },
    { key: "modified", header: t("files.columns.modified"), sortValue: (e) => e.modified, cell: (e) => <RelTime iso={e.modified} /> },
    {
      key: "mode",
      header: t("files.columns.mode"),
      sortValue: (e) => e.mode,
      cell: (e) => (
        <abbr className="ui-mono ui-files-mode" title={symbolicMode(e.mode)}>
          {e.mode.slice(1)}
        </abbr>
      ),
    },
    {
      key: "actions",
      header: t("files.columns.actions"),
      srHeader: true,
      cell: (e) => (
        <>
          <span className="ui-phone-hide">
            <RowActions>
              {e.type === "file" && (
                <a className="ui-icon-btn" href={downloadUrl(site.name, joinPath(path, e.name))} download aria-label={t("files.actions.downloadName", { name: e.name })} data-tip={t("files.actions.download")}>
                  <DownloadSimple size={18} aria-hidden="true" />
                </a>
              )}
              {e.type === "dir" && (
                <a className="ui-icon-btn" href={zipUrl(site.name, [joinPath(path, e.name)])} download aria-label={t("files.actions.downloadNameZip", { name: e.name })} data-tip={t("files.actions.downloadZip")}>
                  <DownloadSimple size={18} aria-hidden="true" />
                </a>
              )}
              <Menu label={t("files.actions.more", { name: e.name })} buttonLabel={t("files.actions.more", { name: e.name })} buttonClass="ui-icon-btn" button={<DotsThree size={20} weight="bold" aria-hidden="true" />} items={entryItems(e, false)} />
            </RowActions>
          </span>
          <span className="ui-phone-only">
            <Menu label={t("files.actions.phone", { name: e.name })} buttonLabel={t("files.actions.phone", { name: e.name })} buttonClass="ui-icon-btn" button={<DotsThree size={20} aria-hidden="true" />} items={entryItems(e, true)} />
          </span>
        </>
      ),
    },
  ];

  const sel = [...selected];
  const selPaths = sel.map((n) => joinPath(path, n));
  const zipHref = zipUrl(site.name, selPaths);

  return (
    <Card padded={false}>
      <div className="ui-files" ref={rootRef}>
      {/* The bar (folder, toolbar, filter and the selection) sticks to the
          top of the window in a compact form while the listing scrolls. */}
      <div ref={barRef} className="ui-files-bar" data-stuck={stuck} style={gap > 0 ? { marginBottom: gap } : undefined}>
      <div className="ui-files-head">
        <Breadcrumb site={site.name} path={path} onPath={onPath} />
        <div className="ui-filters">
          <input className="ui-input" type="search" aria-label={t("files.filterLabel")} placeholder={t("files.filterPlaceholder")} value={filter} onChange={(e) => setFilter(e.target.value)} />
          {/* On a phone the table head is hidden, so the box sits here. */}
          <label className="ui-row ui-phone-only">
            <input type="checkbox" className="ui-check" checked={allSelected} disabled={shown.length === 0} onChange={selectAll} />
            {t("files.selectAll")}
          </label>
        </div>
        <div className="ui-row ui-nowrap" role="toolbar" aria-label={t("files.toolbar")}>
          <IconButton label={t("files.reload")} tipBelow icon={<ArrowClockwise size={18} aria-hidden="true" />} onClick={() => void load()} />
          <Menu
            label={t("files.upload.menu")}
            buttonLabel={t("files.upload.menu")}
            buttonClass="ui-btn ui-btn-sm"
            button={
              <>
                <UploadSimple size={16} aria-hidden="true" />
                <span className="ui-phone-hide">{t("files.upload.menu")}</span>
                <CaretDown size={12} aria-hidden="true" />
              </>
            }
            items={[
              { label: t("files.upload.files"), icon: <FileIcon size={16} aria-hidden="true" />, onSelect: () => fileInput.current?.click() },
              { label: t("files.upload.folder"), icon: <Folder size={16} aria-hidden="true" />, onSelect: () => folderInput.current?.click() },
            ]}
          />
          <Menu
            label={t("files.new.menu")}
            buttonLabel={t("files.new.menu")}
            buttonClass="ui-btn ui-btn-sm"
            button={
              <>
                <Plus size={16} aria-hidden="true" />
                <span className="ui-phone-hide">{t("files.new.menu")}</span>
                <CaretDown size={12} aria-hidden="true" />
              </>
            }
            items={[
              { label: t("files.new.file"), icon: <FilePlus size={16} aria-hidden="true" />, onSelect: () => setDialog({ kind: "touch" }) },
              { label: t("files.new.folder"), icon: <FolderPlus size={16} aria-hidden="true" />, onSelect: () => setDialog({ kind: "mkdir" }) },
            ]}
          />
        </div>
        <input ref={fileInput} type="file" multiple hidden aria-label={t("files.upload.filesInput")} onChange={(e) => { onPick(e.target.files); e.target.value = ""; }} />
        <input
          ref={folderInput}
          type="file"
          hidden
          aria-label={t("files.upload.folderInput")}
          {...{ webkitdirectory: "" }}
          onChange={(e) => { onPick(e.target.files); e.target.value = ""; }}
        />
      </div>
        {sel.length > 0 && (
          <div className="ui-files-selection" role="region" aria-label={t("files.selection.label")}>
            <strong>{t("files.selection.count", { count: sel.length })}</strong>
            {zipHref.length <= maxZipUrl ? (
              <a className="ui-btn" href={zipHref} download>
                <DownloadSimple size={16} aria-hidden="true" />
                {t("files.actions.downloadZip")}
              </a>
            ) : (
              <span className="ui-hint">{t("files.selection.tooManyZip")}</span>
            )}
            <Button icon={<ArrowSquareOut size={16} aria-hidden="true" />} onClick={() => setDialog({ kind: "move", names: sel })}>
              {t("files.selection.move")}
            </Button>
            <Button icon={<Copy size={16} aria-hidden="true" />} onClick={() => setDialog({ kind: "copy", names: sel })}>
              {t("files.selection.copy")}
            </Button>
            <Button icon={<LockKey size={16} aria-hidden="true" />} onClick={() => setDialog({ kind: "chmod", names: sel })}>
              {t("files.selection.chmod")}
            </Button>
            {scanOn &&
              (sel.length <= maxScanPaths ? (
                <Button icon={<ShieldCheck size={16} aria-hidden="true" />} disabled={scanning} onClick={() => void scanSelection(selPaths)}>
                  {scanning ? t("files.selection.scanning") : t("files.actions.scan")}
                </Button>
              ) : (
                <span className="ui-hint">{t("files.selection.tooManyScan")}</span>
              ))}
            <Button variant="danger" icon={<Trash size={16} aria-hidden="true" />} onClick={() => setDialog({ kind: "delete", names: sel })}>
              {t("files.selection.delete")}
            </Button>
            <Button onClick={() => setSelected(new Set())}>{t("files.selection.clear")}</Button>
          </div>
        )}
      </div>
      <div ref={sentinelRef} className="ui-files-sentinel" aria-hidden="true" />
      {(loadError || done || scanNote || uploads.length > 0 || listing?.truncated || (listing?.unnamed ?? 0) > 0 || cardDialog) && (
      <div ref={bodyRef} className="ui-card-body ui-stack ui-files-body">
        {loadError && <Notice tone="err" title={loadError} />}
        {done && <Notice tone="ok" title={done} />}
        {scanNote && (
          <Notice tone={scanNote.tone} title={scanNote.text}>
            {scanNote.tone === "ok" && (
              <Trans i18nKey="files.scan.results" components={{ a: <a href={formatHash({ page: "sites", site: { name: site.name, tab: "malware" } })} /> }} />
            )}
          </Notice>
        )}
        {uploads.length > 0 && <Uploads rows={uploads} onClose={() => setUploads([])} />}
        {listing?.truncated && <Notice tone="warn" title={t("files.truncated")} />}
        {listing !== null && listing.unnamed > 0 && <Notice tone="warn" title={t("files.unnamed", { count: listing.unnamed })} />}
        {cardDialog && <DialogCard site={site.name} path={path} dialog={cardDialog} onCancel={() => setDialog(null)} onDone={dialogDone} />}
      </div>
      )}
      <div
        className="ui-files-drop"
        data-dragging={dragging}
        aria-busy={stale}
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
            {t("files.loading")}
          </p>
        ) : (
          <Table
            label={t("files.tableLabel", { where: where(path) })}
            columns={columns}
            rows={shown}
            rowKey={(e) => e.name}
            sort={sort}
            onSort={setSort}
            compact
            empty={filter === "" ? t("files.empty") : t("files.noMatch")}
            after={(e) =>
              rowDialog?.row === e.name ? (
                <DialogCard site={site.name} path={path} dialog={rowDialog} inline onCancel={() => setDialog(null)} onDone={dialogDone} />
              ) : null
            }
          />
        )}
        {dragging && (
          <div className="ui-files-dropnote" aria-hidden="true">
            <UploadSimple size={28} aria-hidden="true" />
            {t("files.upload.drop", { where: where(path) })}
          </div>
        )}
        {listing !== null && entries.length > 0 && (
          <p className="ui-files-hint ui-hint ui-phone-hide">
            <UploadSimple size={16} aria-hidden="true" />
            {t("files.upload.dragHint")}
          </p>
        )}
      </div>
      {editing && (
        <Suspense fallback={<p aria-busy="true">{t("files.loadingEditor")}</p>}>
          <EditorDialog
            site={site.name}
            path={joinPath(path, editing.name)}
            entry={editing}
            onClose={() => {
              setEditing(null);
              void load();
            }}
            onSaved={() => setDone(t("files.saved", { name: editing.name }))}
          />
        </Suspense>
      )}
      </div>
    </Card>
  );
}

/** The height of the bar and the table head; the head counts only where it
 *  shows (on a phone the rows are cards without one). */
function stickyHeight(root: HTMLElement | null, bar: HTMLElement | null): number {
  const head = root?.querySelector("thead")?.getBoundingClientRect().height ?? 0;
  return (bar?.offsetHeight ?? 0) + (head > 1 ? head : 0);
}

/**
 * Tells whether the bar is stuck (compact): the sentinel below the full bar
 * has scrolled out at the top, that is the listing has reached the bottom
 * of the compact bar under the top bar. The compact bar and table head are
 * lower than the full ones; gap keeps the difference as a margin below it
 * while stuck, so the listing does not jump. The heights of
 * the bar and the table head go to CSS variables: the table head sticks
 * below the bar, and focus scrolls clear of both.
 */
function useStickyBar(root: RefObject<HTMLDivElement | null>, sentinel: RefObject<HTMLDivElement | null>, bar: RefObject<HTMLDivElement | null>) {
  const [stuck, setStuck] = useState(false);
  const [gap, setGap] = useState(0);
  const fullHeight = useRef(0);

  useEffect(() => {
    const s = sentinel.current;
    if (!s || typeof IntersectionObserver === "undefined") return;
    const io = new IntersectionObserver(([e]) => {
      if (!e) return;
      const now = !e.isIntersecting && e.boundingClientRect.top < 0;
      if (now && bar.current?.dataset["stuck"] !== "true") fullHeight.current = stickyHeight(root.current, bar.current);
      setStuck(now);
    });
    io.observe(s);
    return () => io.disconnect();
  }, [root, sentinel, bar]);

  // A long path in the compact bar scrolls; keep the open folder in view.
  useLayoutEffect(() => {
    const crumbs = root.current?.querySelector<HTMLElement>(".ui-files-crumbs");
    if (stuck && crumbs) crumbs.scrollLeft = crumbs.scrollWidth;
  });

  useLayoutEffect(() => {
    setGap(stuck ? Math.max(0, fullHeight.current - stickyHeight(root.current, bar.current)) : 0);
  }, [stuck, root, bar]);

  useEffect(() => {
    const r = root.current;
    const b = bar.current;
    if (!r || !b || typeof ResizeObserver === "undefined") return;
    const html = document.documentElement;
    const measure = () => {
      r.style.setProperty("--files-bar-h", `${String(b.offsetHeight)}px`);
      if (b.dataset["stuck"] === "true") r.style.setProperty("--files-compact-h", `${String(b.offsetHeight)}px`);
      html.style.setProperty("--files-sticky-h", `${String(stickyHeight(r, b))}px`);
    };
    const ro = new ResizeObserver(measure);
    ro.observe(b);
    ro.observe(r);
    measure();
    return () => {
      ro.disconnect();
      html.style.removeProperty("--files-sticky-h");
    };
  }, [root, bar]);

  return { stuck, gap };
}

/** The name of an entry: a folder opens, a text file opens in the editor,
 *  a file too large for it downloads. */
function EntryName({ entry, onOpen, onEdit, href }: { entry: FileEntry; onOpen: () => void; onEdit: () => void; href: string }) {
  const { t } = useTranslation();
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
      <span className="ui-files-name" title={t("files.symlink")}>
        <LinkSimple size={18} aria-hidden="true" />
        <span>
          {entry.name}
          <span className="ui-files-target ui-hint ui-mono">→ {entry.target}</span>
        </span>
      </span>
    );
  }
  if (editable(entry)) {
    return (
      <button type="button" className="ui-files-name ui-files-file" onClick={onEdit} aria-label={t("files.actions.editName", { name: entry.name })}>
        <FileIcon size={18} aria-hidden="true" />
        <span>{entry.name}</span>
      </button>
    );
  }
  if (entry.type === "file") {
    return (
      <a className="ui-files-name ui-files-file" href={href} download aria-label={t("files.actions.downloadName", { name: entry.name })}>
        <FileIcon size={18} aria-hidden="true" />
        <span>{entry.name}</span>
      </a>
    );
  }
  return (
    <span className="ui-files-name">
      <FileIcon size={18} aria-hidden="true" />
      <span>{entry.name}</span>
    </span>
  );
}

/** Renames an entry in its own row: the name becomes a field. */
function RenameForm({ site, path, entry, onCancel, onDone }: { site: string; path: string; entry: FileEntry; onCancel: () => void; onDone: (msg: string) => void }) {
  const { t } = useTranslation();
  const [value, setValue] = useState(entry.name);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const input = useRef<HTMLInputElement>(null);

  // The name is selected without its extension, as in a desktop file manager.
  useEffect(() => {
    const el = input.current;
    if (!el) return;
    el.focus();
    const dot = entry.type === "file" ? entry.name.lastIndexOf(".") : -1;
    el.setSelectionRange(0, dot > 0 ? dot : entry.name.length);
  }, [entry]);

  async function submit(e: FormEvent) {
    e.preventDefault();
    if (value === entry.name) return onCancel();
    const problem = nameProblem(value);
    if (problem) return setError(problem);
    setBusy(true);
    setError("");
    try {
      await rename(site, joinPath(path, entry.name), joinPath(path, value));
      onDone(t("files.done.renamed", { name: value }));
    } catch (err) {
      setError(filesMessage(err));
      setBusy(false);
    }
  }

  return (
    <form className="ui-files-rename" aria-label={t("files.rename.label", { name: entry.name })} onSubmit={(e) => void submit(e)}>
      <div className="ui-row ui-nowrap">
        {entry.type === "dir" ? <Folder size={18} aria-hidden="true" /> : <FileIcon size={18} aria-hidden="true" />}
        <input
          ref={input}
          className="ui-input"
          aria-label={t("files.rename.newName")}
          aria-invalid={error ? true : undefined}
          value={value}
          onChange={(e) => setValue(e.target.value)}
          onKeyDown={(e) => {
            if (e.key === "Escape") {
              e.preventDefault();
              onCancel();
            }
          }}
        />
        <Button type="submit" variant="primary" small disabled={busy}>
          {t("files.rename.submit")}
        </Button>
        <Button small disabled={busy} onClick={onCancel}>
          {t("files.rename.cancel")}
        </Button>
      </div>
      {error && <p className="ui-error ui-flush">{error}</p>}
    </form>
  );
}

function Breadcrumb({ site, path, onPath }: { site: string; path: string; onPath: (p: string) => void }) {
  const { t } = useTranslation();
  const parts = path === "" ? [] : path.split("/");
  return (
    <nav aria-label={t("files.folderNav")} className="ui-files-crumbs">
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
  const { t } = useTranslation();
  const running = rows.some((r) => r.sent < r.size && !r.error);
  return (
    <section className="ui-stack-tight" aria-label={t("files.upload.list")}>
      {rows.map((r) => (
        <div key={r.name} className="ui-row">
          <span className="ui-mono">{r.name}</span>
          {r.error ? (
            <span className="ui-error">{r.error}</span>
          ) : (
            <progress max={Math.max(r.size, 1)} value={r.size === 0 ? 1 : r.sent} aria-label={t("files.upload.progress", { name: r.name })} />
          )}
        </div>
      ))}
      {!running && (
        <div>
          <Button onClick={onClose}>{t("files.upload.close")}</Button>
        </div>
      )}
    </section>
  );
}

type DialogProps = { site: string; path: string; dialog: Dialog; inline?: boolean; onCancel: () => void; onDone: (msg: string) => void };

/** The names a delete lists before it shows "and N more". */
const listedNames = 8;

function DialogCard({ site, path, dialog, inline = false, onCancel, onDone }: DialogProps) {
  const { t } = useTranslation();
  const [value, setValue] = useState(initialValue(dialog, path));
  const [overwrite, setOverwrite] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [progress, setProgress] = useState("");

  const problem = check(dialog, value);
  const n = "names" in dialog ? dialog.names.length : 1;
  const what = "names" in dialog ? (n === 1 ? dialog.names[0]! : t("files.dialog.entries", { count: n })) : "";

  async function submit(e: FormEvent) {
    e.preventDefault();
    if (problem) {
      setError(problem);
      return;
    }
    setBusy(true);
    setError("");
    try {
      onDone(await run(site, path, dialog, value, overwrite, (done, total) => setProgress(t("files.dialog.progress", { done, total }))));
    } catch (err) {
      setError(filesMessage(err));
      setBusy(false);
    }
  }

  const title: Record<Dialog["kind"], string> = {
    mkdir: t("files.dialog.title.mkdir"),
    touch: t("files.dialog.title.touch"),
    rename: t("files.dialog.title.rename", { name: "entry" in dialog ? dialog.entry.name : "" }),
    extract: t("files.dialog.title.extract", { name: "entry" in dialog ? dialog.entry.name : "" }),
    move: t("files.dialog.title.move", { what }),
    copy: t("files.dialog.title.copy", { what }),
    chmod: t("files.dialog.title.chmod", { what }),
    delete: n === 1 ? t("files.dialog.title.deleteOne", { what }) : t("files.dialog.title.deleteMany", { count: n }),
  };

  let body: ReactNode;
  let action: string;
  switch (dialog.kind) {
    case "mkdir":
    case "touch":
    case "rename":
      action = dialog.kind === "rename" ? t("files.dialog.action.rename") : t("files.dialog.action.create");
      body = (
        <Field label={t("files.dialog.name")} {...(error ? { error } : {})}>
          <input className="ui-input" value={value} onChange={(e) => setValue(e.target.value)} autoFocus />
        </Field>
      );
      break;
    case "move":
    case "copy":
    case "extract":
      action = dialog.kind === "move" ? t("files.dialog.action.move") : dialog.kind === "copy" ? t("files.dialog.action.copy") : t("files.dialog.action.extract");
      body = (
        <>
          <Field label={t("files.dialog.into")} hint={t("files.dialog.intoHint")} {...(error ? { error } : {})}>
            <input className="ui-input ui-mono" value={value} onChange={(e) => setValue(e.target.value)} autoFocus />
          </Field>
          {dialog.kind === "extract" && (
            <label className="ui-row">
              <input type="checkbox" className="ui-check" checked={overwrite} onChange={(e) => setOverwrite(e.target.checked)} />
              {t("files.dialog.overwrite")}
            </label>
          )}
          {progress && <p className="ui-hint" role="status">{progress}</p>}
        </>
      );
      break;
    case "chmod":
      action = t("files.dialog.action.chmod");
      body = (
        <Field label={t("files.dialog.mode")} hint={t("files.dialog.modeHint")} {...(error ? { error } : {})}>
          <input className="ui-input ui-mono ui-narrow" value={value} onChange={(e) => setValue(e.target.value)} inputMode="numeric" autoFocus />
        </Field>
      );
      break;
    case "delete":
      action = n === 1 ? t("files.dialog.action.deleteOne") : t("files.dialog.action.deleteMany", { count: n });
      body = (
        <>
          {n > 1 && (
            <ul className="ui-files-names">
              {dialog.names.slice(0, listedNames).map((name) => (
                <li key={name} className="ui-mono">
                  {name}
                </li>
              ))}
              {n > listedNames && <li className="ui-hint">{t("files.dialog.more", { count: n - listedNames })}</li>}
            </ul>
          )}
          <p className="ui-flush">{t("files.dialog.deleteText")}</p>
          {error && <Notice tone="err" title={error} />}
        </>
      );
      break;
  }

  return (
    <form
      className={inline ? "ui-files-inline ui-stack" : "ui-card ui-card-body ui-stack"}
      data-tone={dialog.kind === "delete" ? "err" : undefined}
      role={dialog.kind === "delete" ? "alertdialog" : "dialog"}
      aria-labelledby="files-dialog-title"
      onSubmit={(e) => void submit(e)}
    >
      {inline ? (
        <h3 id="files-dialog-title" className="ui-flush">
          {title[dialog.kind]}
        </h3>
      ) : (
        <h2 id="files-dialog-title" className="ui-flush">
          {title[dialog.kind]}
        </h2>
      )}
      {body}
      <div className="ui-row">
        <Button type="submit" variant={dialog.kind === "delete" ? "destroy" : "primary"} disabled={busy} autoFocus={dialog.kind === "delete"}>
          {action}
        </Button>
        <Button disabled={busy} onClick={onCancel}>
          {t("files.dialog.cancel")}
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
      return cleanPath(value) === null ? i18n.t("files.dialog.badPath") : "";
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
  const t = i18n.t;
  switch (d.kind) {
    case "mkdir":
      await mkdir(site, joinPath(path, value));
      return t("files.done.mkdir", { name: value });
    case "touch":
      await touch(site, joinPath(path, value));
      return t("files.done.touch", { name: value });
    case "rename":
      await rename(site, joinPath(path, d.entry.name), joinPath(path, value));
      return t("files.done.renamed", { name: value });
    case "move": {
      const to = cleanPath(value) ?? "";
      await move(site, paths, to);
      return t("files.done.moved", { where: where(to) });
    }
    case "copy": {
      const to = cleanPath(value) ?? "";
      const r = await copy(site, paths, to);
      return r.skipped > 0 ? t("files.done.copiedSkipped", { where: where(to), count: r.skipped }) : t("files.done.copied", { where: where(to) });
    }
    case "chmod":
      await chmod(site, paths, value.length === 3 ? "0" + value : value);
      return t("files.done.chmod", { mode: value });
    case "delete":
      await remove(site, paths);
      return paths.length === 1 ? t("files.done.deletedOne", { name: d.names[0]! }) : t("files.done.deletedMany", { count: paths.length });
    case "extract": {
      const to = cleanPath(value) ?? "";
      const r = await extract(site, joinPath(path, d.entry.name), to, overwrite, onProgress);
      const parts = [t("files.done.extracted", { count: r.files, where: where(to) })];
      if (r.kept > 0) parts.push(t("files.done.kept", { count: r.kept }));
      if (r.skipped > 0) parts.push(t("files.done.skipped", { count: r.skipped }));
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
