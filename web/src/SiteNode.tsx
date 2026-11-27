// SPDX-License-Identifier: AGPL-3.0-or-later

// Node.js sites (ADR-0054): the app runs as the site's own user on a local
// port, and the site passes every request to it.

import { ArrowClockwise, Play, Stop } from "@phosphor-icons/react";
import { useCallback, useEffect, useState, type FormEvent } from "react";
import { useTranslation } from "react-i18next";
import {
  appAction,
  appView,
  getAppStatus,
  getNode,
  installedLines,
  newestLine,
  nodeMessage,
  programCommand,
  setNodeApp,
  startCommand,
  startProblem,
  workdirProblem,
  type AppAction,
  type AppStatus,
  type NodeApp,
  type NodeStatus,
  type StartKind,
} from "./node";
import { formatBytes } from "./format";
import i18n from "./i18n";
import { pythonCommand } from "./python";
import { formatHash } from "./routes";
import type { Site } from "./sites";
import { Badge, Button, Card, Field, Notice } from "./ui";

export type NodeValue = Pick<NodeApp, "version" | "workdir" | "startKind" | "start">;

export const newNodeApp = (version = ""): NodeValue => ({ version, workdir: "htdocs", startKind: "", start: "" });

/** nodeProblem checks the fields before they are sent. */
export function nodeProblem(v: NodeValue): string {
  if (!v.version) return i18n.t("siteApp.runtime.problems.nodeMissing");
  return workdirProblem(v.workdir) || startProblem(v.startKind, v.start);
}

/** useNodeStatus loads the installed lines; null while loading or when the server does not say. */
export function useNodeStatus() {
  const [status, setStatus] = useState<NodeStatus | null>(null);
  useEffect(() => {
    getNode()
      .then(setStatus)
      .catch(() => setStatus(null));
  }, []);
  return status;
}

/** NodeFields edits the app of a Node.js site, for New site and the App settings card. */
export function NodeFields({
  value,
  onChange,
  status,
  disabled = false,
  admin = false,
}: {
  value: NodeValue;
  onChange: (v: NodeValue) => void;
  status: NodeStatus | null;
  disabled?: boolean;
  /** Shows the link to install more versions. */
  admin?: boolean;
}) {
  const { t } = useTranslation();
  const lines = installedLines(status);
  // A version the site has but the server no longer has stays selectable.
  const choices = value.version && !lines.includes(value.version) ? [value.version, ...lines] : lines;
  const release = (major: string) => status?.releases.find((r) => r.major === major);
  const kinds: { value: StartKind; label: string; hint: string }[] = (["script", "npm", ""] as const).map((value) => {
    const k = value || "none";
    return { value, label: t(`siteApp.node.kinds.${k}.label`), hint: t(`siteApp.node.kinds.${k}.hint`) };
  });

  return (
    <>
      <Field label={t("siteApp.node.version")}>
        <select className="ui-select" value={value.version} disabled={disabled || choices.length === 0} onChange={(e) => onChange({ ...value, version: e.target.value })}>
          {choices.length === 0 && <option value="">{t("siteApp.node.noVersion")}</option>}
          {choices.map((m) => {
            const r = release(m);
            return (
              <option key={m} value={m}>
                {[
                  t("siteApp.node.option", { major: m }),
                  r ? t("siteApp.node.optionRelease", { codename: r.codename, end: r.end }) : "",
                  lines.includes(m) ? "" : t("siteApp.node.notInstalled"),
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
          {t("siteApp.node.onlyInstalled")} <a href={formatHash({ page: "node" })}>{t("siteApp.node.install")}</a>
        </p>
      )}
      <Field label={t("siteApp.settings.workdir")} hint={t("siteApp.node.workdirHint")}>
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
              name="node-start-kind"
              value={k.value}
              checked={value.startKind === k.value}
              disabled={disabled}
              onChange={() => onChange({ ...value, startKind: k.value, start: k.value === "" ? "" : value.startKind === k.value ? value.start : k.value === "npm" ? "start" : "server.js" })}
            />
            <span>
              {k.label}
              <span className="ui-hint ui-flush">{k.hint}</span>
            </span>
          </label>
        ))}
      </fieldset>
      {value.startKind !== "" && (
        <Field
          label={value.startKind === "npm" ? t("siteApp.node.kinds.npm.label") : t("siteApp.node.kinds.script.label")}
          hint={value.startKind === "npm" ? t("siteApp.node.npmHint", { start: value.start || "…" }) : t("siteApp.node.fileHint", { start: value.start || "…" })}
        >
          <input
            className="ui-input ui-mono"
            value={value.start}
            onChange={(e) => onChange({ ...value, start: e.target.value.trim() })}
            disabled={disabled}
            autoComplete="off"
            spellCheck={false}
          />
        </Field>
      )}
      <p className="ui-hint ui-flush">{t("siteApp.node.note")}</p>
    </>
  );
}

/** AppCard shows what the app of a Node.js, app or Python site does and starts, stops or restarts it. */
export function AppCard({ site, onChange, poll = 5000 }: { site: Site; onChange: () => Promise<void>; poll?: number }) {
  const { t } = useTranslation();
  const [status, setStatus] = useState<AppStatus | null>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [done, setDone] = useState("");
  const node = site.node;
  const prog = site.app;
  const py = site.python;

  const load = useCallback(async () => {
    try {
      setStatus(await getAppStatus(site.name));
    } catch {
      setStatus(null);
    }
  }, [site.name]);

  useEffect(() => {
    void load();
    const timer = setInterval(() => void load(), poll);
    return () => clearInterval(timer);
  }, [load, poll, site.updatedAt]);

  const app = node ?? py ?? prog;
  if (!app) return null;
  const view = appView(site, status);
  const set = node ? node.startKind !== "" : py ? py.startKind !== "" : !!prog?.program;
  const canRun = set && !site.disabled;
  const running = !app.stopped && canRun;
  const problem = running ? status?.problem : "";

  async function act(a: AppAction) {
    setBusy(true);
    setError("");
    setDone("");
    try {
      setStatus(await appAction(site.name, a));
      await onChange();
      setDone(t(`siteApp.app.done.${a}`));
    } catch (err) {
      setError(nodeMessage(err));
    } finally {
      setBusy(false);
    }
  }

  return (
    <Card title={t("siteApp.app.title")}>
      <div className="ui-stack">
        <dl className="ui-facts" aria-label={t("siteApp.app.aria", { site: site.name })}>
          <dt>{t("siteApp.app.state")}</dt>
          <dd>
            <Badge tone={view.tone}>{view.word}</Badge>
          </dd>
          <dt>{t("siteApp.app.runs")}</dt>
          <dd className="ui-mono">{!set ? t("siteApp.app.nothing") : node ? startCommand(node) : py ? pythonCommand(py, py.port) : prog ? programCommand(prog) : ""}</dd>
          <dt>{t("siteApp.app.in")}</dt>
          <dd className="ui-mono">~/{app.workdir}</dd>
          {node && (
            <>
              <dt>Node.js</dt>
              <dd>{node.version}</dd>
            </>
          )}
          {py && (
            <>
              <dt>{t("siteApp.app.python")}</dt>
              <dd>
                {t("siteApp.app.pythonEnv", { version: py.version })} <span className="ui-mono">~/.venv</span>
              </dd>
            </>
          )}
          <dt>{t("siteApp.app.port")}</dt>
          <dd className="ui-mono">127.0.0.1:{app.port}</dd>
          {status && running && status.usedBytes > 0 && (
            <>
              <dt>{t("siteApp.app.memory")}</dt>
              <dd>{formatBytes(status.usedBytes)}</dd>
            </>
          )}
          {status && running && status.restarts > 0 && (
            <>
              <dt>{t("siteApp.app.restarts")}</dt>
              <dd>{status.restarts}</dd>
            </>
          )}
        </dl>
        {!set && <Notice tone="info" title={node || py ? t("siteApp.app.setStart") : t("siteApp.app.setProgram")} />}
        {problem && (
          <Notice tone="warn" title={py ? t("siteApp.app.appWaiting") : t("siteApp.app.programWaiting")}>
            {problem.charAt(0).toUpperCase() + problem.slice(1)}.
          </Notice>
        )}
        {site.disabled && <Notice tone="info" title={t("siteApp.app.siteDisabled")} />}
        {view.tone === "err" && (
          <Notice tone="err" title={t("siteApp.app.broken")}>
            <a href={formatHash({ page: "sites", site: { name: site.name, tab: "logs" } })}>{t("siteApp.app.seeLog")}</a>
          </Notice>
        )}
        {error && <Notice tone="err" title={error} />}
        {done && <Notice tone="ok" title={done} />}
        <div className="ui-row">
          {app.stopped || !canRun || problem ? (
            <Button variant="primary" icon={<Play size={16} aria-hidden="true" />} disabled={busy || !canRun} onClick={() => void act("start")}>
              {t("siteApp.app.start")}
            </Button>
          ) : (
            <>
              <Button icon={<ArrowClockwise size={16} aria-hidden="true" />} disabled={busy} onClick={() => void act("restart")}>
                {t("siteApp.app.restart")}
              </Button>
              <Button icon={<Stop size={16} aria-hidden="true" />} disabled={busy} onClick={() => void act("stop")}>
                {t("siteApp.app.stop")}
              </Button>
            </>
          )}
        </div>
        <p className="ui-hint ui-flush">{t("siteApp.app.stoppedNote")}</p>
      </div>
    </Card>
  );
}

/** AppSettingsCard changes the version, working directory and start command. */
export function AppSettingsCard({ site, onChange, admin }: { site: Site; onChange: () => Promise<void>; admin: boolean }) {
  const { t } = useTranslation();
  const status = useNodeStatus();
  const saved: NodeValue = site.node ? { version: site.node.version, workdir: site.node.workdir, startKind: site.node.startKind, start: site.node.start } : newNodeApp(newestLine(status));
  const [value, setValue] = useState<NodeValue>(saved);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [done, setDone] = useState("");
  const unchanged = value.version === saved.version && value.workdir === saved.workdir && value.startKind === saved.startKind && value.start === saved.start;

  async function save(e: FormEvent) {
    e.preventDefault();
    const problem = nodeProblem(value);
    if (problem) {
      setError(problem);
      return;
    }
    setBusy(true);
    setError("");
    setDone("");
    try {
      await setNodeApp(site.name, value);
      await onChange();
      setDone(site.node?.stopped || !value.startKind ? t("siteApp.settings.saved") : t("siteApp.settings.savedRestarted"));
    } catch (err) {
      setError(nodeMessage(err));
    } finally {
      setBusy(false);
    }
  }

  return (
    <Card title={t("siteApp.settings.title")}>
      <form className="ui-stack" onSubmit={(e) => void save(e)} noValidate>
        <NodeFields value={value} onChange={setValue} status={status} disabled={busy} admin={admin} />
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
