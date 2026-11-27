// SPDX-License-Identifier: AGPL-3.0-or-later

import { useState, type FormEvent } from "react";
import { resetMessage, resetPermissions, type PermissionsReset } from "./permissions";
import { siteMessage, type Site } from "./sites";
import { Button, Card, Field, Notice } from "./ui";

const count = (n: number, one: string, many: string) => n.toLocaleString("en") + " " + (n === 1 ? one : many);

// PermissionsResetCard gives every file of the site back to its Linux user
// and resets the modes (ADR-0067), after the site's name is typed.
export function PermissionsResetCard({ site }: { site: Site }) {
  const [open, setOpen] = useState(false);
  const [typed, setTyped] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [result, setResult] = useState<PermissionsReset | null>(null);

  async function run(e: FormEvent) {
    e.preventDefault();
    setBusy(true);
    setError("");
    setResult(null);
    try {
      setResult(await resetPermissions(site.name));
      setOpen(false);
      setTyped("");
    } catch (err) {
      setError(resetMessage(err) ?? siteMessage(err));
    } finally {
      setBusy(false);
    }
  }

  function cancel() {
    setOpen(false);
    setTyped("");
    setError("");
  }

  return (
    <Card title="Reset file permissions">
      <div className="ui-stack">
        <p className="ui-hint ui-flush">
          Gives every file and folder of the site back to its user <span className="ui-mono">{site.name}</span> and sets the usual modes: folders{" "}
          <span className="ui-mono">755</span>, files <span className="ui-mono">644</span>, executable files <span className="ui-mono">755</span>. Use it
          after copying files in as root or when the web server answers 403 for files that exist.
        </p>
        <p className="ui-hint ui-flush">
          Files that were private, such as a <span className="ui-mono">wp-config.php</span> with <span className="ui-mono">600</span>, become readable for
          the web server. <span className="ui-mono">.ssh</span> stays private. Links are not followed, and files on other file systems stay as they are.
        </p>
        {!open ? (
          <div className="ui-row">
            <Button onClick={() => setOpen(true)} disabled={busy}>
              Reset file permissions
            </Button>
          </div>
        ) : (
          <form className="ui-stack" onSubmit={(e) => void run(e)}>
            <div className="ui-narrow">
              <Field
                label={
                  <>
                    Type <code className="ui-confirm-phrase">{site.name}</code> to confirm
                  </>
                }
                hint="Owners and modes of all files of this site change. Running programs keep running."
              >
                <input className="ui-input" value={typed} onChange={(e) => setTyped(e.target.value)} autoComplete="off" spellCheck={false} />
              </Field>
            </div>
            <div className="ui-row">
              <Button type="submit" variant="danger" disabled={busy || typed !== site.name}>
                {busy ? "Resetting…" : `Reset file permissions of ${site.name}`}
              </Button>
              <Button disabled={busy} onClick={cancel}>
                Cancel
              </Button>
            </div>
          </form>
        )}
        {error && <Notice tone="err" title={error} />}
        {result && (
          <Notice
            tone={result.skipped > 0 ? "warn" : "ok"}
            title={
              result.changed === 0
                ? `Nothing to change: all ${count(result.checked, "entry", "entries")} were already right.`
                : `Changed ${count(result.changed, "entry", "entries")} of ${count(result.checked, "entry", "entries")}.`
            }
          >
            {result.skipped > 0 ? (result.skipped === 1 ? "1 entry was left as it is:" : `${count(result.skipped, "entry", "entries")} were left as they are:`) : undefined}
          </Notice>
        )}
        {result && result.skippedPaths.length > 0 && (
          <ul className="ui-flush" aria-label="Skipped entries">
            {result.skippedPaths.map((s) => (
              <li key={s.path}>
                <span className="ui-mono">{s.path}</span>: {s.reason}
              </li>
            ))}
            {result.skipped > result.skippedPaths.length && <li>and {(result.skipped - result.skippedPaths.length).toLocaleString("en")} more</li>}
          </ul>
        )}
      </div>
    </Card>
  );
}
