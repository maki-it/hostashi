// SPDX-License-Identifier: AGPL-3.0-or-later

import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import { Mail } from "./Mail";
import { mockFetch } from "./test-utils";

const off = { configured: false, host: "", port: 0, tls: "", username: "", password_set: false, from: "", from_name: "", reply_to: "", ca_pem: "" };
const on = {
  configured: true,
  host: "smtp.example.com",
  port: 587,
  tls: "starttls",
  username: "relay",
  password_set: true,
  from: "panel@example.com",
  from_name: "Hostashi",
  reply_to: "",
  ca_pem: "",
};

describe("Mail", () => {
  it("sets up a relay with a password", async () => {
    const user = userEvent.setup();
    const calls = mockFetch({
      "GET /api/v1/settings/mail": { body: off },
      "PUT /api/v1/settings/mail": { body: on },
    });
    render(<Mail />);

    expect(await screen.findByText("Email is off.")).toBeInTheDocument();
    await user.type(screen.getByLabelText("Host"), "smtp.example.com");
    await user.selectOptions(screen.getByLabelText("Encryption"), "tls");
    expect(screen.getByLabelText("Port")).toHaveValue("465");
    await user.selectOptions(screen.getByLabelText("Encryption"), "starttls");
    expect(screen.getByLabelText("Port")).toHaveValue("587");
    await user.type(screen.getByLabelText("Username (optional)"), "relay");
    await user.type(screen.getByLabelText("Password"), "secret");
    await user.type(screen.getByLabelText("From address"), "panel@example.com");
    await user.click(screen.getByRole("button", { name: "Save" }));

    expect(await screen.findByRole("heading", { name: "Send a test email" })).toBeInTheDocument();
    expect(calls.find((c) => c.method === "PUT")?.body).toEqual({
      host: "smtp.example.com",
      port: 587,
      tls: "starttls",
      username: "relay",
      password: "secret",
      from: "panel@example.com",
      from_name: "Hostashi",
      reply_to: "",
      ca_pem: "",
    });
    expect(screen.getByRole("button", { name: "Replace password" })).toBeInTheDocument();
  });

  it("keeps the stored password unless it is replaced", async () => {
    const user = userEvent.setup();
    const calls = mockFetch({
      "GET /api/v1/settings/mail": { body: on },
      "PUT /api/v1/settings/mail": { body: { ...on, port: 2525 } },
    });
    render(<Mail />);

    const port = await screen.findByLabelText("Port");
    await user.clear(port);
    await user.type(port, "2525");
    await user.click(screen.getByRole("button", { name: "Save" }));
    await screen.findByDisplayValue("2525");
    const put = calls.find((c) => c.method === "PUT")?.body as Record<string, unknown>;
    expect(put.port).toBe(2525);
    expect("password" in put).toBe(false);
  });

  it("shows the relay's answer when a test fails", async () => {
    const user = userEvent.setup();
    mockFetch({
      "GET /api/v1/settings/mail": { body: on },
      "POST /api/v1/settings/mail/test": {
        status: 502,
        body: { error: { code: "mail_failed", message: "dial failed: SMTP AUTH failed: 535 5.7.8 authentication failed" } },
      },
    });
    render(<Mail />);

    await user.type(await screen.findByLabelText("Send to"), "kim@example.com");
    await user.click(screen.getByRole("button", { name: "Send test email" }));
    expect(await screen.findByText(/The relay did not take the email: .*535 5\.7\.8/)).toBeInTheDocument();
  });

  it("refuses a port out of range without asking the server", async () => {
    const user = userEvent.setup();
    mockFetch({ "GET /api/v1/settings/mail": { body: on } });
    render(<Mail />);

    const port = await screen.findByLabelText("Port");
    await user.clear(port);
    await user.type(port, "70000");
    await user.click(screen.getByRole("button", { name: "Save" }));
    expect(await screen.findByText(/Enter a port between 1 and 65535/)).toBeInTheDocument();
  });

  it("turns email off", async () => {
    const user = userEvent.setup();
    let configured = true;
    mockFetch({
      "GET /api/v1/settings/mail": () => ({ body: configured ? on : off }),
      "DELETE /api/v1/settings/mail": () => {
        configured = false;
        return { status: 204 };
      },
    });
    render(<Mail />);

    await user.click(await screen.findByRole("button", { name: "Turn email off" }));
    expect(await screen.findByText("Email is off.")).toBeInTheDocument();
  });
});
