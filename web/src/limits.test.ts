// SPDX-License-Identifier: AGPL-3.0-or-later

import { describe, expect, it } from "vitest";
import { presetCacheProblem } from "./limits";

const mib = 1024 ** 2;

describe("presetCacheProblem", () => {
  it("allows an off cache and no memory limit", () => {
    expect(presetCacheProblem({ objectCacheMb: 0 }, 128 * mib)).toBe("");
    expect(presetCacheProblem({ objectCacheMb: 1024 }, 0)).toBe("");
  });

  it("allows half of the memory limit", () => {
    expect(presetCacheProblem({ objectCacheMb: 256 }, 512 * mib)).toBe("");
  });

  it("refuses more than half of the memory limit", () => {
    expect(presetCacheProblem({ objectCacheMb: 512 }, 512 * mib)).toContain(
      "256 MiB",
    );
  });
});
