// SPDX-License-Identifier: AGPL-3.0-or-later

import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { useState } from "react";
import { describe, expect, it, vi } from "vitest";
import { Audit } from "./Audit";
import { actionGroups, auditQuery, detailPairs, exportUrl, localDay, type AuditEntry } from "./audit";

const entry = (id: number, over: Partial<AuditEntry> = {}): AuditEntry => ({
  id,
  at: "2026-11-21T10:00:00Z",
  userId: 1,
  actor: "kim",
  action: "site.create",
  target: "shop",
  detail: {},
  ...over,
});

// stubAudit answers the audit routes and records the queries of the list.
function stubAudit(pages: (q: URLSearchParams) => { entries: AuditEntry[]; next: string | null }) {
  const lists: URLSearchParams[] = [];
  vi.stubGlobal(
    "fetch",
    vi.fn(async (input: RequestInfo | URL) => {
      const url = new URL(String(input), "https://panel.test");
      let body: unknown;
      if (url.pathname === "/api/v1/audit/actions") body = { actions: ["login", "login.failed", "site.cert.upload", "site.create"] };
      else if (url.pathname === "/api/v1/audit") {
        lists.push(url.searchParams);
        body = pages(url.searchParams);
      } else throw new Error("unexpected request " + url.pathname);
      return new Response(JSON.stringify(body), { status: 200, headers: { "Content-Type": "application/json" } });
    }),
  );
  return lists;
}

function Harness({ initial = "" }: { initial?: string }) {
  const [query, setQuery] = useState(new URLSearchParams(initial));
  return <Audit query={query} onQuery={setQuery} />;
}

const rows = () =>
  within(screen.getByRole("table", { name: "Audit log" }))
    .getAllByRole("row")
    .slice(1)
    .map((r) => r.textContent ?? "");

describe("Audit", () => {
  it("lists entries with details and pages to older ones", async () => {
    const user = userEvent.setup();
    const lists = stubAudit((q) =>
      q.get("before") === "2"
        ? { entries: [entry(1, { action: "login.failed", actor: "", userId: null, target: "=evil", detail: { ip: "192.0.2.7" } })], next: null }
        : { entries: [entry(3, { actor: "anna", userId: null }), entry(2, { detail: { php: "8.4", ip: "192.0.2.1" } })], next: "2" },
    );
    render(<Harness />);

    await screen.findByRole("table", { name: "Audit log" });
    expect(rows()[0]).toContain("anna (deleted)");
    expect(rows()[1]).toContain("ip192.0.2.1");
    expect(rows()[1]).toContain("php8.4");

    await user.click(screen.getByRole("button", { name: "Show older entries" }));
    await waitFor(() => expect(rows()).toHaveLength(3));
    expect(rows()[2]).toContain("none");
    expect(rows()[2]).toContain("=evil");
    expect(screen.queryByRole("button", { name: "Show older entries" })).not.toBeInTheDocument();
    expect(lists.at(-1)?.get("before")).toBe("2");
  });

  it("filters through the address and exports the same filter", async () => {
    const user = userEvent.setup();
    const lists = stubAudit(() => ({ entries: [], next: null }));
    render(<Harness initial="target=shop" />);

    expect(await screen.findByText("No entry matches the filters.")).toBeInTheDocument();
    expect(lists[0]?.get("target")).toBe("shop");

    await user.selectOptions(screen.getByRole("combobox", { name: "Action" }), "site");
    await waitFor(() => expect(lists.at(-1)?.get("action")).toBe("site"));

    const csv = screen.getByRole("link", { name: "CSV" });
    expect(csv.getAttribute("href")).toContain("format=csv");
    expect(csv.getAttribute("href")).toContain("action=site");
    expect(csv.getAttribute("href")).toContain("target=shop");
    expect(screen.getByRole("link", { name: "JSON" }).getAttribute("href")).toContain("format=json");
  });

  it("offers a group filter only for groups with several actions", async () => {
    stubAudit(() => ({ entries: [], next: null }));
    render(<Harness />);
    await screen.findByText("The audit log is empty.");
    await screen.findByRole("option", { name: "site.* (all)" });
    expect(screen.getByRole("option", { name: "login.* (all)" })).toBeInTheDocument();
  });
});

describe("audit helpers", () => {
  it("turns local days into a time range that includes the last day", () => {
    const q = auditQuery({ from: "2026-11-01", to: "2026-11-21", actor: " kim ", target: "" });
    expect(q.get("since")).toBe(new Date(2026, 10, 1).toISOString());
    expect(q.get("until")).toBe(new Date(2026, 10, 22).toISOString());
    expect(q.get("actor")).toBe("kim");
    expect(q.has("target")).toBe(false);
    expect(auditQuery({ from: "21.11.2026" }).has("since")).toBe(false);
    expect(localDay("2026-12-31", 1)?.getFullYear()).toBe(2027);
  });

  it("builds the export address", () => {
    expect(exportUrl("csv", {})).toBe("/api/v1/audit/export?format=csv");
  });

  it("groups actions and shows details", () => {
    expect(actionGroups(["user.create", "login", "site.create", "site.delete"])).toEqual([
      { prefix: "login", actions: ["login"] },
      { prefix: "site", actions: ["site.create", "site.delete"] },
      { prefix: "user", actions: ["user.create"] },
    ]);
    expect(detailPairs({ z: "1", a: 2, b: { c: true } })).toEqual([
      ["a", "2"],
      ["b", '{"c":true}'],
      ["z", "1"],
    ]);
  });
});
