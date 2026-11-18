// SPDX-License-Identifier: AGPL-3.0-or-later

import { render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { App } from "./App";
import { mockFetch } from "./test-utils";

describe("App", () => {
  it("shows the server status", async () => {
    mockFetch({ "GET /api/v1/health": { body: { status: "ok", agent: "ok", db: "ok", version: "0.1.0" } } });
    render(<App />);
    expect(await screen.findByText("0.1.0")).toBeInTheDocument();
    expect(screen.getByRole("heading", { name: "Hostashi" })).toBeInTheDocument();
  });

  it("shows an error when the server is not reachable", async () => {
    vi.stubGlobal("fetch", vi.fn(async () => Promise.reject(new TypeError("Failed to fetch"))));
    render(<App />);
    expect(await screen.findByRole("alert")).toHaveTextContent("Failed to fetch");
  });
});
