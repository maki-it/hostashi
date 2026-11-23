// SPDX-License-Identifier: AGPL-3.0-or-later

import { describe, expect, it } from "vitest";
import { groupKey } from "./SecondFactor";

describe("groupKey", () => {
  it("splits the key into groups of four", () => {
    expect(groupKey("JBSWY3DPEHPK3PXP")).toBe("JBSW Y3DP EHPK 3PXP");
    expect(groupKey("JBSWY3DPEH")).toBe("JBSW Y3DP EH");
    expect(groupKey("ABCD")).toBe("ABCD");
    expect(groupKey("")).toBe("");
  });
});
