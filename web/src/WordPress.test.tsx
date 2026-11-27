// SPDX-License-Identifier: AGPL-3.0-or-later

import { render, screen, within } from "@testing-library/react";
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
    const card = screen.getByRole("region", { name: "WordPress" });
    const box = within(card).getByRole("region", { name: "Save the WordPress admin password now" });
    expect(within(box).getByRole("textbox", { name: "Admin password" })).toHaveValue("Vq7mR2xK9pLt4Wz8Bn3sHc6J");
    expect(within(box).getByRole("button", { name: "Copy password" })).toBeInTheDocument();
    expect(within(box).getByRole("link", { name: "https://blog.example.com/wp-admin/" })).toHaveAttribute("href", "https://blog.example.com/wp-admin/");
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
    mockFetch({
      ["GET " + path]: { body: { state: "succeeded", installed: true, empty: false, log: "Success: WordPress installed successfully." } },
    });
    const { unmount } = render(<WordPressCard site={site} />);
    expect(await screen.findByText("WordPress is installed.")).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Install WordPress" })).not.toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Reinstall WordPress" })).toBeInTheDocument();
    unmount();

    // A failed install with an empty document root can run again.
    mockFetch({
      ["GET " + path]: {
        body: { state: "failed", error: "core install failed", empty: true, log: "Error: Error establishing a database connection." },
      },
    });
    render(<WordPressCard site={site} />);
    expect(await screen.findByText("The install failed: core install failed.")).toBeInTheDocument();
    expect(screen.getByLabelText("Install log")).toHaveTextContent("database connection");
    expect(screen.getByRole("button", { name: "Install WordPress" })).toBeInTheDocument();
  });

  it("offers the install again when WordPress is gone", async () => {
    mockFetch({ ["GET " + path]: { body: { state: "none", installed: false, empty: true } } });
    render(<WordPressCard site={site} />);
    expect(await screen.findByRole("button", { name: "Install WordPress" })).toBeInTheDocument();
    expect(screen.queryByText("WordPress is installed.")).not.toBeInTheDocument();
  });

  it("reinstalls after the site's name is typed", async () => {
    const calls = mockFetch({
      ["GET " + path]: { body: { state: "succeeded", installed: true, empty: false, database: "blog_wp" } },
      ["POST " + path + "/reinstall"]: {
        status: 202,
        body: { state: "running", database: "blog_wp2", droppedDatabase: "blog_wp", adminPassword: "Vq7mR2xK9pLt4Wz8Bn3sHc6J" },
      },
    });
    const user = userEvent.setup();
    render(<WordPressCard site={site} />);

    await user.click(await screen.findByRole("button", { name: "Reinstall WordPress" }));
    expect(screen.getByText(/Everything in the document root is deleted/)).toBeInTheDocument();
    expect(screen.getByText("blog_wp")).toBeInTheDocument();

    await user.type(screen.getByRole("textbox", { name: /Admin user/ }), "kim");
    await user.type(screen.getByRole("textbox", { name: "Admin email" }), "kim@example.com");

    const submit = screen.getByRole("button", { name: "Reinstall WordPress" });
    expect(submit).toBeDisabled();
    await user.type(screen.getByRole("textbox", { name: /to confirm/ }), "blo");
    expect(submit).toBeDisabled();
    await user.type(screen.getByRole("textbox", { name: /to confirm/ }), "g");
    await user.click(submit);

    expect(await screen.findByText("Save the WordPress admin password now")).toBeInTheDocument();
    expect(calls.at(-1)?.body).toMatchObject({ confirm: "blog", adminUser: "kim", adminEmail: "kim@example.com" });
    expect(screen.getByText("WordPress is being installed. This takes about a minute.")).toBeInTheDocument();
  });

  it("offers a reinstall for other files in the document root", async () => {
    mockFetch({ ["GET " + path]: { body: { state: "none", installed: false, empty: false } } });
    const user = userEvent.setup();
    render(<WordPressCard site={site} />);

    expect(await screen.findByText("The document root holds files, but no WordPress.")).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Install WordPress" })).not.toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Reinstall WordPress" }));
    expect(screen.getByText(/The site's databases stay/)).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Cancel" }));
    expect(screen.queryByRole("textbox", { name: /to confirm/ })).not.toBeInTheDocument();
  });
});
