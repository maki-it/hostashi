// SPDX-License-Identifier: AGPL-3.0-or-later

// Thin client for the panel API (/api/v1). The browser session is an
// HttpOnly cookie set by the server, so this code never sees a token.

export class ApiError extends Error {
  readonly status: number;
  readonly code: string;

  constructor(status: number, code: string, message: string) {
    super(message);
    this.name = "ApiError";
    this.status = status;
    this.code = code;
  }
}

type ErrorBody = { error?: { code?: unknown; message?: unknown } };

async function toApiError(res: Response): Promise<ApiError> {
  let code = "http_" + String(res.status);
  let message = res.statusText || "request failed";
  try {
    const body = (await res.json()) as ErrorBody;
    if (typeof body.error?.code === "string") code = body.error.code;
    if (typeof body.error?.message === "string") message = body.error.message;
  } catch {
    // Not JSON (proxy error page, network cut): keep the HTTP status.
  }
  return new ApiError(res.status, code, message);
}

export async function request<T>(method: "GET" | "POST" | "DELETE", path: string, body?: unknown): Promise<T> {
  const init: RequestInit = {
    method,
    credentials: "same-origin",
    headers: { Accept: "application/json" },
  };
  if (body !== undefined) {
    init.headers = { ...init.headers, "Content-Type": "application/json" };
    init.body = JSON.stringify(body);
  }
  const res = await fetch("/api/v1" + path, init);
  if (!res.ok) throw await toApiError(res);
  if (res.status === 204) return undefined as T;
  return (await res.json()) as T;
}

export type Health = {
  status: "ok" | "degraded";
  agent: string;
  db: string;
  version: string;
};

// getHealth also returns the body of a 503, which still describes the server.
export async function getHealth(): Promise<Health> {
  const res = await fetch("/api/v1/health", { headers: { Accept: "application/json" } });
  if (res.ok || res.status === 503) return (await res.json()) as Health;
  throw await toApiError(res);
}
