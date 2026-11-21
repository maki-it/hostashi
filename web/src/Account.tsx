// SPDX-License-Identifier: AGPL-3.0-or-later

import { useState } from "react";
import { EmailCard } from "./AccountEmail";
import type { User } from "./auth";
import { saveColorScheme, type ColorScheme } from "./scheme";
import { Card, Notice, PageHead } from "./ui";

const choices: { value: ColorScheme; label: string; hint: string }[] = [
  { value: "system", label: "Follow the system", hint: "Light or dark as your device is set." },
  { value: "light", label: "Light", hint: "" },
  { value: "dark", label: "Dark", hint: "" },
];

// Account is the page of the logged-in account: how the panel looks
// (ADR-0037), which follows the account to every browser, and its email
// address (ADR-0044).
export function Account({ user, onScheme }: { user: User; onScheme: (s: ColorScheme) => void }) {
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [saved, setSaved] = useState(false);
  const current = user.colorScheme ?? "system";

  async function pick(s: ColorScheme) {
    onScheme(s);
    setBusy(true);
    setError("");
    setSaved(false);
    try {
      await saveColorScheme(s);
      setSaved(true);
    } catch {
      setError("The colour scheme could not be saved. It applies until you reload.");
    } finally {
      setBusy(false);
    }
  }

  return (
    <>
      <PageHead title="Your account" description={`Logged in as ${user.username} (${user.role}).`} />
      <Card title="Appearance">
        <div className="ui-stack">
          <fieldset className="ui-stack-tight ui-fieldset" disabled={busy}>
            <legend>Colour scheme</legend>
            {choices.map((c) => (
              <label className="ui-check" key={c.value}>
                <input type="radio" name="scheme" value={c.value} checked={current === c.value} onChange={() => void pick(c.value)} />
                <span>
                  {c.label}
                  {c.hint && <span className="ui-hint"> {c.hint}</span>}
                </span>
              </label>
            ))}
          </fieldset>
          {error && <Notice tone="err" title={error} />}
          {saved && !error && <Notice tone="ok" title="Saved. The panel looks like this in every browser you log in with." />}
        </div>
      </Card>
      <EmailCard />
    </>
  );
}
