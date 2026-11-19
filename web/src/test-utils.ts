// SPDX-License-Identifier: AGPL-3.0-or-later

import { vi } from "vitest";

export type Route = { status?: number; body?: unknown };

// mockFetch answers requests by "METHOD /path" and records them. Unknown
// routes fail the test loudly instead of hanging.
export function mockFetch(routes: Record<string, Route | ((body: unknown) => Route)>) {
  const calls: { method: string; path: string; body: unknown }[] = [];
  const fn = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
    const method = (init?.method ?? "GET").toUpperCase();
    const path = typeof input === "string" ? input : input instanceof URL ? input.pathname : input.url;
    const body =
      typeof init?.body === "string" ? (JSON.parse(init.body) as unknown) : init?.body instanceof FormData ? init.body : undefined;
    calls.push({ method, path, body });
    const entry = routes[method + " " + path];
    if (!entry) throw new Error("unexpected request " + method + " " + path);
    const r = typeof entry === "function" ? entry(body) : entry;
    const status = r.status ?? 200;
    return new Response(status === 204 ? null : JSON.stringify(r.body ?? {}), {
      status,
      headers: { "Content-Type": "application/json" },
    });
  });
  vi.stubGlobal("fetch", fn);
  return calls;
}
