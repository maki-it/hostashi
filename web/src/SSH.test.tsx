// SPDX-License-Identifier: AGPL-3.0-or-later

import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import { SSHSettings } from "./SSH";
import type { SSHKey } from "./ssh";
import type { Site } from "./sites";
import { mockFetch } from "./test-utils";

const site: Site = {
  name: "blog",
  pool: "default",
  state: "active",
  php: "8.4",
  phpPrograms: false,
  disabled: false,
  maintenance: { enabled: false, page: "builtin-maintenance", allowIps: [] },
  domains: ["blog.example.com"],
  tls: { enabled: false, active: false, hsts: false, domains: [], notAfter: null },
  createdAt: "2026-11-19T08:00:00Z",
  updatedAt: "2026-11-19T08:00:00Z",
};

const ed = "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIOMqqnkVzrm0SdG6UOoqKLsabgH5C9okWi0dh2l9GKJl";

const laptop: SSHKey = {
  id: 1,
  label: "kim laptop",
  access: "sftp",
  public: ed,
  fingerprint: "SHA256:abc",
  createdAt: 1795600000,
  updatedAt: 1795600000,
};

const base = "/api/v1/sites/blog/ssh-keys";
const hostRoute = { ["GET /api/v1/ssh/host-keys"]: { body: { keys: [{ type: "ssh-ed25519", fingerprint: "SHA256:host" }] } } };

describe("SSHSettings", () => {
  it("lists keys with the login line and the host keys", async () => {
    mockFetch({ ...hostRoute, ["GET " + base]: { body: { keys: [laptop, { ...laptop, id: 2, label: "deploy", access: "shell" }] } } });
    render(<SSHSettings site={site} host="web.example.com" />);

    const one = within(await screen.findByLabelText("Details of key kim laptop"));
    expect(one.getByText("SHA256:abc")).toBeInTheDocument();
    expect(one.getByText("ssh-ed25519")).toBeInTheDocument();
    expect(screen.getByText("SFTP only")).toBeInTheDocument();
    expect(screen.getByText("Shell", { selector: ".badge" })).toBeInTheDocument();
    expect(screen.getByText("sftp blog@web.example.com")).toBeInTheDocument();
    expect(screen.getByText("ssh blog@web.example.com")).toBeInTheDocument();
    expect(await screen.findByText("SHA256:host")).toBeInTheDocument();
  });

  it("offers no ssh line without a shell key and says when there are no keys", async () => {
    mockFetch({ ...hostRoute, ["GET " + base]: { body: { keys: [laptop] } } });
    const { unmount } = render(<SSHSettings site={site} host="h" />);
    expect(await screen.findByText("sftp blog@h")).toBeInTheDocument();
    expect(screen.queryByText("ssh blog@h")).not.toBeInTheDocument();
    unmount();

    mockFetch({ ["GET " + base]: { body: { keys: [] } } });
    render(<SSHSettings site={site} host="h" />);
    expect(await screen.findByText(/No keys yet/)).toBeInTheDocument();
  });

  it("adds a key, SFTP only by default, with the comment as label", async () => {
    const user = userEvent.setup();
    let keys: SSHKey[] = [];
    const calls = mockFetch({
      ...hostRoute,
      ["GET " + base]: () => ({ body: { keys } }),
      ["POST " + base]: (b) => {
        const k = { ...laptop, ...(b as object), id: 7 } as SSHKey;
        keys = [k];
        return { status: 201, body: k };
      },
    });
    render(<SSHSettings site={site} host="h" />);
    await user.click(await screen.findByRole("button", { name: "Add SSH key" }));
    const form = within(screen.getByRole("form", { name: "New SSH key" }));

    await user.click(form.getByRole("button", { name: "Add key" }));
    expect(form.getByRole("alert")).toHaveTextContent("Paste a public key");
    await user.type(form.getByLabelText(/^Public key/), "-----BEGIN OPENSSH PRIVATE KEY-----");
    await user.click(form.getByRole("button", { name: "Add key" }));
    expect(form.getByRole("alert")).toHaveTextContent("private key");
    await user.clear(form.getByLabelText(/^Public key/));
    await user.click(form.getByLabelText(/^Public key/));
    await user.paste(ed + " kim@laptop");
    expect(form.getByLabelText(/^Label/)).toHaveValue("kim@laptop");
    expect(calls.filter((c) => c.method === "POST")).toHaveLength(0);

    await user.click(form.getByRole("button", { name: "Add key" }));
    expect(await screen.findByRole("status")).toHaveTextContent("Key added for SFTP.");
    expect(calls.find((c) => c.method === "POST")?.body).toEqual({ label: "kim@laptop", access: "sftp", public: ed + " kim@laptop" });
  });

  it("changes a key to shell access and shows a refused change", async () => {
    const user = userEvent.setup();
    let fail = true;
    const calls = mockFetch({
      ...hostRoute,
      ["GET " + base]: { body: { keys: [laptop] } },
      ["PUT " + base + "/1"]: (b) =>
        fail ? { status: 503, body: { error: { code: "ssh_unavailable", message: "" } } } : { body: { ...laptop, ...(b as object) } },
    });
    render(<SSHSettings site={site} host="h" />);
    await user.click(await screen.findByRole("button", { name: "Change" }));
    const form = within(screen.getByRole("form", { name: "Change key kim laptop" }));
    await user.click(form.getByLabelText(/^Shell and SFTP/));
    await user.click(form.getByRole("button", { name: "Save key" }));
    expect(await form.findByRole("alert")).toHaveTextContent("SSH access is not available on this panel.");

    fail = false;
    await user.click(form.getByRole("button", { name: "Save key" }));
    expect(await screen.findByRole("status")).toHaveTextContent("Key saved.");
    expect(calls.filter((c) => c.method === "PUT").at(-1)?.body).toEqual({ label: "kim laptop", access: "shell" });
  });

  it("deletes a key after asking", async () => {
    const user = userEvent.setup();
    let keys = [laptop];
    mockFetch({
      ...hostRoute,
      ["GET " + base]: () => ({ body: { keys } }),
      ["DELETE " + base + "/1"]: () => {
        keys = [];
        return { status: 204 };
      },
    });
    render(<SSHSettings site={site} host="h" />);
    await user.click(await screen.findByRole("button", { name: "Delete" }));
    await user.click(screen.getByRole("button", { name: "Delete key" }));
    expect(await screen.findByRole("status")).toHaveTextContent("Key deleted.");
    expect(screen.getByText(/No keys yet/)).toBeInTheDocument();
  });
});
