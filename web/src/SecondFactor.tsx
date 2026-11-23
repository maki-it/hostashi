// SPDX-License-Identifier: AGPL-3.0-or-later

import { useState, type FormEvent } from "react";
import { ApiError } from "./api";
import { message, totpConfirm, totpEnroll } from "./auth";
import { Copy } from "@phosphor-icons/react";
import { Button, Card, Field, Notice, QRCode } from "./ui";

type Props = { onDone: () => void };

/** groupKey splits a base32 key into groups of four for reading it off the screen. */
export function groupKey(secret: string): string {
  return secret.replace(/(.{4})(?=.)/g, "$1 ");
}

// SecondFactor sets up TOTP for an account that must have a second factor
// and has none yet (ADR-0035). Until then the panel refuses everything else.
export function SecondFactor({ onDone }: Props) {
  const [enrolment, setEnrolment] = useState<{ secret: string; uri: string } | null>(null);
  const [code, setCode] = useState("");
  const [codes, setCodes] = useState<string[] | null>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [copied, setCopied] = useState("");

  async function copyKey(secret: string) {
    try {
      await navigator.clipboard.writeText(secret);
      setCopied("Copied.");
    } catch {
      setCopied("Copying is not possible here. Select the key and copy it by hand.");
    }
  }

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
            <p className="ui-flush">Scan this QR code with your authenticator app, then enter the code it shows.</p>
            <QRCode value={enrolment.uri} label="QR code for your authenticator app" />
            <div className="ui-stack-tight">
              <p className="ui-flush">Cannot scan it? Enter this key in the app instead:</p>
              <pre className="ui-code" aria-label="Authenticator key">
                {groupKey(enrolment.secret)}
              </pre>
              <div className="ui-row">
                <Button icon={<Copy size={16} aria-hidden="true" />} onClick={() => void copyKey(enrolment.secret)}>
                  Copy key
                </Button>
                <a className="ui-btn" href={enrolment.uri}>
                  Open in an authenticator app
                </a>
                {copied && (
                  <span role="status" className="ui-hint">
                    {copied}
                  </span>
                )}
              </div>
            </div>
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
