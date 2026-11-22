// SPDX-License-Identifier: AGPL-3.0-or-later

import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { useState } from "react";
import { describe, expect, it } from "vitest";
import { daysUntil, support } from "./php";
import { PHPPage } from "./PHP";
import { mockFetch } from "./test-utils";

const versions = {
  installed: ["8.4"],
  supported: ["7.4", "8.1", "8.2", "8.3", "8.4", "8.5"],
  legacy: ["7.4", "8.1", "8.2"],
  securityUntil: { "7.4": "2022-11-28", "8.1": "2025-12-31", "8.2": "2026-12-31", "8.3": "2027-12-31", "8.4": "2028-12-31", "8.5": "2029-12-31" },
  activeUntil: { "8.2": "2024-12-31", "8.3": "2025-12-31", "8.4": "2026-12-31", "8.5": "2027-12-31" },
  distro: "8.4",
};
const sites = { body: { sites: [{ name: "blog", php: "8.4" }, { name: "shop", php: "8.4" }, { name: "old", php: "7.4" }] } };
const idle = { state: "idle", log: [], repository: true, newer: ["8.7"] };
const today = "2026-11-20";

describe("support", () => {
  it("derives the upstream state from the dates", () => {
    expect(support(versions, "8.5", today)).toBe("active");
    expect(support(versions, "8.3", today)).toBe("security");
    expect(support(versions, "8.2", "2026-12-31")).toBe("security");
    expect(support(versions, "8.2", "2027-01-01")).toBe("eol");
    expect(support(versions, "8.1", today)).toBe("eol");
    expect(support(versions, "9.0", today)).toBe("eol");
    expect(daysUntil("2026-12-31", today)).toBe(41);
  });
});

describe("PHPPage", () => {
  it("lists the versions with their support, warns before the end and greys out newer ones", async () => {
    mockFetch({ "GET /api/v1/php/versions": { body: versions }, "GET /api/v1/php/install": { body: idle }, "GET /api/v1/sites": sites });
    render(<PHPPage today={today} />);

    const row = (v: string) => within(screen.getByRole("row", { name: new RegExp("^PHP " + v.replace(".", "\\.") + "(\\D|$)") }));
    expect(await screen.findByRole("heading", { name: "PHP versions" })).toBeInTheDocument();
    await screen.findByRole("columnheader", { name: "Sites" });
    expect(row("8.4").getByText("Installed")).toBeInTheDocument();
    expect(row("8.4").getByText("Distribution")).toBeInTheDocument();
    expect(row("8.4").getByRole("cell", { name: "2" })).toBeInTheDocument();
    expect(row("8.5").queryByText("Distribution")).not.toBeInTheDocument();
    expect(row("8.5").getByText("Supported")).toBeInTheDocument();
    expect(row("8.2").getByText("Security fixes end in 41 days. Move its sites to a newer version.")).toBeInTheDocument();
    expect(row("7.4").getByText("End of life")).toBeInTheDocument();
    expect(screen.getByText("Not yet released for Hostashi: PHP 8.7")).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Install PHP 8.7" })).not.toBeInTheDocument();
  });

  it("lists the newest version first and filters to installed ones", async () => {
    const user = userEvent.setup();
    mockFetch({ "GET /api/v1/php/versions": { body: versions }, "GET /api/v1/php/install": { body: idle }, "GET /api/v1/sites": sites });
    function Harness() {
      const [query, setQuery] = useState(new URLSearchParams());
      return <PHPPage today={today} query={query} onQuery={setQuery} />;
    }
    render(<Harness />);
    const table = await screen.findByRole("table", { name: "PHP versions" });
    const names = () =>
      within(table)
        .getAllByRole("row")
        .slice(1)
        .map((r) => within(r).getAllByRole("cell")[0]!.querySelector("strong")?.textContent);
    expect(names()).toEqual(["PHP 8.5", "PHP 8.4", "PHP 8.3", "PHP 8.2", "PHP 8.1", "PHP 7.4"]);
    expect(within(table).getByRole("columnheader", { name: "Version" })).toHaveAttribute("aria-sort", "descending");
    await user.selectOptions(screen.getByRole("combobox", { name: "Show" }), "installed");
    expect(names()).toEqual(["PHP 8.4"]);
  });

  it("installs a supported version and follows the log", async () => {
    const user = userEvent.setup();
    let state: unknown = idle;
    const calls = mockFetch({
      "GET /api/v1/php/versions": { body: versions },
      "GET /api/v1/php/install": () => ({ body: state }),
      "POST /api/v1/php/install": () => {
        state = { version: "8.3", state: "succeeded", log: ["php-install: done: PHP 8.3 installed"], repository: true, newer: [] };
        return { status: 202, body: { version: "8.3", state: "running", log: [] } };
      },
    });
    render(<PHPPage today={today} poll={10} />);

    await user.click(await screen.findByRole("button", { name: "Install PHP 8.3" }));
    expect(calls.find((c) => c.method === "POST")?.body).toEqual({ version: "8.3", confirmEol: false });
    expect(await screen.findByRole("heading", { name: "PHP 8.3 was installed" })).toBeInTheDocument();
    expect(screen.getByLabelText("Installation log")).toHaveTextContent("php-install: done: PHP 8.3 installed");
  });

  it("asks before an end-of-life version and explains a refusal", async () => {
    const user = userEvent.setup();
    const calls = mockFetch({
      "GET /api/v1/php/versions": { body: versions },
      "GET /api/v1/php/install": { body: idle },
      "POST /api/v1/php/install": {
        status: 409,
        body: { error: { code: "php_install_refused", message: "phpinstall: an installation is already running" } },
      },
    });
    render(<PHPPage today={today} />);

    await user.click(await screen.findByRole("button", { name: "Install PHP 7.4" }));
    const dialog = screen.getByRole("alertdialog", { name: "Install PHP 7.4 without security fixes?" });
    expect(calls.some((c) => c.method === "POST")).toBe(false);

    await user.click(within(dialog).getByRole("button", { name: "Install anyway" }));
    expect(calls.find((c) => c.method === "POST")?.body).toEqual({ version: "7.4", confirmEol: true });
    expect(await screen.findByRole("alert")).toHaveTextContent("Another installation is still running.");
  });

  it("removes an installed version no site uses, but not the distribution's", async () => {
    const user = userEvent.setup();
    let state: unknown = idle;
    const more = { ...versions, installed: ["8.3", "8.4", "7.4"] };
    const calls = mockFetch({
      "GET /api/v1/php/versions": () => ({ body: state === idle ? more : versions }),
      "GET /api/v1/php/install": () => ({ body: state }),
      "GET /api/v1/sites": sites,
      "POST /api/v1/php/remove": () => {
        state = { version: "8.3", action: "remove", state: "succeeded", log: ["php-install: removing PHP 8.3", "php-install: done: PHP 8.3 removed"], repository: true, newer: [] };
        return { status: 202, body: { version: "8.3", action: "remove", state: "running", log: [] } };
      },
    });
    render(<PHPPage today={today} poll={10} />);

    await user.click(await screen.findByRole("button", { name: "Remove PHP 8.3" }));
    // 8.4 is the distribution's, 7.4 has a site.
    expect(screen.queryByRole("button", { name: "Remove PHP 8.4" })).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Remove PHP 7.4" })).not.toBeInTheDocument();
    const dialog = screen.getByRole("alertdialog", { name: "Remove PHP 8.3?" });
    expect(calls.some((c) => c.method === "POST")).toBe(false);

    await user.click(within(dialog).getByRole("button", { name: "Remove" }));
    expect(calls.find((c) => c.method === "POST")?.body).toEqual({ version: "8.3" });
    expect(await screen.findByRole("heading", { name: "PHP 8.3 was removed" })).toBeInTheDocument();
    expect(screen.getByLabelText("Removal log")).toHaveTextContent("PHP 8.3 removed");
    expect(await screen.findByRole("button", { name: "Install PHP 8.3" })).toBeInTheDocument();
  });

  it("explains a refused removal", async () => {
    const user = userEvent.setup();
    mockFetch({
      "GET /api/v1/php/versions": { body: { ...versions, installed: ["8.3", "8.4"] } },
      "GET /api/v1/php/install": { body: idle },
      "GET /api/v1/sites": sites,
      "POST /api/v1/php/remove": { status: 409, body: { error: { code: "php_in_use", message: "sites still run on PHP 8.3: x" } } },
    });
    render(<PHPPage today={today} />);
    await user.click(await screen.findByRole("button", { name: "Remove PHP 8.3" }));
    await user.click(within(screen.getByRole("alertdialog")).getByRole("button", { name: "Remove" }));
    expect(await screen.findByRole("alert")).toHaveTextContent("Sites still run on this version.");
  });
});
