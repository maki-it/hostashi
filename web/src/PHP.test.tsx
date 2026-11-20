// SPDX-License-Identifier: AGPL-3.0-or-later

import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
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
};
const idle = { state: "idle", log: [], repository: true, newer: ["8.6"] };
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
    mockFetch({ "GET /api/v1/php/versions": { body: versions }, "GET /api/v1/php/install": { body: idle } });
    render(<PHPPage today={today} />);

    const row = (v: string) => within(screen.getByRole("row", { name: new RegExp("^PHP " + v.replace(".", "\\.") + " ") }));
    expect(await screen.findByRole("heading", { name: "PHP versions" })).toBeInTheDocument();
    expect(row("8.4").getByText("Installed")).toBeInTheDocument();
    expect(row("8.5").getByText("Supported")).toBeInTheDocument();
    expect(row("8.2").getByText("Security fixes end in 41 days. Move its sites to a newer version.")).toBeInTheDocument();
    expect(row("7.4").getByText("End of life")).toBeInTheDocument();
    expect(row("8.6").getByText(/not yet released for Hostashi/)).toBeInTheDocument();
    expect(row("8.6").queryByRole("button")).not.toBeInTheDocument();
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
});
