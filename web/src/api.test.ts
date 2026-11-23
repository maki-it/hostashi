// SPDX-License-Identifier: AGPL-3.0-or-later

import { describe, expect, it, vi } from "vitest";
import { ApiError, RawJSON, request } from "./api";
import { mockFetch } from "./test-utils";

describe("request", () => {
  it("sends JSON and returns the parsed body", async () => {
    const calls = mockFetch({ "POST /api/v1/x": { body: { ok: true } } });
    await expect(request("POST", "/x", { a: 1 })).resolves.toEqual({ ok: true });
    expect(calls).toEqual([{ method: "POST", path: "/api/v1/x", body: { a: 1 } }]);
  });

  it("sends JSON text as it is", async () => {
    const fn = vi.fn(async () => new Response("{}", { status: 201 }));
    vi.stubGlobal("fetch", fn);
    await request("POST", "/x", new RawJSON('{"format":"a"}'));
    const init = (fn.mock.calls[0] as unknown as [string, RequestInit])[1];
    expect(init.body).toBe('{"format":"a"}');
    expect(init.headers).toMatchObject({ "Content-Type": "application/json" });
  });

  it("returns undefined for 204", async () => {
    mockFetch({ "POST /api/v1/x": { status: 204 } });
    await expect(request("POST", "/x")).resolves.toBeUndefined();
  });

  it("returns undefined for 202 without a body", async () => {
    vi.stubGlobal("fetch", vi.fn(async () => new Response("", { status: 202 })));
    await expect(request("POST", "/backups/run")).resolves.toBeUndefined();
  });

  it("still parses a 202 with a body", async () => {
    vi.stubGlobal("fetch", vi.fn(async () => new Response('{"id":"j1"}', { status: 202 })));
    await expect(request("POST", "/x")).resolves.toEqual({ id: "j1" });
  });

  it("turns the server error body into an ApiError", async () => {
    mockFetch({ "GET /api/v1/x": { status: 401, body: { error: { code: "unauthenticated", message: "not logged in" } } } });
    const err = await request("GET", "/x").catch((e: unknown) => e);
    expect(err).toBeInstanceOf(ApiError);
    expect(err).toMatchObject({ status: 401, code: "unauthenticated", message: "not logged in" });
  });

  it("keeps the HTTP status when the error body is not JSON", async () => {
    vi.stubGlobal("fetch", vi.fn(async () => new Response("<html>bad gateway</html>", { status: 502, statusText: "Bad Gateway" })));
    await expect(request("GET", "/x")).rejects.toMatchObject({ status: 502, code: "http_502", message: "Bad Gateway" });
  });

  it("sends no body and no content type without a body", async () => {
    const fn = vi.fn(async (_input: RequestInfo | URL, _init?: RequestInit) => new Response("{}", { status: 200 }));
    vi.stubGlobal("fetch", fn);
    await request("GET", "/x");
    const init = fn.mock.calls[0]?.[1];
    expect(init?.body).toBeUndefined();
    expect(init?.headers).toEqual({ Accept: "application/json" });
    expect(init?.credentials).toBe("same-origin");
  });
});
