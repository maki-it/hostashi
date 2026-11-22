// SPDX-License-Identifier: AGPL-3.0-or-later

import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import { System } from "./System";
import { hostnameError, uptime, waitForPanel, type SystemInfo } from "./system";
import { mockFetch } from "./test-utils";

const info: SystemInfo = {
  hostname: "web2",
  transientHostname: "web2",
  timezone: "UTC",
  time: "2026-11-22T12:00:00Z",
  uptimeSeconds: 3 * 86400 + 4 * 3600,
  ntpSynchronized: true,
  rebootRequired: true,
  rebootPackages: ["linux-image-6.12"],
  cloudInit: true,
};

const zones = { zones: ["Europe/Berlin", "UTC"] };

describe("system helpers", () => {
  it("checks hostnames like the server", () => {
    expect(hostnameError("web2")).toBe("");
    expect(hostnameError("web2.example.org")).toBe("");
    expect(hostnameError("")).not.toBe("");
    expect(hostnameError("Web2")).not.toBe("");
    expect(hostnameError("web2.")).not.toBe("");
    expect(hostnameError("web_2")).not.toBe("");
    expect(hostnameError("10.0.0.1")).not.toBe("");
    expect(hostnameError("a".repeat(65))).not.toBe("");
  });

  it("formats the uptime", () => {
    expect(uptime(59)).toBe("0 min");
    expect(uptime(3 * 3600 + 120)).toBe("3 h 2 min");
    expect(uptime(2 * 86400 + 5 * 3600)).toBe("2 d 5 h");
  });

  it("waits for the panel to go down and come back", async () => {
    const answers = [true, true, false, false, true];
    const phases: string[] = [];
    const ok = await waitForPanel({
      sleep: () => Promise.resolve(),
      ping: () => Promise.resolve(answers.shift() ?? true),
      onPhase: (p) => phases.push(p),
    });
    expect(ok).toBe(true);
    expect(phases).toEqual(["going-down", "starting"]);
    expect(answers).toHaveLength(0);
  });

  it("gives up after the timeout", async () => {
    const ok = await waitForPanel({ interval: 1000, maxDown: 0, timeout: 5000, sleep: () => Promise.resolve(), ping: () => Promise.resolve(false) });
    expect(ok).toBe(false);
  });
});

describe("System", () => {
  it("shows the server's settings and the pending reboot", async () => {
    mockFetch({ "GET /api/v1/system": { body: info }, "GET /api/v1/system/timezones": { body: zones } });
    render(<System />);

    const server = await screen.findByRole("region", { name: "Server" });
    expect(server).toHaveTextContent("web2");
    expect(server).toHaveTextContent("3 d 4 h");
    expect(server).toHaveTextContent("Reboot needed");
    expect(server).toHaveTextContent("linux-image-6.12");
    expect(screen.getByText(/cloud-init is installed/)).toBeInTheDocument();
  });

  it("saves a valid hostname only", async () => {
    const user = userEvent.setup();
    const calls = mockFetch({
      "GET /api/v1/system": { body: info },
      "GET /api/v1/system/timezones": { body: zones },
      "PUT /api/v1/system/hostname": { body: { ...info, hostname: "web3", transientHostname: "web3" } },
    });
    render(<System />);

    const card = await screen.findByRole("region", { name: "Hostname" });
    const input = within(card).getByLabelText("Hostname");
    const save = within(card).getByRole("button", { name: "Save hostname" });
    expect(save).toBeDisabled();

    await user.clear(input);
    await user.type(input, "Web3");
    expect(save).toBeDisabled();
    expect(card).toHaveTextContent("lowercase letters");

    await user.clear(input);
    await user.type(input, "web3");
    await user.click(save);

    expect(await screen.findByText("The hostname is now web3.")).toBeInTheDocument();
    expect(calls.find((c) => c.method === "PUT")?.body).toEqual({ hostname: "web3" });
  });

  it("refuses an unknown time zone and saves a known one", async () => {
    const user = userEvent.setup();
    const calls = mockFetch({
      "GET /api/v1/system": { body: info },
      "GET /api/v1/system/timezones": { body: zones },
      "PUT /api/v1/system/timezone": { body: { ...info, timezone: "Europe/Berlin" } },
    });
    render(<System />);

    const card = await screen.findByRole("region", { name: "Time zone" });
    const input = within(card).getByLabelText("Time zone");
    const save = within(card).getByRole("button", { name: "Save time zone" });

    await user.clear(input);
    await user.type(input, "Europe/Atlantis");
    expect(save).toBeDisabled();
    expect(card).toHaveTextContent("does not know this time zone");

    await user.clear(input);
    await user.type(input, "Europe/Berlin");
    expect(card).toHaveTextContent("would show");
    await user.click(save);

    expect(await screen.findByText("The time zone is now Europe/Berlin.")).toBeInTheDocument();
    expect(calls.find((c) => c.method === "PUT")?.body).toEqual({ zone: "Europe/Berlin" });
  });

  it("reboots only after the hostname is typed and waits for the panel", async () => {
    const user = userEvent.setup();
    const calls = mockFetch({
      "GET /api/v1/system": { body: info },
      "GET /api/v1/system/timezones": { body: zones },
      "POST /api/v1/system/reboot": { status: 202, body: { delaySeconds: 2 } },
    });
    let waited = false;
    render(
      <System
        wait={(o) => {
          waited = true;
          o.onPhase?.("starting");
          return Promise.resolve(true);
        }}
      />,
    );

    const card = await screen.findByRole("region", { name: "Reboot" });
    await user.click(within(card).getByRole("button", { name: /Reboot server/ }));
    const dialog = within(card).getByRole("alertdialog", { name: "Reboot web2?" });
    const go = within(dialog).getByRole("button", { name: "Reboot now" });
    expect(go).toBeDisabled();

    await user.type(within(dialog).getByLabelText("Type web2 to confirm"), "web3");
    expect(go).toBeDisabled();
    await user.clear(within(dialog).getByLabelText("Type web2 to confirm"));
    await user.type(within(dialog).getByLabelText("Type web2 to confirm"), "web2");
    await user.click(go);

    expect(await screen.findByText("The server is back.")).toBeInTheDocument();
    expect(waited).toBe(true);
    expect(calls.find((c) => c.method === "POST")?.body).toEqual({ confirm: "web2" });
  });
});
