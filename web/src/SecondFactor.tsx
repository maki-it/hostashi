// SPDX-License-Identifier: AGPL-3.0-or-later

import { useState, type FormEvent } from "react";
import { ApiError } from "./api";
import { message, totpConfirm, totpEnroll } from "./auth";
import { Button, Card, Field, Notice } from "./ui";

type Props = { onDone: () => void };

// SecondFactor sets up TOTP for an account that must have a second factor
// and has none yet (ADR-0035). Until then the panel refuses everything else.
export function SecondFactor({ onDone }: Props) {
  const [enrolment, setEnrolment] = useState<{ secret: string; uri: string } | null>(null);
  const [code, setCode] = useState("");
  const [codes, setCodes] = useState<string[] | null>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");

  async function start() {
    setBusy(true);
    setError("");
    try {
      setEnrolment(await totpEnroll());
    } catch (err) {
      setError(message(err));
    } finally {
      setBusy(false);
    }
  }

  async function confirm(e: FormEvent) {
    e.preventDefault();
    setBusy(true);
    setError("");
    try {
      setCodes(await totpConfirm(code.trim()));
    } catch (err) {
      setError(err instanceof ApiError && err.code === "invalid_credentials" ? "Wrong code. Use the current code from your app." : message(err));
      setCode("");
    } finally {
      setBusy(false);
    }
  }

  return (
    <Card title="Set up a second factor">
      <div className="ui-stack">
        {codes ? (
          <>
            <Notice tone="ok" title="Two-factor authentication is on. Keep these recovery codes somewhere safe; each one works once." />
            <ul aria-label="Recovery codes">
              {codes.map((c) => (
                <li key={c}>
                  <code>{c}</code>
                </li>
              ))}
            </ul>
            <Button variant="primary" onClick={onDone}>
              Continue
            </Button>
          </>
        ) : enrolment ? (
          <form className="ui-stack" onSubmit={(e) => void confirm(e)} noValidate>
            <p className="ui-flush">
              Add this key to your authenticator app, then enter the code it shows. Key: <code>{enrolment.secret}</code>
            </p>
            <p className="ui-flush">
              <a href={enrolment.uri}>Open in an authenticator app</a>
            </p>
            <Field label="One-time code">
              <input
                className="ui-input"
                name="code"
                inputMode="numeric"
                autoComplete="one-time-code"
                required
                value={code}
                onChange={(e) => setCode(e.target.value)}
              />
            </Field>
            <Button variant="primary" type="submit" disabled={busy || code.trim() === ""}>
              Turn on
            </Button>
          </form>
        ) : (
          <>
            <p className="ui-flush">Your role needs a second factor. Set up an authenticator app (TOTP) before you continue.</p>
            <Button variant="primary" disabled={busy} onClick={() => void start()}>
              Set up an authenticator app
            </Button>
          </>
        )}
        {error && <Notice tone="err" title={error} />}
      </div>
    </Card>
  );
}
