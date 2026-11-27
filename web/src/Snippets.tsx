// SPDX-License-Identifier: AGPL-3.0-or-later

import { type FormEvent, useState } from "react";
import { useTranslation } from "react-i18next";
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
  const { t } = useTranslation();
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
      setDone(s.server === "" && s.root === "" ? t("siteApp.snippets.none") : t("siteApp.snippets.saved"));
    } catch (err) {
      setProblems(snippetProblems(err));
      setError(snippetMessage(err));
    } finally {
      setBusy(false);
    }
  }

  return (
    <Card title={t("siteApp.snippets.title")}>
      <div className="ui-stack">
        <p className="ui-flush">{t("siteApp.snippets.intro")}</p>
        {!open ? (
          <div className="ui-row">
            <Button disabled={busy} onClick={() => void load()}>
              {t("siteApp.snippets.edit")}
            </Button>
          </div>
        ) : (
          <form className="ui-stack" onSubmit={(e) => void save(e)} noValidate>
            <SnippetField label={t("siteApp.snippets.server")} hint={t("siteApp.snippets.serverHint")} value={server} onChange={setServer} problems={problems?.server} />
            <SnippetField
              label={t("siteApp.snippets.root")}
              hint={t("siteApp.snippets.rootHint")}
              value={root}
              onChange={setRoot}
              problems={problems?.root}
            />
            <div className="ui-row">
              <Button variant="primary" type="submit" disabled={busy}>
                {t("siteApp.snippets.save")}
              </Button>
              <Button disabled={busy} onClick={() => setOpen(false)}>
                {t("siteApp.snippets.close")}
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
  const { t } = useTranslation();
  const lines = problems && problems.length > 0 ? problems.map((p) => t("siteApp.snippets.line", { line: p.line, msg: p.msg })).join(" ") : undefined;
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
