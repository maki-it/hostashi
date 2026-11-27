// SPDX-License-Identifier: AGPL-3.0-or-later

import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import { HardeningCard } from "./Hardening";
import { fromHardeningForm, parsePaths, toHardeningForm, type Profile, type SiteProfileView, type WebHardening } from "./profiles";
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
  tls: { enabled: true, active: true, hsts: false, domains: [], notAfter: null },
  createdAt: "2026-11-19T08:00:00Z",
  updatedAt: "2026-11-19T08:00:00Z",
};

const wp: WebHardening = { configFiles: true, noPhpDirs: ["wp-content/uploads"], denyFiles: ["xmlrpc.php"], limitFiles: ["wp-login.php"] };
const profiles: Profile[] = [
  { name: "php", title: "Generic PHP", type: "php", hardening: { configFiles: true }, builtin: true },
  { name: "wordpress", title: "WordPress", type: "php", minPhp: "7.4", hardening: wp, checklist: ["wp-login.php is rate limited per address."], builtin: true },
];
const view = (profile: string, hardening: WebHardening, changed = false): SiteProfileView => ({
  profile,
  hardening,
  defaults: profiles.find((p) => p.name === profile)!.hardening,
  changed,
  choices: { profiles, configFileSuffixes: [".sql", ".bak", "~"], limitPerMinute: 12, limitBurst: 10, maxPaths: 20 },
});
const path = "/api/v1/sites/blog/profile";

describe("hardening form", () => {
  it("reads one path per line", () => {
    expect(parsePaths(" /wp-content/uploads/ \n\nxmlrpc.php\nxmlrpc.php\n")).toEqual(["wp-content/uploads", "xmlrpc.php"]);
  });

  it("round-trips rules and drops the PHP ones without PHP", () => {
    expect(fromHardeningForm(toHardeningForm(wp), true)).toEqual(wp);
    expect(fromHardeningForm(toHardeningForm({}), true)).toEqual({});
    expect(fromHardeningForm(toHardeningForm(wp), false)).toEqual({ configFiles: true, denyFiles: ["xmlrpc.php"] });
  });
});

describe("HardeningCard", () => {
  it("switches to WordPress and allows xmlrpc.php again", async () => {
    const calls = mockFetch({
      ["GET " + path]: { body: view("php", { configFiles: true }) },
      ["PUT " + path]: (body) => {
        const b = body as { profile: string; hardening?: WebHardening };
        return { body: view(b.profile, b.hardening ?? wp, b.hardening !== undefined) };
      },
    });
    const user = userEvent.setup();
    render(<HardeningCard site={site} />);

    const select = await screen.findByRole("combobox", { name: "Profile" });
    expect(select).toHaveValue("php");
    await user.selectOptions(select, "wordpress");

    // The profile's rules come into the form, with its notes.
    const blocked = screen.getByRole("textbox", { name: "Blocked files" });
    expect(blocked).toHaveValue("xmlrpc.php");
    expect(screen.getByText("Needs PHP 7.4 or newer.")).toBeInTheDocument();
    expect(screen.getByText(/rate limited per address/)).toBeInTheDocument();

    await user.clear(blocked);
    await user.click(screen.getByRole("button", { name: "Save profile and rules" }));

    expect(await screen.findByRole("status")).toHaveTextContent("Saved.");
    expect(calls).toContainEqual({
      method: "PUT",
      path,
      body: { profile: "wordpress", hardening: { configFiles: true, noPhpDirs: ["wp-content/uploads"], limitFiles: ["wp-login.php"] } },
    });
    expect(screen.getByText("Changed from the profile")).toBeInTheDocument();

    await user.click(screen.getByRole("button", { name: "Use the profile's rules" }));
    expect(await screen.findByText(/follows the profile's rules again/)).toBeInTheDocument();
    expect(calls.at(-1)).toEqual({ method: "PUT", path, body: { profile: "wordpress" } });
    expect(screen.queryByText("Changed from the profile")).not.toBeInTheDocument();
  });

  it("shows the server block of the unsaved rules", async () => {
    const calls = mockFetch({
      ["GET " + path]: { body: view("php", { configFiles: true }) },
      ["POST " + path + "/preview"]: (body) => {
        const b = body as { profile: string; hardening: WebHardening };
        const files = (b.hardening.denyFiles ?? []).map((f) => `    location = /${f} {\n        return 404;\n    }\n`);
        return { body: { config: `# Preview of the server block of site blog (read only).\nserver {\n${files.join("")}}\n` } };
      },
    });
    const user = userEvent.setup();
    render(<HardeningCard site={site} />);

    await user.selectOptions(await screen.findByRole("combobox", { name: "Profile" }), "wordpress");
    // Nothing is loaded while the block is closed.
    expect(calls.some((c) => c.method === "POST")).toBe(false);

    await user.click(screen.getByText("Show the server block"));
    expect(await screen.findByLabelText("Server block")).toHaveTextContent("location = /xmlrpc.php");
    expect(calls.at(-1)).toEqual({ method: "POST", path: path + "/preview", body: { profile: "wordpress", hardening: wp } });

    // A change in the form shows up without saving.
    await user.clear(screen.getByRole("textbox", { name: "Blocked files" }));
    await waitFor(() => expect(screen.getByLabelText("Server block")).not.toHaveTextContent("xmlrpc.php"));
    expect(calls.some((c) => c.method === "PUT")).toBe(false);
  });

  it("says why the server block cannot be shown", async () => {
    mockFetch({
      ["GET " + path]: { body: view("php", { configFiles: true }) },
      ["POST " + path + "/preview"]: { status: 422, body: { error: { code: "invalid_argument", message: `hosting: invalid input: unknown profile "x" for a php site` } } },
    });
    const user = userEvent.setup();
    render(<HardeningCard site={site} />);

    await user.click(await screen.findByText("Show the server block"));
    expect(await screen.findByRole("alert")).toHaveTextContent(`Unknown profile "x" for a php site.`);
  });

  it("offers own profiles next to the built-in ones", async () => {
    const own: Profile = { name: "agency", title: "Agency", type: "php", hardening: { configFiles: true, denyDirs: ["private"] }, builtin: false };
    const withOwn = view("php", { configFiles: true });
    withOwn.choices.profiles = [...profiles, own];
    const calls = mockFetch({ ["GET " + path]: { body: withOwn }, ["PUT " + path]: { body: { ...withOwn, profile: "agency" } } });
    const user = userEvent.setup();
    render(<HardeningCard site={site} />);

    await user.selectOptions(await screen.findByRole("combobox", { name: "Profile" }), "agency");
    expect(screen.getByRole("textbox", { name: "Blocked folders" })).toHaveValue("private");
    await user.click(screen.getByRole("button", { name: "Save profile and rules" }));
    await screen.findByRole("status");
    expect(calls.at(-1)?.body).toMatchObject({ profile: "agency" });
  });

  it("hides the PHP rules on a static site", async () => {
    mockFetch({ ["GET " + path]: { body: view("php", { configFiles: true }) } });
    render(<HardeningCard site={{ ...site, php: "" }} />);

    expect(await screen.findByRole("textbox", { name: "Blocked folders" })).toBeInTheDocument();
    expect(screen.queryByRole("textbox", { name: "Folders without PHP" })).not.toBeInTheDocument();
    expect(screen.queryByRole("textbox", { name: "Rate limited PHP files" })).not.toBeInTheDocument();
  });

  it("shows why the server refused a path", async () => {
    mockFetch({
      ["GET " + path]: { body: view("php", { configFiles: true }) },
      ["PUT " + path]: {
        status: 422,
        body: {
          error: {
            code: "invalid_argument",
            message: `hosting: invalid input: hardening: blocked file "a;b": use names of letters, digits, '.', '_' and '-', separated by '/', without a leading slash`,
          },
        },
      },
    });
    const user = userEvent.setup();
    render(<HardeningCard site={site} />);

    await user.type(await screen.findByRole("textbox", { name: "Blocked files" }), "a;b");
    await user.click(screen.getByRole("button", { name: "Save profile and rules" }));

    expect(await screen.findByRole("alert")).toHaveTextContent(`Blocked file "a;b": use names of letters`);
  });
});
