// SPDX-License-Identifier: AGPL-3.0-or-later

// Node.js sites (ADR-0054): the app runs as the site's own user on a local
// port, and the site passes every request to it.

import { ArrowClockwise, Play, Stop } from "@phosphor-icons/react";
import { useCallback, useEffect, useState, type FormEvent } from "react";
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
import { formatBytes } from "./limits";
import { formatHash } from "./routes";
import type { Site } from "./sites";
import { Badge, Button, Card, Field, Notice } from "./ui";

export type NodeValue = Pick<NodeApp, "version" | "workdir" | "startKind" | "start">;

export const newNodeApp = (version = ""): NodeValue => ({ version, workdir: "htdocs", startKind: "", start: "" });

/** nodeProblem checks the fields before they are sent. */
export function nodeProblem(v: NodeValue): string {
  if (!v.version) return "No Node.js version is installed. An administrator installs one under Node.js versions.";
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
  const lines = installedLines(status);
  // A version the site has but the server no longer has stays selectable.
  const choices = value.version && !lines.includes(value.version) ? [value.version, ...lines] : lines;
  const release = (major: string) => status?.releases.find((r) => r.major === major);
  const kinds: { value: StartKind; label: string; hint: string }[] = [
    { value: "script", label: "Start file", hint: "Runs node with a .js, .mjs or .cjs file, for example server.js or dist/main.js." },
    { value: "npm", label: "npm script", hint: "Runs npm run with a script from package.json, for example start." },
    { value: "", label: "Not yet", hint: "The app stays stopped until a start command is set." },
  ];

  return (
    <>
      <Field label="Node.js version">
        <select className="ui-select" value={value.version} disabled={disabled || choices.length === 0} onChange={(e) => onChange({ ...value, version: e.target.value })}>
          {choices.length === 0 && <option value="">No version installed</option>}
          {choices.map((m) => {
            const r = release(m);
            return (
              <option key={m} value={m}>
                Node.js {m}
                {r ? ` (${r.codename}, until ${r.end})` : ""}
                {!lines.includes(m) ? " (not installed)" : ""}
              </option>
            );
          })}
        </select>
      </Field>
      {admin && (
        <p className="ui-hint ui-flush">
          Only installed versions are listed. <a href={formatHash({ page: "node" })}>Install Node.js versions</a>
        </p>
      )}
      <Field label="Working directory" hint="The folder the app starts in, below the site's home. Usually where package.json is.">
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
        <Field label={value.startKind === "npm" ? "npm script" : "Start file"} hint={value.startKind === "npm" ? "Runs: npm run " + (value.start || "…") : "Relative to the working directory. Runs: node " + (value.start || "…")}>
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
      <p className="ui-hint ui-flush">
        The app gets PORT and HOST=127.0.0.1 and must listen there. NODE_ENV is production. Install the dependencies first, for example with npm ci over SSH.
      </p>
    </>
  );
}

const actionWord: Record<AppAction, string> = { start: "started", stop: "stopped", restart: "restarted" };

/** AppCard shows what the app of a Node.js or app site does and starts, stops or restarts it. */
export function AppCard({ site, onChange, poll = 5000 }: { site: Site; onChange: () => Promise<void>; poll?: number }) {
  const [status, setStatus] = useState<AppStatus | null>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [done, setDone] = useState("");
  const node = site.node;
  const prog = site.app;

  const load = useCallback(async () => {
    try {
      setStatus(await getAppStatus(site.name));
    } catch {
      setStatus(null);
    }
  }, [site.name]);

  useEffect(() => {
    void load();
    const t = setInterval(() => void load(), poll);
    return () => clearInterval(t);
  }, [load, poll, site.updatedAt]);

  const app = node ?? prog;
  if (!app) return null;
  const view = appView(site, status);
  const set = node ? node.startKind !== "" : !!prog?.program;
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
      setDone(`The app was ${actionWord[a]}.`);
    } catch (err) {
      setError(nodeMessage(err));
    } finally {
      setBusy(false);
    }
  }

  return (
    <Card title="App">
      <div className="ui-stack">
        <dl className="ui-facts" aria-label={"App of " + site.name}>
          <dt>State</dt>
          <dd>
            <Badge tone={view.tone}>{view.word}</Badge>
          </dd>
          <dt>Runs</dt>
          <dd className="ui-mono">{!set ? "nothing yet" : node ? startCommand(node) : prog ? programCommand(prog) : ""}</dd>
          <dt>In</dt>
          <dd className="ui-mono">~/{app.workdir}</dd>
          {node && (
            <>
              <dt>Node.js</dt>
              <dd>{node.version}</dd>
            </>
          )}
          <dt>Port</dt>
          <dd className="ui-mono">127.0.0.1:{app.port}</dd>
          {status && running && status.usedBytes > 0 && (
            <>
              <dt>Memory</dt>
              <dd>{formatBytes(status.usedBytes)}</dd>
            </>
          )}
          {status && running && status.restarts > 0 && (
            <>
              <dt>Restarts after a crash</dt>
              <dd>{status.restarts}</dd>
            </>
          )}
        </dl>
        {!set && <Notice tone="info" title={node ? "Set a start command below, then the app starts." : "Set the program below, then the app starts."} />}
        {problem && (
          <Notice tone="warn" title="The program is not started yet.">
            {problem.charAt(0).toUpperCase() + problem.slice(1)}.
          </Notice>
        )}
        {site.disabled && <Notice tone="info" title="The site is disabled, so its app is stopped. It starts again when the site is enabled." />}
        {view.tone === "err" && (
          <Notice tone="err" title="The app is not running as it should.">
            <a href={formatHash({ page: "sites", site: { name: site.name, tab: "logs" } })}>See the app log</a>
          </Notice>
        )}
        {error && <Notice tone="err" title={error} />}
        {done && <Notice tone="ok" title={done} />}
        <div className="ui-row">
          {app.stopped || !canRun || problem ? (
            <Button variant="primary" icon={<Play size={16} aria-hidden="true" />} disabled={busy || !canRun} onClick={() => void act("start")}>
              Start
            </Button>
          ) : (
            <>
              <Button icon={<ArrowClockwise size={16} aria-hidden="true" />} disabled={busy} onClick={() => void act("restart")}>
                Restart
              </Button>
              <Button icon={<Stop size={16} aria-hidden="true" />} disabled={busy} onClick={() => void act("stop")}>
                Stop
              </Button>
            </>
          )}
        </div>
        <p className="ui-hint ui-flush">A stopped app stays stopped, also after a reboot, until it is started again. After a crash it restarts by itself.</p>
      </div>
    </Card>
  );
}

/** AppSettingsCard changes the version, working directory and start command. */
export function AppSettingsCard({ site, onChange, admin }: { site: Site; onChange: () => Promise<void>; admin: boolean }) {
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
      setDone(site.node?.stopped || !value.startKind ? "Saved." : "Saved. The app was restarted with it.");
    } catch (err) {
      setError(nodeMessage(err));
    } finally {
      setBusy(false);
    }
  }

  return (
    <Card title="App settings">
      <form className="ui-stack" onSubmit={(e) => void save(e)} noValidate>
        <NodeFields value={value} onChange={setValue} status={status} disabled={busy} admin={admin} />
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
