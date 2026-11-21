// SPDX-License-Identifier: AGPL-3.0-or-later

import { type FormEvent, useState } from "react";
import type { Site } from "./sites";
import { Button, Card, Field, Notice } from "./ui";
import {
  getSnippets,
  setSnippets,
  sizeProblem,
  snippetMessage,
  snippetProblems,
  type SnippetProblem,
  type SnippetProblems,
} from "./snippets";

// SnippetSettings edits the custom nginx snippets of a site. They load only
// when opened: most sites never need them.
export function SnippetSettings({ site }: { site: Site }) {
  const [open, setOpen] = useState(false);
  const [server, setServer] = useState("");
  const [root, setRoot] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [done, setDone] = useState("");
  const [problems, setProblems] = useState<SnippetProblems | null>(null);

  async function load() {
    setBusy(true);
    setError("");
    try {
      const s = await getSnippets(site.name);
      setServer(s.server);
      setRoot(s.root);
      setOpen(true);
    } catch (err) {
      setError(snippetMessage(err));
    } finally {
      setBusy(false);
    }
  }

  async function save(e: FormEvent) {
    e.preventDefault();
    const problem = sizeProblem({ server, root });
    if (problem) {
      setError(problem);
      setDone("");
      return;
    }
    setBusy(true);
    setError("");
    setDone("");
    setProblems(null);
    try {
      const s = await setSnippets(site.name, { server, root });
      setServer(s.server);
      setRoot(s.root);
      setDone(s.server === "" && s.root === "" ? "The site has no custom snippets now." : "Snippets saved and active.");
    } catch (err) {
      setProblems(snippetProblems(err));
      setError(snippetMessage(err));
    } finally {
      setBusy(false);
    }
  }

  return (
    <Card title="Custom nginx snippets">
      <div className="ui-stack">
        <p className="ui-flush">
          For rules Hostashi has no setting for yet: headers, redirects, extra locations. Only allowed directives pass; paths must stay inside the
          site's home. Snippets are not used while the site is disabled or in maintenance.
        </p>
        {!open ? (
          <div className="ui-row">
            <Button disabled={busy} onClick={() => void load()}>
              Edit snippets
            </Button>
          </div>
        ) : (
          <form className="ui-stack" onSubmit={(e) => void save(e)} noValidate>
            <SnippetField label="Inside the server block" hint="After Hostashi's own locations." value={server} onChange={setServer} problems={problems?.server} />
            <SnippetField
              label="Inside location /"
              hint="Before Hostashi's try_files, for every request that no other location takes."
              value={root}
              onChange={setRoot}
              problems={problems?.root}
            />
            <div className="ui-row">
              <Button variant="primary" type="submit" disabled={busy}>
                Save snippets
              </Button>
              <Button disabled={busy} onClick={() => setOpen(false)}>
                Close snippets
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

export function SnippetField({
  label,
  hint,
  value,
  onChange,
  problems,
}: {
  label: string;
  hint: string;
  value: string;
  onChange: (v: string) => void;
  problems?: SnippetProblem[] | undefined;
}) {
  const lines = problems && problems.length > 0 ? problems.map((p) => `Line ${p.line}: ${p.msg}.`).join(" ") : undefined;
  return (
    <Field label={label} hint={hint} {...(lines ? { error: lines } : {})}>
      <textarea
        className="ui-input ui-textarea ui-mono"
        value={value}
        onChange={(e) => onChange(e.target.value)}
        rows={Math.min(16, Math.max(4, value.split("\n").length + 1))}
        autoComplete="off"
        spellCheck={false}
      />
    </Field>
  );
}
