// SPDX-License-Identifier: AGPL-3.0-or-later

import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import { GitSettings } from "./Git";
import { activePreset, applyPreset, canDeploy, commitURL, hostOf, inputProblem, isSSHURL, liveRelease, presets, type Deploy, type GitInput, type GitSetting } from "./git";
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

const unset: GitSetting = {
  configured: false,
  url: "",
  branch: "main",
  deployDir: "htdocs",
  shared: [],
  commands: [],
  keep: 5,
  buildTimeoutMinutes: 15,
  submodules: false,
  auth: "none",
  tokenUser: "",
  hasToken: false,
  publicKey: "",
  hostKey: "",
  hostKeyFingerprint: "",
  webhook: false,
  webhookPath: "",
  updatedAt: "",
};

const sshGit: GitSetting = {
  ...unset,
  configured: true,
  url: "git@github.com:acme/blog.git",
  auth: "ssh",
  commands: ["composer install --no-dev"],
  publicKey: "ssh-ed25519 AAAAkey hostashi-blog",
  updatedAt: "2026-11-24T08:00:00Z",
};

const run = (id: number, o: Partial<Deploy> = {}): Deploy => ({
  id,
  release: String(id),
  kind: "deploy",
  source: "panel",
  commit: "ab".repeat(20),
  author: "Kim",
  message: "Change " + String(id),
  state: "succeeded",
  error: "",
  createdAt: "2026-11-24T08:00:00Z",
  startedAt: "2026-11-24T08:00:00Z",
  finishedAt: "2026-11-24T08:01:30Z",
  ...o,
});

const base = "/api/v1/sites/blog";

describe("git helpers", () => {
  it("knows SSH addresses and hosts", () => {
    expect(isSSHURL("git@github.com:acme/blog.git")).toBe(true);
    expect(isSSHURL("ssh://git@git.example.com:2222/acme/blog.git")).toBe(true);
    expect(isSSHURL("https://github.com/acme/blog.git")).toBe(false);
    expect(hostOf("git@github.com:acme/blog.git")).toBe("github.com");
    expect(hostOf("ssh://git@Git.Example.com:2222/a.git")).toBe("git.example.com");
    expect(hostOf("https://gitlab.com/a/b.git")).toBe("gitlab.com");
  });

  it("links a commit at the Git host", () => {
    const sha = "ab".repeat(20);
    expect(commitURL("https://github.com/acme/blog.git", sha)).toBe("https://github.com/acme/blog/commit/" + sha);
    expect(commitURL("git@github.com:acme/blog.git", sha)).toBe("https://github.com/acme/blog/commit/" + sha);
    expect(commitURL("ssh://git@git.example.com:2222/acme/blog.git", sha)).toBe("https://git.example.com/acme/blog/commit/" + sha);
    expect(commitURL("https://git.example.com:3000/acme/blog", sha)).toBe("https://git.example.com:3000/acme/blog/commit/" + sha);
    expect(commitURL("https://gitlab.com/group/sub/blog.git", sha)).toBe("https://gitlab.com/group/sub/blog/-/commit/" + sha);
    expect(commitURL("git@bitbucket.org:acme/blog.git", sha)).toBe("https://bitbucket.org/acme/blog/commits/" + sha);
    expect(commitURL("https://github.com/acme/blog.git", "not a sha")).toBe("");
    expect(commitURL("file:///srv/blog.git", sha)).toBe("");
  });

  it("checks the form like the server", () => {
    const ok: GitInput = { url: "https://github.com/a/b.git", branch: "main", deployDir: "htdocs", shared: [], commands: [], keep: 5, buildTimeoutMinutes: 15, submodules: false, auth: "none", tokenUser: "", token: "" };
    expect(inputProblem(ok, false)).toBe("");
    expect(inputProblem({ ...ok, url: "file:///srv/x" }, false)).toMatch(/https/);
    expect(inputProblem({ ...ok, auth: "ssh" }, false)).toMatch(/SSH address/);
    expect(inputProblem({ ...ok, auth: "token" }, false)).toMatch(/token/);
    expect(inputProblem({ ...ok, auth: "token" }, true)).toBe("");
    expect(inputProblem({ ...ok, deployDir: "../x" }, false)).toMatch(/below the home/);
    expect(inputProblem({ ...ok, keep: 1 }, false)).toMatch(/Keep/);
    expect(inputProblem({ ...ok, buildTimeoutMinutes: 61 }, false)).toMatch(/1 to 60/);
  });

  it("replaces preset values and names own text it replaces", () => {
    const laravel = applyPreset(presets.find((p) => p.label === "Laravel")!, "", "");
    expect(laravel).toEqual({ commands: expect.stringContaining("php artisan migrate --force"), shared: ".env\nstorage", edited: [] });
    const wp = applyPreset(presets.find((p) => p.label === "WordPress")!, laravel.commands, laravel.shared);
    expect(wp).toEqual({ commands: "", shared: "wp-config.php\nwp-content/uploads", edited: [] });
    expect(activePreset(wp.commands, wp.shared)).toBe("WordPress");
    const own = applyPreset(presets[0]!, "make build", " .env \n\nconfig/local.php");
    expect(own.edited).toEqual(["build commands", "shared paths"]);
    expect(own.shared).toBe(".env");
    expect(activePreset("make build", ".env")).toBe("");
  });

  it("finds the live release and the site types", () => {
    expect(liveRelease([run(3, { state: "failed" }), run(2, { kind: "rollback", release: "1" }), run(1)])).toBe("1");
    expect(canDeploy(site)).toBe(true);
    expect(canDeploy({ ...site, type: "proxy", account: false })).toBe(false);
  });
});

describe("GitSettings", () => {
  it("sets up a repository", async () => {
    let saved: unknown;
    mockFetch({
      ["GET " + base + "/git"]: () => ({ body: saved ? { ...unset, configured: true, url: "https://github.com/acme/blog.git" } : unset }),
      ["PUT " + base + "/git"]: (b) => {
        saved = b;
        return { body: { ...unset, configured: true, url: "https://github.com/acme/blog.git" } };
      },
      ["GET " + base + "/deploys"]: { body: [] },
    });
    render(<GitSettings site={site} canEdit />);

    await userEvent.type(await screen.findByLabelText(/Repository address/), "https://github.com/acme/blog.git");
    await userEvent.click(screen.getByRole("button", { name: "Laravel" }));
    await userEvent.click(screen.getByRole("button", { name: "Save repository" }));

    expect(await screen.findByText("No deploys yet.")).toBeInTheDocument();
    expect(saved).toMatchObject({
      url: "https://github.com/acme/blog.git",
      auth: "none",
      commands: ["composer install --no-dev --optimize-autoloader", "php artisan migrate --force", "php artisan optimize"],
      shared: [".env", "storage"],
      keep: 5,
      buildTimeoutMinutes: 15,
    });
  });

  it("switches presets over filled fields and undoes replaced own text", async () => {
    mockFetch({ ["GET " + base + "/git"]: { body: unset }, ["GET " + base + "/deploys"]: { body: [] } });
    render(<GitSettings site={site} canEdit />);
    const commands = await screen.findByLabelText(/Build commands/);
    const shared = screen.getByLabelText(/Shared paths/);

    await userEvent.click(screen.getByRole("button", { name: "Laravel" }));
    await userEvent.click(screen.getByRole("button", { name: "WordPress" }));
    expect(shared).toHaveValue("wp-config.php\nwp-content/uploads");
    expect(commands).toHaveValue("");
    expect(screen.getByRole("button", { name: "WordPress" })).toHaveAttribute("aria-pressed", "true");
    expect(screen.queryByRole("button", { name: "Undo" })).not.toBeInTheDocument();

    await userEvent.type(shared, "\nconfig.php");
    await userEvent.click(screen.getByRole("button", { name: "Node.js (npm)" }));
    expect(shared).toHaveValue(".env");
    expect(screen.getByText("Node.js (npm) replaced your own shared paths.")).toBeInTheDocument();
    await userEvent.click(screen.getByRole("button", { name: "Undo" }));
    expect(shared).toHaveValue("wp-config.php\nwp-content/uploads\nconfig.php");
    expect(commands).toHaveValue("");
  });

  it("tells a user without the right that nothing is set up", async () => {
    mockFetch({ ["GET " + base + "/git"]: { body: unset } });
    render(<GitSettings site={site} canEdit={false} />);
    expect(await screen.findByText(/does not deploy from Git/)).toBeInTheDocument();
  });

  it("needs the host key before an SSH deploy and pins the scanned one", async () => {
    const calls = mockFetch({
      ["GET " + base + "/git"]: { body: sshGit },
      ["GET " + base + "/deploys"]: { body: [] },
      ["POST " + base + "/git/hostkey/scan"]: { body: { key: "ssh-ed25519 AAAAhost", fingerprint: "SHA256:abc" } },
      ["PUT " + base + "/git/hostkey"]: { body: { ...sshGit, hostKey: "ssh-ed25519 AAAAhost", hostKeyFingerprint: "SHA256:abc" } },
    });
    render(<GitSettings site={site} canEdit />);

    expect(await screen.findByRole("button", { name: "Deploy now" })).toBeDisabled();
    expect(screen.getByText(/Confirm the server's host key/)).toBeInTheDocument();

    await userEvent.click(screen.getByRole("button", { name: "Read the host key" }));
    const box = within(await screen.findByLabelText("Scanned host key"));
    expect(box.getByText("SHA256:abc")).toBeInTheDocument();
    expect(box.getByRole("link", { name: "GitHub publishes" })).toHaveAttribute("href", expect.stringContaining("docs.github.com"));

    await userEvent.click(box.getByRole("button", { name: /trust this key/ }));
    expect(calls.find((c) => c.method === "PUT")?.body).toEqual({ key: "ssh-ed25519 AAAAhost" });

    // Replacing the deploy key asks first.
    await userEvent.click(screen.getByRole("button", { name: "Replace deploy key" }));
    const ask = within(screen.getByRole("alertdialog", { name: "Replace the deploy key" }));
    await userEvent.click(ask.getByRole("button", { name: "Cancel" }));
    expect(calls.some((c) => c.path === base + "/git/key")).toBe(false);
  });

  it("deploys, shows the log and rolls back", async () => {
    const ready = { ...sshGit, hostKey: "ssh-ed25519 AAAAhost", hostKeyFingerprint: "SHA256:abc" };
    const calls = mockFetch({
      ["GET " + base + "/git"]: { body: ready },
      ["GET " + base + "/deploys"]: { body: [run(3, { state: "failed", error: "a step of the deploy failed" }), run(2), run(1)] },
      ["GET " + base + "/deploys/2/log"]: { body: { log: "$ composer install\n", truncated: false } },
      ["POST " + base + "/deploys"]: { status: 202, body: run(4, { state: "running" }) },
      ["POST " + base + "/deploys/1/rollback"]: { body: run(5, { kind: "rollback", release: "1" }) },
    });
    render(<GitSettings site={site} canEdit={false} />);

    const list = within(await screen.findByLabelText("Deploys of blog"));
    expect(list.getByText("Live")).toBeInTheDocument();
    expect(list.getAllByRole("link", { name: /^Commit abababa/ })[0]).toHaveAttribute("href", "https://github.com/acme/blog/commit/" + "ab".repeat(20));
    expect(list.getByText("Failed")).toBeInTheDocument();
    expect(list.getByText("a step of the deploy failed")).toBeInTheDocument();
    // Only an older successful release can be a target.
    expect(list.getAllByRole("button", { name: "Roll back to this release" })).toHaveLength(1);
    // Without the right to change it, the setting is read only.
    expect(screen.queryByRole("button", { name: "Change" })).not.toBeInTheDocument();
    expect(screen.queryByText("Webhook")).not.toBeInTheDocument();

    await userEvent.click(list.getAllByRole("button", { name: "Show log" })[1]!);
    expect(await screen.findByLabelText("Log of deploy 2")).toHaveTextContent("$ composer install");

    await userEvent.click(list.getByRole("button", { name: "Roll back to this release" }));
    await userEvent.click(screen.getByRole("button", { name: "Deploy now" }));
    expect(calls.filter((c) => c.method === "POST").map((c) => c.path)).toEqual([base + "/deploys/1/rollback", base + "/deploys"]);
  });

  it("shows the webhook secret once", async () => {
    mockFetch({
      ["GET " + base + "/git"]: { body: { ...sshGit, auth: "none", url: "https://github.com/acme/blog.git" } },
      ["GET " + base + "/deploys"]: { body: [] },
      ["POST " + base + "/git/webhook"]: { body: { id: "ab".repeat(16), path: "/api/v1/hooks/deploy/" + "ab".repeat(16), secret: "s3cr3t" } },
    });
    render(<GitSettings site={site} canEdit />);

    await userEvent.click(await screen.findByRole("button", { name: "Turn webhook on" }));
    const box = within(await screen.findByLabelText("Webhook secret"));
    expect(box.getByLabelText("Secret")).toHaveValue("s3cr3t");
    expect(box.getByLabelText("Payload URL")).toHaveValue(window.location.origin + "/api/v1/hooks/deploy/" + "ab".repeat(16));
    await userEvent.click(box.getByRole("button", { name: "I have saved it" }));
    expect(screen.queryByDisplayValue("s3cr3t")).not.toBeInTheDocument();
  });
});
