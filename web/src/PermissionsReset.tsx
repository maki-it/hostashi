// SPDX-License-Identifier: AGPL-3.0-or-later

import { useState, type FormEvent } from "react";
import { Trans, useTranslation } from "react-i18next";
import { formatCount } from "./format";
import i18n from "./i18n";
import { resetMessage, resetPermissions, type PermissionsReset } from "./permissions";
import { siteMessage, type Site } from "./sites";
import { Button, Card, Field, Notice } from "./ui";

// entries counts entries with the language's separators: "1,234 entries" or "1.234 Einträge".
const entries = (n: number) => i18n.t("permissions.entries", { count: n, n: formatCount(n) });

// PermissionsResetCard gives every file of the site back to its Linux user
// and resets the modes (ADR-0067), after the site's name is typed.
export function PermissionsResetCard({ site }: { site: Site }) {
  const { t } = useTranslation();
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
    <Card title={t("permissions.title")}>
      <div className="ui-stack">
        <p className="ui-hint ui-flush">
          <Trans i18nKey="permissions.intro" values={{ user: site.name }} components={{ code1: <span className="ui-mono" />, code2: <span className="ui-mono" />, code3: <span className="ui-mono" />, code4: <span className="ui-mono" /> }} />
        </p>
        <p className="ui-hint ui-flush">
          <Trans i18nKey="permissions.private" components={{ code1: <span className="ui-mono" />, code2: <span className="ui-mono" />, code3: <span className="ui-mono" /> }} />
        </p>
        {!open ? (
          <div className="ui-row">
            <Button onClick={() => setOpen(true)} disabled={busy}>
              {t("permissions.open")}
            </Button>
          </div>
        ) : (
          <form className="ui-stack" onSubmit={(e) => void run(e)}>
            <div className="ui-narrow">
              <Field
                label={<Trans i18nKey="permissions.confirm" values={{ name: site.name }} components={{ phrase: <code className="ui-confirm-phrase" /> }} />}
                hint={t("permissions.confirmHint")}
              >
                <input className="ui-input" value={typed} onChange={(e) => setTyped(e.target.value)} autoComplete="off" spellCheck={false} />
              </Field>
            </div>
            <div className="ui-row">
              <Button type="submit" variant="danger" disabled={busy || typed !== site.name}>
                {busy ? t("permissions.resetting") : t("permissions.submit", { name: site.name })}
              </Button>
              <Button disabled={busy} onClick={cancel}>
                {t("permissions.cancel")}
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
                ? t("permissions.nothing", { checked: entries(result.checked) })
                : t("permissions.changed", { changed: entries(result.changed), checked: entries(result.checked) })
            }
          >
            {result.skipped > 0 ? t("permissions.skipped", { count: result.skipped, n: formatCount(result.skipped) }) : undefined}
          </Notice>
        )}
        {result && result.skippedPaths.length > 0 && (
          <ul className="ui-flush" aria-label={t("permissions.skippedList")}>
            {result.skippedPaths.map((s) => (
              <li key={s.path}>
                <span className="ui-mono">{s.path}</span>: {s.reason}
              </li>
            ))}
            {result.skipped > result.skippedPaths.length && <li>{t("permissions.more", { n: formatCount(result.skipped - result.skippedPaths.length) })
}</li>}

          </ul>
        )}
      </div>
    </Card>
  );
}
