// SPDX-License-Identifier: AGPL-3.0-or-later

import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { useState } from "react";
import { describe, expect, it } from "vitest";
import { mockFetch } from "./test-utils";
import { Users } from "./Users";

const kim = {
  id: 1,
  username: "kim",
  role: "admin",
  disabled: false,
  disabledPage: "builtin-maintenance",
  totpEnabled: true,
  passkeys: 0,
  requireSecondFactor: false,
  invited: false,
  invitationExpiresAt: null,
};
const uma = { ...kim, id: 2, username: "uma", role: "user", totpEnabled: false };
const err = (status: number, code: string, message = code) => ({ status, body: { error: { code, message } } });

// Harness keeps the address query in state, as the app does.
function Harness({ initial = "" }: { initial?: string }) {
  const [query, setQuery] = useState(new URLSearchParams(initial));
  return (
    <>
      <Users me="kim" query={query} onQuery={setQuery} />
      <output data-testid="query">{query.toString()}</output>
    </>
  );
}

describe("Users", () => {
  it("invites someone and shows the link once", async () => {
    const user = userEvent.setup();
    const calls = mockFetch({
      "GET /api/v1/users": { body: { users: [kim] } },
      "GET /api/v1/sites": { body: { sites: [] } },
      "GET /api/v1/users/policy": { body: { requireSecondFactor: false } },
      "POST /api/v1/users": (b) =>
        (b as { username: string }).username === "kim"
          ? err(409, "user_exists")
          : {
              status: 201,
              body: {
                user: { ...uma, username: "mona", role: "manager", invited: true },
                invitation: { token: "T".repeat(43), expiresAt: "2026-11-21T12:00:00Z" },
              },
            },
    });
    render(<Harness initial="new=1" />);
    await user.type(await screen.findByLabelText("Username"), "kim");
    await user.click(screen.getByRole("button", { name: "Create invitation" }));
    expect(await screen.findByRole("alert")).toHaveTextContent("This username is taken.");

    await user.clear(screen.getByLabelText("Username"));
    await user.type(screen.getByLabelText("Username"), "mona");
    await user.selectOptions(screen.getByLabelText("Role"), "manager");
    await user.click(screen.getByRole("button", { name: "Create invitation" }));
    expect(await screen.findByLabelText("Invitation link")).toHaveValue(window.location.origin + "/#/invite/" + "T".repeat(43));
    expect(screen.getByRole("region", { name: "Invitation for mona" })).toBeInTheDocument();
    expect(screen.getByTestId("query")).toHaveTextContent(/^$/);
    expect(calls.find((c) => c.method === "POST")?.body).toEqual({ username: "kim", role: "user" });
    expect(calls.filter((c) => c.method === "POST").at(-1)?.body).toEqual({ username: "mona", role: "manager" });
  });

  it("emails the invitation when an address is given", async () => {
    const user = userEvent.setup();
    const calls = mockFetch({
      "GET /api/v1/users": { body: { users: [kim] } },
      "GET /api/v1/sites": { body: { sites: [] } },
      "GET /api/v1/users/policy": { body: { requireSecondFactor: false } },
      "POST /api/v1/users": {
        status: 201,
        body: {
          user: { ...uma, username: "anna", invited: true },
          invitation: { token: "T".repeat(43), expiresAt: "2026-11-21T12:00:00Z", emailedTo: "anna@example.com" },
        },
      },
    });
    render(<Harness initial="new=1" />);
    await user.type(await screen.findByLabelText("Username"), "anna");
    await user.type(screen.getByLabelText("Email (optional)"), "anna@example.com");
    await user.click(screen.getByRole("button", { name: "Create invitation" }));
    expect(await screen.findByText("The link was emailed to anna@example.com.")).toBeInTheDocument();
    expect(screen.getByLabelText("Invitation link")).toBeInTheDocument();
    expect(calls.find((c) => c.method === "POST")?.body).toEqual({ username: "anna", role: "user", email: "anna@example.com" });
  });

  it("lists accounts with filters in the address and no actions on the own account", async () => {
    const user = userEvent.setup();
    mockFetch({
      "GET /api/v1/users": { body: { users: [kim, uma] } },
      "GET /api/v1/sites": { body: { sites: [] } },
      "GET /api/v1/users/policy": { body: { requireSecondFactor: false } },
    });
    render(<Harness />);
    const table = await screen.findByRole("table", { name: "Users" });
    const rows = within(table).getAllByRole("row").slice(1);
    expect(rows).toHaveLength(2);
    expect(within(rows[0]!).getByText("You")).toBeInTheDocument();
    expect(within(rows[0]!).queryByRole("link", { name: /Manage/ })).not.toBeInTheDocument();
    expect(within(rows[1]!).getByRole("link", { name: "Manage uma" })).toHaveAttribute("href", "#/users?user=uma");
    expect(within(rows[1]!).getByText("No second factor")).toBeInTheDocument();

    await user.selectOptions(screen.getByLabelText("Role"), "user");
    expect(screen.getByTestId("query")).toHaveTextContent("role=user");
    expect(within(table).getAllByRole("row")).toHaveLength(2);
  });

  it("grants sites to a user and requires a second factor", async () => {
    const user = userEvent.setup();
    const calls = mockFetch({
      "GET /api/v1/users": { body: { users: [kim, uma] } },
      "GET /api/v1/sites": { body: { sites: [{ name: "alpha" }, { name: "beta" }] } },
      "GET /api/v1/users/policy": { body: { requireSecondFactor: false } },
      "GET /api/v1/users/2/grants": { body: { sites: ["alpha"] } },
      "PUT /api/v1/users/2/grants": (b) => ({ body: b }),
      "PUT /api/v1/users/2": (b) => ({ body: { ...uma, ...(b as object) } }),
    });
    render(<Harness initial="user=uma" />);
    expect(await screen.findByRole("checkbox", { name: "alpha" })).toBeChecked();
    await user.click(screen.getByRole("checkbox", { name: "beta" }));
    await user.click(screen.getByRole("button", { name: "Save sites" }));
    expect(await screen.findByText("Sites: alpha, beta.")).toBeInTheDocument();
    expect(calls.find((c) => c.method === "PUT")?.body).toEqual({ sites: ["alpha", "beta"] });

    await user.click(screen.getByRole("checkbox", { name: "Require a second factor" }));
    expect(await screen.findByText(/now required/)).toBeInTheDocument();
    expect(calls.filter((c) => c.method === "PUT").at(-1)?.body).toEqual({ requireSecondFactor: true });
  });

  it("shows no actions for the own account", async () => {
    mockFetch({
      "GET /api/v1/users": { body: { users: [kim, uma] } },
      "GET /api/v1/sites": { body: { sites: [] } },
      "GET /api/v1/users/policy": { body: { requireSecondFactor: false } },
    });
    render(<Harness initial="user=kim" />);
    expect(await screen.findByText("This is your own account.")).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: /Delete|Disable|Reset/ })).not.toBeInTheDocument();
  });

  it("explains the last admin", async () => {
    const user = userEvent.setup();
    const ada = { ...kim, id: 3, username: "ada" };
    mockFetch({
      "GET /api/v1/users": { body: { users: [kim, ada] } },
      "GET /api/v1/sites": { body: { sites: [] } },
      "GET /api/v1/users/policy": { body: { requireSecondFactor: false } },
      "PUT /api/v1/users/3": err(409, "last_admin"),
    });
    render(<Harness initial="user=ada" />);
    await user.click(await screen.findByRole("button", { name: "Disable" }));
    expect(await screen.findByRole("alert")).toHaveTextContent("last enabled admin");
  });

  it("resets an account and shows the new link", async () => {
    const user = userEvent.setup();
    mockFetch({
      "GET /api/v1/users": { body: { users: [kim, uma] } },
      "GET /api/v1/sites": { body: { sites: [] } },
      "GET /api/v1/users/policy": { body: { requireSecondFactor: false } },
      "GET /api/v1/users/2/grants": { body: { sites: [] } },
      "POST /api/v1/users/2/reset": { body: { invitation: { token: "R".repeat(43), expiresAt: "2026-11-21T12:00:00Z" } } },
    });
    render(<Harness initial="user=uma" />);
    await user.click(await screen.findByRole("button", { name: "Reset password and second factor" }));
    await user.click(within(screen.getByRole("alertdialog")).getByRole("button", { name: "Reset uma" }));
    expect(await screen.findByRole("heading", { name: "Invitation for uma" })).toBeInTheDocument();
    expect(screen.getByLabelText("Invitation link")).toHaveValue(window.location.origin + "/#/invite/" + "R".repeat(43));
  });

  it("deletes an account after asking", async () => {
    const user = userEvent.setup();
    let list = [kim, uma];
    const calls = mockFetch({
      "GET /api/v1/users": () => ({ body: { users: list } }),
      "GET /api/v1/sites": { body: { sites: [] } },
      "GET /api/v1/users/policy": { body: { requireSecondFactor: false } },
      "GET /api/v1/users/2/grants": { body: { sites: [] } },
      "DELETE /api/v1/users/2": () => {
        list = [kim];
        return { status: 204 };
      },
    });
    render(<Harness initial="user=uma" />);
    await user.click(await screen.findByRole("button", { name: "Delete account" }));
    await user.click(within(screen.getByRole("alertdialog")).getByRole("button", { name: "Delete uma" }));
    expect(await screen.findByText("Account uma deleted.")).toBeInTheDocument();
    expect(calls.some((c) => c.method === "DELETE")).toBe(true);
    expect(screen.getByRole("table", { name: "Users" })).toBeInTheDocument();
  });

  it("requires a second factor of every account after naming those without one", async () => {
    const user = userEvent.setup();
    const calls = mockFetch({
      "GET /api/v1/users": { body: { users: [kim, uma] } },
      "GET /api/v1/sites": { body: { sites: [] } },
      "GET /api/v1/users/policy": { body: { requireSecondFactor: false } },
      "PUT /api/v1/users/policy": (b) => ({ body: b }),
    });
    render(<Harness />);
    const box = await screen.findByRole("checkbox", { name: "Require a second factor for every account" });
    expect(box).not.toBeChecked();
    await user.click(box);
    const dialog = screen.getByRole("alertdialog", { name: "1 account has no second factor yet" });
    expect(dialog).toHaveTextContent("uma");
    expect(calls.some((c) => c.method === "PUT")).toBe(false);

    await user.click(within(dialog).getByRole("button", { name: "Require for every account" }));
    expect(await screen.findByText("Every account now needs a second factor.")).toBeInTheDocument();
    expect(box).toBeChecked();
    expect(calls.find((c) => c.method === "PUT")?.body).toEqual({ requireSecondFactor: true });

    await user.click(box);
    expect(await screen.findByText("A second factor is optional again for users.")).toBeInTheDocument();
    expect(calls.filter((c) => c.method === "PUT").at(-1)?.body).toEqual({ requireSecondFactor: false });
  });

  it("shows the per-account demand as set while every account needs one", async () => {
    mockFetch({
      "GET /api/v1/users": { body: { users: [kim, uma] } },
      "GET /api/v1/sites": { body: { sites: [] } },
      "GET /api/v1/users/policy": { body: { requireSecondFactor: true } },
      "GET /api/v1/users/2/grants": { body: { sites: [] } },
    });
    render(<Harness initial="user=uma" />);
    const box = await screen.findByRole("checkbox", { name: "Require a second factor" });
    expect(box).toBeChecked();
    expect(box).toBeDisabled();
    expect(screen.getByText(/Required for every account/)).toBeInTheDocument();
  });
});
