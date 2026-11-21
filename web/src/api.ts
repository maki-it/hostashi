// SPDX-License-Identifier: AGPL-3.0-or-later

// Thin client for the panel API (/api/v1). The browser session is an
// HttpOnly cookie set by the server, so this code never sees a token.

export class ApiError extends Error {
  readonly status: number;
  readonly code: string;
  // body is the parsed error response, for errors that carry details such
  // as the rejected lines of a snippet.
  readonly body: unknown;

  constructor(status: number, code: string, message: string, body?: unknown) {
    super(message);
    this.name = "ApiError";
    this.status = status;
    this.code = code;
    this.body = body;
  }
}

// RawJSON is a request body that is JSON text already, like an uploaded
// file the server checks itself.
export class RawJSON {
  readonly text: string;
  constructor(text: string) {
    this.text = text;
  }
}

type ErrorBody = { error?: { code?: unknown; message?: unknown } };

async function toApiError(res: Response): Promise<ApiError> {
  let code = "http_" + String(res.status);
  let message = res.statusText || "request failed";
  let raw: unknown;
  try {
    raw = await res.json();
    const body = raw as ErrorBody;
    if (typeof body.error?.code === "string") code = body.error.code;
    if (typeof body.error?.message === "string") message = body.error.message;
  } catch {
    // Not JSON (proxy error page, network cut): keep the HTTP status.
  }
  return new ApiError(res.status, code, message, raw);
}

export async function request<T>(method: "GET" | "POST" | "PUT" | "PATCH" | "DELETE", path: string, body?: unknown): Promise<T> {
  const init: RequestInit = {
    method,
    credentials: "same-origin",
    headers: { Accept: "application/json" },
  };
  if (body instanceof FormData) {
    // The browser sets multipart/form-data with its boundary.
    init.body = body;
  } else if (body instanceof RawJSON) {
    init.headers = { ...init.headers, "Content-Type": "application/json" };
    init.body = body.text;
  } else if (body !== undefined) {
    init.headers = { ...init.headers, "Content-Type": "application/json" };
    init.body = JSON.stringify(body);
  }
  const res = await fetch("/api/v1" + path, init);
  if (!res.ok) throw await toApiError(res);
  if (res.status === 204) return undefined as T;
  return (await res.json()) as T;
}
