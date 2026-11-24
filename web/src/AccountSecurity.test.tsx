// SPDX-License-Identifier: AGPL-3.0-or-later

import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import { SecurityCard } from "./AccountSecurity";
import { mockFetch } from "./test-utils";

const kim = { username: "kim", role: "admin", secondFactor: true, secondFactorRequired: true, totp: true };
const phone = { id: 3, name: "Phone", createdAt: "2026-11-01T08:00:00Z", lastUsedAt: null };

describe("SecurityCard", () => {
  it("changes the password", async () => {
    const user = userEvent.setup();
    const calls = mockFetch({
      "GET /api/v1/auth/passkeys": { body: { passkeys: [] } },
      "POST /api/v1/auth/password": { status: 204 },
    });
    render(<SecurityCard user={kim} />);
    await user.type(screen.getByLabelText("Current password"), "old password 123");
    await user.type(screen.getByLabelText(/^New password/), "a new long passphrase");
    await user.type(screen.getByLabelText("Repeat the new password"), "a new long passphrase");
    await user.click(screen.getByRole("button", { name: "Change password" }));
    expect(await screen.findByText(/Password changed/)).toBeInTheDocument();
    expect(calls.find((c) => c.path === "/api/v1/auth/password")?.body).toEqual({
      currentPassword: "old password 123",
      newPassword: "a new long passphrase",
    });
    expect(screen.getByLabelText("Current password")).toHaveValue("");
  });

  it("checks the repeated password and names a wrong current one", async () => {
    const user = userEvent.setup();
    const calls = mockFetch({
      "GET /api/v1/auth/passkeys": { body: { passkeys: [] } },
      "POST /api/v1/auth/password": { status: 401, body: { error: { code: "invalid_credentials", message: "invalid username or password" } } },
    });
    render(<SecurityCard user={kim} />);
    await user.type(screen.getByLabelText("Current password"), "wrong");
    await user.type(screen.getByLabelText(/^New password/), "a new long passphrase");
    await user.type(screen.getByLabelText("Repeat the new password"), "a new long passphrasX");
    await user.click(screen.getByRole("button", { name: "Change password" }));
    expect(screen.getByRole("alert")).toHaveTextContent("do not match");
    expect(calls.filter((c) => c.method === "POST")).toHaveLength(0);

    await user.clear(screen.getByLabelText("Repeat the new password"));
    await user.type(screen.getByLabelText("Repeat the new password"), "a new long passphrase");
    await user.click(screen.getByRole("button", { name: "Change password" }));
    expect(await screen.findByRole("alert")).toHaveTextContent("The current password is wrong.");
  });

  it("turns TOTP off with password and code and explains a refusal", async () => {
    const user = userEvent.setup();
    let refuse = true;
    const calls = mockFetch({
      "GET /api/v1/auth/passkeys": { body: { passkeys: [] } },
      "POST /api/v1/auth/totp/disable": () =>
        refuse
          ? { status: 409, body: { error: { code: "second_factor_required", message: "x" } } }
          : { status: 204 },
    });
    render(<SecurityCard user={kim} />);
    expect(screen.getByText(/^On\./)).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Turn off" }));
    await user.type(screen.getByLabelText("Your password"), "my password 123");
    await user.type(screen.getByLabelText(/recovery code/), "123456");
    await user.click(screen.getByRole("button", { name: "Turn off" }));
    expect(await screen.findByRole("alert")).toHaveTextContent("must keep a second factor");
    expect(calls.find((c) => c.path === "/api/v1/auth/totp/disable")?.body).toEqual({ password: "my password 123", code: "123456" });

    refuse = false;
    await user.type(screen.getByLabelText("Your password"), "my password 123");
    await user.type(screen.getByLabelText(/recovery code/), "654321");
    await user.click(screen.getByRole("button", { name: "Turn off" }));
    expect(await screen.findByText(/The authenticator app is off/)).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Set up an authenticator app" })).toBeInTheDocument();
  });

  it("sets up TOTP right away", async () => {
    const user = userEvent.setup();
    mockFetch({
      "GET /api/v1/auth/passkeys": { body: { passkeys: [] } },
      "POST /api/v1/auth/totp/enroll": { body: { secret: "JBSWY3DPEHPK3PXP", uri: "otpauth://totp/Hostashi:kim?secret=JBSWY3DPEHPK3PXP" } },
      "POST /api/v1/auth/totp/confirm": { body: { recoveryCodes: ["aaaa-bbbb-cccc-dddd"] } },
    });
    render(<SecurityCard user={{ ...kim, totp: false }} />);
    await user.click(screen.getByRole("button", { name: "Set up an authenticator app" }));
    expect(await screen.findByLabelText("Authenticator key")).toHaveTextContent("JBSW Y3DP EHPK 3PXP");
    await user.type(screen.getByLabelText("One-time code"), "123456");
    await user.click(screen.getByRole("button", { name: "Turn on" }));
    expect(await screen.findByText("aaaa-bbbb-cccc-dddd")).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Continue" }));
    expect(screen.getByText(/^On\./)).toBeInTheDocument();
  });

  it("lists and removes passkeys", async () => {
    const user = userEvent.setup();
    let keys = [phone];
    mockFetch({
      "GET /api/v1/auth/passkeys": () => ({ body: { passkeys: keys } }),
      "DELETE /api/v1/auth/passkeys/3": () => {
        keys = [];
        return { status: 204 };
      },
    });
    render(<SecurityCard user={kim} />);
    const list = await screen.findByRole("list", { name: "Your passkeys" });
    expect(within(list).getByText("Phone")).toBeInTheDocument();
    expect(list).toHaveTextContent("never used");
    await user.click(screen.getByRole("button", { name: "Remove passkey Phone" }));
    expect(await screen.findByText("Passkey Phone removed.")).toBeInTheDocument();
    expect(screen.getByText("No passkeys yet.")).toBeInTheDocument();
  });
});
