// SPDX-License-Identifier: AGPL-3.0-or-later

import { describe, expect, it } from "vitest";
import { groupScopes, scopeRows } from "./TokenGrant";

const s = (name: string) => ({ name, description: name, highRisk: false });

describe("groupScopes", () => {
  it("sorts scopes into areas, reading first, and keeps unknown ones under Other", () => {
    const groups = groupScopes([s("databases:delete"), s("cron:write"), s("databases:read"), s("sites:read"), s("cron:read"), s("new:read"), s("audit:read")]);
    expect(groups.map((g) => [g.label, g.scopes.map((x) => x.name)])).toEqual([
      ["Sites", ["sites:read", "cron:read", "cron:write"]],
      ["Databases", ["databases:read", "databases:delete"]],
      ["Panel", ["audit:read"]],
      ["Other", ["new:read"]],
    ]);
  });
});

describe("scopeRows", () => {
  it("gives each area its own rows, reading and changing side by side", () => {
    const rows = scopeRows([s("sites:read"), s("sites:write"), s("sites:delete"), s("doctor:read"), s("php:install"), s("cron:read"), s("cron:write")]);
    expect(rows.map((r) => [r.scope.name, r.row])).toEqual([
      ["sites:read", 2],
      ["sites:write", 2],
      ["sites:delete", 3],
      ["doctor:read", 4],
      ["php:install", 5],
      ["cron:read", 6],
      ["cron:write", 6],
    ]);
  });
});
