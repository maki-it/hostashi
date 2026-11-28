// SPDX-License-Identifier: AGPL-3.0-or-later

import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import { CronSettings } from "./Cron";
import type { CronJob, CronList } from "./cron";
import type { Site } from "./sites";
import { setLanguage } from "./i18n";
import { mockFetch } from "./test-utils";

const site: Site = {
  name: "blog",
  pool: "default",
  state: "active",
  php: "8.4",
  phpPrograms: false,
  disabled: false,
  disabledPage: "builtin-maintenance",
  maintenance: { enabled: false, page: "builtin-maintenance", allowIps: [] },
  domains: ["blog.example.com"],
  tls: { enabled: false, active: false, hsts: false, domains: [], notAfter: null },
  createdAt: "2026-11-19T08:00:00Z",
  updatedAt: "2026-11-19T08:00:00Z",
};

const wpCron: CronJob = {
  id: 1,
  schedule: "*/5 * * * *",
  command: "php htdocs/wp-cron.php",
  timeoutSec: 0,
  nice: null,
  paused: false,
  createdAt: 1,
  updatedAt: 1,
  status: { running: false, result: "success", exitStatus: 0, lastStart: 1795600000, lastEnd: 1795600002, next: 1795600300 },
};

const base = "/api/v1/sites/blog/cron";
const err = (status: number, code: string, message = "") => ({ status, body: { error: { code, message } } });

describe("CronSettings", () => {
  it("lists jobs with their last and next run", async () => {
    const failed: CronJob = {
      ...wpCron,
      id: 2,
      schedule: "0 3 * * *",
      command: "./backup.sh",
      timeoutSec: 7200,
      nice: 15,
      status: { running: false, result: "exit-code", exitStatus: 3, lastStart: 1795600000 },
    };
    const paused: CronJob = { ...wpCron, id: 3, schedule: "@weekly", paused: true, status: { running: false } };
    mockFetch({ ["GET " + base]: { body: { jobs: [wpCron, failed, paused], statusKnown: true } } });
    render(<CronSettings site={site} />);

    const one = within(await screen.findByLabelText("Details of cron job 1"));
    expect(one.getByText(/^Succeeded/)).toBeInTheDocument();
    expect(one.getByText("Limits: 60 min, priority 10")).toBeInTheDocument();
    expect(screen.getByText("every 5 minutes")).toBeInTheDocument();
    expect(screen.getAllByText("php htdocs/wp-cron.php")).toHaveLength(2);

    const two = within(screen.getByLabelText("Details of cron job 2"));
    expect(two.getByText(/with exit code 3/)).toBeInTheDocument();
    expect(two.getByText("Limits: 120 min, priority 15")).toBeInTheDocument();
    expect(two.getByText("Not planned")).toBeInTheDocument();

    const three = within(screen.getByLabelText("Details of cron job 3"));
    expect(three.getByText("Not run yet")).toBeInTheDocument();
    // The badge beside the schedule and the next run.
    expect(three.getAllByText("Paused")).toHaveLength(2);
    expect(screen.getByText("Paused", { selector: ".ui-badge" })).toBeInTheDocument();
  });

  it("says when the status is not known", async () => {
    mockFetch({ ["GET " + base]: { body: { jobs: [{ ...wpCron, status: undefined }], statusKnown: false } } });
    render(<CronSettings site={site} />);
    expect(await screen.findByText(/Last and next runs are not known/)).toBeInTheDocument();
    const d = within(screen.getByLabelText("Details of cron job 1"));
    expect(d.getAllByText("Unknown")).toHaveLength(2);
  });

  it("shows an error when the list cannot be loaded", async () => {
    mockFetch({ ["GET " + base]: err(503, "cron_unavailable") });
    render(<CronSettings site={site} />);
    expect(await screen.findByRole("alert")).toHaveTextContent("Cron jobs are not available on this panel.");
    expect(screen.queryByRole("button", { name: "Add cron job" })).not.toBeInTheDocument();
  });

  it("adds a job with the defaults after checking the form", async () => {
    const user = userEvent.setup();
    let list: CronList = { jobs: [], statusKnown: true };
    const calls = mockFetch({
      ["GET " + base]: () => ({ body: list }),
      ["POST " + base]: (b) => {
        const j = { ...wpCron, ...(b as object), id: 7, status: undefined } as CronJob;
        list = { jobs: [j], statusKnown: true };
        return { status: 201, body: j };
      },
    });
    render(<CronSettings site={site} />);
    expect(await screen.findByText("No cron jobs yet.")).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Add cron job" }));
    const form = within(screen.getByRole("form", { name: "New cron job" }));

    await user.click(form.getByRole("button", { name: "Add cron job" }));
    expect(form.getByRole("alert")).toHaveTextContent("Enter a schedule.");
    await user.type(form.getByLabelText(/^Schedule/), "@reboot");
    await user.click(form.getByRole("button", { name: "Add cron job" }));
    expect(form.getByRole("alert")).toHaveTextContent("@reboot is not supported");
    await user.clear(form.getByLabelText(/^Schedule/));
    await user.type(form.getByLabelText(/^Schedule/), "30 4 * * *");
    expect(form.getByText(/Runs every day at 04:30/)).toBeInTheDocument();
    await user.click(form.getByRole("button", { name: "Add cron job" }));
    expect(form.getByRole("alert")).toHaveTextContent("Enter a command.");
    await user.type(form.getByLabelText(/^Command/), "date +%F >> cron.log");
    await user.clear(form.getByLabelText(/^Time limit/));
    await user.type(form.getByLabelText(/^Time limit/), "0");
    await user.click(form.getByRole("button", { name: "Add cron job" }));
    expect(form.getByRole("alert")).toHaveTextContent("whole minutes from 1 to 1440");
    await user.clear(form.getByLabelText(/^Time limit/));
    await user.type(form.getByLabelText(/^Time limit/), "60");
    await user.clear(form.getByLabelText(/^Priority/));
    await user.click(form.getByRole("button", { name: "Add cron job" }));
    expect(form.getByRole("alert")).toHaveTextContent("0 to 19");
    await user.type(form.getByLabelText(/^Priority/), "10");
    expect(calls.filter((c) => c.method === "POST")).toHaveLength(0);

    await user.click(form.getByRole("button", { name: "Add cron job" }));
    expect(await screen.findByRole("status")).toHaveTextContent("Cron job added.");
    expect(calls.find((c) => c.method === "POST")?.body).toEqual({
      schedule: "30 4 * * *",
      command: "date +%F >> cron.log",
      timeoutSec: 0,
      nice: null,
      paused: false,
    });
    expect(screen.getByLabelText("Details of cron job 7")).toBeInTheDocument();
  });

  it("shows a schedule the server refuses and keeps the form", async () => {
    const user = userEvent.setup();
    mockFetch({
      ["GET " + base]: { body: { jobs: [], statusKnown: true } },
      ["POST " + base]: err(422, "invalid_argument", "cron job 1: cron: invalid schedule: day of month: 31 never in feb"),
    });
    render(<CronSettings site={site} />);
    await user.click(await screen.findByRole("button", { name: "Add cron job" }));
    const form = within(screen.getByRole("form", { name: "New cron job" }));
    await user.type(form.getByLabelText(/^Schedule/), "0 0 31 2 *");
    await user.type(form.getByLabelText(/^Command/), "true");
    await user.click(form.getByLabelText(/^Paused/));
    await user.click(form.getByRole("button", { name: "Add cron job" }));
    expect(await form.findByRole("alert")).toHaveTextContent("Cron job 1: cron: invalid schedule: day of month: 31 never in feb.");
    expect(form.getByLabelText(/^Schedule/)).toHaveValue("0 0 31 2 *");
  });

  it("changes a job and keeps explicit limits", async () => {
    const user = userEvent.setup();
    const job: CronJob = { ...wpCron, timeoutSec: 3600, nice: 10 };
    const calls = mockFetch({
      ["GET " + base]: { body: { jobs: [job], statusKnown: true } },
      ["PUT " + base + "/1"]: (b) => ({ body: { ...job, ...(b as object) } }),
    });
    render(<CronSettings site={site} />);
    await user.click(await screen.findByRole("button", { name: "More actions for cron job 1" }));
    await user.click(screen.getByRole("menuitem", { name: "Change" }));
    const form = within(screen.getByRole("form", { name: "Change cron job 1" }));
    expect(form.getByLabelText(/^Command/)).toHaveValue("php htdocs/wp-cron.php");
    await user.clear(form.getByLabelText(/^Schedule/));
    await user.type(form.getByLabelText(/^Schedule/), "*/10 * * * *");
    await user.click(form.getByRole("button", { name: "Save cron job" }));
    expect(await screen.findByRole("status")).toHaveTextContent("Cron job saved.");
    expect(calls.find((c) => c.method === "PUT")?.body).toEqual({
      schedule: "*/10 * * * *",
      command: "php htdocs/wp-cron.php",
      timeoutSec: 3600,
      nice: 10,
      paused: false,
    });
  });

  it("pauses, resumes and runs a job", async () => {
    const user = userEvent.setup();
    let cur = wpCron;
    const calls = mockFetch({
      ["GET " + base]: () => ({ body: { jobs: [cur], statusKnown: true } }),
      ["PUT " + base + "/1"]: (b) => {
        cur = { ...cur, ...(b as object) };
        return { body: cur };
      },
      ["POST " + base + "/1/run"]: { status: 202 },
    });
    render(<CronSettings site={site} />);
    await user.click(await screen.findByRole("button", { name: "More actions for cron job 1" }));
    await user.click(screen.getByRole("menuitem", { name: "Pause" }));
    expect(await screen.findByRole("status")).toHaveTextContent("Cron job paused.");
    expect(await screen.findByText("Paused", { selector: ".ui-badge" })).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "More actions for cron job 1" }));
    await user.click(screen.getByRole("menuitem", { name: "Resume" }));
    expect(await screen.findByText("Cron job resumed.")).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Run now" }));
    expect(await screen.findByText(/The cron job started/)).toBeInTheDocument();
    expect(calls.filter((c) => c.method !== "GET").map((c) => [c.method, c.path, (c.body as { paused?: boolean } | undefined)?.paused])).toEqual([
      ["PUT", base + "/1", true],
      ["PUT", base + "/1", false],
      ["POST", base + "/1/run", undefined],
    ]);
  });

  it("shows why a run was refused", async () => {
    const user = userEvent.setup();
    mockFetch({
      ["GET " + base]: { body: { jobs: [wpCron], statusKnown: true } },
      ["POST " + base + "/1/run"]: err(409, "agent_precondition", "the site is disabled"),
    });
    render(<CronSettings site={site} />);
    await user.click(await screen.findByRole("button", { name: "Run now" }));
    expect(await screen.findByRole("alert")).toHaveTextContent("The site is disabled. Enable it to run its cron jobs.");
  });

  it("offers no run on a disabled site or while a job runs", async () => {
    mockFetch({
      ["GET " + base]: { body: { jobs: [wpCron, { ...wpCron, id: 2, status: { running: true } }], statusKnown: true } },
    });
    const { rerender } = render(<CronSettings site={site} />);
    const runs = await screen.findAllByRole("button", { name: "Run now" });
    expect(runs[0]).toBeEnabled();
    expect(runs[1]).toBeDisabled();
    expect(screen.getByText("Running now")).toBeInTheDocument();

    rerender(<CronSettings site={{ ...site, disabled: true }} />);
    expect(screen.getByText(/its cron jobs do not run until it is enabled/)).toBeInTheDocument();
    for (const b of screen.getAllByRole("button", { name: "Run now" })) expect(b).toBeDisabled();
    expect(screen.getAllByText("Site disabled")).toHaveLength(2);
  });

  it("deletes a job after a confirmation", async () => {
    const user = userEvent.setup();
    let jobs = [wpCron];
    const calls = mockFetch({
      ["GET " + base]: () => ({ body: { jobs, statusKnown: true } }),
      ["DELETE " + base + "/1"]: () => {
        jobs = [];
        return { status: 204 };
      },
    });
    render(<CronSettings site={site} />);
    await user.click(await screen.findByRole("button", { name: "More actions for cron job 1" }));
    await user.click(screen.getByRole("menuitem", { name: "Delete" }));
    await user.click(screen.getByRole("button", { name: "Cancel" }));
    expect(calls.some((c) => c.method === "DELETE")).toBe(false);
    await user.click(screen.getByRole("button", { name: "More actions for cron job 1" }));
    await user.click(screen.getByRole("menuitem", { name: "Delete" }));
    await user.click(screen.getByRole("button", { name: "Delete cron job" }));
    expect(await screen.findByText("No cron jobs yet.")).toBeInTheDocument();
    expect(screen.getByRole("status")).toHaveTextContent("Cron job deleted.");
  });

  it("shows a job that is gone", async () => {
    const user = userEvent.setup();
    mockFetch({
      ["GET " + base]: { body: { jobs: [wpCron], statusKnown: true } },
      ["DELETE " + base + "/1"]: err(404, "not_found", "no such cron job"),
    });
    render(<CronSettings site={site} />);
    await user.click(await screen.findByRole("button", { name: "More actions for cron job 1" }));
    await user.click(screen.getByRole("menuitem", { name: "Delete" }));
    await user.click(screen.getByRole("button", { name: "Delete cron job" }));
    expect(await screen.findByRole("alert")).toHaveTextContent("This cron job does not exist any more.");
  });

  it("offers no new job at the limit", async () => {
    const jobs = Array.from({ length: 50 }, (_, i) => ({ ...wpCron, id: i + 1 }));
    mockFetch({ ["GET " + base]: { body: { jobs, statusKnown: true } } });
    render(<CronSettings site={site} />);
    expect(await screen.findByText("A site has at most 50 cron jobs.")).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Add cron job" })).not.toBeInTheDocument();
  });

  it("shows the output of the last run from the badge and the menu", async () => {
    const user = userEvent.setup();
    mockFetch({
      ["GET " + base]: { body: { jobs: [wpCron], statusKnown: true } },
      ["GET " + base + "/1/output"]: { body: { ran: true, lines: ["Hi!", "PHP Warning: x", "Fatal error: y"], truncated: true } },
    });
    render(<CronSettings site={site} />);

    const badge = await screen.findByRole("button", { name: /^Succeeded/ });
    expect(badge).toHaveAttribute("aria-expanded", "false");
    await user.click(badge);
    expect(badge).toHaveAttribute("aria-expanded", "true");

    const out = await screen.findByRole("region", { name: "Output of cron job 1" });
    const log = await within(out).findByRole("log", { name: "Output of cron job 1" });
    expect(within(log).getAllByRole("listitem").map((li) => li.textContent)).toEqual(["Hi!", "PHP Warning: x", "Fatal error: y"]);
    expect(within(log).getByText("PHP Warning: x")).toHaveClass("ui-cron-line-warn");
    expect(within(log).getByText("Fatal error: y")).toHaveClass("ui-cron-line-err");
    expect(within(log).getByText("Hi!")).not.toHaveClass("ui-cron-line-err");
    expect(within(log).getByText("Earlier lines are not shown.")).toBeInTheDocument();
    expect(within(out).getByText(/took 2 s · 3 lines \(the last ones\)/)).toBeInTheDocument();
    const wrap = within(out).getByRole("button", { name: "Wrap lines" });
    expect(wrap).toHaveAttribute("aria-pressed", "true");
    await user.click(wrap);
    expect(wrap).toHaveAttribute("aria-pressed", "false");
    expect(log).toHaveClass("ui-cron-output-nowrap");
    expect(within(out).getByRole("button", { name: "Copy output" })).toBeInTheDocument();

    await user.click(within(out).getByRole("button", { name: "Close" }));
    expect(screen.queryByRole("region", { name: "Output of cron job 1" })).not.toBeInTheDocument();

    await user.click(screen.getByRole("button", { name: "More actions for cron job 1" }));
    await user.click(screen.getByRole("menuitem", { name: "Show output" }));
    expect(await screen.findByRole("region", { name: "Output of cron job 1" })).toBeInTheDocument();
  });

  it("lists the last runs and shows the output of an earlier one", async () => {
    const user = userEvent.setup();
    const older = "0a2d3aa75594430b9dfe6ea1a1b255f7";
    const calls = mockFetch({
      ["GET " + base]: { body: { jobs: [wpCron], statusKnown: true } },
      ["GET " + base + "/1/runs"]: {
        body: {
          runs: [
            { id: "1e68564f9432460e8f472682fd9ae514", start: 1795600000, end: 1795600002, running: false, result: "success", exitStatus: 0 },
            { id: older, start: 1795599700, end: 1795599761, running: false, result: "exit-code", exitStatus: 3 },
            { id: "3c2d3aa75594430b9dfe6ea1a1b255f7", start: 1795599400, running: false },
          ],
        },
      },
      ["GET " + base + "/1/output"]: { body: { ran: true, lines: ["Hi!", "Sat Nov 28"], truncated: false } },
      ["GET " + base + "/1/output?run=" + older]: { body: { ran: true, lines: ["Fatal error: y"], truncated: false } },
    });
    render(<CronSettings site={site} />);
    await user.click(await screen.findByRole("button", { name: /^Succeeded/ }));

    const out = await screen.findByRole("region", { name: "Output of cron job 1" });
    const nav = within(out).getByRole("navigation", { name: "Last runs of cron job 1" });
    const runs = await within(nav).findAllByRole("button");
    expect(runs).toHaveLength(3);
    expect(runs[0]).toHaveAttribute("aria-current", "true");
    expect(runs[0]).toHaveTextContent("latest");
    expect(runs[0]).toHaveTextContent("latest · succeeded · 2 s");
    expect(runs[1]).toHaveTextContent("exit code 3 · 1 min 1 s");
    expect(runs[2]).toHaveTextContent("end unknown");
    expect(await within(out).findByText("Sat Nov 28")).toBeInTheDocument();

    await user.click(runs[1]!);
    expect(await within(out).findByText("Fatal error: y")).toBeInTheDocument();
    expect(runs[1]).toHaveAttribute("aria-current", "true");
    expect(within(out).getByText(/^Output of the run from/)).toBeInTheDocument();
    expect(out).toHaveClass("ui-cron-output-err");
    expect(calls.some((c) => c.path === base + "/1/output?run=" + older)).toBe(true);

    // Back to the last run.
    await user.click(runs[0]!);
    expect(await within(out).findByText("Sat Nov 28")).toBeInTheDocument();
    expect(within(out).getByText("Output of the last run")).toBeInTheDocument();
  });

  it("says when a run is gone", async () => {
    const user = userEvent.setup();
    const older = "0a2d3aa75594430b9dfe6ea1a1b255f7";
    mockFetch({
      ["GET " + base]: { body: { jobs: [wpCron], statusKnown: true } },
      ["GET " + base + "/1/runs"]: {
        body: {
          runs: [
            { id: "1e68564f9432460e8f472682fd9ae514", start: 1795600000, end: 1795600002, running: false, result: "success" },
            { id: older, start: 1795599700, end: 1795599761, running: false, result: "success" },
          ],
        },
      },
      ["GET " + base + "/1/output"]: { body: { ran: true, lines: ["Hi!"], truncated: false } },
      ["GET " + base + "/1/output?run=" + older]: err(404, "run_not_found", "no such run among the last runs of the cron job"),
    });
    render(<CronSettings site={site} />);
    await user.click(await screen.findByRole("button", { name: /^Succeeded/ }));
    const out = await screen.findByRole("region", { name: "Output of cron job 1" });
    const runs = await within(within(out).getByRole("navigation")).findAllByRole("button");
    await user.click(runs[1]!);
    expect(await within(out).findByText(/no longer among the last runs/)).toBeInTheDocument();
  });

  it("says when there is no output", async () => {
    const user = userEvent.setup();
    mockFetch({
      ["GET " + base]: { body: { jobs: [wpCron, { ...wpCron, id: 2 }], statusKnown: true } },
      ["GET " + base + "/1/output"]: { body: { ran: true, lines: [], truncated: false } },
      ["GET " + base + "/2/output"]: { body: { ran: false, lines: [], truncated: false } },
    });
    render(<CronSettings site={site} />);

    const [one, two] = await screen.findAllByRole("button", { name: /^Succeeded/ });
    await user.click(one!);
    expect(await screen.findByText("This run wrote nothing.")).toBeInTheDocument();
    await user.click(two!);
    expect(await screen.findByText(/No output known/)).toBeInTheDocument();
  });

  it("offers no output for a job that has not run", async () => {
    const user = userEvent.setup();
    mockFetch({ ["GET " + base]: { body: { jobs: [{ ...wpCron, status: { running: false } }], statusKnown: true } } });
    render(<CronSettings site={site} />);
    await user.click(await screen.findByRole("button", { name: "More actions for cron job 1" }));
    expect(screen.queryByRole("menuitem", { name: "Show output" })).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: /^Not run yet/ })).not.toBeInTheDocument();
  });

  it("speaks German, dates included", async () => {
    setLanguage("de");
    mockFetch({ ["GET " + base]: { body: { jobs: [wpCron], statusKnown: true } } });
    render(<CronSettings site={site} />);
    const one = within(await screen.findByLabelText("Details von Cron-Job 1"));
    // 1795600000 is 25 November 2026, 09:46 UTC: the 25th or 26th in any time zone.
    expect(one.getByText(/^Erfolg: 2[56]\.11\.2026, \d\d:\d\d$/)).toBeInTheDocument();
    expect(one.getByText("Limits: 60 min, Priorität 10")).toBeInTheDocument();
    expect(screen.getByText("alle 5 Minuten")).toBeInTheDocument();
    expect(screen.getByRole("columnheader", { name: "Nächster Lauf" })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Cron-Job hinzufügen" })).toBeInTheDocument();
  });
});
