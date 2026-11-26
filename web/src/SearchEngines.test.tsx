// SPDX-License-Identifier: AGPL-3.0-or-later

import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import { SearchEnginesCard } from "./SearchEngines";
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
  tls: { enabled: false, active: false, hsts: false, domains: [], notAfter: null },
  createdAt: "2026-11-19T08:00:00Z",
  updatedAt: "2026-11-19T08:00:00Z",
};

const path = "/api/v1/sites/blog/headers";
const view = (headers: object) => ({ headers, defaults: {}, referrerPolicies: [], permissionsPolicyStrict: "", maxValueBytes: 1024 });

describe("SearchEnginesCard", () => {
  it("switches noindex and keeps the other headers", async () => {
    const calls = mockFetch({
      ["GET " + path]: { body: view({ noSniff: true, frameOptions: "SAMEORIGIN" }) },
      ["PUT " + path]: (body) => ({ body: view(body as object) }),
    });
    const user = userEvent.setup();
    render(<SearchEnginesCard site={site} />);

    const box = await screen.findByRole("checkbox", { name: "Ask search engines not to index the site" });
    expect(box).not.toBeChecked();
    expect(screen.getByRole("button", { name: "Save" })).toBeDisabled();
    await user.click(box);
    await user.click(screen.getByRole("button", { name: "Save" }));

    expect(await screen.findByRole("status")).toHaveTextContent("Search engines are asked not to index the site.");
    expect(calls).toContainEqual({ method: "PUT", path, body: { noSniff: true, frameOptions: "SAMEORIGIN", noIndex: true } });
  });

  it("turns noindex off for a staging copy", async () => {
    const calls = mockFetch({
      ["GET " + path]: { body: view({ noIndex: true }) },
      ["PUT " + path]: (body) => ({ body: view(body as object) }),
    });
    const user = userEvent.setup();
    render(<SearchEnginesCard site={site} />);

    const box = await screen.findByRole("checkbox", { name: "Ask search engines not to index the site" });
    expect(box).toBeChecked();
    await user.click(box);
    await user.click(screen.getByRole("button", { name: "Save" }));

    expect(await screen.findByRole("status")).toHaveTextContent("Search engines may index the site.");
    expect(calls).toContainEqual({ method: "PUT", path, body: {} });
  });
});
