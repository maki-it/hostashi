// SPDX-License-Identifier: AGPL-3.0-or-later

import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import { parseEnv } from "./node";
import { EnvCard } from "./SiteEnv";
import type { Site } from "./sites";
import { mockFetch } from "./test-utils";

const site = { name: "shop", type: "node" } as Site;

describe("parseEnv", () => {
  it("reads NAME=value lines as written", () => {
    expect(parseEnv('# comment\nDB_URL=mysql://u:p@x/db?a=b\n\nGREETING="hi there"\nEMPTY=\r\n')).toEqual({
      vars: [
        { name: "DB_URL", value: "mysql://u:p@x/db?a=b" },
        { name: "GREETING", value: '"hi there"' },
        { name: "EMPTY", value: "" },
      ],
      problem: "",
    });
  });

  it.each([
    ["no equals sign", "JUST_A_NAME", "Line 1: write NAME=value."],
    ["lower case", "api_key=1", "Line 1: use A-Z"],
    ["reserved", "PORT=80", "Line 1: PORT is set by Hostashi."],
    ["twice", "A=1\nA=2", "Line 2: A is set twice."],
    ["too long", "A=" + "x".repeat(4097), "Line 1: the value is longer"],
  ])("refuses %s", (_, text, want) => {
    expect(parseEnv(text).problem).toContain(want);
  });
});

describe("EnvCard", () => {
  it("shows the names and saves edited values", async () => {
    const user = userEvent.setup();
    const calls = mockFetch({
      "GET /api/v1/sites/shop/app/env": { body: { names: ["API_KEY"] } },
      "GET /api/v1/sites/shop/app/env/values": { body: { variables: [{ name: "API_KEY", value: "s3cr3t" }] } },
      "PUT /api/v1/sites/shop/app/env": {
        body: { names: ["API_KEY", "NODE_ENV"], status: { state: "active", subState: "running", restarts: 0, usedBytes: 0, since: "" } },
      },
    });
    render(<EnvCard site={site} />);
    expect(await screen.findByText("API_KEY=")).toBeInTheDocument();
    expect(screen.queryByText(/s3cr3t/)).not.toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Edit variables" }));
    const box = await screen.findByLabelText("Variables of shop");
    expect(box).toHaveValue("API_KEY=s3cr3t");
    await user.type(box, "{Enter}NODE_ENV=staging");
    await user.click(screen.getByRole("button", { name: "Save variables" }));
    expect(await screen.findByText("Saved. The app was restarted with them.")).toBeInTheDocument();
    expect(calls.find((c) => c.method === "PUT")?.body).toEqual({
      variables: [
        { name: "API_KEY", value: "s3cr3t" },
        { name: "NODE_ENV", value: "staging" },
      ],
    });
    expect(screen.queryByText(/s3cr3t/)).not.toBeInTheDocument();
  });

  it("does not send what it refuses", async () => {
    const user = userEvent.setup();
    const calls = mockFetch({
      "GET /api/v1/sites/shop/app/env": { body: { names: [] } },
      "GET /api/v1/sites/shop/app/env/values": { body: { variables: [] } },
    });
    render(<EnvCard site={site} />);
    await user.click(await screen.findByRole("button", { name: "Add variables" }));
    await user.type(await screen.findByLabelText("Variables of shop"), "HOME=/tmp");
    await user.click(screen.getByRole("button", { name: "Save variables" }));
    expect(screen.getByText("Line 1: HOME is set by Hostashi.")).toBeInTheDocument();
    expect(calls.some((c) => c.method === "PUT")).toBe(false);
  });
});
