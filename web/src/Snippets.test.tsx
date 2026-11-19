// SPDX-License-Identifier: AGPL-3.0-or-later

import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import { SnippetSettings } from "./Snippets";
import type { Site } from "./sites";
import { sizeProblem } from "./snippets";
import { mockFetch } from "./test-utils";

const site: Site = {
  name: "blog",
  pool: "default",
  state: "active",
  php: "8.4",
  disabled: false,
  maintenance: { enabled: false, page: "builtin-maintenance", allowIps: [] },
  domains: ["blog.example.com"],
  tls: { enabled: false, active: false, hsts: false, domains: [], notAfter: null },
  createdAt: "2026-11-19T08:00:00Z",
  updatedAt: "2026-11-19T08:00:00Z",
};

describe("SnippetSettings", () => {
  it("loads only when opened and saves both snippets", async () => {
    const calls = mockFetch({
      "GET /api/v1/sites/blog/snippets": { body: { server: "add_header X-A b;", root: "" } },
      "PUT /api/v1/sites/blog/snippets": (b) => ({ body: { ...(b as object), updatedAt: "2026-11-19T22:00:00Z" } }),
    });
    const user = userEvent.setup();
    render(<SnippetSettings site={site} />);
    expect(calls).toHaveLength(0);

    await user.click(screen.getByRole("button", { name: "Edit snippets" }));
    const server = await screen.findByRole("textbox", { name: /Inside the server block/ });
    expect(server).toHaveValue("add_header X-A b;");

    await user.type(screen.getByRole("textbox", { name: /Inside location \// }), "expires 1h;");
    await user.click(screen.getByRole("button", { name: "Save snippets" }));

    expect(await screen.findByRole("status")).toHaveTextContent("Snippets saved and active.");
    expect(calls.at(-1)).toEqual({ method: "PUT", path: "/api/v1/sites/blog/snippets", body: { server: "add_header X-A b;", root: "expires 1h;" } });
  });

  it("shows every rejected line", async () => {
    mockFetch({
      "GET /api/v1/sites/blog/snippets": { body: { server: "", root: "" } },
      "PUT /api/v1/sites/blog/snippets": {
        status: 422,
        body: {
          error: { code: "snippet_rejected", message: "a snippet contains directives that are not allowed" },
          problems: { server: [{ line: 2, msg: '"listen" is not allowed in custom snippets' }], root: [{ line: 1, msg: "root: must be inside the site's home directory" }] },
        },
      },
    });
    const user = userEvent.setup();
    render(<SnippetSettings site={site} />);
    await user.click(screen.getByRole("button", { name: "Edit snippets" }));
    await user.type(await screen.findByRole("textbox", { name: /Inside the server block/ }), "add_header X 1;{enter}listen 81;");
    await user.click(screen.getByRole("button", { name: "Save snippets" }));

    expect(await screen.findByRole("alert")).toHaveTextContent("Some lines are not allowed. Nothing was saved.");
    expect(screen.getByText(/Line 2: "listen" is not allowed/)).toBeInTheDocument();
    expect(screen.getByText(/Line 1: root: must be inside/)).toBeInTheDocument();
    expect(screen.getByRole("textbox", { name: /Inside the server block/ })).toHaveAttribute("aria-invalid", "true");
  });

  it("says when the web server refuses", async () => {
    mockFetch({
      "GET /api/v1/sites/blog/snippets": { body: { server: "", root: "" } },
      "PUT /api/v1/sites/blog/snippets": { status: 409, body: { error: { code: "agent_precondition", message: "nginx rejected" } } },
    });
    const user = userEvent.setup();
    render(<SnippetSettings site={site} />);
    await user.click(screen.getByRole("button", { name: "Edit snippets" }));
    await user.click(await screen.findByRole("button", { name: "Save snippets" }));
    expect(await screen.findByRole("alert")).toHaveTextContent("previous snippets stay active");
  });
});

describe("sizeProblem", () => {
  it("limits each snippet to 64 KiB", () => {
    expect(sizeProblem({ server: "a".repeat(64 * 1024), root: "" })).toBe("");
    expect(sizeProblem({ server: "", root: "a".repeat(64 * 1024 + 1) })).toContain("64 KiB");
    expect(sizeProblem({ server: "ä".repeat(32 * 1024 + 1), root: "" })).toContain("64 KiB");
  });
});
