// SPDX-License-Identifier: AGPL-3.0-or-later

// The editor dialog of the file manager (ADR-0040): one text file up to
// 1 MiB in CodeMirror, saved with the version it was opened with. Closing
// with unsaved changes asks first; leaving the page triggers the browser's
// own warning. Footer buttons are labelled; icons are for toolbars.

import { X } from "@phosphor-icons/react";
import { useCallback, useEffect, useRef, useState } from "react";
import { createEditor, language, type EditorHandle } from "./codemirror";
import { downloadUrl, fileVersion, filesMessage, maxEditBytes, saveFile, type FileEntry } from "./files";
import { ApiError } from "./api";
import { Badge, Button, IconButton, Notice } from "./ui";

type Props = {
  site: string;
  /** The file's path in the home. */
  path: string;
  entry: FileEntry;
  onClose: () => void;
  /** Called after each save, with the saved entry. */
  onSaved: (e: FileEntry) => void;
};

type Loaded = { text: string; version: string; crlf: boolean; bom: boolean };

const wrapKey = "hostashi.editor.wrap";

function readWrap(): boolean {
  try {
    return localStorage.getItem(wrapKey) === "1";
  } catch {
    return false;
  }
}

function storeWrap(on: boolean) {
  try {
    localStorage.setItem(wrapKey, on ? "1" : "0");
  } catch {
    // The setting is a convenience; without storage it lasts until reload.
  }
}

/** loadText fetches a file and decodes it as UTF-8, or explains why not. */
export async function loadText(site: string, path: string): Promise<Loaded> {
  const res = await fetch(downloadUrl(site, path), { credentials: "same-origin", headers: { Accept: "application/octet-stream" } });
  if (!res.ok) {
    let code = "http_" + String(res.status);
    let message = res.statusText;
    try {
      const b = (await res.json()) as { error?: { code?: string; message?: string } };
      code = b.error?.code ?? code;
      message = b.error?.message ?? message;
    } catch {
      // not JSON
    }
    throw new ApiError(res.status, code, message);
  }
  const buf = new Uint8Array(await res.arrayBuffer());
  if (buf.length > maxEditBytes) throw new ApiError(413, "files_too_large", "files: larger than 1 MiB; download it instead");
  let text: string;
  try {
    text = new TextDecoder("utf-8", { fatal: true }).decode(buf);
  } catch {
    throw new ApiError(422, "files_not_text", "not UTF-8 text");
  }
  if (text.includes("\0")) throw new ApiError(422, "files_not_text", "not text");
  const bom = buf[0] === 0xef && buf[1] === 0xbb && buf[2] === 0xbf;
  return { text, version: res.headers.get("X-Hostashi-Version") ?? "", crlf: text.includes("\r\n"), bom };
}

export default function EditorDialog({ site, path, entry, onClose, onSaved }: Props) {
  const dialog = useRef<HTMLDialogElement>(null);
  const host = useRef<HTMLDivElement>(null);
  const editor = useRef<EditorHandle | null>(null);
  const [loaded, setLoaded] = useState<Loaded | null>(null);
  const [loadError, setLoadError] = useState("");
  const [original, setOriginal] = useState("");
  const [doc, setDoc] = useState("");
  const [cursor, setCursor] = useState({ line: 1, col: 1 });
  const [wrap, setWrap] = useState(readWrap);
  const [saving, setSaving] = useState(false);
  const [saveError, setSaveError] = useState("");
  const [asking, setAsking] = useState(false);
  const [saved, setSaved] = useState(false);

  const dirty = loaded !== null && doc !== original;
  const lang = language(entry.name).label;

  // Opens as a modal: the page behind it is inert, so the site's tabs
  // cannot be switched while it is open.
  useEffect(() => {
    const d = dialog.current;
    if (!d) return;
    if (typeof d.showModal === "function") d.showModal();
    else d.setAttribute("open", "");
    return () => {
      if (typeof d.close === "function") d.close();
    };
  }, []);

  useEffect(() => {
    let live = true;
    loadText(site, path).then(
      (l) => {
        if (!live) return;
        setLoaded(l);
        setOriginal(l.text.replace(/\r\n/g, "\n"));
        setDoc(l.text.replace(/\r\n/g, "\n"));
      },
      (err: unknown) => {
        if (live) setLoadError(err instanceof ApiError && err.code === "files_not_text" ? "This file is not UTF-8 text and cannot be edited here. Download it instead." : filesMessage(err));
      },
    );
    return () => {
      live = false;
    };
  }, [site, path]);

  // The save callback changes with every keystroke; the editor calls it
  // through a ref so that it is not rebuilt.
  const save = useCallback(async (): Promise<boolean> => {
    if (!loaded || saving) return false;
    const current = editor.current?.view.state.doc.toString() ?? doc;
    setSaving(true);
    setSaveError("");
    try {
      let body = loaded.crlf ? current.replace(/\n/g, "\r\n") : current;
      if (loaded.bom && !body.startsWith("﻿")) body = "﻿" + body;
      const e = await saveFile(site, path, body, loaded.version);
      setLoaded({ ...loaded, version: fileVersion(e) });
      setOriginal(current);
      setSaved(true);
      onSaved(e);
      return true;
    } catch (err) {
      setSaveError(
        err instanceof ApiError && err.code === "files_changed"
          ? "The file was changed by someone else since it was opened. Copy your changes, close the editor and open the file again."
          : filesMessage(err),
      );
      return false;
    } finally {
      setSaving(false);
    }
  }, [loaded, saving, doc, site, path, onSaved]);
  const saveRef = useRef(save);
  saveRef.current = save;

  useEffect(() => {
    if (!loaded || !host.current || editor.current) return;
    editor.current = createEditor({
      parent: host.current,
      doc: loaded.text.replace(/\r\n/g, "\n"),
      name: entry.name,
      wrap,
      label: "Content of " + entry.name,
      onChange: (d) => {
        setDoc(d);
        setSaved(false);
      },
      onCursor: (line, col) => setCursor({ line, col }),
    });
    editor.current.view.focus();
    return () => {
      editor.current?.destroy();
      editor.current = null;
    };
    // The editor is built once per loaded file; wrapping is switched below.
  }, [loaded !== null]);

  useEffect(() => {
    editor.current?.setWrap(wrap);
    storeWrap(wrap);
  }, [wrap]);

  // Leaving or reloading the page with unsaved changes: the browser asks.
  useEffect(() => {
    if (!dirty) return;
    const warn = (e: BeforeUnloadEvent) => {
      e.preventDefault();
    };
    window.addEventListener("beforeunload", warn);
    return () => window.removeEventListener("beforeunload", warn);
  }, [dirty]);

  const requestClose = () => {
    if (dirty) setAsking(true);
    else onClose();
  };

  return (
    <dialog
      ref={dialog}
      className="ui-editor"
      aria-labelledby="editor-title"
      onCancel={(e) => {
        // Esc: never close with unsaved changes without asking.
        e.preventDefault();
        if (asking) setAsking(false);
        else requestClose();
      }}
      onClick={(e) => {
        if (e.target === dialog.current) requestClose();
      }}
      onKeyDown={(e) => {
        if ((e.ctrlKey || e.metaKey) && !e.altKey && e.key.toLowerCase() === "s") {
          e.preventDefault();
          if (dirty) void saveRef.current();
        }
      }}
    >
      <div className="ui-editor-head">
        <div className="ui-editor-title">
          <h2 id="editor-title" className="ui-flush">
            {entry.name}
          </h2>
          <span className="ui-hint ui-mono">{path}</span>
        </div>
        <IconButton label="Close" tipBelow icon={<X size={18} aria-hidden="true" />} onClick={requestClose} />
      </div>
      <div className="ui-editor-body">
        {loadError ? (
          <div className="ui-card-body">
            <Notice tone="err" title={loadError} />
          </div>
        ) : loaded === null ? (
          <p className="ui-card-body" aria-busy="true">
            Loading…
          </p>
        ) : (
          <div ref={host} className="ui-editor-host" />
        )}
      </div>
      {asking && (
        <div className="ui-editor-ask" role="alertdialog" aria-labelledby="editor-ask">
          <strong id="editor-ask">Discard your changes?</strong>
          <span className="ui-hint">The changes to {entry.name} are not saved.</span>
          <div className="ui-row">
            <Button
              autoFocus
              onClick={() => {
                setAsking(false);
                editor.current?.view.focus();
              }}
            >
              Keep editing
            </Button>
            <Button
              variant="primary"
              disabled={saving}
              onClick={() =>
                void save().then((ok) => {
                  if (ok) onClose();
                  else setAsking(false);
                })
              }
            >
              Save and close
            </Button>
            <Button variant="danger" onClick={onClose}>
              Discard changes
            </Button>
          </div>
        </div>
      )}
      {saveError && (
        <div className="ui-editor-error">
          <Notice tone="err" title={saveError} />
        </div>
      )}
      <div className="ui-editor-foot">
        <div className="ui-editor-status" aria-live="polite">
          {loaded && (
            <>
              <span>
                Ln {cursor.line}, Col {cursor.col}
              </span>
              <span>{loaded.bom ? "UTF-8 with BOM" : "UTF-8"}</span>
              <span>{loaded.crlf ? "CRLF" : "LF"}</span>
              <span>{lang}</span>
              {dirty ? <Badge tone="warn">Unsaved changes</Badge> : saved && <Badge tone="ok">Saved</Badge>}
            </>
          )}
        </div>
        <label className="ui-check">
          <input type="checkbox" checked={wrap} onChange={(e) => setWrap(e.target.checked)} />
          Wrap lines
        </label>
        <div className="ui-row">
          <Button onClick={requestClose}>Close</Button>
          <Button variant="primary" disabled={!dirty || saving} onClick={() => void save()}>
            {saving ? "Saving…" : "Save changes"}
          </Button>
        </div>
      </div>
    </dialog>
  );
}
