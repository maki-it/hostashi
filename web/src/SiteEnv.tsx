// SPDX-License-Identifier: AGPL-3.0-or-later

// The environment variables of the app of a Node.js or app site (ADR-0054):
// the names are shown, the values only while they are edited.

import { useEffect, useState, type FormEvent } from "react";
import { formatEnv, getAppEnv, getAppEnvNames, parseEnv, setAppEnv } from "./node";
import { siteMessage, type Site } from "./sites";
import { Button, Card, Field, Notice } from "./ui";

export function EnvCard({ site }: { site: Site }) {
  const [names, setNames] = useState<string[] | null>(null);
  const [text, setText] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [done, setDone] = useState("");

  useEffect(() => {
    getAppEnvNames(site.name)
      .then(setNames)
      .catch((err) => setError(siteMessage(err)));
  }, [site.name]);

  async function edit() {
    setBusy(true);
    setError("");
    setDone("");
    try {
      setText(formatEnv(await getAppEnv(site.name)));
    } catch (err) {
      setError(siteMessage(err));
    } finally {
      setBusy(false);
    }
  }

  async function save(e: FormEvent) {
    e.preventDefault();
    const { vars, problem } = parseEnv(text ?? "");
    if (problem) {
      setError(problem);
      return;
    }
    setBusy(true);
    setError("");
    try {
      const r = await setAppEnv(site.name, vars);
      setNames(r.names);
      setText(null);
      const running = r.status.state === "active";
      setDone(running ? "Saved. The app was restarted with them." : "Saved. The app gets them when it starts.");
    } catch (err) {
      setError(siteMessage(err));
    } finally {
      setBusy(false);
    }
  }

  return (
    <Card title="Environment variables">
      {text === null ? (
        <div className="ui-stack">
          {names === null ? (
            !error && <p aria-busy="true">Loading…</p>
          ) : names.length === 0 ? (
            <p className="ui-flush">None yet. The app gets PORT, HOST and the other basics from Hostashi.</p>
          ) : (
            <ul className="ui-plain-list ui-mono" aria-label={"Environment variables of " + site.name}>
              {names.map((n) => (
                <li key={n}>
                  {n}=<span className="ui-hint">••••••</span>
                </li>
              ))}
            </ul>
          )}
          <p className="ui-hint ui-flush">The values are stored encrypted and shown only while you edit them.</p>
          {error && <Notice tone="err" title={error} />}
          {done && <Notice tone="ok" title={done} />}
          <div className="ui-row">
            <Button onClick={() => void edit()} disabled={busy || names === null}>
              {names?.length ? "Edit variables" : "Add variables"}
            </Button>
          </div>
        </div>
      ) : (
        <form className="ui-stack" onSubmit={(e) => void save(e)} noValidate>
          <Field
            label={"Variables of " + site.name}
            hint="One per line as NAME=value, for example DATABASE_URL=mysql://… Names use A-Z, 0-9 and _. The value is taken as written, quotes included. Lines starting with # are left out. HOST, PORT, HOME, USER, LOGNAME and PATH are set by Hostashi."
          >
            <textarea
              className="ui-input ui-textarea ui-mono"
              rows={Math.min(Math.max(text.split("\n").length + 1, 4), 16)}
              value={text}
              onChange={(e) => setText(e.target.value)}
              spellCheck={false}
              autoComplete="off"
            />
          </Field>
          {error && <Notice tone="err" title={error} />}
          <div className="ui-row">
            <Button type="submit" variant="primary" disabled={busy}>
              Save variables
            </Button>
            <Button
              disabled={busy}
              onClick={() => {
                setText(null);
                setError("");
              }}
            >
              Cancel
            </Button>
          </div>
          <p className="ui-hint ui-flush">Saving restarts a running app.</p>
        </form>
      )}
    </Card>
  );
}
