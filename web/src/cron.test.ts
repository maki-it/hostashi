// SPDX-License-Identifier: AGPL-3.0-or-later

import { describe, expect, it } from "vitest";
import { ApiError } from "./api";
import { commandProblem, cronMessage, describeSchedule, lastRun, niceProblem, scheduleProblem, timeoutProblem } from "./cron";

describe("scheduleProblem", () => {
  it.each([
    ["*/5 * * * *", ""],
    ["0 3 * * *", ""],
    ["0,30 8-18 * * mon-fri", ""],
    ["15 2 1 jan,jul sun", ""],
    ["5/10 * * * 7", ""],
    ["1-30/5 * * * *", ""],
    ["  @Daily ", ""],
    ["@hourly", ""],
    ["@annually", ""],
    ["", "Enter a schedule"],
    ["@reboot", "@reboot is not supported"],
    ["@often", "not a known keyword"],
    ["* * * *", "five fields"],
    ["* * * * * *", "five fields"],
    ["60 * * * *", "minute 60 is outside 0-59"],
    ["* 24 * * *", "hour 24 is outside 0-23"],
    ["* * 0 * *", "day of month 0 is outside 1-31"],
    ["* * * 13 *", "month 13 is outside 1-12"],
    ["* * * * 8", "day of week 8 is outside 0-7"],
    ["*/0 * * * *", "step"],
    ["*/61 * * * *", "step"],
    ["10-5 * * * *", "runs backwards"],
    ["* * * dec-jan *", "runs backwards"],
    ["a * * * *", "not valid"],
    ["1,,2 * * * *", "not valid"],
    ["-1 * * * *", "not valid"],
    ["* * * * " + "1,".repeat(100) + "1", "longer than 200"],
  ] as const)("%j", (v, want) => {
    const got = scheduleProblem(v);
    if (want === "") expect(got).toBe("");
    else expect(got).toContain(want);
  });
});

describe("commandProblem", () => {
  it.each([
    ["php htdocs/wp-cron.php", ""],
    ["date +%Y-%m-%d >> log.txt\t# tab is fine", ""],
    ["", "Enter a command"],
    ["   ", "Enter a command"],
    ["echo a\necho b", "one line"],
    ["echo a\recho b", "one line"],
    ["echo \u2028", "one line"],
    ["echo \u0000", "one line"],
    ["x".repeat(4096), ""],
    ["x".repeat(4097), "longer than 4096 bytes"],
    // Bytes, not characters: 2049 × "ä" is 4098 bytes.
    ["ä".repeat(2049), "longer than 4096 bytes"],
  ] as const)("%j", (v, want) => {
    const got = commandProblem(v);
    if (want === "") expect(got).toBe("");
    else expect(got).toContain(want);
  });
});

describe("limits", () => {
  it.each([
    [1, ""],
    [1440, ""],
    [0, "whole minutes"],
    [1441, "whole minutes"],
    [1.5, "whole minutes"],
    [NaN, "whole minutes"],
  ])("timeout %d", (v, want) => {
    const got = timeoutProblem(v);
    if (want === "") expect(got).toBe("");
    else expect(got).toContain(want);
  });

  it.each([
    [0, ""],
    [19, ""],
    [-1, "0 to 19"],
    [20, "0 to 19"],
    [NaN, "0 to 19"],
  ])("nice %d", (v, want) => {
    const got = niceProblem(v);
    if (want === "") expect(got).toBe("");
    else expect(got).toContain(want);
  });
});

describe("describeSchedule", () => {
  it.each([
    ["@daily", "every day at 00:00"],
    ["*/15 * * * *", "every 15 minutes"],
    ["7 * * * *", "every hour at minute 7"],
    ["30 4 * * *", "every day at 04:30"],
    ["* * * * *", "every minute"],
    ["0 4 * * 1", ""],
  ])("%s", (v, want) => expect(describeSchedule(v)).toBe(want));
});

describe("lastRun", () => {
  it.each([
    [undefined, ""],
    [{ running: true }, "Running now"],
    [{ running: false }, "Not run yet"],
    [{ running: false, result: "success", lastStart: 1 }, "Succeeded"],
    [{ running: false, result: "exit-code", exitStatus: 2, lastStart: 1 }, "exit code 2"],
    [{ running: false, result: "timeout", lastStart: 1 }, "longer than its time limit"],
    [{ running: false, result: "signal", lastStart: 1 }, "Killed"],
    [{ running: false, result: "oom-kill", lastStart: 1 }, "(oom-kill)"],
  ])("%j", (st, want) => expect(lastRun(st)).toContain(want));
});

describe("cronMessage", () => {
  it.each([
    [new ApiError(409, "too_many_cron_jobs", "x"), "at most 50"],
    [new ApiError(409, "agent_precondition", "the site is disabled"), "The site is disabled"],
    [new ApiError(503, "cron_unavailable", "x"), "not available"],
    [new ApiError(404, "not_found", "no such cron job"), "This cron job does not exist"],
    [new ApiError(404, "not_found", "no such site"), "This site does not exist"],
    [new ApiError(422, "invalid_argument", "cron job 1: cron: invalid schedule"), "Cron job 1: cron: invalid schedule."],
    [new TypeError("fetch"), "not reachable"],
  ])("%s", (err, want) => expect(cronMessage(err)).toContain(want));
});
