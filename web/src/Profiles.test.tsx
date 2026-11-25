// SPDX-License-Identifier: AGPL-3.0-or-later

import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { useState } from "react";
import { describe, expect, it } from "vitest";
import { Profiles } from "./Profiles";
import { copyName, fromProfileForm, ownProfileMessage, parseCron, profileNameProblem, toProfileForm, type Profile } from "./profiles";
import { ApiError } from "./api";
import { mockFetch } from "./test-utils";

const wordpress: Profile = {
  name: "wordpress",
  title: "WordPress",
  type: "php",
  minPhp: "7.4",
  hardening: { configFiles: true, noPhpDirs: ["wp-content/uploads"], denyFiles: ["xmlrpc.php"], limitFiles: ["wp-login.php"] },
  pageCache: "wordpress",
  checklist: ["xmlrpc.php is blocked."],
  builtin: true,
};
const agency: Profile = {
  name: "agency",
  title: "Agency",
  type: "php",
  webDir: "public",
  hardening: { configFiles: true },
  cron: [{ schedule: "*/5 * * * *", command: "php cron.php", note: "The app's cron." }],
  builtin: false,
};
const list = (profiles: Profile[]) => ({
  "GET /api/v1/profiles": { body: { profiles, configFileSuffixes: [".sql"], limitPerMinute: 12, limitBurst: 10, maxPaths: 20 } },
});

function Harness({ initial = "" }: { initial?: string }) {
  const [query, setQuery] = useState(new URLSearchParams(initial));
  return <Profiles query={query} onQuery={setQuery} />;
}

describe("profile helpers", () => {
  it("checks names like the server", () => {
    expect(profileNameProblem("agency", ["wordpress"])).toBe("");
    expect(profileNameProblem("wordpress", ["wordpress"])).toMatch(/exists already/);
    expect(profileNameProblem("Agency", [])).toMatch(/lower-case/);
    expect(profileNameProblem("1a", [])).toMatch(/starting with a letter/);
    expect(profileNameProblem("a".repeat(41), [])).toMatch(/up to 40/);
  });

  it("finds a free name for a copy", () => {
    expect(copyName("wordpress", ["wordpress"])).toBe("wordpress-copy");
    expect(copyName("wordpress", ["wordpress-copy"])).toBe("wordpress-copy-2");
    expect(copyName("a".repeat(40), []).length).toBe(40);
  });

  it("reads cron lines and keeps notes", () => {
    expect(parseCron("*/5 * * * * php cron.php\n@daily php clean.php\n", agency.cron)).toEqual([
      { schedule: "*/5 * * * *", command: "php cron.php", note: "The app's cron." },
      { schedule: "@daily", command: "php clean.php" },
    ]);
    expect(parseCron("* * * * *")).toMatch(/needs a schedule and a command/);
  });

  it("turns the form into a request and back", () => {
    const { builtin: _, ...input } = wordpress;
    expect(fromProfileForm(toProfileForm(input))).toEqual(input);
    const f = toProfileForm({ ...input, name: "site" });
    expect(fromProfileForm({ ...f, title: " " })).toMatch(/title/);
    expect(fromProfileForm({ ...f, minPhp: "eight" })).toMatch(/like 8.2/);
    // A static profile drops what needs PHP.
    expect(fromProfileForm({ ...f, type: "static" })).toEqual({
      name: "site",
      title: "WordPress",
      type: "static",
      hardening: { configFiles: true, denyFiles: ["xmlrpc.php"] },
      checklist: ["xmlrpc.php is blocked."],
    });
  });

  it("explains refusals", () => {
    expect(ownProfileMessage(new ApiError(409, "profile_in_use", "sites use this profile (blog)"))).toBe(
      "Sites use this profile (blog). Give those sites another profile first.",
    );
    expect(ownProfileMessage(new ApiError(422, "invalid_argument", "hosting: invalid input: hardening: blocked file \"a;\" is bad"))).toBe(
      'Blocked file "a;" is bad.',
    );
  });
});

describe("Profiles", () => {
  it("lists built-in and own profiles with what each can do", async () => {
    mockFetch(list([wordpress, agency]));
    render(<Harness />);
    const table = within(await screen.findByRole("table", { name: "Application profiles" }));
    const wp = within(table.getByText("WordPress").closest("tr")!);
    expect(wp.getByText("Built-in")).toBeInTheDocument();
    expect(wp.getByRole("link", { name: "Copy WordPress" })).toHaveAttribute("href", "#/profiles?copy=wordpress");
    expect(wp.getByRole("link", { name: "Export WordPress as YAML" })).toHaveAttribute("href", "/api/v1/profiles/wordpress/yaml");
    expect(wp.queryByRole("link", { name: "Change WordPress" })).not.toBeInTheDocument();
    expect(wp.queryByRole("button", { name: "Delete WordPress" })).not.toBeInTheDocument();
    const own = within(table.getByText("Agency").closest("tr")!);
    expect(own.getByText("Own")).toBeInTheDocument();
    expect(own.getByText("htdocs/public")).toBeInTheDocument();
    expect(own.getByRole("link", { name: "Change Agency" })).toHaveAttribute("href", "#/profiles?edit=agency");
  });

  it("copies a built-in profile into an own one", async () => {
    const user = userEvent.setup();
    const calls = mockFetch({ ...list([wordpress]), "POST /api/v1/profiles": { status: 201, body: { ...wordpress, name: "wordpress-copy", builtin: false } } });
    render(<Harness initial="copy=wordpress" />);
    const form = within(await screen.findByRole("form", { name: "Copy WordPress" }));
    expect(form.getByLabelText(/^Name/)).toHaveValue("wordpress-copy");
    expect(form.getByLabelText(/^Folders without PHP/)).toHaveValue("wp-content/uploads");
    await user.clear(form.getByLabelText(/^Name/));
    await user.type(form.getByLabelText(/^Name/), "wordpress");
    await user.click(form.getByRole("button", { name: "Add profile" }));
    expect(screen.getByText("A profile named wordpress exists already.")).toBeInTheDocument();
    await user.clear(form.getByLabelText(/^Name/));
    await user.type(form.getByLabelText(/^Name/), "agency-wp");
    await user.clear(form.getByLabelText(/^Blocked files/));
    await user.click(form.getByRole("button", { name: "Add profile" }));
    expect(await screen.findByText("Profile agency-wp added.")).toBeInTheDocument();
    const body = calls.find((c) => c.method === "POST")?.body as Record<string, unknown>;
    expect(body).toMatchObject({ name: "agency-wp", title: "WordPress (copy)", type: "php", minPhp: "7.4", pageCache: "wordpress" });
    expect(body.hardening).toEqual({ configFiles: true, noPhpDirs: ["wp-content/uploads"], limitFiles: ["wp-login.php"] });
    expect(body).not.toHaveProperty("builtin");
  });

  it("changes an own profile and shows the server's refusal", async () => {
    const user = userEvent.setup();
    const calls = mockFetch({
      ...list([wordpress, agency]),
      "PUT /api/v1/profiles/agency": (b) =>
        (b as Profile).webDir === "web"
          ? { status: 200, body: { ...agency, webDir: "web" } }
          : { status: 422, body: { error: { code: "invalid_argument", message: "hosting: invalid input: document root folder \"x;y\": use folder names of letters" } } },
    });
    render(<Harness initial="edit=agency" />);
    const form = within(await screen.findByRole("form", { name: "Change profile agency" }));
    expect(form.getByLabelText(/^Name/)).toBeDisabled();
    expect(form.getByLabelText(/^Suggested cron jobs/)).toHaveValue("*/5 * * * * php cron.php");
    await user.clear(form.getByLabelText(/^Web root folder/));
    await user.type(form.getByLabelText(/^Web root folder/), "x;y");
    await user.click(form.getByRole("button", { name: "Save profile" }));
    expect(await screen.findByText(/Document root folder "x;y"/)).toBeInTheDocument();
    await user.clear(form.getByLabelText(/^Web root folder/));
    await user.type(form.getByLabelText(/^Web root folder/), "web");
    await user.click(form.getByRole("button", { name: "Save profile" }));
    expect(await screen.findByText(/Profile agency saved/)).toBeInTheDocument();
    const put = calls.filter((c) => c.method === "PUT").at(-1)?.body as Profile;
    expect(put.cron).toEqual(agency.cron);
  });

  it("refuses to edit a built-in profile", async () => {
    mockFetch(list([wordpress]));
    render(<Harness initial="edit=wordpress" />);
    expect(await screen.findByText(/WordPress is built in and cannot be changed/)).toBeInTheDocument();
  });

  it("deletes an own profile only when no site uses it", async () => {
    const user = userEvent.setup();
    let inUse = true;
    mockFetch({
      ...list([wordpress, agency]),
      "DELETE /api/v1/profiles/agency": () =>
        inUse ? { status: 409, body: { error: { code: "profile_in_use", message: "sites use this profile (blog)" } } } : { status: 204 },
    });
    render(<Harness />);
    await user.click(await screen.findByRole("button", { name: "Delete Agency" }));
    const dialog = within(screen.getByRole("alertdialog"));
    await user.click(dialog.getByRole("button", { name: "Delete profile" }));
    expect(await dialog.findByText("Sites use this profile (blog). Give those sites another profile first.")).toBeInTheDocument();
    inUse = false;
    await user.click(dialog.getByRole("button", { name: "Delete profile" }));
    expect(await screen.findByText("Profile agency deleted.")).toBeInTheDocument();
  });

  it("imports YAML from the text field or a file", async () => {
    const user = userEvent.setup();
    const yaml = "version: 1\nname: imported\ntitle: Imported\ntype: php\nhardening:\n  configFiles: true\n";
    const calls = mockFetch({
      ...list([wordpress]),
      "POST /api/v1/profiles/import": (b) =>
        (b as { yaml: string }).yaml.includes("nginx")
          ? { status: 422, body: { error: { code: "invalid_argument", message: 'hosting: invalid input: the YAML cannot be read: line 7: field nginx not found in type profile.yamlDoc' } } }
          : { status: 201, body: { name: "imported", title: "Imported", type: "php", hardening: { configFiles: true }, builtin: false } },
    });
    render(<Harness initial="import=1" />);
    const form = within(await screen.findByRole("form", { name: "Import a profile" }));
    await user.click(form.getByRole("button", { name: "Import profile" }));
    expect(screen.getByText("Paste the YAML or choose a file.")).toBeInTheDocument();

    await user.upload(form.getByLabelText(/^YAML file/), new File([yaml + "nginx: deny all;\n"], "p.yaml", { type: "application/yaml" }));
    await waitFor(() => expect(form.getByLabelText(/^YAML$/)).toHaveValue(yaml + "nginx: deny all;\n"));
    await user.click(form.getByRole("button", { name: "Import profile" }));
    expect(await screen.findByText(/field nginx not found/)).toBeInTheDocument();

    const area = form.getByLabelText(/^YAML$/);
    await user.clear(area);
    await user.click(area);
    await user.paste(yaml);
    await user.click(form.getByRole("button", { name: "Import profile" }));
    expect(await screen.findByText("Profile imported imported.")).toBeInTheDocument();
    expect(calls.filter((c) => c.method === "POST").at(-1)?.body).toEqual({ yaml });
  });
});
