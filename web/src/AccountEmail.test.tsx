// SPDX-License-Identifier: AGPL-3.0-or-later

import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import { EmailCard } from "./AccountEmail";
import { ConfirmEmail } from "./ConfirmEmail";
import { mockFetch } from "./test-utils";

const none = { email: "", pending: "", mail_enabled: true };

describe("EmailCard", () => {
  it("sends a confirmation link and shows the address as pending", async () => {
    const user = userEvent.setup();
    const calls = mockFetch({
      "GET /api/v1/account/email": { body: none },
      "PUT /api/v1/account/email": { body: { ...none, pending: "kim@example.com", pending_expires: "2099-01-01T00:00:00Z" } },
    });
    render(<EmailCard />);

    await user.type(await screen.findByLabelText("Email address"), "kim@example.com");
    await user.click(screen.getByRole("button", { name: "Send confirmation link" }));

    expect(await screen.findByText("Waiting for confirmation: kim@example.com")).toBeInTheDocument();
    expect(screen.getByText(/We sent a link to the new address/)).toBeInTheDocument();
    expect(calls.find((c) => c.method === "PUT")?.body).toEqual({ email: "kim@example.com" });
  });

  it("changes and removes a confirmed address", async () => {
    const user = userEvent.setup();
    let email = "kim@example.com";
    mockFetch({
      "GET /api/v1/account/email": () => ({ body: { ...none, email } }),
      "PUT /api/v1/account/email": { status: 422, body: { error: { code: "invalid_argument", message: "this is already your address" } } },
      "DELETE /api/v1/account/email": () => {
        email = "";
        return { status: 204 };
      },
    });
    render(<EmailCard />);

    expect(await screen.findByText("kim@example.com")).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Change address" }));
    await user.type(screen.getByLabelText("New email address"), "kim@example.com");
    await user.click(screen.getByRole("button", { name: "Send confirmation link" }));
    expect(await screen.findByText("This is already your address.")).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Cancel" }));

    await user.click(screen.getByRole("button", { name: "Remove address" }));
    expect(await screen.findByLabelText("Email address")).toBeInTheDocument();
  });

  it("explains that email is off", async () => {
    mockFetch({ "GET /api/v1/account/email": { body: { ...none, mail_enabled: false } } });
    render(<EmailCard />);

    expect(await screen.findByText("Email is not set up on this panel.")).toBeInTheDocument();
    expect(screen.queryByLabelText("Email address")).not.toBeInTheDocument();
  });
});

describe("ConfirmEmail", () => {
  it("confirms once and clears the link from the address bar", async () => {
    window.history.replaceState(null, "", "#/confirm-email/abcdefghijklmnopqrstuvwxyz");
    const calls = mockFetch({ "POST /api/v1/account/email/confirm": { body: { email: "kim@example.com" } } });
    render(<ConfirmEmail token="abcdefghijklmnopqrstuvwxyz" onDone={() => {}} />);

    expect(await screen.findByText("Confirmed. Hostashi sends email to kim@example.com from now on.")).toBeInTheDocument();
    expect(window.location.hash).toBe("#/");
    expect(calls.filter((c) => c.method === "POST")).toHaveLength(1);
  });

  it("says when the link is no longer valid", async () => {
    mockFetch({ "POST /api/v1/account/email/confirm": { status: 404, body: { error: { code: "link_invalid", message: "x" } } } });
    render(<ConfirmEmail token="abcdefghijklmnopqrstuvwxyz" onDone={() => {}} />);

    expect(await screen.findByText(/The link is invalid, used or expired/)).toBeInTheDocument();
  });
});
