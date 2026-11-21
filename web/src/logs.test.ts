// SPDX-License-Identifier: AGPL-3.0-or-later

import { describe, expect, it } from "vitest";
import { appendLines, filterLines, lineTone, retentionProblem } from "./logs";

describe("logs", () => {
  it("keeps the newest lines", () => {
    expect(appendLines(["a", "b"], ["c"], 2)).toEqual(["b", "c"]);
    expect(appendLines([], ["a"], 2)).toEqual(["a"]);
  });

  it("filters by every word, case insensitive", () => {
    const l = ['1.2.3.4 "GET /wp-login.php" 404', '1.2.3.4 "GET /" 200'];
    expect(filterLines(l, "WP-LOGIN 404")).toEqual([l[0]]);
    expect(filterLines(l, "  ")).toEqual(l);
    expect(filterLines(l, "wp-login 200")).toEqual([]);
  });

  it("colours lines", () => {
    expect(lineTone("access", '1.2.3.4 - - [x] "GET / HTTP/2.0" 503 12 "-" "ua"')).toBe("err");
    expect(lineTone("access", '1.2.3.4 - - [x] "GET / HTTP/2.0" 404 12 "-" "ua"')).toBe("warn");
    expect(lineTone("access", '1.2.3.4 - - [x] "GET / HTTP/2.0" 200 12 "-" "ua"')).toBe("");
    expect(lineTone("error", "2026/11/21 [error] 1#1: x")).toBe("err");
    expect(lineTone("php", "NOTICE: PHP message: PHP Warning:  x")).toBe("warn");
    expect(lineTone("php", "NOTICE: PHP message: PHP Fatal error:  x")).toBe("err");
  });

  it("checks the retention", () => {
    const r = { minDays: 1, maxDays: 365 };
    expect(retentionProblem("7", r)).toBe("");
    expect(retentionProblem("0", r)).not.toBe("");
    expect(retentionProblem("366", r)).not.toBe("");
    expect(retentionProblem("7.5", r)).not.toBe("");
    expect(retentionProblem("", r)).not.toBe("");
  });
});
