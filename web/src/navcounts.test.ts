// SPDX-License-Identifier: AGPL-3.0-or-later

import { describe, expect, it } from "vitest";
import { countLabel, healthCount, servicesCount } from "./navcounts";

const check = (status: "ok" | "warn" | "fail") => ({ id: "x", status, summary: "" });

describe("nav counts", () => {
  it("counts failed and warning checks, red when one failed", () => {
    expect(healthCount({ checks: [check("ok")] })).toBeNull();
    expect(healthCount({ checks: [] })).toBeNull();
    expect(healthCount({ checks: [check("warn"), check("warn"), check("ok")] })).toEqual({ n: 2, tone: "warn" });
    expect(healthCount({ checks: [check("warn"), check("fail")] })).toEqual({ n: 2, tone: "err" });
  });

  it("counts failed services only", () => {
    expect(
      servicesCount([
        { installed: true, active: "active" },
        { installed: true, active: "failed" },
        { installed: true, active: "inactive" },
        { installed: false, active: "failed" },
        { installed: true, active: "activating" },
      ]),
    ).toEqual({ n: 1, tone: "err" });
    expect(servicesCount([{ installed: true, active: "active" }])).toBeNull();
  });

  it("says what it counts", () => {
    expect(countLabel({ n: 1, tone: "err" })).toBe("1 problem");
    expect(countLabel({ n: 3, tone: "err" })).toBe("3 problems");
    expect(countLabel({ n: 1, tone: "warn" })).toBe("1 warning");
    expect(countLabel({ n: 2, tone: "warn" })).toBe("2 warnings");
  });
});
