// SPDX-License-Identifier: AGPL-3.0-or-later

// The file manager of a site (ADR-0040). Every request goes to the panel,
// which checks the role and the site and passes it on to the site's helper;
// the helper runs as the site user, so files keep the site's owner.

import { ApiError, request } from "./api";
import { formatCount, formatNumber } from "./format";
import i18n, { areaErrorText } from "./i18n";


export type FileType = "file" | "dir" | "symlink" | "other";

export type FileEntry = {
  name: string;
  type: FileType;
  size: number;
  modified: string; // RFC 3339
  mode: string; // "0644"
  target?: string; // symlinks: shown only, never followed
  version?: string; // files: sent back when saving from the editor
};

export type Listing = {
  path: string;
  entries: FileEntry[];
  truncated: boolean;
  unnamed: number;
};

export type ExtractResult = { files: number; dirs: number; kept: number; skipped: number };

const base = (site: string) => "/sites/" + encodeURIComponent(site) + "/files";

const q = (params: Record<string, string>) => "?" + new URLSearchParams(params).toString();

/** joinPath joins a folder ("" is the home) and a name. */
export const joinPath = (dir: string, name: string) => (dir === "" ? name : dir + "/" + name);

/** parentPath is the folder of a path; the parent of a top-level entry is "". */
export const parentPath = (p: string) => {
  const i = p.lastIndexOf("/");
  return i < 0 ? "" : p.slice(0, i);
};

/** cleanPath gives the path the API accepts, or null: no "", ".", ".." or empty parts. */
export function cleanPath(p: string): string | null {
  const t = p.trim().replace(/^\/+/, "").replace(/\/+$/, "");
  if (t === "") return "";
  const parts = t.split("/");
  if (parts.some((c) => c === "" || c === "." || c === ".." || c.includes("\0") || new TextEncoder().encode(c).length > 255)) return null;
  return parts.join("/");
}

/** nameProblem checks one new name, mirroring files.ValidName. */
export function nameProblem(name: string): string {
  if (name.trim() !== name) return i18n.t("files.check.nameSpace");
  if (name === "" || name === "." || name === "..") return i18n.t("files.check.nameEmpty");
  if (name.includes("/")) return i18n.t("files.check.nameSlash");
  if (new TextEncoder().encode(name).length > 255) return i18n.t("files.check.nameLong");
  return "";
}

/** modeProblem checks permission bits in octal, mirroring files.ParseMode. */
export function modeProblem(mode: string): string {
  if (!/^[0-7]{3,4}$/.test(mode)) return i18n.t("files.check.modeDigits");
  if (mode.length === 4 && mode[0] !== "0") return i18n.t("files.check.modeSpecial");
  return "";
}

export const listFiles = (site: string, path: string) => request<Listing>("GET", base(site) + q({ path }));

export const mkdir = (site: string, path: string) => request<unknown>("POST", base(site) + "/mkdir", { path });
export const touch = (site: string, path: string) => request<unknown>("POST", base(site) + "/touch", { path });
export const rename = (site: string, from: string, to: string) => request<unknown>("POST", base(site) + "/rename", { from, to });
export const move = (site: string, paths: string[], to: string) => request<unknown>("POST", base(site) + "/move", { paths, to });
export const copy = (site: string, paths: string[], to: string) =>
  request<{ skipped: number }>("POST", base(site) + "/copy", { paths, to });
export const remove = (site: string, paths: string[]) => request<unknown>("POST", base(site) + "/delete", { paths });
export const chmod = (site: string, paths: string[], mode: string) => request<unknown>("POST", base(site) + "/chmod", { paths, mode });

/** The largest file the editor opens (files.MaxEditBytes). */
export const maxEditBytes = 1 << 20;

/** fileVersion is the version of an entry, "" when it has none. */
export const fileVersion = (e: FileEntry) => e.version ?? "";

/**
 * saveFile writes the editor's text. version is the one the file was
 * opened with; the helper refuses the save when the file changed since.
 */
export async function saveFile(site: string, path: string, text: string, version: string): Promise<FileEntry> {
  const params: Record<string, string> = { path };
  if (version !== "") params.version = version;
  const res = await fetch("/api/v1" + base(site) + "/content" + q(params), {
    method: "PUT",
    credentials: "same-origin",
    headers: { Accept: "application/json", "Content-Type": "text/plain; charset=utf-8" },
    body: text,
  });
  if (!res.ok) throw await errorOf(res);
  return (await res.json()) as FileEntry;
}

/** downloadUrl is the address of one file; the browser downloads it. */
export const downloadUrl = (site: string, path: string) => "/api/v1" + base(site) + "/content" + q({ path });

// Browsers and nginx accept addresses of about 8 KiB; a longer selection
// cannot go into one ZIP link.
export const maxZipUrl = 7000;

/** zipUrl is the address of a ZIP of the selection. */
export function zipUrl(site: string, paths: string[]): string {
  const sp = new URLSearchParams();
  for (const p of paths) sp.append("path", p);
  return "/api/v1" + base(site) + "/zip?" + sp.toString();
}

/** The chunk size of uploads (files.MaxChunk). */
export const chunkSize = 8 << 20;

type Upload = { id: string; received: number; done: boolean };

/**
 * uploadFile sends one file in chunks. A chunk that the helper did not
 * expect at that offset (a retry after a cut connection) is sent again from
 * where the helper is. onProgress gets the bytes sent so far.
 */
export async function uploadFile(
  site: string,
  path: string,
  file: Blob,
  opts: { overwrite?: boolean; parents?: boolean; onProgress?: (sent: number) => void; signal?: AbortSignal } = {},
): Promise<void> {
  const overwrite = opts.overwrite ?? false;
  const start = await request<Upload>("POST", base(site) + "/upload-start", {
    path,
    size: file.size,
    overwrite,
    parents: opts.parents ?? false,
  });
  if (start.done) {
    opts.onProgress?.(file.size);
    return;
  }
  let offset = 0;
  let retries = 0;
  try {
    while (offset < file.size) {
      if (opts.signal?.aborted) throw new DOMException("upload cancelled", "AbortError");
      const end = Math.min(offset + chunkSize, file.size);
      const params: Record<string, string> = { path, id: start.id, offset: String(offset), size: String(file.size) };
      if (overwrite) params.overwrite = "1";
      const init: RequestInit = { method: "PUT", credentials: "same-origin", body: file.slice(offset, end), headers: { Accept: "application/json" } };
      if (opts.signal) init.signal = opts.signal;
      const res = await fetch("/api/v1" + base(site) + "/upload" + q(params), init);
      if (res.status === 409) {
        const got = res.headers.get("X-Hostashi-Received");
        if (got !== null && retries < 3) {
          retries++;
          offset = Number(got);
          continue;
        }
      }
      if (!res.ok) throw await errorOf(res);
      const u = (await res.json()) as Upload;
      offset = u.received;
      retries = 0;
      opts.onProgress?.(offset);
      if (u.done) return;
    }
  } catch (err) {
    await request("POST", base(site) + "/upload-cancel", { path, id: start.id }).catch(() => undefined);
    throw err;
  }
}

async function errorOf(res: Response): Promise<ApiError> {
  try {
    const b = (await res.json()) as { error?: { code?: string; message?: string } };
    return new ApiError(res.status, b.error?.code ?? "http_" + String(res.status), b.error?.message ?? res.statusText);
  } catch {
    return new ApiError(res.status, "http_" + String(res.status), res.statusText || "request failed");
  }
}

type ExtractLine = { done?: boolean; entries?: number; total?: number; result?: ExtractResult; error?: string; message?: string };

/** extract unpacks a ZIP into a folder and reports progress as it goes. */
export async function extract(
  site: string,
  path: string,
  to: string,
  overwrite: boolean,
  onProgress?: (entries: number, total: number) => void,
): Promise<ExtractResult> {
  const res = await fetch("/api/v1" + base(site) + "/extract", {
    method: "POST",
    credentials: "same-origin",
    headers: { Accept: "application/x-ndjson", "Content-Type": "application/json" },
    body: JSON.stringify({ path, to, overwrite }),
  });
  if (!res.ok) throw await errorOf(res);
  const text = await readLines(res, (l) => {
    if (!l.done && l.total) onProgress?.(l.entries ?? 0, l.total);
  });
  const last = text.at(-1);
  if (!last?.done) throw new ApiError(502, "files_stopped", "the extraction stopped without a result");
  if (last.error) throw new ApiError(422, "files_" + last.error, last.message ?? "extraction failed");
  return last.result ?? { files: 0, dirs: 0, kept: 0, skipped: 0 };
}

async function readLines(res: Response, each: (l: ExtractLine) => void): Promise<ExtractLine[]> {
  const out: ExtractLine[] = [];
  const add = (s: string) => {
    if (s.trim() === "") return;
    const l = JSON.parse(s) as ExtractLine;
    out.push(l);
    each(l);
  };
  if (!res.body) {
    for (const s of (await res.text()).split("\n")) add(s);
    return out;
  }
  const reader = res.body.getReader();
  const dec = new TextDecoder();
  let buf = "";
  for (;;) {
    const { value, done } = await reader.read();
    if (done) break;
    buf += dec.decode(value, { stream: true });
    let i: number;
    while ((i = buf.indexOf("\n")) >= 0) {
      add(buf.slice(0, i));
      buf = buf.slice(i + 1);
    }
  }
  add(buf);
  return out;
}

/** filesMessage turns an API error into a sentence for the file manager. */
export function filesMessage(err: unknown): string {
  if (!(err instanceof ApiError)) return i18n.t("files.errors.unreachable");
  switch (err.code) {
    case "files_too_large":
      return i18n.t("files.errors.files_too_large", { detail: err.message.replace(/^files: (too large: )?/, "") });
    case "files_timeout":
      return i18n.t("files.errors.files_unavailable");
    default:
      return areaErrorText("files.errors", err);
  }
}

/** formatSize shows a byte count, binary units. */
export function formatSize(n: number): string {
  if (n < 1024) return formatCount(n) + " B";
  const units = ["KiB", "MiB", "GiB", "TiB"];
  let v = n / 1024;
  let u = 0;
  while (v >= 1024 && u < units.length - 1) {
    v /= 1024;
    u++;
  }
  const digits = v < 10 ? 1 : 0;
  return formatNumber(v, { minimumFractionDigits: digits, maximumFractionDigits: digits }) + " " + units[u];

}
