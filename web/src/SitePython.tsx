// SPDX-License-Identifier: AGPL-3.0-or-later

// Python sites (ADR-0061): the app runs from the site's own environment
// ~/.venv with gunicorn, uvicorn or a .py file, as the site's user on a
// local port. Start, stop and the state are the AppCard of Node.js sites.

import { useEffect, useState, type FormEvent } from "react";
import { maxArgs, parseArgs } from "./node";
import {
  getPython,
  installedPythonLines,
  newestPythonLine,
  pythonCommand,
  pythonMessage,
  pythonProblem,
  recreateVenv,
  setPythonApp,
  type PythonApp,
  type PythonStartKind,
  type PythonStatus,
  type PythonValue,
} from "./python";
import { formatHash } from "./routes";
import type { Site } from "./sites";
import { Button, Card, Field, Notice } from "./ui";

/** The fields of a Python app; args as the text of the arguments box. */
export type PythonFieldsValue = Omit<PythonValue, "args"> & { argsText: string };

export const newPythonApp = (version = ""): PythonFieldsValue => ({ version, workdir: "htdocs", startKind: "", target: "", argsText: "" });

export const pythonFieldsValue = (a: PythonApp): PythonFieldsValue => ({
  version: a.version,
  workdir: a.workdir,
  startKind: a.startKind,
  target: a.target,
  argsText: a.args.join("\n"),
});

/** pythonRequest is what the API takes. */
export const pythonRequest = (v: PythonFieldsValue): PythonValue => ({
  version: v.version,
  workdir: v.workdir,
  startKind: v.startKind,
  target: v.startKind ? v.target : "",
  args: v.startKind ? parseArgs(v.argsText) : [],
});

export const pythonFieldsProblem = (v: PythonFieldsValue) => pythonProblem(pythonRequest(v));

/** usePythonStatus loads the installed lines; null while loading or when the server does not say. */
export function usePythonStatus() {
  const [status, setStatus] = useState<PythonStatus | null>(null);
  useEffect(() => {
    getPython()
      .then(setStatus)
      .catch(() => setStatus(null));
  }, []);
  return status;
}

const kinds: { value: PythonStartKind; label: string; hint: string; example: string }[] = [
  { value: "wsgi", label: "WSGI with gunicorn", hint: "Django, Flask and other WSGI apps. Install gunicorn in .venv.", example: "mysite.wsgi:application" },
  { value: "asgi", label: "ASGI with uvicorn", hint: "FastAPI, Starlette, Django with ASGI. Install uvicorn in .venv.", example: "main:app" },
  { value: "script", label: "Python file", hint: "Runs a .py file that listens on HOST and PORT itself.", example: "app.py" },
  { value: "", label: "Not yet", hint: "The app stays stopped until a start command is set.", example: "" },
];

/** PythonFields edits the app of a Python site, for New site, Change type and the App settings card. */
export function PythonFields({
  value,
  onChange,
  status,
  disabled = false,
  admin = false,
}: {
  value: PythonFieldsValue;
  onChange: (v: PythonFieldsValue) => void;
  status: PythonStatus | null;
  disabled?: boolean;
  admin?: boolean;
}) {
  const lines = installedPythonLines(status);
  const choices = value.version && !lines.includes(value.version) ? [value.version, ...lines] : lines;
  const release = (line: string) => status?.releases.find((r) => r.line === line);
  const req = pythonRequest(value);
  const kind = kinds.find((k) => k.value === value.startKind);

  return (
    <>
      <Field label="Python version">
        <select className="ui-select" value={value.version} disabled={disabled || choices.length === 0} onChange={(e) => onChange({ ...value, version: e.target.value })}>
          {choices.length === 0 && <option value="">No version installed</option>}
          {choices.map((l) => {
            const r = release(l);
            return (
              <option key={l} value={l}>
                Python {r ? r.version : l}
                {r ? ` (until ${r.end})` : ""}
                {!lines.includes(l) ? " (not installed)" : ""}
              </option>
            );
          })}
        </select>
      </Field>
      {admin && (
        <p className="ui-hint ui-flush">
          Only installed versions are listed. <a href={formatHash({ page: "python" })}>Install Python versions</a>
        </p>
      )}
      <Field label="Working directory" hint="The folder the app starts in, below the site's home. Usually where manage.py or main.py is.">
        <input
          className="ui-input ui-mono"
          value={value.workdir}
          onChange={(e) => onChange({ ...value, workdir: e.target.value.trim() })}
          disabled={disabled}
          autoComplete="off"
          spellCheck={false}
        />
      </Field>
      <fieldset className="ui-fieldset ui-stack-tight">
        <legend>Start command</legend>
        {kinds.map((k) => (
          <label key={k.value || "none"} className="ui-check ui-check-hint">
            <input
              type="radio"
              name="python-start-kind"
              value={k.value}
              checked={value.startKind === k.value}
              disabled={disabled}
              onChange={() =>
                onChange({
                  ...value,
                  startKind: k.value,
                  target: k.value === "" ? "" : (value.startKind === "script") === (k.value === "script") && value.target ? value.target : k.example,
                })
              }
            />
            <span>
              {k.label}
              <span className="ui-hint ui-flush">{k.hint}</span>
            </span>
          </label>
        ))}
      </fieldset>
      {value.startKind !== "" && (
        <>
          <Field
            label={value.startKind === "script" ? "Start file" : "App"}
            hint={value.startKind === "script" ? "Relative to the working directory." : "module.path:callable, for example " + (kind?.example ?? "") + "."}
          >
            <input
              className="ui-input ui-mono"
              value={value.target}
              onChange={(e) => onChange({ ...value, target: e.target.value.trim() })}
              disabled={disabled}
              autoComplete="off"
              spellCheck={false}
            />
          </Field>
          <Field
            label="Extra arguments"
            hint={`One per line, at most ${maxArgs}, for example --workers and 3 on two lines. ${value.startKind === "script" ? "" : "The address is Hostashi's: --bind, --host and --port are refused."}`}
          >
            <textarea
              className="ui-input ui-textarea ui-mono"
              value={value.argsText}
              onChange={(e) => onChange({ ...value, argsText: e.target.value })}
              rows={Math.min(6, Math.max(2, value.argsText.split("\n").length + 1))}
              disabled={disabled}
              autoComplete="off"
              spellCheck={false}
            />
          </Field>
          <p className="ui-hint ui-flush ui-mono">Runs: ~/.venv/bin/{pythonCommand(req)}</p>
        </>
      )}
      <p className="ui-hint ui-flush">
        Hostashi creates the environment ~/.venv with this version. Install the packages into it over SSH, for example with ~/.venv/bin/pip install -r requirements.txt
        {value.startKind === "wsgi" ? " and gunicorn" : value.startKind === "asgi" ? " and uvicorn" : ""}. The app gets PORT and HOST=127.0.0.1.
      </p>
    </>
  );
}

/** VenvConfirm asks for the site's name before .venv is rebuilt; the name to type is highlighted. */
function VenvConfirm({ site, typed, onType, why }: { site: Site; typed: string; onType: (v: string) => void; why: string }) {
  return (
    <div className="ui-stack-tight">
      <Notice tone="warn" title="The environment .venv is rebuilt">
        {why} Every package installed in it goes, gunicorn and uvicorn too. Install them again over SSH afterwards; until then the app stays stopped. Your files in the home stay.
      </Notice>
      <div className="ui-narrow">
        <Field
          label={
            <>
              Type <code className="ui-confirm-phrase">{site.name}</code> to confirm
            </>
          }
        >
          <input className="ui-input" value={typed} onChange={(e) => onType(e.target.value)} autoComplete="off" spellCheck={false} />
        </Field>
      </div>
    </div>
  );
}

/** PythonSettingsCard changes the version, working directory, start command and arguments; a new version asks first. */
export function PythonSettingsCard({ site, onChange, admin }: { site: Site; onChange: () => Promise<void>; admin: boolean }) {
  const status = usePythonStatus();
  const saved = site.python ? pythonFieldsValue(site.python) : newPythonApp(newestPythonLine(status));
  const [value, setValue] = useState<PythonFieldsValue>(saved);
  const [typed, setTyped] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [done, setDone] = useState("");
  const unchanged = JSON.stringify(pythonRequest(value)) === JSON.stringify(pythonRequest(saved));
  const newLine = value.version !== saved.version;

  async function save(e: FormEvent) {
    e.preventDefault();
    const problem = pythonFieldsProblem(value);
    if (problem) {
      setError(problem);
      return;
    }
    setBusy(true);
    setError("");
    setDone("");
    try {
      const out = await setPythonApp(site.name, pythonRequest(value), newLine ? typed : "");
      await onChange();
      setTyped("");
      setDone(
        out.status.problem
          ? "Saved. The app starts once its packages are installed in .venv."
          : site.python?.stopped || !value.startKind
            ? "Saved."
            : "Saved. The app was restarted with it.",
      );
    } catch (err) {
      setError(pythonMessage(err));
    } finally {
      setBusy(false);
    }
  }

  return (
    <Card title="App settings">
      <form className="ui-stack" onSubmit={(e) => void save(e)} noValidate>
        <PythonFields value={value} onChange={setValue} status={status} disabled={busy} admin={admin} />
        {newLine && <VenvConfirm site={site} typed={typed} onType={setTyped} why={`Python ${value.version} needs a new environment.`} />}
        {error && <Notice tone="err" title={error} />}
        {done && <Notice tone="ok" title={done} />}
        <div className="ui-row">
          <Button type="submit" variant={newLine ? "danger" : "primary"} disabled={busy || unchanged || (newLine && typed !== site.name)}>
            {newLine ? `Switch to Python ${value.version}` : "Save app settings"}
          </Button>
        </div>
      </form>
    </Card>
  );
}

/** VenvCard rebuilds .venv with the site's version, after the site's name is typed. */
export function VenvCard({ site, onChange }: { site: Site; onChange: () => Promise<void> }) {
  const [open, setOpen] = useState(false);
  const [typed, setTyped] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [done, setDone] = useState("");
  if (!site.python) return null;

  async function recreate(e: FormEvent) {
    e.preventDefault();
    setBusy(true);
    setError("");
    try {
      await recreateVenv(site.name);
      await onChange();
      setOpen(false);
      setTyped("");
      setDone("The environment was created again. Install the packages over SSH, then start the app.");
    } catch (err) {
      setError(pythonMessage(err));
    } finally {
      setBusy(false);
    }
  }

  return (
    <Card title="Python environment">
      <div className="ui-stack">
        <p className="ui-flush">
          The app runs from <span className="ui-mono">~/.venv</span> with Python {site.python.version}. Over SSH, <span className="ui-mono">python</span> and <span className="ui-mono">pip</span> in{" "}
          <span className="ui-mono">~/.venv/bin</span> install into it.
        </p>
        {!open ? (
          <div className="ui-row">
            <Button
              onClick={() => {
                setOpen(true);
                setDone("");
              }}
            >
              Recreate environment
            </Button>
          </div>
        ) : (
          <form className="ui-stack" onSubmit={(e) => void recreate(e)}>
            <VenvConfirm site={site} typed={typed} onType={setTyped} why="This helps when .venv is broken or was deleted." />
            <div className="ui-row">
              <Button type="submit" variant="danger" disabled={busy || typed !== site.name}>
                Recreate environment
              </Button>
              <Button
                disabled={busy}
                onClick={() => {
                  setOpen(false);
                  setTyped("");
                  setError("");
                }}
              >
                Cancel
              </Button>
            </div>
          </form>
        )}
        {error && <Notice tone="err" title={error} />}
        {done && <Notice tone="ok" title={done} />}
      </div>
    </Card>
  );
}
