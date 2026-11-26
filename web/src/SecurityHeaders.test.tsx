// SPDX-License-Identifier: AGPL-3.0-or-later

import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import { fromForm, toForm, type HeadersView, type WebHeaders } from "./headers";
import { SecurityHeadersCard } from "./SecurityHeaders";
import type { Site } from "./sites";
import { mockFetch } from "./test-utils";

const site: Site = {
  name: "blog",
  pool: "default",
  state: "active",
  php: "8.4",
  phpPrograms: false,
  disabled: false,
  disabledPage: "builtin-maintenance",
  maintenance: { enabled: false, page: "builtin-maintenance", allowIps: [] },
  domains: ["blog.example.com"],
  tls: {
    enabled: true,
    active: true,
    hsts: false,
    domains: [],
    notAfter: null,
  },
  createdAt: "2026-11-19T08:00:00Z",
  updatedAt: "2026-11-19T08:00:00Z",
};

const strict = "camera=(), microphone=(), geolocation=(), payment=(), usb=()";
const defaults: WebHeaders = {
  noSniff: true,
  frameOptions: "SAMEORIGIN",
  referrerPolicy: "strict-origin-when-cross-origin",
};
const view = (headers: WebHeaders): HeadersView => ({
  headers,
  defaults,
  referrerPolicies: [
    "no-referrer",
    "origin",
    "same-origin",
    "strict-origin-when-cross-origin",
  ],
  permissionsPolicyStrict: strict,
  maxValueBytes: 4096,
});
const path = "/api/v1/sites/blog/headers";

describe("headers form", () => {
  it("round-trips headers", () => {
    for (const h of [
      {},
      defaults,
      {
        permissionsPolicy: strict,
        csp: "default-src 'self'",
        cspReportOnly: true,
      },
      { permissionsPolicy: "camera=()", csp: "default-src 'self'" },
      { noSniff: true, noIndex: true },
    ] satisfies WebHeaders[]) {
      expect(fromForm(toForm(h, strict), strict)).toEqual({ headers: h });
    }
    expect(toForm({ permissionsPolicy: strict }, strict).permissions).toBe(
      "strict",
    );
    expect(toForm({ permissionsPolicy: "usb=()" }, strict).permissions).toBe(
      "own",
    );
  });

  it("refuses an own value left empty", () => {
    expect(
      fromForm({ ...toForm({}, strict), csp: "report", cspValue: " " }, strict)
        .error,
    ).toMatch(/Content-Security-Policy/);
    expect(
      fromForm({ ...toForm({}, strict), permissions: "own" }, strict).error,
    ).toMatch(/Permissions-Policy/);
  });
});

describe("SecurityHeadersCard", () => {
  it("saves a report-only policy", async () => {
    const calls = mockFetch({
      ["GET " + path]: { body: view(defaults) },
      ["PUT " + path]: (body) => ({ body: view(body as WebHeaders) }),
    });
    const user = userEvent.setup();
    render(<SecurityHeadersCard site={site} />);

    expect(
      await screen.findByRole("combobox", { name: "Show in frames" }),
    ).toHaveValue("SAMEORIGIN");
    expect(
      screen.queryByRole("textbox", { name: "Policy" }),
    ).not.toBeInTheDocument();

    await user.selectOptions(
      screen.getByRole("combobox", { name: "Mode" }),
      "report",
    );
    await user.type(
      screen.getByRole("textbox", { name: "Policy" }),
      "default-src 'self'",
    );
    expect(screen.queryByText(/breaks the site/)).not.toBeInTheDocument();
    await user.click(
      screen.getByRole("button", { name: "Save security headers" }),
    );

    expect(await screen.findByRole("status")).toHaveTextContent("Saved.");
    expect(calls).toContainEqual({
      method: "PUT",
      path,
      body: { ...defaults, csp: "default-src 'self'", cspReportOnly: true },
    });
  });

  it("warns before enforcing a policy", async () => {
    mockFetch({ ["GET " + path]: { body: view({}) } });
    const user = userEvent.setup();
    render(<SecurityHeadersCard site={site} />);

    await user.selectOptions(
      await screen.findByRole("combobox", { name: "Mode" }),
      "enforce",
    );
    expect(screen.getByText(/breaks the site at once/)).toBeInTheDocument();
  });

  it("shows a snippet conflict", async () => {
    mockFetch({
      ["GET " + path]: { body: view({}) },
      ["PUT " + path]: {
        status: 422,
        body: {
          error: {
            code: "header_conflict",
            message:
              "header conflict: the server snippet sets X-Frame-Options, which is switched on as a security header",
          },
        },
      },
    });
    const user = userEvent.setup();
    render(<SecurityHeadersCard site={site} />);

    await user.click(
      await screen.findByRole("button", { name: "Use recommended" }),
    );
    expect(await screen.findByRole("alert")).toHaveTextContent(
      "The server snippet sets X-Frame-Options, which is switched on as a security header. Remove it from the snippet first.",
    );
  });
});
