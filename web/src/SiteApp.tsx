// SPDX-License-Identifier: AGPL-3.0-or-later

// App sites (ADR-0054 addendum 2): one program of the site's own, for
// example a Go or Rust service, runs as the site's user on a local port,
// and the site passes every request to it. Start, stop and the state are
// the AppCard of Node.js sites.

import { useState, type FormEvent } from "react";
import { useTranslation } from "react-i18next";
import { argsProblem, maxArgs, nodeMessage, parseArgs, programCommand, programProblem, setProgramApp, workdirProblem, type ProgramApp } from "./node";
import type { Site } from "./sites";
import { Button, Card, Field, Notice } from "./ui";

// An example of two arguments; code, the same in every language.
const exampleArgs = ["--config", "config.toml"].join("\n");

/** The fields of a program; args as the text of the arguments box. */
export type ProgramValue = { workdir: string; program: string; argsText: string };

export const newProgramApp = (): ProgramValue => ({ workdir: "htdocs", program: "", argsText: "" });

export const programValue = (a: ProgramApp): ProgramValue => ({ workdir: a.workdir, program: a.program, argsText: a.args.join("\n") });

/** programRequest is what the API takes. */
export const programRequest = (v: ProgramValue): Pick<ProgramApp, "workdir" | "program" | "args"> => ({
  workdir: v.workdir,
  program: v.program,
  args: v.program ? parseArgs(v.argsText) : [],
});

/** programFieldsProblem checks the fields before they are sent. */
export function programFieldsProblem(v: ProgramValue): string {
  return workdirProblem(v.workdir) || programProblem(v.program) || argsProblem(programRequest(v).args);
}

/** ProgramFields edits the program of an app site, for New site and the App settings card. */
export function ProgramFields({ value, onChange, disabled = false }: { value: ProgramValue; onChange: (v: ProgramValue) => void; disabled?: boolean }) {
  const { t } = useTranslation();
  const req = programRequest(value);
  return (
    <>
      <Field label={t("siteApp.program.label")} hint={t("siteApp.program.hint")}>
        <input
          className="ui-input ui-mono"
          value={value.program}
          onChange={(e) => onChange({ ...value, program: e.target.value.trim() })}
          disabled={disabled}
          placeholder="bin/server"
          autoComplete="off"
          spellCheck={false}
        />
      </Field>
      <Field label={t("siteApp.program.args")} hint={t("siteApp.program.argsHint", { max: maxArgs })}>
        <textarea
          className="ui-input ui-textarea ui-mono"
          value={value.argsText}
          onChange={(e) => onChange({ ...value, argsText: e.target.value })}
          rows={Math.min(8, Math.max(3, value.argsText.split("\n").length + 1))}
          disabled={disabled || !value.program}
          placeholder={exampleArgs}
          autoComplete="off"
          spellCheck={false}
        />
      </Field>
      {value.program && <p className="ui-hint ui-flush ui-mono">{t("siteApp.program.runs", { command: programCommand(req) })}</p>}
      <Field label={t("siteApp.settings.workdir")} hint={t("siteApp.program.workdirHint")}>
        <input
          className="ui-input ui-mono"
          value={value.workdir}
          onChange={(e) => onChange({ ...value, workdir: e.target.value.trim() })}
          disabled={disabled}
          autoComplete="off"
          spellCheck={false}
        />
      </Field>
      <p className="ui-hint ui-flush">{t("siteApp.program.note")}</p>
    </>
  );
}

/** ProgramSettingsCard changes the program, its arguments and the working directory. */
export function ProgramSettingsCard({ site, onChange }: { site: Site; onChange: () => Promise<void> }) {
  const { t } = useTranslation();
  const saved = site.app ? programValue(site.app) : newProgramApp();
  const [value, setValue] = useState<ProgramValue>(saved);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [done, setDone] = useState("");
  const unchanged = JSON.stringify(programRequest(value)) === JSON.stringify(programRequest(saved));

  async function save(e: FormEvent) {
    e.preventDefault();
    const problem = programFieldsProblem(value);
    if (problem) {
      setError(problem);
      return;
    }
    setBusy(true);
    setError("");
    setDone("");
    try {
      const out = await setProgramApp(site.name, programRequest(value));
      await onChange();
      setDone(out.status.problem ? t("siteApp.program.savedWaiting") : site.app?.stopped || !value.program ? t("siteApp.settings.saved") : t("siteApp.settings.savedRestarted"));
    } catch (err) {
      setError(nodeMessage(err));
    } finally {
      setBusy(false);
    }
  }

  return (
    <Card title={t("siteApp.settings.title")}>
      <form className="ui-stack" onSubmit={(e) => void save(e)} noValidate>
        <ProgramFields value={value} onChange={setValue} disabled={busy} />
        {error && <Notice tone="err" title={error} />}
        {done && <Notice tone="ok" title={done} />}
        <div className="ui-row">
          <Button type="submit" variant="primary" disabled={busy || unchanged}>
            {t("siteApp.settings.save")}
          </Button>
        </div>
      </form>
    </Card>
  );
}
