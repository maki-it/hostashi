// SPDX-License-Identifier: AGPL-3.0-or-later

import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import { Parked } from "./Parked";
import type { ParkedDomain } from "./parked";
import type { StatusPage } from "./pages";

import { mockFetch } from "./test-utils";

const page = (id: string, name: string, kind: StatusPage["kind"]): StatusPage => ({
  id,
  name,
  kind,
  builtin: id.startsWith("builtin-"),
  sha256: "a".repeat(64),
  files: [{ path: "index.html", size: 900 }],
  sites: [],
  parked: [],
  createdAt: null,
  updatedAt: null,
});
const pages = [
  page("builtin-maintenance", "Built-in maintenance page", "maintenance"),
  page("builtin-parking", "Built-in parking page", "parking"),
  page("p2", "For sale", "parking"),
];
const parkedDomain = (domain: string, tls: ParkedDomain["tls"]): ParkedDomain => ({
  domain,
  page: "builtin-parking",
  tls,
  createdAt: "2026-11-19T08:00:00Z",
  updatedAt: "2026-11-19T08:00:00Z",
});

describe("Parked", () => {
  it("parks a domain with HTTPS and offers only parking pages", async () => {
    const user = userEvent.setup();
    let list: ParkedDomain[] = [];
    const calls = mockFetch({
      "GET /api/v1/parked-domains": () => ({ body: { parkedDomains: list } }),
      "GET /api/v1/status-pages": { body: { pages } },
      "PUT /api/v1/parked-domains/old.example": (body) => {
        const b = body as { page: string; tls: boolean };
        list = [{ ...parkedDomain("old.example", { enabled: b.tls, active: false, notAfter: null }), page: b.page }];
        return { body: list[0] };
      },
    });
    render(<Parked />);
    expect(await screen.findByText("No domain is parked.")).toBeInTheDocument();

    const form = within(screen.getByRole("region", { name: "Park a domain" }));
    const select = form.getByRole("combobox", { name: "Page" });
    expect(within(select).getAllByRole("option").map((o) => o.textContent)).toEqual(["Built-in parking page", "For sale"]);

    await user.type(form.getByRole("textbox", { name: /^Domain/ }), "localhost");
    await user.click(form.getByRole("button", { name: "Park domain" }));
    expect(form.getByRole("alert")).toHaveTextContent("needs at least two parts");

    await user.clear(form.getByRole("textbox", { name: /^Domain/ }));
    await user.type(form.getByRole("textbox", { name: /^Domain/ }), "Old.Example");
    await user.selectOptions(select, "p2");
    await user.click(form.getByRole("button", { name: "Park domain" }));
    expect(await form.findByRole("status")).toHaveTextContent("old.example is parked. It is served over HTTP until its certificate is there.");
    expect(calls.find((c) => c.method === "PUT")?.body).toEqual({ page: "p2", tls: true });

    const row = within(await screen.findByRole("region", { name: "Parked domains" }));
    expect(row.getByText("old.example")).toBeInTheDocument();
    expect(row.getByText("For sale")).toBeInTheDocument();
    expect(row.getByText("Waiting for the certificate")).toBeInTheDocument();
  });

  it("shows a failed certificate, changes and unparks a domain", async () => {
    const user = userEvent.setup();
    let list = [parkedDomain("old.example", { enabled: true, active: false, notAfter: null, error: "no DNS record", failedAt: "2026-11-19T09:00:00Z" })];
    const calls = mockFetch({
      "GET /api/v1/parked-domains": () => ({ body: { parkedDomains: list } }),
      "GET /api/v1/status-pages": { body: { pages } },
      "PUT /api/v1/parked-domains/old.example": (body) => {
        const b = body as { page: string; tls: boolean };
        list = [{ ...parkedDomain("old.example", { enabled: b.tls, active: false, notAfter: null }), page: b.page }];
        return { body: list[0] };
      },
      "DELETE /api/v1/parked-domains/old.example": () => {
        list = [];
        return { status: 204 };
      },
    });
    render(<Parked />);
    expect(await screen.findByText(/Last attempt failed: no DNS record/)).toBeInTheDocument();
    expect(screen.getByText("Waiting: the last attempt failed")).toBeInTheDocument();

    await user.click(screen.getByRole("button", { name: "Change" }));
    await user.click(screen.getByRole("checkbox", { name: "HTTPS for old.example" }));
    await user.click(screen.getByRole("button", { name: "Save" }));
    expect(await screen.findByRole("status")).toHaveTextContent("Saved.");
    expect(calls.find((c) => c.method === "PUT")?.body).toEqual({ page: "builtin-parking", tls: false });
    expect(await screen.findByText("Off")).toBeInTheDocument();

    await user.click(screen.getByRole("button", { name: "Unpark" }));
    await user.click(screen.getByRole("button", { name: "Unpark old.example" }));
    expect(await screen.findByText("No domain is parked.")).toBeInTheDocument();
  });

  it("explains a taken domain", async () => {
    const user = userEvent.setup();
    mockFetch({
      "GET /api/v1/parked-domains": { body: { parkedDomains: [] } },
      "GET /api/v1/status-pages": { body: { pages } },
      "PUT /api/v1/parked-domains/blog.example": { status: 409, body: { error: { code: "domain_taken", message: "taken" } } },
    });
    render(<Parked />);
    const form = within(await screen.findByRole("region", { name: "Park a domain" }));
    await user.type(form.getByRole("textbox", { name: /^Domain/ }), "blog.example");
    await user.click(form.getByRole("button", { name: "Park domain" }));
    expect(await form.findByRole("alert")).toHaveTextContent("A site or the panel already uses this domain.");
  });
});
