// SPDX-License-Identifier: AGPL-3.0-or-later

import { describe, expect, it } from "vitest";
import { parseHash } from "./routes";
import { pageTitle } from "./Shell";

describe("pageTitle", () => {
  const host = "panel.example.com";
  it("names the page and the host", () => {
    expect(pageTitle(parseHash("#/"), host)).toBe("Overview · panel.example.com");
    expect(pageTitle(parseHash("#/backups"), host)).toBe("Backups · panel.example.com");
    expect(pageTitle(parseHash("#/account"), host)).toBe("Your account · panel.example.com");
    expect(pageTitle(parseHash("#/node"), host)).toBe("Node.js · Runtimes · panel.example.com");
  });
  it("puts a site's tab before its name", () => {
    expect(pageTitle(parseHash("#/sites/blog"), host)).toBe("blog · panel.example.com");
    expect(pageTitle(parseHash("#/sites/blog/logs"), host)).toBe("Logs · blog · panel.example.com");
    expect(pageTitle(parseHash("#/sites/blog/realip"), host)).toBe("Visitor address · blog · panel.example.com");
  });
  it("falls back to the product name without a host", () => {
    expect(pageTitle(parseHash("#/sites"), "")).toBe("Sites · Hostashi");
  });
});
