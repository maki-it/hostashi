// SPDX-License-Identifier: AGPL-3.0-or-later

import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import type { Site } from "./sites";
import { mockFetch } from "./test-utils";
import { WordPressCard } from "./WordPress";

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
  tls: { enabled: true, active: true, hsts: false, domains: [], notAfter: null },
  createdAt: "2026-11-19T08:00:00Z",
  updatedAt: "2026-11-19T08:00:00Z",
};
const path = "/api/v1/sites/blog/wordpress";

describe("WordPressCard", () => {
  it("installs with a generated password and shows it once", async () => {
    const calls = mockFetch({
      ["GET " + path]: { body: { state: "none" } },
      ["POST " + path]: { status: 202, body: { state: "running", database: "blog_wp", adminPassword: "Vq7mR2xK9pLt4Wz8Bn3sHc6J" } },
    });
    const user = userEvent.setup();
    render(<WordPressCard site={site} />);

    expect(await screen.findByRole("textbox", { name: "Site title" })).toHaveValue("blog.example.com");
    await user.selectOptions(screen.getByRole("combobox", { name: "Language" }), "de_DE");
    await user.type(screen.getByRole("textbox", { name: /Admin user/ }), "kim");
    await user.type(screen.getByRole("textbox", { name: "Admin email" }), "kim@example.com");
    await user.click(screen.getByRole("checkbox", { name: /search engines/ }));
    await user.click(screen.getByRole("button", { name: "Install WordPress" }));

    expect(await screen.findByRole("heading", { name: "Save the WordPress admin password now" })).toBeInTheDocument();
    expect(screen.getByDisplayValue("Vq7mR2xK9pLt4Wz8Bn3sHc6J")).toBeInTheDocument();
    expect(screen.getByText("https://blog.example.com/wp-admin/")).toBeInTheDocument();
    expect(screen.getByText(/being installed/)).toBeInTheDocument();
    expect(calls).toContainEqual({
      method: "POST",
      path,
      body: { locale: "de_DE", title: "blog.example.com", adminUser: "kim", adminEmail: "kim@example.com", discourage: true },
    });

    await user.click(screen.getByRole("button", { name: "I have saved it" }));
    expect(screen.queryByDisplayValue("Vq7mR2xK9pLt4Wz8Bn3sHc6J")).not.toBeInTheDocument();
  });

  it("sends an own password and shows a refusal", async () => {
    const calls = mockFetch({
      ["GET " + path]: { body: { state: "none" } },
      ["POST " + path]: {
        status: 409,
        body: { error: { code: "agent_precondition", message: "wpunit: the document root is not empty" } },
      },
    });
    const user = userEvent.setup();
    render(<WordPressCard site={site} />);

    await user.type(await screen.findByRole("textbox", { name: /Admin user/ }), "kim");
    await user.type(screen.getByRole("textbox", { name: "Admin email" }), "kim@example.com");
    await user.click(screen.getByRole("radio", { name: "Set my own password" }));
    await user.type(screen.getByLabelText(/Admin password/), "correct horse battery");
    await user.click(screen.getByRole("button", { name: "Install WordPress" }));

    expect(await screen.findByRole("alert")).toHaveTextContent("The document root is not empty");
    expect(calls.at(-1)?.body).toMatchObject({ adminPassword: "correct horse battery" });
  });

  it("shows a finished and a failed install", async () => {
    mockFetch({ ["GET " + path]: { body: { state: "succeeded", log: "Success: WordPress installed successfully." } } });
    const { unmount } = render(<WordPressCard site={site} />);
    expect(await screen.findByText("WordPress is installed.")).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Install WordPress" })).not.toBeInTheDocument();
    unmount();

    mockFetch({ ["GET " + path]: { body: { state: "failed", error: "core install failed", log: "Error: Error establishing a database connection." } } });
    render(<WordPressCard site={site} />);
    expect(await screen.findByText("The install failed: core install failed.")).toBeInTheDocument();
    expect(screen.getByLabelText("Install log")).toHaveTextContent("database connection");
  });
});
