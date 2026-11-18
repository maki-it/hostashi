// SPDX-License-Identifier: AGPL-3.0-or-later

import { useState, type FormEvent } from "react";
import { ApiError } from "./api";
import { login, loginWithPasskey, message, type User } from "./auth";
import { passkeysSupported } from "./webauthn";

type Props = { notice?: string; onLogin: (user: User) => void };

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
    <section className="card" aria-labelledby="login-title">
      <h2 id="login-title">Log in</h2>
      {notice && <p role="status">{notice}</p>}
      <form onSubmit={(e) => void submit(e)} noValidate>
        {!needCode ? (
          <>
            <label>
              Username
              <input
                name="username"
                autoComplete="username webauthn"
                autoCapitalize="none"
                spellCheck={false}
                required
                value={username}
                onChange={(e) => setUsername(e.target.value)}
              />
            </label>
            <label>
              Password
              <input
                name="password"
                type="password"
                autoComplete="current-password"
                required
                value={password}
                onChange={(e) => setPassword(e.target.value)}
              />
            </label>
          </>
        ) : (
          <label>
            One-time code
            <input
              name="totp"
              autoComplete="one-time-code"
              autoCapitalize="none"
              spellCheck={false}
              required
              autoFocus
              value={code}
              onChange={(e) => setCode(e.target.value)}
            />
            <small>The 6-digit code from your authenticator app, or a recovery code.</small>
          </label>
        )}
        {error && (
          <p role="alert" className="error">
            {error}
          </p>
        )}
        <button type="submit" disabled={busy || (needCode ? code.trim() === "" : username === "" || password === "")}>
          {needCode ? "Verify" : "Log in"}
        </button>
        {needCode && (
          <button type="button" className="link" onClick={back}>
            Back
          </button>
        )}
      </form>
      {!needCode && passkeysSupported() && (
        <>
          <p className="divider">or</p>
          <button type="button" className="secondary" disabled={busy} onClick={() => void passkey()}>
            Log in with a passkey
          </button>
        </>
      )}
    </section>
  );
}
