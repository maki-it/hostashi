// SPDX-License-Identifier: AGPL-3.0-or-later

import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import { PermissionsResetCard } from "./PermissionsReset";
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

const path = "/api/v1/sites/blog/permissions/reset";

describe("PermissionsResetCard", () => {
  it("resets only after the site's name is typed and shows the skipped entries", async () => {
    const calls = mockFetch({
      ["POST " + path]: {
        body: { checked: 1234, changed: 56, skipped: 2, skippedPaths: [{ path: "htdocs/mnt", reason: "on another file system" }, { path: "htdocs/null", reason: "device file" }] },
      },
    });
    const user = userEvent.setup();
    render(<PermissionsResetCard site={site} />);

    await user.click(screen.getByRole("button", { name: "Reset permissions" }));
    const go = screen.getByRole("button", { name: "Reset permissions of blog" });
    expect(go).toBeDisabled();
    expect(screen.getByText("blog", { selector: "code" })).toHaveClass("ui-confirm-phrase");

    await user.type(screen.getByRole("textbox", { name: /to confirm/ }), "blo");
    expect(go).toBeDisabled();
    await user.type(screen.getByRole("textbox", { name: /to confirm/ }), "g");
    await user.click(go);

    expect(await screen.findByRole("status")).toHaveTextContent("Changed 56 entries of 1,234 entries.");
    expect(screen.getByRole("list", { name: "Skipped entries" })).toHaveTextContent("htdocs/mnt: on another file system");
    expect(calls.filter((c) => c.method === "POST")).toHaveLength(1);
    expect(screen.queryByRole("textbox")).toBeNull();
  });

  it("says when there was nothing to do", async () => {
    mockFetch({ ["POST " + path]: { body: { checked: 3, changed: 0, skipped: 0, skippedPaths: [] } } });
    const user = userEvent.setup();
    render(<PermissionsResetCard site={site} />);

    await user.click(screen.getByRole("button", { name: "Reset permissions" }));
    await user.type(screen.getByRole("textbox", { name: /to confirm/ }), "blog");
    await user.click(screen.getByRole("button", { name: "Reset permissions of blog" }));

    expect(await screen.findByRole("status")).toHaveTextContent("Nothing to change: all 3 entries were already right.");
  });

  it("explains a reset that ran out of time", async () => {
    mockFetch({ ["POST " + path]: { status: 504, body: { error: { code: "reset_incomplete", message: "x" } } } });
    const user = userEvent.setup();
    render(<PermissionsResetCard site={site} />);

    await user.click(screen.getByRole("button", { name: "Reset permissions" }));
    await user.type(screen.getByRole("textbox", { name: /to confirm/ }), "blog");
    await user.click(screen.getByRole("button", { name: "Reset permissions of blog" }));

    expect(await screen.findByRole("alert")).toHaveTextContent(/stopped part way. Run it again/);
  });

  it("cancels without a request", async () => {
    const calls = mockFetch({});
    const user = userEvent.setup();
    render(<PermissionsResetCard site={site} />);

    await user.click(screen.getByRole("button", { name: "Reset permissions" }));
    await user.type(screen.getByRole("textbox", { name: /to confirm/ }), "blog");
    await user.click(screen.getByRole("button", { name: "Cancel" }));

    expect(screen.queryByRole("textbox")).toBeNull();
    expect(calls).toHaveLength(0);
  });
});
