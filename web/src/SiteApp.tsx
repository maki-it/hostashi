// SPDX-License-Identifier: AGPL-3.0-or-later

// App sites (ADR-0054 addendum 2): one program of the site's own, for
// example a Go or Rust service, runs as the site's user on a local port,
// and the site passes every request to it. Start, stop and the state are
// the AppCard of Node.js sites.

import { useState, type FormEvent } from "react";
import { argsProblem, maxArgs, nodeMessage, parseArgs, programCommand, programProblem, setProgramApp, workdirProblem, type ProgramApp } from "./node";
import type { Site } from "./sites";
import { Button, Card, Field, Notice } from "./ui";

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
  const req = programRequest(value);
  return (
    <>
      <Field label="Program" hint="The executable file below the site's home, for example bin/server. Leave it empty until it is uploaded: the app starts once it is set and there.">
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
      <Field label="Arguments" hint={`One per line, at most ${maxArgs}. Each line reaches the program exactly as written, spaces and quotes included; no shell reads it.`}>
        <textarea
          className="ui-input ui-textarea ui-mono"
          value={value.argsText}
          onChange={(e) => onChange({ ...value, argsText: e.target.value })}
          rows={Math.min(8, Math.max(3, value.argsText.split("\n").length + 1))}
          disabled={disabled || !value.program}
          placeholder={"--config\nconfig.toml"}
          autoComplete="off"
          spellCheck={false}
        />
      </Field>
      {value.program && <p className="ui-hint ui-flush ui-mono">Runs: {programCommand(req)}</p>}
      <Field label="Working directory" hint="The folder the program starts in, below the site's home.">
        <input
          className="ui-input ui-mono"
          value={value.workdir}
          onChange={(e) => onChange({ ...value, workdir: e.target.value.trim() })}
          disabled={disabled}
          autoComplete="off"
          spellCheck={false}
        />
      </Field>
      <p className="ui-hint ui-flush">
        The program gets PORT and HOST=127.0.0.1 and must listen there. It must belong to the site's user and be executable (chmod u+x). Upload it over SFTP or in Files, then start or restart the app.
      </p>
    </>
  );
}

/** ProgramSettingsCard changes the program, its arguments and the working directory. */
export function ProgramSettingsCard({ site, onChange }: { site: Site; onChange: () => Promise<void> }) {
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
      setDone(out.status.problem ? "Saved. The app starts once its program is there." : site.app?.stopped || !value.program ? "Saved." : "Saved. The app was restarted with it.");
    } catch (err) {
      setError(nodeMessage(err));
    } finally {
      setBusy(false);
    }
  }

  return (
    <Card title="App settings">
      <form className="ui-stack" onSubmit={(e) => void save(e)} noValidate>
        <ProgramFields value={value} onChange={setValue} disabled={busy} />
        {error && <Notice tone="err" title={error} />}
        {done && <Notice tone="ok" title={done} />}
        <div className="ui-row">
          <Button type="submit" variant="primary" disabled={busy || unchanged}>
            Save app settings
          </Button>
        </div>
      </form>
    </Card>
  );
}
