// SPDX-License-Identifier: AGPL-3.0-or-later

import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import { NotificationsCard } from "./Notifications";
import { mockFetch } from "./test-utils";

const admin = {
  email: "kim@example.com",
  mail_enabled: true,
  categories: [
    { id: "backups", enabled: true },
    { id: "certificates", enabled: true },
  ],
};

describe("NotificationsCard", () => {
  it("switches a category and saves only that one", async () => {
    const user = userEvent.setup();
    const calls = mockFetch({
      "GET /api/v1/account/notifications": { body: admin },
      "PUT /api/v1/account/notifications": { body: { ...admin, categories: [{ id: "backups", enabled: false }, admin.categories[1]] } },
    });
    render(<NotificationsCard />);

    const backups = await screen.findByRole("switch", { name: /Backups/ });
    expect(backups).toBeChecked();
    await user.click(backups);

    expect(await screen.findByRole("switch", { name: /Backups/ })).not.toBeChecked();
    expect(calls.find((c) => c.method === "PUT")?.body).toEqual({ backups: false });
    expect(screen.getByRole("switch", { name: /Certificates/ })).toBeChecked();
  });

  it("shows the services switch and skips a category it does not know", async () => {
    const cats = [...admin.categories, { id: "services", enabled: true }, { id: "deploys", enabled: false }, { id: "later", enabled: true }];
    mockFetch({ "GET /api/v1/account/notifications": { body: { ...admin, categories: cats } } });
    render(<NotificationsCard />);

    expect(await screen.findByRole("switch", { name: /^Services/, description: /keeps crashing/ })).toBeChecked();
    expect(screen.getByRole("switch", { name: /^Git deploys/, description: /failed/ })).not.toBeChecked();
    expect(screen.getAllByRole("switch")).toHaveLength(4);
  });

  it("asks for a confirmed address first", async () => {
    mockFetch({ "GET /api/v1/account/notifications": { body: { ...admin, email: "" } } });
    render(<NotificationsCard />);

    expect(await screen.findByText("Add and confirm an email address above to get notifications.")).toBeInTheDocument();
    expect(screen.getByRole("switch", { name: /Backups/ })).toBeDisabled();
  });

  it("explains that email is off", async () => {
    mockFetch({ "GET /api/v1/account/notifications": { body: { ...admin, mail_enabled: false } } });
    render(<NotificationsCard />);

    expect(await screen.findByText("Notifications need email, which is not set up on this panel yet.")).toBeInTheDocument();
  });
});
