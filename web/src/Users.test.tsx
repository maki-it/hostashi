// SPDX-License-Identifier: AGPL-3.0-or-later

import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import { mockFetch } from "./test-utils";
import { Users } from "./Users";

const kim = {
  id: 1,
  username: "kim",
  role: "admin",
  disabled: false,
  totpEnabled: true,
  passkeys: 0,
  requireSecondFactor: false,
  invited: false,
  invitationExpiresAt: null,
};
const uma = { ...kim, id: 2, username: "uma", role: "user", totpEnabled: false };
const err = (status: number, code: string, message = code) => ({ status, body: { error: { code, message } } });

describe("Users", () => {
  it("invites someone and shows the link once", async () => {
    const user = userEvent.setup();
    const calls = mockFetch({
      "GET /api/v1/users": { body: { users: [kim] } },
      "GET /api/v1/sites": { body: { sites: [] } },
      "POST /api/v1/users": (b) =>
        (b as { username: string }).username === "kim"
          ? err(409, "user_exists")
          : { status: 201, body: { user: { ...uma, username: "mona", role: "manager", invited: true }, invitation: { token: "T".repeat(43), expiresAt: "2026-11-21T12:00:00Z" } } },
    });
    render(<Users me="kim" />);
    await user.type(await screen.findByLabelText(/Username/), "kim");
    await user.click(screen.getByRole("button", { name: "Create invitation" }));
    expect(await screen.findByRole("alert")).toHaveTextContent("This username is taken.");

    await user.clear(screen.getByLabelText(/Username/));
    await user.type(screen.getByLabelText(/Username/), "mona");
    await user.selectOptions(screen.getByLabelText(/Role/), "manager");
    await user.click(screen.getByRole("button", { name: "Create invitation" }));
    expect(await screen.findByLabelText("Invitation link")).toHaveValue(window.location.origin + "/#/invite/" + "T".repeat(43));
    expect(calls.find((c) => c.method === "POST")?.body).toEqual({ username: "kim", role: "user" });
    expect(calls.filter((c) => c.method === "POST").at(-1)?.body).toEqual({ username: "mona", role: "manager" });
  });

  it("does not offer actions on the own account", async () => {
    mockFetch({ "GET /api/v1/users": { body: { users: [kim, uma] } }, "GET /api/v1/sites": { body: { sites: [] } } });
    render(<Users me="kim" />);
    const rows = await screen.findAllByRole("listitem");
    expect(within(rows[0]!).queryByRole("button")).not.toBeInTheDocument();
    expect(within(rows[1]!).getByRole("button", { name: "Delete" })).toBeInTheDocument();
  });

  it("grants sites to a user and requires a second factor", async () => {
    const user = userEvent.setup();
    const calls = mockFetch({
      "GET /api/v1/users": { body: { users: [kim, uma] } },
      "GET /api/v1/sites": { body: { sites: [{ name: "alpha" }, { name: "beta" }] } },
      "GET /api/v1/users/2/grants": { body: { sites: ["alpha"] } },
      "PUT /api/v1/users/2/grants": (b) => ({ body: b }),
      "PUT /api/v1/users/2": (b) => ({ body: { ...uma, ...(b as object) } }),
    });
    render(<Users me="kim" />);
    await user.click(await screen.findByRole("button", { name: "Sites" }));
    expect(await screen.findByRole("checkbox", { name: "alpha" })).toBeChecked();
    await user.click(screen.getByRole("checkbox", { name: "beta" }));
    await user.click(screen.getByRole("button", { name: "Save sites" }));
    expect(await screen.findByRole("status")).toHaveTextContent("Sites: alpha, beta.");
    expect(calls.find((c) => c.method === "PUT")?.body).toEqual({ sites: ["alpha", "beta"] });

    await user.click(screen.getByRole("checkbox", { name: "Require a second factor" }));
    expect(await screen.findByRole("status")).toHaveTextContent("now required");
    expect(calls.filter((c) => c.method === "PUT").at(-1)?.body).toEqual({ requireSecondFactor: true });
  });

  it("explains the last admin", async () => {
    const user = userEvent.setup();
    const ada = { ...kim, id: 3, username: "ada" };
    mockFetch({
      "GET /api/v1/users": { body: { users: [kim, ada] } },
      "GET /api/v1/sites": { body: { sites: [] } },
      "PUT /api/v1/users/3": err(409, "last_admin"),
    });
    render(<Users me="kim" />);
    await user.click(await screen.findByRole("button", { name: "Disable" }));
    expect(await screen.findByRole("alert")).toHaveTextContent("last enabled admin");
  });

  it("resets an account and shows the new link", async () => {
    const user = userEvent.setup();
    mockFetch({
      "GET /api/v1/users": { body: { users: [kim, uma] } },
      "GET /api/v1/sites": { body: { sites: [] } },
      "POST /api/v1/users/2/reset": { body: { invitation: { token: "R".repeat(43), expiresAt: "2026-11-21T12:00:00Z" } } },
    });
    render(<Users me="kim" />);
    await user.click(await screen.findByRole("button", { name: "Reset" }));
    await user.click(screen.getByRole("button", { name: "Reset" }));
    expect(await screen.findByRole("heading", { name: "Invitation for uma" })).toBeInTheDocument();
    expect(screen.getByLabelText("Invitation link")).toHaveValue(window.location.origin + "/#/invite/" + "R".repeat(43));
  });
});
