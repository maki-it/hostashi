// SPDX-License-Identifier: AGPL-3.0-or-later

// Python sites (ADR-0061): the app runs from the site's own environment
// ~/.venv with gunicorn, uvicorn or a .py file, as the site's user on a
// local port. Start, stop and the state are the AppCard of Node.js sites.

import { useEffect, useState, type FormEvent } from "react";
import { Trans, useTranslation } from "react-i18next";
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

// The start kinds; label and hint are under siteApp.python.kinds.<id>.
const kinds: { value: PythonStartKind; id: "wsgi" | "asgi" | "script" | "none"; example: string }[] = [
  { value: "wsgi", id: "wsgi", example: "mysite.wsgi:application" },
  { value: "asgi", id: "asgi", example: "main:app" },
  { value: "script", id: "script", example: "app.py" },
  { value: "", id: "none", example: "" },
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
  const { t } = useTranslation();
  const lines = installedPythonLines(status);
  const choices = value.version && !lines.includes(value.version) ? [value.version, ...lines] : lines;
  const release = (line: string) => status?.releases.find((r) => r.line === line);
  const req = pythonRequest(value);
  const kind = kinds.find((k) => k.value === value.startKind);

  return (
    <>
      <Field label={t("siteApp.python.version")}>
        <select className="ui-select" value={value.version} disabled={disabled || choices.length === 0} onChange={(e) => onChange({ ...value, version: e.target.value })}>
          {choices.length === 0 && <option value="">{t("siteApp.node.noVersion")}</option>}
          {choices.map((l) => {
            const r = release(l);
            return (
              <option key={l} value={l}>
                {[
                  t("siteApp.python.option", { version: r ? r.version : l }),
                  r ? t("siteApp.python.optionEnd", { end: r.end }) : "",
                  lines.includes(l) ? "" : t("siteApp.node.notInstalled"),
                ]
                  .filter(Boolean)
                  .join(" ")}
              </option>
            );
          })}
        </select>
      </Field>
      {admin && (
        <p className="ui-hint ui-flush">
          {t("siteApp.python.onlyInstalled")} <a href={formatHash({ page: "python" })}>{t("siteApp.python.install")}</a>
        </p>
      )}
      <Field label={t("siteApp.settings.workdir")} hint={t("siteApp.python.workdirHint")}>
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
        <legend>{t("siteApp.node.startCommand")}</legend>
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
              {t(`siteApp.python.kinds.${k.id}.label`)}
              <span className="ui-hint ui-flush">{t(`siteApp.python.kinds.${k.id}.hint`)}</span>
            </span>
          </label>
        ))}
      </fieldset>
      {value.startKind !== "" && (
        <>
          <Field
            label={value.startKind === "script" ? t("siteApp.python.startFile") : t("siteApp.python.app")}
            hint={value.startKind === "script" ? t("siteApp.python.startFileHint") : t("siteApp.python.appHint", { example: kind?.example ?? "" })}
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
            label={t("siteApp.python.extraArgs")}
            hint={[t("siteApp.python.extraArgsHint", { max: maxArgs }), value.startKind === "script" ? "" : t("siteApp.python.bindRefused")].filter(Boolean).join(" ")}
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
          <p className="ui-hint ui-flush ui-mono">{t("siteApp.python.runs", { command: pythonCommand(req) })}</p>
        </>
      )}
      <p className="ui-hint ui-flush">{value.startKind === "wsgi" ? t("siteApp.python.noteWSGI") : value.startKind === "asgi" ? t("siteApp.python.noteASGI") : t("siteApp.python.note")}</p>
    </>
  );
}

/** VenvConfirm asks for the site's name before .venv is rebuilt; the name to type is highlighted. */
function VenvConfirm({ site, typed, onType, why }: { site: Site; typed: string; onType: (v: string) => void; why: string }) {
  const { t } = useTranslation();
  return (
    <div className="ui-stack-tight">
      <Notice tone="warn" title={t("siteApp.python.rebuiltTitle")}>
        {why} {t("siteApp.python.rebuiltBody")}
      </Notice>
      <div className="ui-narrow">
        <Field
          label={<Trans i18nKey="siteApp.python.confirm" values={{ name: site.name }} components={{ code: <code className="ui-confirm-phrase" /> }} />}
        >
          <input className="ui-input" value={typed} onChange={(e) => onType(e.target.value)} autoComplete="off" spellCheck={false} />
        </Field>
      </div>
    </div>
  );
}

/** PythonSettingsCard changes the version, working directory, start command and arguments; a new version asks first. */
export function PythonSettingsCard({ site, onChange, admin }: { site: Site; onChange: () => Promise<void>; admin: boolean }) {
  const { t } = useTranslation();
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
          ? t("siteApp.python.savedPackages")
          : site.python?.stopped || !value.startKind
            ? t("siteApp.settings.saved")
            : t("siteApp.settings.savedRestarted"),
      );
    } catch (err) {
      setError(pythonMessage(err));
    } finally {
      setBusy(false);
    }
  }

  return (
    <Card title={t("siteApp.settings.title")}>
      <form className="ui-stack" onSubmit={(e) => void save(e)} noValidate>
        <PythonFields value={value} onChange={setValue} status={status} disabled={busy} admin={admin} />
        {newLine && <VenvConfirm site={site} typed={typed} onType={setTyped} why={t("siteApp.python.newVersionWhy", { version: value.version })} />}
        {error && <Notice tone="err" title={error} />}
        {done && <Notice tone="ok" title={done} />}
        <div className="ui-row">
          <Button type="submit" variant={newLine ? "danger" : "primary"} disabled={busy || unchanged || (newLine && typed !== site.name)}>
            {newLine ? t("siteApp.python.switchTo", { version: value.version }) : t("siteApp.settings.save")}
          </Button>
        </div>
      </form>
    </Card>
  );
}

/** VenvCard rebuilds .venv with the site's version, after the site's name is typed. */
export function VenvCard({ site, onChange }: { site: Site; onChange: () => Promise<void> }) {
  const { t } = useTranslation();
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
      setDone(t("siteApp.python.recreated"));
    } catch (err) {
      setError(pythonMessage(err));
    } finally {
      setBusy(false);
    }
  }

  return (
    <Card title={t("siteApp.python.venvTitle")}>
      <div className="ui-stack">
        <p className="ui-flush">
          <Trans i18nKey="siteApp.python.venvIntro" values={{ version: site.python.version }} components={{ mono: <span className="ui-mono" /> }} />
        </p>
        {!open ? (
          <div className="ui-row">
            <Button
              onClick={() => {
                setOpen(true);
                setDone("");
              }}
            >
              {t("siteApp.python.recreate")}
            </Button>
          </div>
        ) : (
          <form className="ui-stack" onSubmit={(e) => void recreate(e)}>
            <VenvConfirm site={site} typed={typed} onType={setTyped} why={t("siteApp.python.recreateWhy")} />
            <div className="ui-row">
              <Button type="submit" variant="danger" disabled={busy || typed !== site.name}>
                {t("siteApp.python.recreate")}
              </Button>
              <Button
                disabled={busy}
                onClick={() => {
                  setOpen(false);
                  setTyped("");
                  setError("");
                }}
              >
                {t("common.actions.cancel")}
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
