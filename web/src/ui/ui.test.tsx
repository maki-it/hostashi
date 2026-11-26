// SPDX-License-Identifier: AGPL-3.0-or-later

import { Trash } from "@phosphor-icons/react";
import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { useState } from "react";
import { describe, expect, it, vi } from "vitest";
import { Badge, Button, Card, Field, IconButton, Menu, Notice, nextSort, relativeTime, RelTime, sortRows, Switch, Table, tabPanelProps, Tabs, UnsavedNote } from ".";
import type { Column, Sort } from ".";

describe("Button", () => {
  it("is a plain button by default and calls onClick", async () => {
    const onClick = vi.fn();
    render(<Button onClick={onClick}>Save changes</Button>);
    const btn = screen.getByRole("button", { name: "Save changes" });
    expect(btn).toHaveAttribute("type", "button");
    await userEvent.click(btn);
    expect(onClick).toHaveBeenCalledOnce();
  });

  it("can submit a form", () => {
    render(
      <Button type="submit" variant="primary">
        Create
      </Button>,
    );
    expect(screen.getByRole("button", { name: "Create" })).toHaveAttribute("type", "submit");
  });
});

describe("IconButton", () => {
  it("has its label as name and tooltip", () => {
    render(<IconButton label="Delete" icon={<Trash aria-hidden="true" />} danger />);
    const btn = screen.getByRole("button", { name: "Delete" });
    expect(btn).toHaveAttribute("data-tip", "Delete");
    expect(btn).toHaveClass("ui-danger");
  });
});

describe("Card", () => {
  it("is a region named by its title", () => {
    render(
      <Card title="Certificate" actions={<Button>Renew now</Button>}>
        <p>Valid until 2027-02-18</p>
      </Card>,
    );
    const region = screen.getByRole("region", { name: "Certificate" });
    expect(within(region).getByRole("heading", { name: "Certificate" })).toBeInTheDocument();
    expect(within(region).getByRole("button", { name: "Renew now" })).toBeInTheDocument();
  });
});

describe("Badge", () => {
  it("shows the state as a word", () => {
    render(<Badge tone="warn">Expires soon</Badge>);
    expect(screen.getByText("Expires soon")).toHaveClass("ui-badge", "ui-tone-warn");
  });
});

describe("Notice", () => {
  it("announces errors as alerts and other tones as status", () => {
    render(
      <>
        <Notice tone="err" title="Certificate failed">
          Let&apos;s Encrypt could not reach the site.
        </Notice>
        <Notice tone="ok" title="Saved" />
      </>,
    );
    expect(screen.getByRole("alert")).toHaveTextContent("Certificate failed");
    expect(screen.getByRole("status")).toHaveTextContent("Saved");
  });
});

describe("Field", () => {
  it("labels the control and links hint and error", () => {
    render(
      <Field label="Domain" hint="Without www." error="Already in use">
        <input className="ui-input" />
      </Field>,
    );
    const input = screen.getByLabelText("Domain");
    expect(input).toHaveAttribute("aria-invalid", "true");
    expect(input).toHaveAccessibleDescription("Without www. Already in use");
  });

  it("keeps an id the control already has", () => {
    render(
      <Field label="Name">
        <input id="site-name" />
      </Field>,
    );
    expect(screen.getByLabelText("Name")).toHaveAttribute("id", "site-name");
    expect(screen.getByLabelText("Name")).not.toHaveAttribute("aria-describedby");
  });
});

describe("Tabs", () => {
  const tabs = [
    { id: "overview", label: "Overview" },
    { id: "php", label: "PHP" },
    { id: "cron", label: "Cron" },
  ];

  function Harness() {
    const [active, setActive] = useState("overview");
    return (
      <>
        <Tabs label="example.com" tabs={tabs} active={active} onChange={setActive} idPrefix="site" />
        <div {...tabPanelProps("site", active)}>Panel {active}</div>
      </>
    );
  }

  it("switches by click and by arrow keys, with one tab in the tab order", async () => {
    const user = userEvent.setup();
    render(<Harness />);
    const list = screen.getByRole("tablist", { name: "example.com" });
    expect(within(list).getByRole("tab", { selected: true })).toHaveTextContent("Overview");
    expect(screen.getByRole("tabpanel", { name: "Overview" })).toHaveTextContent("Panel overview");

    await user.click(screen.getByRole("tab", { name: "PHP" }));
    expect(screen.getByRole("tabpanel", { name: "PHP" })).toBeInTheDocument();

    await user.keyboard("{ArrowRight}");
    expect(screen.getByRole("tab", { name: "Cron" })).toHaveFocus();
    expect(screen.getByRole("tab", { name: "Cron" })).toHaveAttribute("aria-selected", "true");

    await user.keyboard("{ArrowRight}");
    expect(screen.getByRole("tab", { name: "Overview" })).toHaveFocus();
    await user.keyboard("{End}");
    expect(screen.getByRole("tab", { name: "Cron" })).toHaveFocus();
    await user.keyboard("{Home}{ArrowLeft}");
    expect(screen.getByRole("tab", { name: "Cron" })).toHaveFocus();

    expect(screen.getAllByRole("tab").filter((t) => t.tabIndex === 0)).toHaveLength(1);
  });
});

type Site = { name: string; disk: number };
const columns: Column<Site>[] = [
  { key: "name", header: "Name", cell: (s) => s.name, sortValue: (s) => s.name },
  { key: "disk", header: "Disk", cell: (s) => `${s.disk} GB`, sortValue: (s) => s.disk },
  { key: "actions", header: "Actions", cell: () => null, srHeader: true },
];
const sites: Site[] = [
  { name: "shop10.de", disk: 3 },
  { name: "Blog.de", disk: 12 },
  { name: "shop2.de", disk: 3 },
];

describe("sortRows and nextSort", () => {
  it("sorts text naturally and case-insensitively, numbers numerically, stable", () => {
    expect(sortRows(sites, columns, { key: "name", dir: "asc" }).map((s) => s.name)).toEqual([
      "Blog.de",
      "shop2.de",
      "shop10.de",
    ]);
    expect(sortRows(sites, columns, { key: "disk", dir: "desc" }).map((s) => s.name)).toEqual([
      "Blog.de",
      "shop10.de",
      "shop2.de",
    ]);
    expect(sortRows(sites, columns, { key: "actions", dir: "asc" })).toEqual(sites);
    expect(sortRows(sites, columns, undefined)).not.toBe(sites);
  });

  it("goes from ascending to descending and starts other columns ascending", () => {
    expect(nextSort(undefined, "name")).toEqual({ key: "name", dir: "asc" });
    expect(nextSort({ key: "name", dir: "asc" }, "name")).toEqual({ key: "name", dir: "desc" });
    expect(nextSort({ key: "name", dir: "desc" }, "name")).toEqual({ key: "name", dir: "asc" });
    expect(nextSort({ key: "name", dir: "desc" }, "disk")).toEqual({ key: "disk", dir: "asc" });
  });
});

describe("Table", () => {
  function Harness({ rows = sites }: { rows?: Site[] }) {
    const [sort, setSort] = useState<Sort | undefined>(undefined);
    return (
      <Table
        label="Sites"
        columns={columns}
        rows={rows}
        rowKey={(s) => s.name}
        sort={sort}
        onSort={setSort}
        empty="No sites yet."
      />
    );
  }

  const names = () =>
    screen
      .getAllByRole("row")
      .slice(1)
      .map((r) => within(r).getAllByRole("cell")[0]!.textContent);

  it("sorts by a header click and marks the column", async () => {
    const user = userEvent.setup();
    render(<Harness />);
    const table = screen.getByRole("table", { name: "Sites" });
    expect(names()).toEqual(["shop10.de", "Blog.de", "shop2.de"]);

    await user.click(within(table).getByRole("button", { name: "Name" }));
    expect(names()).toEqual(["Blog.de", "shop2.de", "shop10.de"]);
    expect(screen.getByRole("columnheader", { name: "Name" })).toHaveAttribute("aria-sort", "ascending");

    await user.click(within(table).getByRole("button", { name: "Name" }));
    expect(names()).toEqual(["shop10.de", "shop2.de", "Blog.de"]);
    expect(screen.getByRole("columnheader", { name: "Name" })).toHaveAttribute("aria-sort", "descending");
    expect(screen.getByRole("columnheader", { name: "Disk" })).not.toHaveAttribute("aria-sort");
    expect(within(table).queryByRole("button", { name: "Actions" })).not.toBeInTheDocument();
  });

  it("shows the empty text without rows", () => {
    render(<Harness rows={[]} />);
    expect(screen.getByRole("cell", { name: "No sites yet." })).toBeInTheDocument();
  });
});

describe("relativeTime", () => {
  const now = new Date("2026-11-20T12:00:00Z");
  it.each([
    ["2026-11-20T11:59:30Z", "just now"],
    ["2026-11-20T11:55:00Z", "5 minutes ago"],
    ["2026-11-20T09:00:00Z", "3 hours ago"],
    ["2026-11-19T10:00:00Z", "yesterday"],
    ["2026-11-17T12:00:00Z", "3 days ago"],
    ["2026-11-06T12:00:00Z", "2 weeks ago"],
    ["2026-08-20T12:00:00Z", "3 months ago"],
    ["2024-11-20T12:00:00Z", "2 years ago"],
    ["2026-11-20T14:00:00Z", "in 2 hours"],
    ["not a date", ""],
  ])("%s", (iso, want) => {
    expect(relativeTime(new Date(iso), now)).toBe(want);
  });

  it("shows the exact time in a tooltip and the label", () => {
    render(<RelTime iso="2026-11-17T12:00:00Z" now={now} />);
    const t = screen.getByText("3 days ago");
    expect(t).toHaveAttribute("datetime", "2026-11-17T12:00:00Z");
    expect(t.getAttribute("data-tip")).toMatch(/17 Nov 2026/);
    expect(t).toHaveAccessibleName(/^3 days ago, 17 Nov 2026/);
  });
});

describe("Switch", () => {
  it("is a switch named by its label and described by its hint", async () => {
    const changed = vi.fn();
    render(
      <Switch checked={false} onChange={changed} hint="Applies to new visits.">
        Maintenance mode
      </Switch>,
    );
    const sw = screen.getByRole("switch", { name: "Maintenance mode" });
    expect(sw).toHaveAccessibleDescription("Applies to new visits.");
    await userEvent.click(sw);
    expect(changed).toHaveBeenCalledWith(true);
  });

  it("names the save state beside a save button", () => {
    const { rerender } = render(<UnsavedNote dirty={false} />);
    expect(screen.getByText("No changes")).toBeInTheDocument();
    rerender(<UnsavedNote dirty />);
    expect(screen.getByText("Unsaved changes")).toBeInTheDocument();
  });
});

describe("Menu", () => {
  it("runs an action, offers a link and moves with the arrow keys", async () => {
    const user = userEvent.setup();
    const rename = vi.fn();
    render(
      <Menu
        label="Actions for a.txt"
        buttonLabel="Actions for a.txt"
        button="⋯"
        items={[
          { label: "Download", href: "/dl/a.txt", download: true },
          { label: "Rename", onSelect: rename },
          { label: "Delete", danger: true, onSelect: () => undefined },
        ]}
      />,
    );
    await user.click(screen.getByRole("button", { name: "Actions for a.txt" }));
    const menu = screen.getByRole("menu", { name: "Actions for a.txt" });
    expect(within(menu).getByRole("menuitem", { name: "Download" })).toHaveAttribute("href", "/dl/a.txt");
    expect(within(menu).getByRole("menuitem", { name: "Download" })).toHaveFocus();
    await user.keyboard("{End}");
    expect(within(menu).getByRole("menuitem", { name: "Delete" })).toHaveFocus();
    await user.keyboard("{ArrowUp}{Enter}");
    expect(rename).toHaveBeenCalledOnce();
    expect(screen.queryByRole("menu")).toBeNull();
  });
});
