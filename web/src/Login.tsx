// SPDX-License-Identifier: AGPL-3.0-or-later

import { ArrowLeft, Fingerprint } from "@phosphor-icons/react";
import { useState, type FormEvent } from "react";
import { ApiError } from "./api";
import { login, loginWithPasskey, message, type User } from "./auth";
import { Button, Card, Field, Notice } from "./ui";
import { passkeysSupported } from "./webauthn";

type Props = { notice?: string; onLogin: (user: User) => void };

// Login asks in two steps (ADR-0037): username and password first, then the
// one-time code only when the account has an authenticator app.
export function Login({ notice, onLogin }: Props) {
  const [username, setUsername] = useState("");
  const [password, setPassword] = useState("");
  const [code, setCode] = useState("");
  // needCode: the password was right and the account has a second factor.
  const [needCode, setNeedCode] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");

  async function submit(e: FormEvent) {
    e.preventDefault();
    setBusy(true);
    setError("");
    try {
      onLogin(await login(username, password, needCode ? code.trim() : ""));
    } catch (err) {
      if (err instanceof ApiError && err.code === "totp_required") {
        setNeedCode(true);
      } else if (needCode && err instanceof ApiError && err.code === "invalid_credentials") {
        setError("Wrong code. Use the current code from your app or a recovery code.");
        setCode("");
      } else {
        setError(message(err));
      }
    } finally {
      setBusy(false);
    }
  }

  async function passkey() {
    setBusy(true);
    setError("");
    try {
      onLogin(await loginWithPasskey());
    } catch (err) {
      setError(message(err));
    } finally {
      setBusy(false);
    }
  }

  function back() {
    setNeedCode(false);
    setCode("");
    setPassword("");
    setError("");
  }

  return (
    <Card title="Log in">
      <div className="ui-stack">
        {notice && <Notice tone="ok" title={notice} />}
        <form className="ui-stack" onSubmit={(e) => void submit(e)} noValidate>
          {!needCode ? (
            <>
              <Field label="Username">
                <input
                  className="ui-input"
                  name="username"
                  autoComplete="username webauthn"
                  autoCapitalize="none"
                  spellCheck={false}
                  required
                  value={username}
                  onChange={(e) => setUsername(e.target.value)}
                />
              </Field>
              <Field label="Password">
                <input
                  className="ui-input"
                  name="password"
                  type="password"
                  autoComplete="current-password"
                  required
                  value={password}
                  onChange={(e) => setPassword(e.target.value)}
                />
              </Field>
            </>
          ) : (
            <>
              <p className="ui-flush">
                Signing in as <strong>{username}</strong>.
              </p>
              <Field label="One-time code" hint="The 6-digit code from your authenticator app, or a recovery code.">
                <input
                  className="ui-input ui-mono"
                  name="totp"
                  autoComplete="one-time-code"
                  autoCapitalize="none"
                  spellCheck={false}
                  required
                  autoFocus
                  value={code}
                  onChange={(e) => setCode(e.target.value)}
                />
              </Field>
            </>
          )}
          {error && <Notice tone="err" title={error} />}
          <div className="ui-auth-actions">
            <Button type="submit" variant="primary" disabled={busy || (needCode ? code.trim() === "" : username === "" || password === "")}>
              {needCode ? "Verify" : "Log in"}
            </Button>
            {needCode && (
              <Button icon={<ArrowLeft size={16} aria-hidden="true" />} onClick={back}>
                Back
              </Button>
            )}
          </div>
        </form>
        {!needCode && passkeysSupported() && (
          <>
            <p className="ui-auth-or">or</p>
            <div className="ui-auth-actions">
              <Button icon={<Fingerprint size={16} aria-hidden="true" />} disabled={busy} onClick={() => void passkey()}>
                Log in with a passkey
              </Button>
            </div>
          </>
        )}
        {!needCode && <p className="ui-hint ui-flush">Forgot your password? Ask an administrator of this panel.</p>}
      </div>
    </Card>
  );
}
